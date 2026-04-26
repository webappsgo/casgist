package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/labstack/echo/v4"
)

// Dashboard renders the admin dashboard page
func (h *AdminHandler) DashboardPage(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Gather statistics
	var userCount, gistCount, orgCount int64
	var newUsersToday, newGistsToday int64
	
	// Basic counts
	h.db.Model(&models.User{}).Where("deleted_at IS NULL").Count(&userCount)
	h.db.Model(&models.Gist{}).Where("deleted_at IS NULL").Count(&gistCount)
	h.db.Model(&models.Organization{}).Where("deleted_at IS NULL").Count(&orgCount)
	
	// Today's activity
	today := time.Now().Truncate(24 * time.Hour)
	h.db.Model(&models.User{}).Where("created_at >= ?", today).Count(&newUsersToday)
	h.db.Model(&models.Gist{}).Where("created_at >= ?", today).Count(&newGistsToday)

	// Recent activity
	var recentUsers []models.User
	h.db.Order("created_at DESC").Limit(5).Find(&recentUsers)

	var recentGists []models.Gist
	h.db.Preload("User").Order("created_at DESC").Limit(5).Find(&recentGists)
	
	// Generate chart data for last 7 days
	userLabels := []string{}
	userData := []int{}
	gistLabels := []string{}
	gistData := []int{}
	
	for i := 6; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i)
		dayStart := day.Truncate(24 * time.Hour)
		dayEnd := dayStart.Add(24 * time.Hour)
		
		// User registrations for this day
		var dayUsers int64
		h.db.Model(&models.User{}).Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).Count(&dayUsers)
		
		// Gist creations for this day
		var dayGists int64
		h.db.Model(&models.Gist{}).Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).Count(&dayGists)
		
		userLabels = append(userLabels, day.Format("Jan 2"))
		userData = append(userData, int(dayUsers))
		gistLabels = append(gistLabels, day.Format("Jan 2"))
		gistData = append(gistData, int(dayGists))
	}
	
	// Create data structure for template
	data := map[string]interface{}{
		"Title": "Admin Dashboard",
		"Stats": map[string]interface{}{
			"TotalUsers":     userCount,
			"NewUsersToday":  newUsersToday,
			"TotalGists":     gistCount,
			"NewGistsToday":  newGistsToday,
			"StorageUsed":    "2.1 GB",
			"StorageTotal":   "100 GB",
			"SystemHealth":   95,
		},
		"RecentUsers": recentUsers,
		"RecentGists": recentGists,
		"SystemInfo": map[string]interface{}{
			"Version":    h.config.GetString("version"),
			"Uptime":     "72h 15m 30s", // TODO: Calculate actual uptime
			"Database":   h.config.GetString("database.type"),
			"CPUUsage":   45,
			"MemoryUsage": 68,
			"DiskUsage":  23,
		},
		"ChartData": map[string]interface{}{
			"UserActivity": map[string]interface{}{
				"Labels": userLabels,
				"Data":   userData,
			},
			"GistCreation": map[string]interface{}{
				"Labels": gistLabels,
				"Data":   gistData,
			},
		},
		"RecentAlerts": []map[string]interface{}{},
	}

	return c.Render(http.StatusOK, "admin_dashboard", data)
}

