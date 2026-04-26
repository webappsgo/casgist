package performance

import (
	"fmt"
	"sync"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// GistCache provides high-performance caching for gists
type GistCache struct {
	db            *gorm.DB
	cfg           *viper.Viper
	memoryCache   *MemoryCache
	queryCache    *QueryResultCache
	popularCache  *PopularGistsCache
	mu            sync.RWMutex
}

// NewGistCache creates a new gist cache
func NewGistCache(db *gorm.DB, cfg *viper.Viper) *GistCache {
	return &GistCache{
		db:           db,
		cfg:          cfg,
		memoryCache:  NewMemoryCache(cfg),
		queryCache:   NewQueryResultCache(cfg),
		popularCache: NewPopularGistsCache(db, cfg),
	}
}

// GetGist retrieves a gist with caching
func (gc *GistCache) GetGist(id uuid.UUID) (*models.Gist, error) {
	// Check memory cache first
	if cached, found := gc.memoryCache.Get(id.String()); found {
		return cached.(*models.Gist), nil
	}

	// Load from database
	var gist models.Gist
	if err := gc.db.Preload("Files").Preload("User").First(&gist, id).Error; err != nil {
		return nil, err
	}

	// Cache the result
	gc.memoryCache.Set(id.String(), &gist, 5*time.Minute)

	// Update popular cache if needed
	gc.popularCache.TrackAccess(id)

	return &gist, nil
}

// GetUserGists retrieves user gists with caching
func (gc *GistCache) GetUserGists(userID uuid.UUID, limit, offset int) ([]models.Gist, int64, error) {
	cacheKey := fmt.Sprintf("user_gists:%s:%d:%d", userID, limit, offset)
	
	// Check query cache
	if cached, found := gc.queryCache.Get(cacheKey); found {
		result := cached.(*UserGistsResult)
		return result.Gists, result.Total, nil
	}

	// Query database
	var gists []models.Gist
	var total int64

	query := gc.db.Model(&models.Gist{}).Where("user_id = ?", userID)
	query.Count(&total)
	
	if err := query.
		Preload("Files").
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&gists).Error; err != nil {
		return nil, 0, err
	}

	// Cache result
	gc.queryCache.Set(cacheKey, &UserGistsResult{
		Gists: gists,
		Total: total,
	}, 2*time.Minute)

	return gists, total, nil
}

// GetPublicGists retrieves public gists with caching
func (gc *GistCache) GetPublicGists(limit, offset int, sortBy string) ([]models.Gist, int64, error) {
	cacheKey := fmt.Sprintf("public_gists:%d:%d:%s", limit, offset, sortBy)
	
	// Check query cache
	if cached, found := gc.queryCache.Get(cacheKey); found {
		result := cached.(*UserGistsResult)
		return result.Gists, result.Total, nil
	}

	// Query database
	var gists []models.Gist
	var total int64

	query := gc.db.Model(&models.Gist{}).Where("visibility = ?", "public")
	query.Count(&total)

	// Apply sorting
	switch sortBy {
	case "stars":
		query = query.Order("star_count DESC, created_at DESC")
	case "views":
		query = query.Order("view_count DESC, created_at DESC")
	case "updated":
		query = query.Order("updated_at DESC")
	default:
		query = query.Order("created_at DESC")
	}
	
	if err := query.
		Preload("Files").
		Preload("User").
		Limit(limit).
		Offset(offset).
		Find(&gists).Error; err != nil {
		return nil, 0, err
	}

	// Cache result
	gc.queryCache.Set(cacheKey, &UserGistsResult{
		Gists: gists,
		Total: total,
	}, 1*time.Minute)

	return gists, total, nil
}

// InvalidateGist removes a gist from cache
func (gc *GistCache) InvalidateGist(id uuid.UUID) {
	gc.memoryCache.Delete(id.String())
	gc.queryCache.InvalidatePattern(fmt.Sprintf("*%s*", id.String()))
}

// InvalidateUserGists invalidates all cached queries for a user
func (gc *GistCache) InvalidateUserGists(userID uuid.UUID) {
	gc.queryCache.InvalidatePattern(fmt.Sprintf("user_gists:%s:*", userID))
}

// GetPopularGists retrieves popular/trending gists
func (gc *GistCache) GetPopularGists(limit int) ([]models.Gist, error) {
	return gc.popularCache.GetTop(limit)
}

// UserGistsResult stores user gists query result
type UserGistsResult struct {
	Gists []models.Gist
	Total int64
}

// MemoryCache provides in-memory caching
type MemoryCache struct {
	data sync.Map
	ttl  time.Duration
}

// NewMemoryCache creates a new memory cache
func NewMemoryCache(cfg *viper.Viper) *MemoryCache {
	mc := &MemoryCache{
		ttl: cfg.GetDuration("cache.memory_ttl"),
	}
	if mc.ttl == 0 {
		mc.ttl = 5 * time.Minute
	}

	// Start cleanup routine
	go mc.cleanup()

	return mc
}

// Get retrieves a value from cache
func (mc *MemoryCache) Get(key string) (interface{}, bool) {
	if val, ok := mc.data.Load(key); ok {
		entry := val.(*memoryCacheEntry)
		if time.Now().Before(entry.expiry) {
			return entry.value, true
		}
		mc.data.Delete(key)
	}
	return nil, false
}

// Set stores a value in cache
func (mc *MemoryCache) Set(key string, value interface{}, ttl time.Duration) {
	if ttl == 0 {
		ttl = mc.ttl
	}
	mc.data.Store(key, &memoryCacheEntry{
		value:  value,
		expiry: time.Now().Add(ttl),
	})
}

