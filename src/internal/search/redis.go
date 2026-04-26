package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// RedisProvider implements search using Redis/Valkey
type RedisProvider struct {
	db     *gorm.DB
	client *redis.Client
	prefix string
}

// NewRedisProvider creates a new Redis search provider
func NewRedisProvider(db *gorm.DB, redisURL string) (SearchProvider, error) {
	// Parse Redis URL
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URL: %w", err)
	}

	client := redis.NewClient(opt)
	
	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis connection failed: %w", err)
	}

	provider := &RedisProvider{
		db:     db,
		client: client,
		prefix: "casgists:search:",
	}

	return provider, nil
}

// Index adds or updates a gist in the Redis search index
func (r *RedisProvider) Index(ctx context.Context, gist *models.Gist) error {
	// Create search document
	doc := map[string]interface{}{
		"id":          gist.ID.String(),
		"title":       gist.Title,
		"description": gist.Description,
		"visibility":  string(gist.Visibility),
		"language":    gist.Language,
		"tags":        gist.TagsString,
		"star_count":  gist.StarCount,
		"fork_count":  gist.ForkCount,
		"view_count":  gist.ViewCount,
		"created_at":  gist.CreatedAt.Unix(),
		"updated_at":  gist.UpdatedAt.Unix(),
	}

	// Add user information
	if gist.UserID != nil {
		doc["user_id"] = gist.UserID.String()
		if gist.User != nil {
			doc["username"] = gist.User.Username
			doc["user_display_name"] = gist.User.DisplayName
		}
	}

	// Add organization information
	if gist.OrganizationID != nil {
		doc["organization_id"] = gist.OrganizationID.String()
	}

	// Add file content for full-text search
	var content strings.Builder
	var filenames []string
	for _, file := range gist.Files {
		content.WriteString(file.Content)
		content.WriteString(" ")
		filenames = append(filenames, file.Filename)
	}
	doc["content"] = content.String()
	doc["filenames"] = strings.Join(filenames, " ")

	// Convert to JSON
	docJSON, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal document: %w", err)
	}

	// Store in Redis with multiple keys for different search types
	gistKey := r.prefix + "gist:" + gist.ID.String()
	
	pipe := r.client.Pipeline()
	
	// Store full document
	pipe.Set(ctx, gistKey, docJSON, 0)
	
	// Add to sorted sets for different sort orders
	pipe.ZAdd(ctx, r.prefix+"by_updated", redis.Z{
		Score:  float64(gist.UpdatedAt.Unix()),
		Member: gist.ID.String(),
	})
	pipe.ZAdd(ctx, r.prefix+"by_created", redis.Z{
		Score:  float64(gist.CreatedAt.Unix()),
		Member: gist.ID.String(),
	})
	pipe.ZAdd(ctx, r.prefix+"by_stars", redis.Z{
		Score:  float64(gist.StarCount),
		Member: gist.ID.String(),
	})

	// Add to text search index (simplified - would use RediSearch in production)
	searchText := strings.ToLower(gist.Title + " " + gist.Description + " " + gist.TagsString + " " + content.String())
	words := strings.Fields(searchText)
	for _, word := range words {
		if len(word) > 2 { // Skip very short words
			pipe.SAdd(ctx, r.prefix+"word:"+word, gist.ID.String())
		}
	}

	// Add to filter indexes
	if gist.Language != "" {
		pipe.SAdd(ctx, r.prefix+"lang:"+gist.Language, gist.ID.String())
	}
	if gist.UserID != nil {
		pipe.SAdd(ctx, r.prefix+"user:"+gist.UserID.String(), gist.ID.String())
	}
	if gist.OrganizationID != nil {
		pipe.SAdd(ctx, r.prefix+"org:"+gist.OrganizationID.String(), gist.ID.String())
	}
	pipe.SAdd(ctx, r.prefix+"visibility:"+string(gist.Visibility), gist.ID.String())

	_, err = pipe.Exec(ctx)
	return err
}