// UsersPage renders the user management page
func (h *AdminHandler) UsersPage(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit := 20

	// Parse filters
	search := c.QueryParam("search")
	role := c.QueryParam("role")
	status := c.QueryParam("status")

	// Build query
	query := h.db.Model(&models.User{})

	if search != "" {
		query = query.Where("username ILIKE ? OR email ILIKE ? OR display_name ILIKE ?", 
			"%"+search+"%", "%"+search+"%", "%"+search+"%")
	}

	if role == "admin" {
		query = query.Where("is_admin = ?", true)
	} else if role == "user" {
		query = query.Where("is_admin = ?", false)
	}

	if status == "active" {
		query = query.Where("deleted_at IS NULL AND is_suspended = ?", false)
	} else if status == "suspended" {
		query = query.Where("is_suspended = ?", true)
	}

	// Count total
	var total int64
	query.Count(&total)

	// Get users
	var users []models.User
	offset := (page - 1) * limit
	if err := query.
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&users).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch users")
	}
	
	// Add computed fields for template
	for i := range users {
		// Set status based on suspension and deletion
		if users[i].IsSuspended {
			users[i].Status = "suspended"
		} else if users[i].DeletedAt.Valid {
			users[i].Status = "deleted"
		} else if !users[i].IsEmailVerified {
			users[i].Status = "pending"
		} else {
			users[i].Status = "active"
		}
		
		// Get gist count for this user
		var gistCount int64
		h.db.Model(&models.Gist{}).Where("user_id = ? AND deleted_at IS NULL", users[i].ID).Count(&gistCount)
		users[i].GistCount = int(gistCount)
		
		// Set placeholder values for template
		users[i].StorageUsed = "1.2 MB"
		users[i].AvatarURL = fmt.Sprintf("https://www.gravatar.com/avatar/%s?d=identicon", users[i].ID.String()[:8])
	}

	// Calculate pagination
	totalPages := (total + int64(limit) - 1) / int64(limit)
	prevPage := page - 1
	if prevPage < 1 {
		prevPage = 1
	}
	nextPage := page + 1
	if nextPage > int(totalPages) {
		nextPage = int(totalPages)
	}

	data := map[string]interface{}{
		"Title": "User Management",
		"Users": users,
		"Pagination": map[string]interface{}{
			"Current": page,
			"Total":   int(totalPages),
			"Prev":    prevPage,
			"Next":    nextPage,
		},
	}

	return c.Render(http.StatusOK, "admin_users", data)
}

// SettingsPage renders the settings management page
func (h *AdminHandler) SettingsPage(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Get all system config entries
	var configs []models.SystemConfig
	if err := h.db.Find(&configs).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch settings")
	}

	// Convert to map
	settings := map[string]interface{}{}
	for _, config := range configs {
		settings[config.Key] = config.Value
	}

	// Add defaults for missing keys
	defaults := map[string]interface{}{
		"auth.signup_enabled":     true,
		"auth.2fa_enabled":        true,
		"auth.session_timeout":    86400,
		"gist.max_files":          10,
		"gist.max_file_size":      10485760, // 10MB
		"user.max_gists":          1000,
		"search.enabled":          true,
		"webhook.enabled":         true,
		"webhook.max_per_user":    10,
		"email.enabled":           false,
		"backup.enabled":          true,
		"backup.interval":         86400, // Daily
		"maintenance.mode":        false,
		"maintenance.message":     "System is under maintenance",
	}

	for key, defaultValue := range defaults {
		if _, exists := settings[key]; !exists {
			settings[key] = defaultValue
		}
	}

	data := map[string]interface{}{
		"Title":    "System Settings",
		"Settings": settings,
	}

	return c.Render(http.StatusOK, "admin_settings", data)
}

// UpdateAdminRoutes updates the routes to use the new template-based handlers
func (h *AdminHandler) UpdateAdminRoutes(g *echo.Group) {
	// HTML pages
	g.GET("/admin", h.DashboardPage)
	g.GET("/admin/dashboard", h.DashboardPage)
	g.GET("/admin/users", h.UsersPage)
	g.GET("/admin/settings", h.SettingsPage)
	
	// API endpoints (keep existing functionality)
	g.GET("/admin/api/dashboard", h.Dashboard)
	g.GET("/admin/api/users", h.GetUsers)
	g.GET("/admin/api/users/:id", h.GetUser)
	g.PUT("/admin/api/users/:id", h.UpdateUser)
	g.DELETE("/admin/api/users/:id", h.DeleteUser)
	g.GET("/admin/api/system", h.GetSystemInfo)
	g.GET("/admin/api/settings", h.GetSettings)
	g.PUT("/admin/api/settings", h.UpdateSettings)
	g.POST("/admin/api/backup", h.CreateBackup)
	g.GET("/admin/api/audit", h.GetAuditLogs)
}