// Delete removes a value from cache
func (mc *MemoryCache) Delete(key string) {
	mc.data.Delete(key)
}

// cleanup removes expired entries
func (mc *MemoryCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		mc.data.Range(func(key, value interface{}) bool {
			entry := value.(*memoryCacheEntry)
			if now.After(entry.expiry) {
				mc.data.Delete(key)
			}
			return true
		})
	}
}

type memoryCacheEntry struct {
	value  interface{}
	expiry time.Time
}

// QueryResultCache caches database query results
type QueryResultCache struct {
	cache sync.Map
	ttl   time.Duration
	mu    sync.RWMutex
}

// NewQueryResultCache creates a new query result cache
func NewQueryResultCache(cfg *viper.Viper) *QueryResultCache {
	return &QueryResultCache{
		ttl: cfg.GetDuration("cache.query_ttl"),
	}
}

// Get retrieves a cached query result
func (qc *QueryResultCache) Get(key string) (interface{}, bool) {
	if val, ok := qc.cache.Load(key); ok {
		entry := val.(*queryCacheEntry)
		if time.Now().Before(entry.expiry) {
			return entry.data, true
		}
		qc.cache.Delete(key)
	}
	return nil, false
}

// Set stores a query result
func (qc *QueryResultCache) Set(key string, data interface{}, ttl time.Duration) {
	if ttl == 0 {
		ttl = qc.ttl
	}
	if ttl == 0 {
		ttl = 2 * time.Minute
	}

	qc.cache.Store(key, &queryCacheEntry{
		data:   data,
		expiry: time.Now().Add(ttl),
	})
}

// InvalidatePattern invalidates all keys matching pattern
func (qc *QueryResultCache) InvalidatePattern(pattern string) {
	qc.cache.Range(func(key, value interface{}) bool {
		if keyStr, ok := key.(string); ok {
			if matchesPattern(keyStr, pattern) {
				qc.cache.Delete(key)
			}
		}
		return true
	})
}

func matchesPattern(str, pattern string) bool {
	// Simple pattern matching with * wildcard
	if pattern == "*" {
		return true
	}
	
	// Handle prefix/suffix wildcards
	if len(pattern) > 0 && pattern[0] == '*' {
		suffix := pattern[1:]
		return len(str) >= len(suffix) && str[len(str)-len(suffix):] == suffix
	}
	
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(str) >= len(prefix) && str[:len(prefix)] == prefix
	}
	
	// Handle middle wildcards (simple implementation)
	return str == pattern
}

type queryCacheEntry struct {
	data   interface{}
	expiry time.Time
}

// PopularGistsCache tracks and caches popular gists
type PopularGistsCache struct {
	db          *gorm.DB
	cfg         *viper.Viper
	accessCount sync.Map
	topGists    []models.Gist
	mu          sync.RWMutex
	lastUpdate  time.Time
}

// NewPopularGistsCache creates a new popular gists cache
func NewPopularGistsCache(db *gorm.DB, cfg *viper.Viper) *PopularGistsCache {
	pc := &PopularGistsCache{
		db:       db,
		cfg:      cfg,
		topGists: make([]models.Gist, 0),
	}

	// Start update routine
	go pc.updateRoutine()

	return pc
}

// TrackAccess records a gist access
func (pc *PopularGistsCache) TrackAccess(gistID uuid.UUID) {
	key := gistID.String()
	var count int64 = 1

	if val, ok := pc.accessCount.Load(key); ok {
		count = val.(int64) + 1
	}

	pc.accessCount.Store(key, count)
}

// GetTop returns top popular gists
func (pc *PopularGistsCache) GetTop(limit int) ([]models.Gist, error) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	// Update if cache is stale
	if time.Since(pc.lastUpdate) > 5*time.Minute {
		pc.mu.RUnlock()
		pc.update()
		pc.mu.RLock()
	}

	if limit > len(pc.topGists) {
		limit = len(pc.topGists)
	}

	return pc.topGists[:limit], nil
}

// update refreshes the popular gists cache
func (pc *PopularGistsCache) update() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Query for popular gists
	var gists []models.Gist
	pc.db.
		Where("visibility = ?", "public").
		Order("star_count DESC, view_count DESC, created_at DESC").
		Limit(100).
		Preload("Files").
		Preload("User").
		Find(&gists)

	// Combine with access tracking
	accessMap := make(map[string]int64)
	pc.accessCount.Range(func(key, value interface{}) bool {
		accessMap[key.(string)] = value.(int64)
		return true
	})

	// Sort by combined score
	for i := range gists {
		if count, ok := accessMap[gists[i].ID.String()]; ok {
			// Boost score based on recent access
			gists[i].StarCount += int(count / 10)
		}
	}

	pc.topGists = gists
	pc.lastUpdate = time.Now()

	// Clear old access counts
	pc.accessCount = sync.Map{}
}

// updateRoutine periodically updates the cache
func (pc *PopularGistsCache) updateRoutine() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		pc.update()
	}
}

// CacheStats provides cache statistics
type CacheStats struct {
	Hits       int64     `json:"hits"`
	Misses     int64     `json:"misses"`
	Size       int64     `json:"size"`
	LastReset  time.Time `json:"last_reset"`
}

// GetStats returns cache statistics
func (gc *GistCache) GetStats() *CacheStats {
	// Implementation depends on specific cache backend
	return &CacheStats{
		Hits:      0, // TODO: Track hits
		Misses:    0, // TODO: Track misses
		Size:      0, // TODO: Track size
		LastReset: time.Now(),
	}
}