// Search performs a search query using Redis
func (r *RedisProvider) Search(ctx context.Context, query string, filters SearchFilters) (*SearchResult, error) {
	startTime := time.Now()
	
	var gistIDs []string
	var err error

	// Determine search strategy
	if query == "" {
		// Filter-only search
		gistIDs, err = r.searchByFilters(ctx, filters)
	} else {
		// Text search with filters
		gistIDs, err = r.searchByText(ctx, query, filters)
	}

	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Apply sorting and pagination
	gistIDs, err = r.applySortingAndPagination(ctx, gistIDs, filters)
	if err != nil {
		return nil, fmt.Errorf("sorting failed: %w", err)
	}

	// Get gist details from database
	var gists []*models.Gist
	if len(gistIDs) > 0 {
		if err := r.db.Preload("User").Preload("Files").Where("id IN ?", gistIDs).Find(&gists).Error; err != nil {
			return nil, fmt.Errorf("failed to load gists: %w", err)
		}

		// Maintain order from Redis results
		gistMap := make(map[string]*models.Gist)
		for _, gist := range gists {
			gistMap[gist.ID.String()] = gist
		}
		
		orderedGists := make([]*models.Gist, 0, len(gistIDs))
		for _, id := range gistIDs {
			if gist, exists := gistMap[id]; exists {
				orderedGists = append(orderedGists, gist)
			}
		}
		gists = orderedGists
	}

	// Get total count (estimate)
	totalCount := int64(len(gistIDs))

	duration := time.Since(startTime)

	return &SearchResult{
		Gists:     gists,
		Total:     totalCount,
		Page:      (filters.Offset / filters.Limit) + 1,
		Limit:     filters.Limit,
		Query:     query,
		Filters:   filters,
		TimeTaken: duration.Milliseconds(),
	}, nil
}

// searchByFilters performs filter-only search
func (r *RedisProvider) searchByFilters(ctx context.Context, filters SearchFilters) ([]string, error) {
	var keys []string

	// Build filter keys
	if filters.Language != "" {
		keys = append(keys, r.prefix+"lang:"+filters.Language)
	}
	if filters.UserID != "" {
		keys = append(keys, r.prefix+"user:"+filters.UserID)
	}
	if filters.OrganizationID != "" {
		keys = append(keys, r.prefix+"org:"+filters.OrganizationID)
	}
	if filters.Visibility != "" {
		keys = append(keys, r.prefix+"visibility:"+filters.Visibility)
	} else {
		// Default to public + unlisted for non-authenticated searches
		keys = append(keys, r.prefix+"visibility:public")
	}

	if len(keys) == 0 {
		// No filters, get all public gists
		keys = []string{r.prefix + "visibility:public"}
	}

	// Intersect all filter sets
	var result []string
	if len(keys) == 1 {
		result, _ = r.client.SMembers(ctx, keys[0]).Result()
	} else {
		// Use temporary key for intersection
		tempKey := r.prefix + "temp:" + fmt.Sprintf("%d", time.Now().UnixNano())
		defer r.client.Del(ctx, tempKey)
		
		r.client.SInterStore(ctx, tempKey, keys...)
		result, _ = r.client.SMembers(ctx, tempKey).Result()
	}

	return result, nil
}

// searchByText performs text search
func (r *RedisProvider) searchByText(ctx context.Context, query string, filters SearchFilters) ([]string, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return r.searchByFilters(ctx, filters)
	}

	var wordKeys []string
	for _, word := range words {
		wordKeys = append(wordKeys, r.prefix+"word:"+word)
	}

	// Intersect word keys to find gists containing all words
	tempKey := r.prefix + "temp:search:" + fmt.Sprintf("%d", time.Now().UnixNano())
	defer r.client.Del(ctx, tempKey)

	if len(wordKeys) == 1 {
		r.client.SUnionStore(ctx, tempKey, wordKeys...)
	} else {
		r.client.SInterStore(ctx, tempKey, wordKeys...)
	}

	// Apply filters
	if filters.Language != "" || filters.UserID != "" || filters.OrganizationID != "" || filters.Visibility != "" {
		filterResults, err := r.searchByFilters(ctx, filters)
		if err != nil {
			return nil, err
		}

		// Intersect with filter results
		filterKey := r.prefix + "temp:filter:" + fmt.Sprintf("%d", time.Now().UnixNano())
		defer r.client.Del(ctx, filterKey)
		
		r.client.SAdd(ctx, filterKey, filterResults)
		r.client.SInterStore(ctx, tempKey, tempKey, filterKey)
	}

	return r.client.SMembers(ctx, tempKey).Result()
}

