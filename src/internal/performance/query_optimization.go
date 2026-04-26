package performance

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// QueryOptimizations provides optimized database queries
type QueryOptimizations struct {
	db *gorm.DB
}

// NewQueryOptimizations creates a new query optimizations instance
func NewQueryOptimizations(db *gorm.DB) *QueryOptimizations {
	return &QueryOptimizations{db: db}
}

// OptimizedGistList returns an optimized query for listing gists
func (qo *QueryOptimizations) OptimizedGistList(filters GistFilters) *gorm.DB {
	query := qo.db.Model(&models.Gist{})

	// Use selective preloading with only needed fields
	query = query.Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id", "username", "display_name", "avatar_url")
	})

	// Preload files with only essential fields
	if filters.IncludeFiles {
		query = query.Preload("Files", func(db *gorm.DB) *gorm.DB {
			return db.Select("id", "gist_id", "filename", "language", "size").
				Order("filename ASC").
				Limit(5) // Limit files per gist
		})
	}

	// Preload tags if needed
	if filters.IncludeTags {
		query = query.Preload("Tags", func(db *gorm.DB) *gorm.DB {
			return db.Select("tags.id", "tags.name").
				Joins("JOIN gist_tags ON gist_tags.tag_id = tags.id")
		})
	}

	// Apply filters
	if filters.UserID != nil {
		query = query.Where("user_id = ?", *filters.UserID)
	}

	if filters.Visibility != "" {
		query = query.Where("visibility = ?", filters.Visibility)
	}

	if filters.Language != "" {
		query = query.Joins("JOIN gist_files ON gist_files.gist_id = gists.id").
			Where("gist_files.language = ?", filters.Language).
			Distinct("gists.id")
	}

	if len(filters.Tags) > 0 {
		query = query.Joins("JOIN gist_tags ON gist_tags.gist_id = gists.id").
			Joins("JOIN tags ON tags.id = gist_tags.tag_id").
			Where("tags.name IN ?", filters.Tags).
			Group("gists.id").
			Having("COUNT(DISTINCT tags.id) = ?", len(filters.Tags))
	}

	// Use index-friendly ordering
	switch filters.OrderBy {
	case "stars":
		query = query.Order("star_count DESC, created_at DESC")
	case "views":
		query = query.Order("view_count DESC, created_at DESC")
	case "updated":
		query = query.Order("updated_at DESC")
	default:
		query = query.Order("created_at DESC")
	}

	return query
}

// GistFilters represents filters for gist queries
type GistFilters struct {
	UserID       *uuid.UUID
	Visibility   string
	Language     string
	Tags         []string
	OrderBy      string
	IncludeFiles bool
	IncludeTags  bool
}

// OptimizedUserStats generates optimized user statistics query
func (qo *QueryOptimizations) OptimizedUserStats(userID uuid.UUID) (*UserStats, error) {
	stats := &UserStats{UserID: userID}

	// Use raw SQL for efficient counting
	queries := []struct {
		sql  string
		dest interface{}
	}{
		{
			sql:  "SELECT COUNT(*) FROM gists WHERE user_id = ?",
			dest: &stats.GistCount,
		},
		{
			sql:  "SELECT COUNT(*) FROM gist_stars JOIN gists ON gists.id = gist_stars.gist_id WHERE gists.user_id = ?",
			dest: &stats.TotalStars,
		},
		{
			sql:  "SELECT COUNT(*) FROM user_follows WHERE following_id = ?",
			dest: &stats.FollowerCount,
		},
		{
			sql:  "SELECT COUNT(*) FROM user_follows WHERE follower_id = ?",
			dest: &stats.FollowingCount,
		},
		{
			sql:  "SELECT SUM(view_count) FROM gists WHERE user_id = ?",
			dest: &stats.TotalViews,
		},
	}

	for _, q := range queries {
		if err := qo.db.Raw(q.sql, userID).Scan(q.dest).Error; err != nil {
			return nil, err
		}
	}

	// Handle NULL for total views
	if stats.TotalViews == nil {
		zero := int64(0)
		stats.TotalViews = &zero
	}

	return stats, nil
}

// UserStats represents user statistics
type UserStats struct {
	UserID         uuid.UUID
	GistCount      int64
	TotalStars     int64
	FollowerCount  int64
	FollowingCount int64
	TotalViews     *int64
}

// OptimizedSearch performs optimized full-text search
func (qo *QueryOptimizations) OptimizedSearch(query string, filters SearchFilters) *gorm.DB {
	// Clean and prepare search query
	searchTerms := prepareSearchTerms(query)
	
	dbQuery := qo.db.Model(&models.Gist{})

	// Build search conditions based on database type
	switch qo.db.Dialector.Name() {
	case "postgres":
		dbQuery = qo.postgresSearch(dbQuery, searchTerms, filters)
	case "mysql":
		dbQuery = qo.mysqlSearch(dbQuery, searchTerms, filters)
	default:
		dbQuery = qo.sqliteSearch(dbQuery, searchTerms, filters)
	}

	// Apply common filters
	if filters.UserID != nil {
		dbQuery = dbQuery.Where("gists.user_id = ?", *filters.UserID)
	}

	if filters.Language != "" {
		dbQuery = dbQuery.Joins("JOIN gist_files ON gist_files.gist_id = gists.id").
			Where("gist_files.language = ?", filters.Language)
	}

	if filters.After != nil {
		dbQuery = dbQuery.Where("gists.created_at > ?", *filters.After)
	}

	if filters.Before != nil {
		dbQuery = dbQuery.Where("gists.created_at < ?", *filters.Before)
	}

	// Add relevance-based ordering
	return dbQuery.Order("relevance DESC, gists.created_at DESC")
}

