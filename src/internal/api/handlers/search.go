package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/search"
	"gorm.io/gorm"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
)

// SearchHandler handles search-related endpoints
type SearchHandler struct {
	searchManager *search.Manager
	config        *viper.Viper
	db            *gorm.DB
}

// NewSearchHandler creates a new search handler
func NewSearchHandler(searchManager *search.Manager, config *viper.Viper, db *gorm.DB) *SearchHandler {
	return &SearchHandler{
		searchManager: searchManager,
		config:        config,
		db:            db,
	}
}

// Search performs a search across all resources
func (h *SearchHandler) Search(c echo.Context) error {
	// Get query parameters
	query := strings.TrimSpace(c.QueryParam("q"))
	if query == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Search query is required",
		})
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Get authenticated user (optional) - for future use
	// userID, _ := c.Get("user_id").(uuid.UUID)

	// Perform search
	filters := search.SearchFilters{
		Limit:  limit,
		Offset: (page - 1) * limit,
	}
	results, err := h.searchManager.Search(c.Request().Context(), query, filters)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Search failed")
	}

	// Return results
	return c.JSON(http.StatusOK, results)
}

// SearchGists searches only gists
func (h *SearchHandler) SearchGists(c echo.Context) error {
	return h.Search(c) // Same as general search for now
}

// SearchUsers searches for users
func (h *SearchHandler) SearchUsers(c echo.Context) error {
	query := strings.TrimSpace(c.QueryParam("q"))
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Search users in database
	var users []models.User
	searchQuery := h.db.Model(&models.User{}).
		Where("deleted_at IS NULL")
	
	if query != "" {
		searchQuery = searchQuery.Where(
			"username LIKE ? OR email LIKE ? OR display_name LIKE ?",
			"%"+query+"%", "%"+query+"%", "%"+query+"%",
		)
	}
	
	searchQuery = searchQuery.Limit(limit)
	
	err := searchQuery.Find(&users).Error
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Search failed")
	}

	// Convert to response format
	response := make([]map[string]interface{}, len(users))
	for i, user := range users {
		response[i] = map[string]interface{}{
			"id":           user.ID,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"avatar_url":   user.AvatarURL,
			"is_admin":     user.IsAdmin,
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"users": response,
		"total": len(response),
	})
}

// Autocomplete provides search suggestions
func (h *SearchHandler) Autocomplete(c echo.Context) error {
	prefix := strings.TrimSpace(c.QueryParam("q"))
	if len(prefix) < 2 {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"suggestions": []string{},
		})
	}

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	// For now, return empty suggestions as autocomplete is not implemented
	// TODO: Implement autocomplete functionality
	return c.JSON(http.StatusOK, map[string]interface{}{
		"suggestions": []string{},
	})
}

// Reindex triggers a search index rebuild (admin only)
func (h *SearchHandler) Reindex(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Start reindexing in background
	go func() {
		if err := h.searchManager.UpdateIndex(c.Request().Context()); err != nil {
			// Log error
			c.Logger().Errorf("Failed to reindex: %v", err)
		}
	}()

	return c.JSON(http.StatusOK, map[string]string{
		"status": "Reindexing started",
	})
}

// GetStats returns search statistics (admin only)
func (h *SearchHandler) GetStats(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// TODO: Implement search statistics
	// For now, return basic stats
	stats := map[string]interface{}{
		"provider": "sqlite_fts",
		"indexed_gists": 0,
		"last_reindex": nil,
	}

	return c.JSON(http.StatusOK, stats)
}


// RegisterRoutes registers search routes
func (h *SearchHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/search", h.Search)
	g.GET("/search/gists", h.SearchGists)
	g.GET("/search/users", h.SearchUsers)
	g.GET("/search/autocomplete", h.Autocomplete)
	
	// Admin routes
	g.POST("/search/reindex", h.Reindex)
	g.GET("/search/stats", h.GetStats)
}