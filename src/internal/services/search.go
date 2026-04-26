package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/cache"
	"github.com/casapps/casgist/src/internal/database/models"
)

// SearchBackend represents the type of search backend
type SearchBackend string

const (
	SearchBackendRedis  SearchBackend = "redis"
	SearchBackendSQLite SearchBackend = "sqlite"
)

// SearchResult represents a search result
type SearchResult struct {
	Type        string      `json:"type"` // "gist", "user", "file"
	ID          uuid.UUID   `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Content     string      `json:"content,omitempty"`
	Username    string      `json:"username,omitempty"`
	Filename    string      `json:"filename,omitempty"`
	Language    string      `json:"language,omitempty"`
	Score       float64     `json:"score"`
	Highlights  []string    `json:"highlights"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// SearchOptions represents search options
type SearchOptions struct {
	Query       string
	Type        string   // "all", "gist", "user", "file"
	UserID      *uuid.UUID
	Language    string
	Tags        []string
	DateFrom    *time.Time
	DateTo      *time.Time
	SortBy      string   // "relevance", "date", "stars"
	Page        int
	Limit       int
}

// SearchService handles search operations
type SearchService struct {
	db      *gorm.DB
	cfg     *viper.Viper
	backend SearchBackend
	cache   *cache.CacheManager
}

// NewSearchService creates a new search service
func NewSearchService(db *gorm.DB, cfg *viper.Viper, cacheManager *cache.CacheManager) *SearchService {
	backend := SearchBackendSQLite
	if cfg.GetBool("redis.enabled") {
		backend = SearchBackendRedis
	}

	return &SearchService{
		db:      db,
		cfg:     cfg,
		backend: backend,
		cache:   cacheManager,
	}
}

// Search performs a search based on the provided options
func (s *SearchService) Search(ctx context.Context, opts SearchOptions, viewerID *uuid.UUID) ([]SearchResult, int64, error) {
	// Validate options
	if opts.Query == "" && len(opts.Tags) == 0 && opts.UserID == nil {
		return nil, 0, errors.New("search query, tags, or user filter required")
	}

	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.Limit < 1 || opts.Limit > 100 {
		opts.Limit = 20
	}

	// Create cache key from search options
	cacheKey := s.createSearchCacheKey(opts, viewerID)
	
	// Try to get from cache first
	if s.cache != nil {
		type cachedSearchResult struct {
			Results []SearchResult `json:"results"`
			Total   int64          `json:"total"`
		}
		
		var cached cachedSearchResult
		if err := s.cache.GetJSON(ctx, cacheKey, &cached); err == nil {
			return cached.Results, cached.Total, nil
		}
	}

	// Use appropriate backend
	var results []SearchResult
	var total int64
	var err error
	
	switch s.backend {
	case SearchBackendRedis:
		// TODO: Implement Redis search
		// For now, fallback to SQLite
		results, total, err = s.searchSQLite(ctx, opts, viewerID)
	default:
		results, total, err = s.searchSQLite(ctx, opts, viewerID)
	}

	if err != nil {
		return nil, 0, err
	}

	// Cache the results for future requests
	if s.cache != nil {
		cachedResult := struct {
			Results []SearchResult `json:"results"`
			Total   int64          `json:"total"`
		}{
			Results: results,
			Total:   total,
		}
		s.cache.SetJSON(ctx, cacheKey, cachedResult, cache.TTLShort)
	}

	return results, total, nil
}

// searchSQLite performs search using SQLite full-text search
func (s *SearchService) searchSQLite(ctx context.Context, opts SearchOptions, viewerID *uuid.UUID) ([]SearchResult, int64, error) {
	var results []SearchResult
	var total int64

	// Clean search query
	query := strings.TrimSpace(opts.Query)
	searchPattern := "%" + query + "%"

	// Build base query
	baseQuery := s.db.WithContext(ctx)

	// Search gists
	if opts.Type == "all" || opts.Type == "gist" {
		gistResults, gistCount, err := s.searchGists(baseQuery, searchPattern, opts, viewerID)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, gistResults...)
		total += gistCount
	}

	// Search users
	if opts.Type == "all" || opts.Type == "user" {
		userResults, userCount, err := s.searchUsers(baseQuery, searchPattern, opts)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, userResults...)
		total += userCount
	}

	// Search files
	if opts.Type == "all" || opts.Type == "file" {
		fileResults, fileCount, err := s.searchFiles(baseQuery, searchPattern, opts, viewerID)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, fileResults...)
		total += fileCount
	}

	// Sort results by score/relevance
	// In a real implementation, we would calculate relevance scores
	// For now, just paginate the results
	start := (opts.Page - 1) * opts.Limit
	end := start + opts.Limit
	if end > len(results) {
		end = len(results)
	}

	if start < len(results) {
		results = results[start:end]
	} else {
		results = []SearchResult{}
	}

	return results, total, nil
}