// SearchFilters represents search filters
type SearchFilters struct {
	UserID   *uuid.UUID
	Language string
	After    *time.Time
	Before   *time.Time
}

// postgresSearch implements PostgreSQL full-text search
func (qo *QueryOptimizations) postgresSearch(query *gorm.DB, terms []string, filters SearchFilters) *gorm.DB {
	// Use PostgreSQL's full-text search
	searchQuery := strings.Join(terms, " & ")
	
	return query.Select("gists.*, " +
		"ts_rank(to_tsvector('english', gists.title || ' ' || gists.description), " +
		"to_tsquery('english', ?)) as relevance", searchQuery).
		Where("to_tsvector('english', gists.title || ' ' || gists.description) @@ " +
			"to_tsquery('english', ?)", searchQuery)
}

// mysqlSearch implements MySQL full-text search
func (qo *QueryOptimizations) mysqlSearch(query *gorm.DB, terms []string, filters SearchFilters) *gorm.DB {
	// Use MySQL's full-text search
	searchQuery := strings.Join(terms, " ")
	
	return query.Select("gists.*, " +
		"MATCH(title, description) AGAINST(? IN NATURAL LANGUAGE MODE) as relevance", searchQuery).
		Where("MATCH(title, description) AGAINST(? IN NATURAL LANGUAGE MODE)", searchQuery)
}

// sqliteSearch implements SQLite full-text search fallback
func (qo *QueryOptimizations) sqliteSearch(query *gorm.DB, terms []string, filters SearchFilters) *gorm.DB {
	// Fallback to LIKE for SQLite
	conditions := make([]string, 0)
	values := make([]interface{}, 0)

	for _, term := range terms {
		conditions = append(conditions, "(gists.title LIKE ? OR gists.description LIKE ?)")
		searchTerm := "%" + term + "%"
		values = append(values, searchTerm, searchTerm)
	}

	if len(conditions) > 0 {
		query = query.Where(strings.Join(conditions, " AND "), values...)
	}

	// Add a simple relevance score
	query = query.Select("gists.*, 1 as relevance")

	return query
}

// prepareSearchTerms prepares search terms for querying
func prepareSearchTerms(query string) []string {
	// Simple tokenization - in production, use proper text processing
	terms := strings.Fields(strings.ToLower(query))
	
	// Remove common stop words
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
	}

	filtered := make([]string, 0)
	for _, term := range terms {
		if !stopWords[term] && len(term) > 2 {
			filtered = append(filtered, term)
		}
	}

	return filtered
}

// BatchUpdate performs optimized batch updates
func (qo *QueryOptimizations) BatchUpdate(model interface{}, updates map[string]interface{}, 
	conditions map[string]interface{}, batchSize int) error {
	
	if batchSize <= 0 {
		batchSize = 1000
	}

	// Build base query
	query := qo.db.Model(model)
	
	for key, value := range conditions {
		query = query.Where(key+" = ?", value)
	}

	// Perform batch update
	return query.Updates(updates).Error
}

// BulkInsert performs optimized bulk inserts
func (qo *QueryOptimizations) BulkInsert(models interface{}, batchSize int) error {
	if batchSize <= 0 {
		batchSize = 1000
	}

	return qo.db.CreateInBatches(models, batchSize).Error
}

// OptimizedCount performs optimized counting
func (qo *QueryOptimizations) OptimizedCount(model interface{}, conditions map[string]interface{}) (int64, error) {
	var count int64
	query := qo.db.Model(model)

	for key, value := range conditions {
		query = query.Where(key+" = ?", value)
	}

	// Use Count with specific column for better performance
	err := query.Count(&count).Error
	return count, err
}

// ExplainQuery explains a query plan (for debugging)
func (qo *QueryOptimizations) ExplainQuery(query *gorm.DB) ([]map[string]interface{}, error) {
	var results []map[string]interface{}

	sql := qo.db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return query.Find(&[]models.Gist{})
	})

	// Run EXPLAIN based on database type
	switch qo.db.Dialector.Name() {
	case "postgres":
		err := qo.db.Raw("EXPLAIN ANALYZE " + sql).Scan(&results).Error
		return results, err
	case "mysql":
		err := qo.db.Raw("EXPLAIN " + sql).Scan(&results).Error
		return results, err
	default:
		return nil, fmt.Errorf("EXPLAIN not supported for this database type")
	}
}