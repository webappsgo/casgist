package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// AdminHandler handles admin-related endpoints
type AdminHandler struct {
	db     *gorm.DB
	config *viper.Viper
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(db *gorm.DB, config *viper.Viper) *AdminHandler {
	return &AdminHandler{
		db:     db,
		config: config,
	}
}

// Dashboard returns admin dashboard data
func (h *AdminHandler) Dashboard(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Gather statistics
	stats := map[string]interface{}{}

	// User stats
	var userCount int64
	h.db.Model(&models.User{}).Where("deleted_at IS NULL").Count(&userCount)
	stats["total_users"] = userCount

	var adminCount int64
	h.db.Model(&models.User{}).Where("deleted_at IS NULL AND is_admin = ?", true).Count(&adminCount)
	stats["admin_users"] = adminCount

	// Gist stats
	var gistCount int64
	h.db.Model(&models.Gist{}).Where("deleted_at IS NULL").Count(&gistCount)
	stats["total_gists"] = gistCount

	var publicGistCount int64
	h.db.Model(&models.Gist{}).Where("deleted_at IS NULL AND is_public = ?", true).Count(&publicGistCount)
	stats["public_gists"] = publicGistCount

	// Organization stats
	var orgCount int64
	h.db.Model(&models.Organization{}).Where("deleted_at IS NULL").Count(&orgCount)
	stats["total_organizations"] = orgCount

	// Recent activity
	var recentUsers []models.User
	h.db.Order("created_at DESC").Limit(5).Find(&recentUsers)

	var recentGists []models.Gist
	h.db.Preload("User").Order("created_at DESC").Limit(5).Find(&recentGists)

	// System info
	systemInfo := map[string]interface{}{
		"version":     h.config.GetString("version"),
		"environment": h.config.GetString("environment"),
		"database":    h.config.GetString("database.type"),
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"stats":        stats,
		"recent_users": recentUsers,
		"recent_gists": recentGists,
		"system_info":  systemInfo,
	})
}

// GetUsers returns paginated list of users
func (h *AdminHandler) GetUsers(c echo.Context) error {
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
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// Parse filters
	search := c.QueryParam("search")
	role := c.QueryParam("role") // admin, user
	status := c.QueryParam("status") // active, suspended

	// Build query
	query := h.db.Model(&models.User{})

	if search != "" {
		query = query.Where("username LIKE ? OR email LIKE ? OR display_name LIKE ?", 
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

	return c.JSON(http.StatusOK, map[string]interface{}{
		"users": users,
		"total": total,
		"page":  page,
		"limit": limit,
		"pages": (total + int64(limit) - 1) / int64(limit),
	})
}

// GetUser returns detailed user information
func (h *AdminHandler) GetUser(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Get user ID from URL
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID")
	}

	// Find user
	var user models.User
	if err := h.db.First(&user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch user")
	}

	// Get user stats
	var gistCount int64
	h.db.Model(&models.Gist{}).Where("user_id = ?", userID).Count(&gistCount)

	var orgCount int64
	h.db.Model(&models.OrganizationMember{}).Where("user_id = ?", userID).Count(&orgCount)

	// Get recent activity
	var recentGists []models.Gist
	h.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(10).
		Find(&recentGists)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"user": user,
		"stats": map[string]interface{}{
			"gist_count":         gistCount,
			"organization_count": orgCount,
		},
		"recent_gists": recentGists,
	})
}

// UpdateUser updates user information
func (h *AdminHandler) UpdateUser(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Get user ID from URL
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID")
	}

	// Find user
	var user models.User
	if err := h.db.First(&user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch user")
	}

	// Parse request
	var req struct {
		Username    string `json:"username"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		IsAdmin     *bool  `json:"is_admin"`
		IsSuspended *bool  `json:"is_suspended"`
		MaxGists    *int   `json:"max_gists"`
		MaxFileSize *int64 `json:"max_file_size"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// Update fields
	updates := map[string]interface{}{}
	
	if req.Username != "" && req.Username != user.Username {
		// Check if username is available
		var count int64
		h.db.Model(&models.User{}).Where("username = ? AND id != ?", req.Username, userID).Count(&count)
		if count > 0 {
			return echo.NewHTTPError(http.StatusConflict, "Username already taken")
		}
		updates["username"] = req.Username
	}
	
	if req.Email != "" && req.Email != user.Email {
		// Check if email is available
		var count int64
		h.db.Model(&models.User{}).Where("email = ? AND id != ?", req.Email, userID).Count(&count)
		if count > 0 {
			return echo.NewHTTPError(http.StatusConflict, "Email already taken")
		}
		updates["email"] = req.Email
		updates["is_email_verified"] = false // Reset verification
	}
	
	if req.DisplayName != "" {
		updates["display_name"] = req.DisplayName
	}
	
	if req.IsAdmin != nil {
		updates["is_admin"] = *req.IsAdmin
	}
	
	if req.IsSuspended != nil {
		updates["is_suspended"] = *req.IsSuspended
	}
	
	if req.MaxGists != nil {
		updates["max_gists"] = *req.MaxGists
	}
	
	if req.MaxFileSize != nil {
		updates["max_file_size"] = *req.MaxFileSize
	}

	// Update user
	if len(updates) > 0 {
		if err := h.db.Model(&user).Updates(updates).Error; err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update user")
		}
	}

	// Reload user
	h.db.First(&user, userID)

	return c.JSON(http.StatusOK, user)
}