// applySortingAndPagination applies sorting and pagination to results
func (r *RedisProvider) applySortingAndPagination(ctx context.Context, gistIDs []string, filters SearchFilters) ([]string, error) {
	if len(gistIDs) == 0 {
		return gistIDs, nil
	}

	// For sorting, we need to use Redis sorted sets
	tempKey := r.prefix + "temp:sort:" + fmt.Sprintf("%d", time.Now().UnixNano())
	defer r.client.Del(ctx, tempKey)

	// Add gists to temporary sorted set
	r.client.SAdd(ctx, tempKey, gistIDs)

	// Determine sort key
	var sortKey string
	var reverseOrder bool

	switch filters.Sort {
	case "created":
		sortKey = r.prefix + "by_created"
		reverseOrder = true
	case "stars":
		sortKey = r.prefix + "by_stars"
		reverseOrder = true
	case "relevance", "updated", "":
		sortKey = r.prefix + "by_updated"
		reverseOrder = true
	default:
		sortKey = r.prefix + "by_updated"
		reverseOrder = true
	}

	// Intersect with sort order
	resultKey := r.prefix + "temp:result:" + fmt.Sprintf("%d", time.Now().UnixNano())
	defer r.client.Del(ctx, resultKey)

	r.client.ZInterStore(ctx, resultKey, &redis.ZStore{
		Keys: []string{tempKey, sortKey},
	})

	// Apply pagination
	start := int64(filters.Offset)
	stop := start + int64(filters.Limit) - 1

	var result []string
	if reverseOrder {
		result, _ = r.client.ZRevRange(ctx, resultKey, start, stop).Result()
	} else {
		result, _ = r.client.ZRange(ctx, resultKey, start, stop).Result()
	}

	return result, nil
}

// Delete removes a gist from the search index
func (r *RedisProvider) Delete(ctx context.Context, gistID string) error {
	gistKey := r.prefix + "gist:" + gistID
	
	// Get document to remove from indexes
	docJSON, err := r.client.Get(ctx, gistKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil // Already deleted
		}
		return err
	}

	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(docJSON), &doc); err != nil {
		return err
	}

	pipe := r.client.Pipeline()

	// Remove from all indexes
	pipe.Del(ctx, gistKey)
	pipe.ZRem(ctx, r.prefix+"by_updated", gistID)
	pipe.ZRem(ctx, r.prefix+"by_created", gistID)
	pipe.ZRem(ctx, r.prefix+"by_stars", gistID)

	// Remove from text search indexes
	if content, ok := doc["content"].(string); ok {
		words := strings.Fields(strings.ToLower(content))
		for _, word := range words {
			if len(word) > 2 {
				pipe.SRem(ctx, r.prefix+"word:"+word, gistID)
			}
		}
	}

	// Remove from filter indexes
	if lang, ok := doc["language"].(string); ok && lang != "" {
		pipe.SRem(ctx, r.prefix+"lang:"+lang, gistID)
	}
	if userID, ok := doc["user_id"].(string); ok && userID != "" {
		pipe.SRem(ctx, r.prefix+"user:"+userID, gistID)
	}
	if orgID, ok := doc["organization_id"].(string); ok && orgID != "" {
		pipe.SRem(ctx, r.prefix+"org:"+orgID, gistID)
	}
	if vis, ok := doc["visibility"].(string); ok {
		pipe.SRem(ctx, r.prefix+"visibility:"+vis, gistID)
	}

	_, err = pipe.Exec(ctx)
	return err
}

// UpdateIndex rebuilds the entire search index
func (r *RedisProvider) UpdateIndex(ctx context.Context) error {
	// Clear existing index
	keys, err := r.client.Keys(ctx, r.prefix+"*").Result()
	if err != nil {
		return err
	}

	if len(keys) > 0 {
		if err := r.client.Del(ctx, keys...).Err(); err != nil {
			return err
		}
	}

	// Reindex all gists
	var gists []models.Gist
	if err := r.db.Preload("User").Preload("Files").Where("deleted_at IS NULL").Find(&gists).Error; err != nil {
		return err
	}

	for _, gist := range gists {
		if err := r.Index(ctx, &gist); err != nil {
			return fmt.Errorf("failed to index gist %s: %w", gist.ID, err)
		}
	}

	// Store rebuild timestamp
	r.client.Set(ctx, r.prefix+"last_rebuild", time.Now().Unix(), 0)

	return nil
}

// GetSearchStats returns search statistics
func (r *RedisProvider) GetSearchStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Get index size
	keys, _ := r.client.Keys(ctx, r.prefix+"gist:*").Result()
	stats["indexed_gists"] = len(keys)

	// Get last rebuild time
	lastRebuild, err := r.client.Get(ctx, r.prefix+"last_rebuild").Result()
	if err == nil {
		if timestamp, err := strconv.ParseInt(lastRebuild, 10, 64); err == nil {
			stats["last_rebuild"] = time.Unix(timestamp, 0).Format(time.RFC3339)
		}
	}

	// Get memory usage
	info, _ := r.client.Info(ctx, "memory").Result()
	stats["redis_info"] = info

	return stats, nil
}