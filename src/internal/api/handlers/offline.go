package handlers

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/models"
)

// OfflineHandler handles offline-related operations for PWA
type OfflineHandler struct {
	db *gorm.DB
}

// NewOfflineHandler creates a new offline handler
func NewOfflineHandler(db *gorm.DB) *OfflineHandler {
	return &OfflineHandler{db: db}
}

// RegisterRoutes registers offline routes
func (h *OfflineHandler) RegisterRoutes(e *echo.Group) {
	offline := e.Group("/offline")
	
	// Public offline routes (no auth required)
	offline.GET("", h.ShowOfflinePage)
	offline.GET("/", h.ShowOfflinePage)
	
	// Authenticated offline routes - TODO: Add auth middleware when it's properly implemented
	offline.GET("/gists", h.ShowOfflineGists)
	offline.GET("/search", h.ShowOfflineSearch)
	offline.POST("/sync", h.SyncOfflineData)
}

// ShowOfflinePage renders the offline page
func (h *OfflineHandler) ShowOfflinePage(c echo.Context) error {
	return c.Render(http.StatusOK, "pages/offline", map[string]interface{}{
		"Title":       "Offline - CasGists",
		"Description": "You're offline. Here's what you can still do.",
	})
}

// ShowOfflineGists shows cached gists for offline viewing
func (h *OfflineHandler) ShowOfflineGists(c echo.Context) error {
	// TODO: Get actual user from auth context when auth is properly implemented
	// For now, use a mock user or skip auth check
	var user models.User
	if err := h.db.First(&user).Error; err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "No users found"})
	}

	// Get pagination parameters
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit < 1 || limit > 100 {
		limit = 30
	}
	offset := (page - 1) * limit

	// Get user's gists (these are cached in service worker for offline access)
	var gists []models.Gist
	var total int64

	query := h.db.Where("user_id = ?", user.ID)
	
	// Count total
	if err := query.Model(&models.Gist{}).Count(&total).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to count gists"})
	}

	// Get gists with pagination
	if err := query.Preload("Files").Preload("User").
		Order("updated_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&gists).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to get gists"})
	}

	// If this is an API request, return JSON
	if c.Request().Header.Get("Accept") == "application/json" || c.QueryParam("format") == "json" {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"gists":   gists,
			"total":   total,
			"page":    page,
			"limit":   limit,
			"offline": true,
			"message": "Showing your gists - offline mode",
		})
	}

	// Otherwise render the page
	return c.Render(http.StatusOK, "pages/offline-gists", map[string]interface{}{
		"Title":       "My Gists - Offline",
		"Description": "Your cached gists available offline",
		"Gists":       gists,
		"Total":       total,
		"Page":        page,
		"Limit":       limit,
		"User":        user,
		"Offline":     true,
	})
}

// ShowOfflineSearch shows offline search functionality
func (h *OfflineHandler) ShowOfflineSearch(c echo.Context) error {
	// TODO: Get actual user from auth context when auth is properly implemented
	// For now, use a mock user or skip auth check
	var user models.User
	if err := h.db.First(&user).Error; err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "No users found"})
	}

	query := c.QueryParam("q")
	language := c.QueryParam("language")
	visibility := c.QueryParam("visibility")
	
	var gists []models.Gist

	if query != "" {
		// Build search query
		dbQuery := h.db.Where("user_id = ?", user.ID)

		// Add language filter
		if language != "" {
			dbQuery = dbQuery.Where("language = ?", language)
		}

		// Add visibility filter
		if visibility != "" {
			dbQuery = dbQuery.Where("visibility = ?", visibility)
		}

		// Search in title, description, and file names
		searchTerm := "%" + query + "%"
		dbQuery = dbQuery.Where(
			h.db.Where("title ILIKE ? OR description ILIKE ?", searchTerm, searchTerm).
				Or("id IN (SELECT gist_id FROM gist_files WHERE filename ILIKE ?)", searchTerm),
		)

		if err := dbQuery.Preload("Files").Preload("User").
			Order("updated_at DESC").
			Limit(50).
			Find(&gists).Error; err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Search failed"})
		}
	}

	// If this is an API request, return JSON
	if c.Request().Header.Get("Accept") == "application/json" || c.QueryParam("format") == "json" {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"results": gists,
			"query":   query,
			"offline": true,
			"message": "Showing cached search results",
		})
	}

	// Otherwise render the page
	return c.Render(http.StatusOK, "pages/offline-search", map[string]interface{}{
		"Title":       "Search - Offline",
		"Description": "Search your cached gists",
		"Results":     gists,
		"Query":       query,
		"Language":    language,
		"Visibility":  visibility,
		"User":        user,
		"Offline":     true,
	})
}

// SyncOfflineData handles syncing offline data when connection is restored
func (h *OfflineHandler) SyncOfflineData(c echo.Context) error {
	// TODO: Get actual user from auth context when auth is properly implemented
	// For now, use a mock user or skip auth check
	var user models.User
	if err := h.db.First(&user).Error; err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "No users found"})
	}

	// This endpoint primarily exists for the service worker to call
	// The actual syncing is handled by the service worker's background sync
	
	// Return sync status
	return c.JSON(http.StatusOK, map[string]interface{}{
		"message":    "Sync initiated",
		"user_id":    user.ID,
		"timestamp":  "now", // Time would be handled by service worker
		"offline":    false,
	})
}

// GetOfflineManifest returns the PWA manifest with offline capabilities
func (h *OfflineHandler) GetOfflineManifest(c echo.Context) error {
	manifest := map[string]interface{}{
		"name":        "CasGists - Self-Hosted Gist Alternative",
		"short_name":  "CasGists",
		"description": "A powerful, self-hosted alternative to GitHub Gists with advanced features",
		"start_url":   "/",
		"display":     "standalone",
		"background_color": "#1e1e2e",
		"theme_color":      "#a6e3a1",
		"orientation":      "any",
		"scope":           "/",
		"offline":         true,
		"capabilities": []string{
			"view-cached-gists",
			"create-gists-offline",
			"search-cached-content",
			"edit-existing-gists",
		},
	}

	c.Response().Header().Set("Content-Type", "application/json")
	return c.JSON(http.StatusOK, manifest)
}