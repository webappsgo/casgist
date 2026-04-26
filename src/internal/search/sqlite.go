package search

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// SQLiteProvider implements search using SQLite FTS
type SQLiteProvider struct {
	db *gorm.DB
}

// NewSQLiteProvider creates a new SQLite search provider
func NewSQLiteProvider(db *gorm.DB) (SearchProvider, error) {
	provider := &SQLiteProvider{db: db}
	
	// Initialize FTS index if it doesn't exist
	if err := provider.initializeFTSIndex(); err != nil {
		return nil, fmt.Errorf("failed to initialize FTS index: %w", err)
	}
	
	return provider, nil
}


// NewElasticsearchProvider creates a new Elasticsearch search provider (placeholder)
func NewElasticsearchProvider(config map[string]interface{}) (SearchProvider, error) {
	return nil, fmt.Errorf("Elasticsearch search provider not implemented")
}

// Index adds or updates a gist in the search index
func (s *SQLiteProvider) Index(ctx context.Context, gist *models.Gist) error {
	// For now, just return nil - FTS indexing can be implemented later
	return nil
}

// Search performs a search query using SQLite FTS
func (s *SQLiteProvider) Search(ctx context.Context, query string, filters SearchFilters) (*SearchResult, error) {
	startTime := time.Now()
	
	// Build search query
	var gists []models.Gist
	dbQuery := s.db.Model(&models.Gist{}).Where("deleted_at IS NULL")
	
	// Apply visibility filter
	if filters.Visibility != "" {
		dbQuery = dbQuery.Where("visibility = ?", filters.Visibility)
	} else {
		dbQuery = dbQuery.Where("visibility IN ?", []string{"public", "unlisted"})
	}
	
	// Apply user filter
	if filters.UserID != "" {
		dbQuery = dbQuery.Where("user_id = ?", filters.UserID)
	}
	
	// Apply organization filter
	if filters.OrganizationID != "" {
		dbQuery = dbQuery.Where("organization_id = ?", filters.OrganizationID)
	}
	
	// Apply language filter
	if filters.Language != "" {
		dbQuery = dbQuery.Where("language = ?", filters.Language)
	}
	
	// Apply text search (basic LIKE search for now)
	if query != "" {
		searchTerm := "%" + query + "%"
		dbQuery = dbQuery.Where("title ILIKE ? OR description ILIKE ? OR tags ILIKE ?", 
			searchTerm, searchTerm, searchTerm)
	}
	
	// Apply sorting
	switch filters.Sort {
	case "created":
		dbQuery = dbQuery.Order("created_at DESC")
	case "updated":
		dbQuery = dbQuery.Order("updated_at DESC") 
	case "stars":
		dbQuery = dbQuery.Order("star_count DESC")
	case "relevance":
		dbQuery = dbQuery.Order("updated_at DESC") // Fallback
	default:
		dbQuery = dbQuery.Order("updated_at DESC")
	}
	
	// Apply pagination
	limit := filters.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := filters.Offset
	if offset < 0 {
		offset = 0
	}
	
	dbQuery = dbQuery.Limit(limit).Offset(offset)
	
	// Execute query with user information
	if err := dbQuery.Preload("User").Find(&gists).Error; err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	
	// Get total count for pagination
	var total int64
	countQuery := s.db.Model(&models.Gist{}).Where("deleted_at IS NULL")
	if filters.Visibility != "" {
		countQuery = countQuery.Where("visibility = ?", filters.Visibility)
	} else {
		countQuery = countQuery.Where("visibility IN ?", []string{"public", "unlisted"})
	}
	if filters.UserID != "" {
		countQuery = countQuery.Where("user_id = ?", filters.UserID)
	}
	if filters.OrganizationID != "" {
		countQuery = countQuery.Where("organization_id = ?", filters.OrganizationID)
	}
	if filters.Language != "" {
		countQuery = countQuery.Where("language = ?", filters.Language)
	}
	if query != "" {
		searchTerm := "%" + query + "%"
		countQuery = countQuery.Where("title ILIKE ? OR description ILIKE ? OR tags ILIKE ?", 
			searchTerm, searchTerm, searchTerm)
	}
	countQuery.Count(&total)
	
	duration := time.Since(startTime)
	
	// Convert to pointer slice as expected by SearchResult
	gistPtrs := make([]*models.Gist, len(gists))
	for i := range gists {
		gistPtrs[i] = &gists[i]
	}
	
	return &SearchResult{
		Gists:     gistPtrs,
		Total:     total,
		Page:      (offset / limit) + 1,
		Limit:     limit,
		Query:     query,
		Filters:   filters,
		TimeTaken: duration.Milliseconds(),
	}, nil
}

// Delete removes a gist from the search index
func (s *SQLiteProvider) Delete(ctx context.Context, gistID string) error {
	// Nothing to do for now
	return nil
}

// UpdateIndex rebuilds the entire search index
func (s *SQLiteProvider) UpdateIndex(ctx context.Context) error {
	// Nothing to do for now
	return nil
}

// initializeFTSIndex creates the FTS table if it doesn't exist
func (s *SQLiteProvider) initializeFTSIndex() error {
	// Skip FTS initialization for now
	return nil
}