// searchGists searches for gists
func (s *SearchService) searchGists(db *gorm.DB, pattern string, opts SearchOptions, viewerID *uuid.UUID) ([]SearchResult, int64, error) {
	query := db.Model(&models.Gist{})

	// Apply visibility filter
	if viewerID == nil {
		query = query.Where("visibility = ?", models.VisibilityPublic)
	} else {
		query = query.Where("visibility = ? OR user_id = ?", models.VisibilityPublic, *viewerID)
	}

	// Apply search pattern
	if pattern != "" {
		query = query.Where("title LIKE ? OR description LIKE ?", pattern, pattern)
	}

	// Apply user filter
	if opts.UserID != nil {
		query = query.Where("user_id = ?", *opts.UserID)
	}

	// Apply tag filter
	if len(opts.Tags) > 0 {
		query = query.Joins("JOIN gist_tags ON gist_tags.gist_id = gists.id").
			Joins("JOIN tags ON tags.id = gist_tags.tag_id").
			Where("tags.name IN ?", opts.Tags).
			Group("gists.id").
			Having("COUNT(DISTINCT tags.id) = ?", len(opts.Tags))
	}

	// Apply date filters
	if opts.DateFrom != nil {
		query = query.Where("gists.created_at >= ?", *opts.DateFrom)
	}
	if opts.DateTo != nil {
		query = query.Where("gists.created_at <= ?", *opts.DateTo)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply sorting
	switch opts.SortBy {
	case "date":
		query = query.Order("created_at DESC")
	case "stars":
		query = query.Order("star_count DESC")
	default:
		query = query.Order("updated_at DESC")
	}

	// Apply pagination
	query = query.Limit(opts.Limit).Offset((opts.Page - 1) * opts.Limit)

	// Load gists with user
	var gists []models.Gist
	if err := query.Preload("User").Find(&gists).Error; err != nil {
		return nil, 0, err
	}

	// Convert to search results
	results := make([]SearchResult, len(gists))
	for i, gist := range gists {
		results[i] = SearchResult{
			Type:        "gist",
			ID:          gist.ID,
			Title:       gist.Title,
			Description: gist.Description,
			Username:    gist.User.Username,
			Score:       1.0, // TODO: Calculate actual relevance score
			CreatedAt:   gist.CreatedAt,
			UpdatedAt:   gist.UpdatedAt,
		}

		// Add highlights
		if pattern != "" && strings.Contains(strings.ToLower(gist.Title), strings.ToLower(opts.Query)) {
			results[i].Highlights = append(results[i].Highlights, gist.Title)
		}
		if pattern != "" && strings.Contains(strings.ToLower(gist.Description), strings.ToLower(opts.Query)) {
			results[i].Highlights = append(results[i].Highlights, truncateString(gist.Description, 200))
		}
	}

	return results, total, nil
}

// searchUsers searches for users
func (s *SearchService) searchUsers(db *gorm.DB, pattern string, opts SearchOptions) ([]SearchResult, int64, error) {
	query := db.Model(&models.User{})

	// Apply search pattern
	if pattern != "" {
		query = query.Where("username LIKE ? OR bio LIKE ?", pattern, pattern)
	}

	// Only search active users
	query = query.Where("is_active = ?", true)

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply sorting
	query = query.Order("created_at DESC")

	// Apply pagination
	query = query.Limit(opts.Limit).Offset((opts.Page - 1) * opts.Limit)

	// Load users
	var users []models.User
	if err := query.Find(&users).Error; err != nil {
		return nil, 0, err
	}

	// Convert to search results
	results := make([]SearchResult, len(users))
	for i, user := range users {
		results[i] = SearchResult{
			Type:        "user",
			ID:          user.ID,
			Title:       user.Username,
			Description: user.Bio,
			Username:    user.Username,
			Score:       1.0,
			CreatedAt:   user.CreatedAt,
			UpdatedAt:   user.UpdatedAt,
		}

		// Add highlights
		if pattern != "" && strings.Contains(strings.ToLower(user.Username), strings.ToLower(opts.Query)) {
			results[i].Highlights = append(results[i].Highlights, user.Username)
		}
		if pattern != "" && strings.Contains(strings.ToLower(user.Bio), strings.ToLower(opts.Query)) {
			results[i].Highlights = append(results[i].Highlights, truncateString(user.Bio, 200))
		}
	}

	return results, total, nil
}

// searchFiles searches for files within gists
func (s *SearchService) searchFiles(db *gorm.DB, pattern string, opts SearchOptions, viewerID *uuid.UUID) ([]SearchResult, int64, error) {
	query := db.Model(&models.GistFile{}).
		Joins("JOIN gists ON gist_files.gist_id = gists.id").
		Joins("JOIN users ON gists.user_id = users.id")

	// Apply visibility filter
	if viewerID == nil {
		query = query.Where("gists.visibility = ?", models.VisibilityPublic)
	} else {
		query = query.Where("gists.visibility = ? OR gists.user_id = ?", models.VisibilityPublic, *viewerID)
	}

	// Apply search pattern
	if pattern != "" {
		query = query.Where("gist_files.filename LIKE ? OR gist_files.content LIKE ?", pattern, pattern)
	}

	// Apply language filter
	if opts.Language != "" {
		query = query.Where("gist_files.language = ?", opts.Language)
	}

	// Apply user filter
	if opts.UserID != nil {
		query = query.Where("gists.user_id = ?", *opts.UserID)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Apply sorting
	query = query.Order("gist_files.created_at DESC")

	// Apply pagination
	query = query.Limit(opts.Limit).Offset((opts.Page - 1) * opts.Limit)

	// Load files
	var files []struct {
		models.GistFile
		GistTitle string
		Username  string
	}
	if err := query.Select("gist_files.*, gists.title as gist_title, users.username as username").Scan(&files).Error; err != nil {
		return nil, 0, err
	}

	// Convert to search results
	results := make([]SearchResult, len(files))
	for i, file := range files {
		contentPreview := truncateString(file.Content, 200)
		
		results[i] = SearchResult{
			Type:        "file",
			ID:          file.ID,
			Title:       file.Filename,
			Description: fmt.Sprintf("in %s by %s", file.GistTitle, file.Username),
			Content:     contentPreview,
			Username:    file.Username,
			Filename:    file.Filename,
			Language:    file.Language,
			Score:       1.0,
			CreatedAt:   file.CreatedAt,
			UpdatedAt:   file.UpdatedAt,
		}

		// Add highlights
		if pattern != "" && strings.Contains(strings.ToLower(file.Filename), strings.ToLower(opts.Query)) {
			results[i].Highlights = append(results[i].Highlights, file.Filename)
		}
		if pattern != "" && strings.Contains(strings.ToLower(file.Content), strings.ToLower(opts.Query)) {
			// Extract snippet around match
			snippet := extractSnippet(file.Content, opts.Query, 100)
			if snippet != "" {
				results[i].Highlights = append(results[i].Highlights, snippet)
			}
		}
	}

	return results, total, nil
}

// IndexGist indexes a gist for search
func (s *SearchService) IndexGist(gist *models.Gist) error {
	if s.backend == SearchBackendRedis {
		// TODO: Implement Redis indexing
		return nil
	}
	// SQLite uses its built-in full-text search, no indexing needed
	return nil
}

// RemoveGist removes a gist from the search index
func (s *SearchService) RemoveGist(gistID uuid.UUID) error {
	if s.backend == SearchBackendRedis {
		// TODO: Implement Redis removal
		return nil
	}
	// SQLite automatically handles this via foreign keys
	return nil
}

// GetPopularSearches returns popular search queries
func (s *SearchService) GetPopularSearches(limit int) ([]string, error) {
	// TODO: Implement search analytics
	// For now, return some default suggestions
	return []string{
		"golang",
		"javascript",
		"python",
		"docker",
		"kubernetes",
		"react",
		"vue",
		"api",
		"config",
		"script",
	}, nil
}

// GetSearchSuggestions returns search suggestions based on partial query
func (s *SearchService) GetSearchSuggestions(query string, limit int) ([]string, error) {
	if len(query) < 2 {
		return []string{}, nil
	}

	var suggestions []string
	pattern := query + "%"

	// Get tag suggestions
	var tags []models.Tag
	if err := s.db.Where("name LIKE ?", pattern).Limit(limit/2).Find(&tags).Error; err == nil {
		for _, tag := range tags {
			suggestions = append(suggestions, "tag:"+tag.Name)
		}
	}

	// Get language suggestions
	var languages []string
	if err := s.db.Model(&models.GistFile{}).
		Distinct("language").
		Where("language LIKE ?", pattern).
		Limit(limit/2).
		Pluck("language", &languages).Error; err == nil {
		for _, lang := range languages {
			suggestions = append(suggestions, "language:"+lang)
		}
	}

	// Get user suggestions
	var users []models.User
	if err := s.db.Where("username LIKE ?", pattern).Limit(limit/2).Find(&users).Error; err == nil {
		for _, user := range users {
			suggestions = append(suggestions, "user:"+user.Username)
		}
	}

	return suggestions, nil
}

// Helper functions

// truncateString truncates a string to the specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// extractSnippet extracts a snippet around a search term
func extractSnippet(content, searchTerm string, contextLen int) string {
	lowerContent := strings.ToLower(content)
	lowerTerm := strings.ToLower(searchTerm)
	
	index := strings.Index(lowerContent, lowerTerm)
	if index == -1 {
		return ""
	}

	start := index - contextLen
	if start < 0 {
		start = 0
	}

	end := index + len(searchTerm) + contextLen
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}

	return snippet
}

// createSearchCacheKey creates a cache key for search results
func (s *SearchService) createSearchCacheKey(opts SearchOptions, viewerID *uuid.UUID) string {
	key := fmt.Sprintf("search:q=%s:page=%d:limit=%d", opts.Query, opts.Page, opts.Limit)
	
	if opts.UserID != nil {
		key += fmt.Sprintf(":user=%s", opts.UserID.String())
	}
	
	if len(opts.Tags) > 0 {
		key += fmt.Sprintf(":tags=%s", strings.Join(opts.Tags, ","))
	}
	
	if opts.Language != "" {
		key += fmt.Sprintf(":lang=%s", opts.Language)
	}
	
	if opts.Type != "" {
		key += fmt.Sprintf(":type=%s", opts.Type)
	}
	
	if opts.SortBy != "" {
		key += fmt.Sprintf(":sort=%s", opts.SortBy)
	}
	
	if viewerID != nil {
		key += fmt.Sprintf(":viewer=%s", viewerID.String())
	}
	
	return cache.SearchKey(key)
}

// SearchIndex represents a search index entry for Redis
type SearchIndex struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Tags        []string  `json:"tags"`
	Language    string    `json:"language"`
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	Visibility  string    `json:"visibility"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	StarCount   int       `json:"star_count"`
	ForkCount   int       `json:"fork_count"`
}