// DeleteUser deletes a user
func (h *AdminHandler) DeleteUser(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Get user ID from URL
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID")
	}

	// Cannot delete self
	currentUserID, _ := c.Get("user_id").(uuid.UUID)
	if userID == currentUserID {
		return echo.NewHTTPError(http.StatusBadRequest, "Cannot delete your own account")
	}

	// Find user
	var user models.User
	if err := h.db.First(&user, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch user")
	}

	// Start transaction
	tx := h.db.Begin()
	defer tx.Rollback()

	// Delete user's gists
	if err := tx.Where("user_id = ?", userID).Delete(&models.Gist{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete user gists")
	}

	// Remove from organizations
	if err := tx.Where("user_id = ?", userID).Delete(&models.OrganizationMember{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to remove from organizations")
	}

	// Remove from teams (skip for now since TeamMember model doesn't exist)
	// if err := tx.Where("user_id = ?", userID).Delete(&models.TeamMember{}).Error; err != nil {
	//	return echo.NewHTTPError(http.StatusInternalServerError, "Failed to remove from teams")
	// }

	// Delete user
	if err := tx.Delete(&user).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete user")
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to commit transaction")
	}

	return c.NoContent(http.StatusNoContent)
}

// GetSystemInfo returns system information
func (h *AdminHandler) GetSystemInfo(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Database info
	dbInfo := map[string]interface{}{
		"type": h.config.GetString("database.type"),
		"host": h.config.GetString("database.host"),
		"name": h.config.GetString("database.name"),
	}

	// Storage info
	storageInfo := map[string]interface{}{
		"data_dir":   h.config.GetString("data_dir"),
		"repos_path": h.config.GetString("git.repos_path"),
	}

	// Configuration
	configInfo := map[string]interface{}{
		"server_url":       h.config.GetString("server.url"),
		"server_port":      h.config.GetInt("server.port"),
		"environment":      h.config.GetString("environment"),
		"debug":           h.config.GetBool("debug"),
		"signup_enabled":   h.config.GetBool("auth.signup_enabled"),
		"email_enabled":    h.config.GetBool("email.enabled"),
		"2fa_enabled":      h.config.GetBool("auth.2fa_enabled"),
		"webhook_enabled":  h.config.GetBool("webhook.enabled"),
	}

	// Get database size
	var dbSize struct {
		Size int64 `gorm:"column:size"`
	}
	
	if h.config.GetString("database.type") == "sqlite" {
		// For SQLite, get file size
		h.db.Raw("SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()").Scan(&dbSize)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"version":       h.config.GetString("version"),
		"database":      dbInfo,
		"storage":       storageInfo,
		"configuration": configInfo,
		"database_size": dbSize.Size,
		"uptime":        time.Since(time.Now()).String(), // TODO: Track actual uptime
	})
}

// GetSettings returns system settings
func (h *AdminHandler) GetSettings(c echo.Context) error {
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

	return c.JSON(http.StatusOK, settings)
}

// UpdateSettings updates system settings
func (h *AdminHandler) UpdateSettings(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Parse request
	var settings map[string]interface{}
	if err := c.Bind(&settings); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// Update each setting
	for key, value := range settings {
		var config models.SystemConfig
		err := h.db.Where("key = ?", key).First(&config).Error
		
		if err == gorm.ErrRecordNotFound {
			// Create new setting
			valueStr := fmt.Sprintf("%v", value)
			config = models.SystemConfig{
				Key:   key,
				Value: valueStr,
			}
			if err := h.db.Create(&config).Error; err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create setting: "+key)
			}
		} else if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch setting: "+key)
		} else {
			// Update existing setting
			valueStr := fmt.Sprintf("%v", value)
			config.Value = valueStr
			if err := h.db.Save(&config).Error; err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update setting: "+key)
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]string{
		"message": "Settings updated successfully",
	})
}

// CreateBackup creates a system backup
func (h *AdminHandler) CreateBackup(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// TODO: Implement backup functionality
	return c.JSON(http.StatusNotImplemented, map[string]string{
		"message": "Backup functionality not yet implemented",
	})
}

// GetAuditLogs returns audit logs
func (h *AdminHandler) GetAuditLogs(c echo.Context) error {
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
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// Parse filters
	userID := c.QueryParam("user_id")
	action := c.QueryParam("action")
	resource := c.QueryParam("resource")

	// Build query
	query := h.db.Model(&models.AuditLog{})

	if userID != "" {
		if uid, err := uuid.Parse(userID); err == nil {
			query = query.Where("user_id = ?", uid)
		}
	}

	if action != "" {
		query = query.Where("action = ?", action)
	}

	if resource != "" {
		query = query.Where("resource = ?", resource)
	}

	// Count total
	var total int64
	query.Count(&total)

	// Get logs
	var logs []models.AuditLog
	offset := (page - 1) * limit
	if err := query.
		Preload("User").
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&logs).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch audit logs")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"logs":  logs,
		"total": total,
		"page":  page,
		"limit": limit,
		"pages": (total + int64(limit) - 1) / int64(limit),
	})
}

// RegisterRoutes registers admin routes
func (h *AdminHandler) RegisterRoutes(g *echo.Group) {
	// HTML pages
	g.GET("/admin", h.DashboardPage)
	g.GET("/admin/dashboard", h.DashboardPage)
	g.GET("/admin/users", h.UsersPage)
	g.GET("/admin/settings", h.SettingsPage)
	
	// API endpoints
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