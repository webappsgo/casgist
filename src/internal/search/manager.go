package search

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// Manager handles search operations across different search providers
type Manager struct {
	db       *gorm.DB
	provider SearchProvider
}

// SearchProvider interface for different search implementations
type SearchProvider interface {
	Index(ctx context.Context, gist *models.Gist) error
	Search(ctx context.Context, query string, filters SearchFilters) (*SearchResult, error)
	Delete(ctx context.Context, gistID string) error
	UpdateIndex(ctx context.Context) error
}

// SearchFilters contains search filter criteria
type SearchFilters struct {
	UserID         string
	OrganizationID string
	Language       string
	Visibility     string
	Tags           []string
	DateFrom       string
	DateTo         string
	Sort           string
	Limit          int
	Offset         int
}

// SearchResult contains search results and metadata
type SearchResult struct {
	Gists      []*models.Gist `json:"gists"`
	Total      int64          `json:"total"`
	Page       int            `json:"page"`
	Limit      int            `json:"limit"`
	Query      string         `json:"query"`
	Filters    SearchFilters  `json:"filters"`
	TimeTaken  int64          `json:"time_taken_ms"`
}

// NewManager creates a new search manager
func NewManager(db *gorm.DB, providerType string, config map[string]interface{}) (*Manager, error) {
	var provider SearchProvider
	var err error

	switch providerType {
	case "sqlite_fts":
		provider, err = NewSQLiteProvider(db)
	case "redis":
		if url, ok := config["url"].(string); ok {
			provider, err = NewRedisProvider(db, url)
		} else {
			err = fmt.Errorf("Redis URL required in config")
		}
	case "elasticsearch":
		provider, err = NewElasticsearchProvider(config)
	default:
		provider, err = NewSQLiteProvider(db)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize search provider %s: %w", providerType, err)
	}

	return &Manager{
		db:       db,
		provider: provider,
	}, nil
}

// IndexGist adds or updates a gist in the search index
func (m *Manager) IndexGist(ctx context.Context, gist *models.Gist) error {
	return m.provider.Index(ctx, gist)
}

// Search performs a search query
func (m *Manager) Search(ctx context.Context, query string, filters SearchFilters) (*SearchResult, error) {
	if filters.Limit == 0 {
		filters.Limit = 30
	}
	if filters.Limit > 100 {
		filters.Limit = 100
	}

	return m.provider.Search(ctx, query, filters)
}

// DeleteGist removes a gist from the search index
func (m *Manager) DeleteGist(ctx context.Context, gistID string) error {
	return m.provider.Delete(ctx, gistID)
}

// UpdateIndex rebuilds the search index
func (m *Manager) UpdateIndex(ctx context.Context) error {
	return m.provider.UpdateIndex(ctx)
}

// SearchGists is a convenience method for searching gists with pagination
func (m *Manager) SearchGists(ctx context.Context, query string, userID string, page, limit int) (*SearchResult, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 30
	}

	filters := SearchFilters{
		UserID: userID,
		Limit:  limit,
		Offset: (page - 1) * limit,
	}

	return m.Search(ctx, query, filters)
}

// Close closes the search provider connection
func (m *Manager) Close() error {
	if closer, ok := m.provider.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}