package performance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/casapps/casgist/src/internal/cache"
	"github.com/google/uuid"
)

// CacheStrategies provides various caching strategies for different data types
type CacheStrategies struct {
	cache *cache.CacheManager
}

// NewCacheStrategies creates a new cache strategies instance
func NewCacheStrategies(cacheManager *cache.CacheManager) *CacheStrategies {
	return &CacheStrategies{
		cache: cacheManager,
	}
}

// CacheKeyGenerator generates cache keys for different entities
type CacheKeyGenerator struct {
	prefix string
}

// NewCacheKeyGenerator creates a new cache key generator
func NewCacheKeyGenerator(prefix string) *CacheKeyGenerator {
	return &CacheKeyGenerator{
		prefix: prefix,
	}
}

// UserKey generates cache key for user data
func (g *CacheKeyGenerator) UserKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:user:%s", g.prefix, userID.String())
}

// GistKey generates cache key for gist data
func (g *CacheKeyGenerator) GistKey(gistID uuid.UUID) string {
	return fmt.Sprintf("%s:gist:%s", g.prefix, gistID.String())
}

// GistListKey generates cache key for gist lists
func (g *CacheKeyGenerator) GistListKey(userID *uuid.UUID, visibility string, page int) string {
	if userID != nil {
		return fmt.Sprintf("%s:gists:user:%s:vis:%s:page:%d", g.prefix, userID.String(), visibility, page)
	}
	return fmt.Sprintf("%s:gists:public:vis:%s:page:%d", g.prefix, visibility, page)
}

// SearchResultKey generates cache key for search results
func (g *CacheKeyGenerator) SearchResultKey(query string, filters map[string]string, page int) string {
	filterStr := ""
	for k, v := range filters {
		filterStr += fmt.Sprintf(":%s:%s", k, v)
	}
	return fmt.Sprintf("%s:search:%s%s:page:%d", g.prefix, query, filterStr, page)
}

// UserStatsKey generates cache key for user statistics
func (g *CacheKeyGenerator) UserStatsKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:stats:user:%s", g.prefix, userID.String())
}

// TrendingKey generates cache key for trending gists
func (g *CacheKeyGenerator) TrendingKey(period string, page int) string {
	return fmt.Sprintf("%s:trending:%s:page:%d", g.prefix, period, page)
}

// CacheDurations defines cache TTLs for different data types
var CacheDurations = struct {
	User          time.Duration
	Gist          time.Duration
	GistList      time.Duration
	SearchResults time.Duration
	UserStats     time.Duration
	Trending      time.Duration
	Tags          time.Duration
}{
	User:          5 * time.Minute,
	Gist:          10 * time.Minute,
	GistList:      2 * time.Minute,
	SearchResults: 1 * time.Minute,
	UserStats:     15 * time.Minute,
	Trending:      30 * time.Minute,
	Tags:          1 * time.Hour,
}

// CacheWarmer pre-warms cache with frequently accessed data
type CacheWarmer struct {
	strategies *CacheStrategies
	keyGen     *CacheKeyGenerator
}

// NewCacheWarmer creates a new cache warmer
func NewCacheWarmer(strategies *CacheStrategies, keyGen *CacheKeyGenerator) *CacheWarmer {
	return &CacheWarmer{
		strategies: strategies,
		keyGen:     keyGen,
	}
}

// WarmTrendingGists pre-caches trending gists
func (w *CacheWarmer) WarmTrendingGists(fetchFunc func(period string, page int) (interface{}, error)) error {
	periods := []string{"daily", "weekly", "monthly"}
	
	for _, period := range periods {
		// Cache first 3 pages of each period
		for page := 1; page <= 3; page++ {
			data, err := fetchFunc(period, page)
			if err != nil {
				return fmt.Errorf("failed to fetch trending gists: %w", err)
			}

			key := w.keyGen.TrendingKey(period, page)
			if err := w.strategies.CacheData(key, data, CacheDurations.Trending); err != nil {
				return fmt.Errorf("failed to cache trending gists: %w", err)
			}
		}
	}

	return nil
}

// CacheData caches any data with JSON serialization
func (cs *CacheStrategies) CacheData(key string, data interface{}, ttl time.Duration) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	return cs.cache.Set(context.Background(), key, string(jsonData), ttl)
}

// GetCachedData retrieves cached data with JSON deserialization
func (cs *CacheStrategies) GetCachedData(key string, dest interface{}) error {
	jsonData, err := cs.cache.Get(context.Background(), key)
	if err != nil {
		return err
	}

	if jsonData == "" {
		return fmt.Errorf("cache miss")
	}

	if err := json.Unmarshal([]byte(jsonData), dest); err != nil {
		return fmt.Errorf("failed to unmarshal cached data: %w", err)
	}

	return nil
}

// InvalidationStrategy handles cache invalidation
type InvalidationStrategy struct {
	cache  *cache.CacheManager
	keyGen *CacheKeyGenerator
}

// NewInvalidationStrategy creates a new invalidation strategy
func NewInvalidationStrategy(cache *cache.CacheManager, keyGen *CacheKeyGenerator) *InvalidationStrategy {
	return &InvalidationStrategy{
		cache:  cache,
		keyGen: keyGen,
	}
}

// InvalidateUser invalidates all user-related caches
func (is *InvalidationStrategy) InvalidateUser(userID uuid.UUID) error {
	keys := []string{
		is.keyGen.UserKey(userID),
		is.keyGen.UserStatsKey(userID),
	}

	// Also invalidate user's gist lists (multiple pages)
	for page := 1; page <= 10; page++ {
		keys = append(keys, is.keyGen.GistListKey(&userID, "", page))
		keys = append(keys, is.keyGen.GistListKey(&userID, "public", page))
		keys = append(keys, is.keyGen.GistListKey(&userID, "private", page))
	}

	for _, key := range keys {
		if err := is.cache.Delete(context.Background(), key); err != nil {
			// Log error but continue invalidation
			fmt.Printf("Failed to invalidate cache key %s: %v\n", key, err)
		}
	}

	return nil
}

// InvalidateGist invalidates gist-related caches
func (is *InvalidationStrategy) InvalidateGist(gistID uuid.UUID, userID *uuid.UUID) error {
	// Invalidate the gist itself
	if err := is.cache.Delete(context.Background(), is.keyGen.GistKey(gistID)); err != nil {
		fmt.Printf("Failed to invalidate gist cache: %v\n", err)
	}

	// Invalidate user's gist lists if user ID provided
	if userID != nil {
		is.InvalidateUser(*userID)
	}

	// Invalidate trending caches (gist might be trending)
	is.InvalidateTrending()

	return nil
}

// InvalidateTrending invalidates all trending caches
func (is *InvalidationStrategy) InvalidateTrending() {
	periods := []string{"daily", "weekly", "monthly"}
	
	for _, period := range periods {
		for page := 1; page <= 10; page++ {
			key := is.keyGen.TrendingKey(period, page)
			is.cache.Delete(context.Background(), key)
		}
	}
}

// CacheAside implements cache-aside pattern
type CacheAside struct {
	cache      *cache.CacheManager
	keyGen     *CacheKeyGenerator
	strategies *CacheStrategies
}

// NewCacheAside creates a new cache-aside implementation
func NewCacheAside(cache *cache.CacheManager, keyGen *CacheKeyGenerator) *CacheAside {
	return &CacheAside{
		cache:      cache,
		keyGen:     keyGen,
		strategies: NewCacheStrategies(cache),
	}
}

// GetOrSet implements cache-aside pattern with loader function
func (ca *CacheAside) GetOrSet(key string, ttl time.Duration, loader func() (interface{}, error)) (interface{}, error) {
	// Try to get from cache
	var result interface{}
	err := ca.strategies.GetCachedData(key, &result)
	if err == nil {
		return result, nil
	}

	// If not in cache or error, load from source
	data, err := loader()
	if err != nil {
		return nil, err
	}

	// Cache the result asynchronously
	go func() {
		if err := ca.strategies.CacheData(key, data, ttl); err != nil {
			fmt.Printf("Failed to cache data for key %s: %v\n", key, err)
		}
	}()

	return data, nil
}

// MultiGetOrSet gets multiple items with cache-aside pattern
func (ca *CacheAside) MultiGetOrSet(requests []CacheRequest) (map[string]interface{}, error) {
	results := make(map[string]interface{})
	toLoad := make([]CacheRequest, 0)

	// First pass: check cache
	for _, req := range requests {
		var data interface{}
		err := ca.strategies.GetCachedData(req.Key, &data)
		if err == nil {
			results[req.Key] = data
		} else {
			toLoad = append(toLoad, req)
		}
	}

	// Second pass: load missing data
	for _, req := range toLoad {
		data, err := req.Loader()
		if err != nil {
			return nil, fmt.Errorf("failed to load data for key %s: %w", req.Key, err)
		}

		results[req.Key] = data

		// Cache asynchronously
		go func(key string, data interface{}, ttl time.Duration) {
			ca.strategies.CacheData(key, data, ttl)
		}(req.Key, data, req.TTL)
	}

	return results, nil
}

// CacheRequest represents a cache request
type CacheRequest struct {
	Key    string
	TTL    time.Duration
	Loader func() (interface{}, error)
}