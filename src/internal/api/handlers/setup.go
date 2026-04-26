package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/casapps/casgist/src/internal/auth"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// SetupHandler handles setup wizard endpoints
type SetupHandler struct {
	db         *gorm.DB
	config     *viper.Viper
	auth       *auth.AuthService
}

// NewSetupHandler creates a new setup handler
func NewSetupHandler(db *gorm.DB, config *viper.Viper, authService *auth.AuthService) *SetupHandler {
	return &SetupHandler{
		db:     db,
		config: config,
		auth:   authService,
	}
}

// GetStatus returns the current setup status
func (h *SetupHandler) GetStatus(c echo.Context) error {
	status := map[string]interface{}{
		"initialized": false,
		"step":        "welcome",
		"completed":   false,
	}

	// Check if system is initialized (has at least one admin user)
	var adminCount int64
	h.db.Model(&models.User{}).Where("is_admin = ? AND deleted_at IS NULL", true).Count(&adminCount)
	
	if adminCount > 0 {
		status["initialized"] = true
		status["completed"] = true
		
		// Check current configuration status
		checks := h.performSystemChecks()
		status["checks"] = checks
	} else {
		// Determine current step
		var userCount int64
		h.db.Model(&models.User{}).Where("deleted_at IS NULL").Count(&userCount)
		
		if userCount == 0 {
			status["step"] = "admin"
		} else {
			status["step"] = "configuration"
		}
	}

	return c.JSON(http.StatusOK, status)
}

// CreateFirstAdmin creates the first admin user
func (h *SetupHandler) CreateFirstAdmin(c echo.Context) error {
	// Check if admin already exists
	var adminCount int64
	h.db.Model(&models.User{}).Where("is_admin = ? AND deleted_at IS NULL", true).Count(&adminCount)
	
	if adminCount > 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "Admin user already exists")
	}

	// Parse request
	var req struct {
		Username    string `json:"username" validate:"required,min=3,max=32"`
		Email       string `json:"email" validate:"required,email"`
		Password    string `json:"password" validate:"required,min=8"`
		DisplayName string `json:"display_name"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Check if username is already taken
	var existingCount int64
	h.db.Model(&models.User{}).Where("username = ? OR email = ?", req.Username, req.Email).Count(&existingCount)
	if existingCount > 0 {
		return echo.NewHTTPError(http.StatusConflict, "Username or email already exists")
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to hash password")
	}

	// Create admin user
	user := &models.User{
		ID:               uuid.New(),
		Username:         req.Username,
		Email:            req.Email,
		PasswordHash:     string(hashedPassword),
		DisplayName:      req.DisplayName,
		IsAdmin:          true,
		EmailVerified:    true, // Auto-verify first admin
		IsActive:         true,
	}

	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}

	// Create user
	if err := h.db.Create(user).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create admin user")
	}

	// Generate tokens
	sessionID := uuid.New()
	tokenPair, err := h.auth.GenerateTokenPair(user, sessionID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to generate tokens")
	}

	// Create session
	session := &models.Session{
		ID:           sessionID,
		UserID:       user.ID,
		Token:        tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		UserAgent:    c.Request().UserAgent(),
		IPAddress:    c.RealIP(),
		ExpiresAt:    tokenPair.ExpiresAt,
		LastUsedAt:   time.Now(),
	}

	if err := h.db.Create(session).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create session")
	}

	// Initialize default settings
	h.initializeDefaultSettings()

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"user":          user,
		"access_token":  tokenPair.AccessToken,
		"refresh_token": tokenPair.RefreshToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"message":       "Admin account created successfully",
	})
}

// GetSteps returns setup wizard steps
func (h *SetupHandler) GetSteps(c echo.Context) error {
	steps := []map[string]interface{}{
		{
			"id":          "welcome",
			"title":       "Welcome to CasGists",
			"description": "Let's set up your self-hosted gist service",
			"required":    true,
		},
		{
			"id":          "admin",
			"title":       "Create Admin Account",
			"description": "Set up the first administrator account",
			"required":    true,
		},
		{
			"id":          "database",
			"title":       "Database Configuration",
			"description": "Configure your database connection",
			"required":    true,
		},
		{
			"id":          "storage",
			"title":       "Storage Settings",
			"description": "Configure where gists and repositories are stored",
			"required":    true,
		},
		{
			"id":          "server",
			"title":       "Server Settings",
			"description": "Configure server URL and network settings",
			"required":    true,
		},
		{
			"id":          "email",
			"title":       "Email Configuration",
			"description": "Set up email notifications (optional)",
			"required":    false,
		},
		{
			"id":          "security",
			"title":       "Security Settings",
			"description": "Configure security and authentication options",
			"required":    true,
		},
		{
			"id":          "features",
			"title":       "Features",
			"description": "Enable or disable optional features",
			"required":    false,
		},
		{
			"id":          "review",
			"title":       "Review & Complete",
			"description": "Review your settings and complete setup",
			"required":    true,
		},
	}

	// Check current progress
	var adminCount int64
	h.db.Model(&models.User{}).Where("is_admin = ? AND deleted_at IS NULL", true).Count(&adminCount)
	
	currentStep := "welcome"
	if adminCount > 0 {
		currentStep = "database"
		
		// Check if database is properly configured
		if h.isDatabaseConfigured() {
			currentStep = "storage"
		}
		if h.isStorageConfigured() {
			currentStep = "server"
		}
		if h.isServerConfigured() {
			currentStep = "email"
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"steps":        steps,
		"current_step": currentStep,
		"progress":     h.calculateProgress(),
	})
}

// ProcessStep processes a setup wizard step
func (h *SetupHandler) ProcessStep(c echo.Context) error {
	step := c.Param("step")

	// Check if admin exists for all steps except welcome and admin
	if step != "welcome" && step != "admin" {
		var adminCount int64
		h.db.Model(&models.User{}).Where("is_admin = ? AND deleted_at IS NULL", true).Count(&adminCount)
		if adminCount == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "Please create an admin account first")
		}

		// Verify admin authentication
		isAdmin, _ := c.Get("is_admin").(bool)
		if !isAdmin {
			return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
		}
	}

	switch step {
	case "welcome":
		return h.processWelcomeStep(c)
	case "admin":
		return h.CreateFirstAdmin(c)
	case "database":
		return h.processDatabaseStep(c)
	case "storage":
		return h.processStorageStep(c)
	case "server":
		return h.processServerStep(c)
	case "email":
		return h.processEmailStep(c)
	case "security":
		return h.processSecurityStep(c)
	case "features":
		return h.processFeaturesStep(c)
	case "review":
		return h.processReviewStep(c)
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid setup step")
	}
}

func (h *SetupHandler) processWelcomeStep(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Welcome to CasGists setup",
		"next":    "admin",
	})
}

func (h *SetupHandler) processDatabaseStep(c echo.Context) error {
	var req struct {
		Type     string `json:"type" validate:"required,oneof=sqlite postgresql mysql"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Name     string `json:"name"`
		Username string `json:"username"`
		Password string `json:"password"`
		SSLMode  string `json:"ssl_mode"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Save database configuration
	configs := map[string]interface{}{
		"database.type":     req.Type,
		"database.host":     req.Host,
		"database.port":     req.Port,
		"database.name":     req.Name,
		"database.username": req.Username,
		"database.password": req.Password,
		"database.ssl_mode": req.SSLMode,
	}

	for key, value := range configs {
		h.saveConfig(key, value)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Database configuration saved",
		"next":    "storage",
	})
}

func (h *SetupHandler) processStorageStep(c echo.Context) error {
	var req struct {
		DataDir   string `json:"data_dir" validate:"required"`
		ReposPath string `json:"repos_path"`
		TempDir   string `json:"temp_dir"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Default paths
	if req.ReposPath == "" {
		req.ReposPath = req.DataDir + "/repos"
	}
	if req.TempDir == "" {
		req.TempDir = req.DataDir + "/tmp"
	}

	// Save storage configuration
	configs := map[string]interface{}{
		"data_dir":       req.DataDir,
		"git.repos_path": req.ReposPath,
		"temp_dir":       req.TempDir,
	}

	for key, value := range configs {
		h.saveConfig(key, value)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Storage configuration saved",
		"next":    "server",
	})
}

func (h *SetupHandler) processServerStep(c echo.Context) error {
	var req struct {
		URL      string `json:"url" validate:"required,url"`
		Port     int    `json:"port" validate:"required,min=1,max=65535"`
		HTTPSEnabled bool   `json:"https_enabled"`
		CertFile     string `json:"cert_file"`
		KeyFile      string `json:"key_file"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Validate HTTPS settings
	if req.HTTPSEnabled && (req.CertFile == "" || req.KeyFile == "") {
		return echo.NewHTTPError(http.StatusBadRequest, "Certificate and key files required for HTTPS")
	}

	// Save server configuration
	configs := map[string]interface{}{
		"server.url":          req.URL,
		"server.port":         req.Port,
		"server.https_enabled": req.HTTPSEnabled,
		"server.cert_file":    req.CertFile,
		"server.key_file":     req.KeyFile,
	}

	for key, value := range configs {
		h.saveConfig(key, value)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Server configuration saved",
		"next":    "email",
	})
}

func (h *SetupHandler) processEmailStep(c echo.Context) error {
	var req struct {
		Enabled  bool   `json:"enabled"`
		Provider string `json:"provider"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		From     string `json:"from"`
		UseTLS   bool   `json:"use_tls"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// Save email configuration
	configs := map[string]interface{}{
		"email.enabled":  req.Enabled,
		"email.provider": req.Provider,
		"email.host":     req.Host,
		"email.port":     req.Port,
		"email.username": req.Username,
		"email.password": req.Password,
		"email.from":     req.From,
		"email.use_tls":  req.UseTLS,
	}

	for key, value := range configs {
		h.saveConfig(key, value)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Email configuration saved",
		"next":    "security",
	})
}

func (h *SetupHandler) processSecurityStep(c echo.Context) error {
	var req struct {
		SecretKey        string `json:"secret_key" validate:"required,min=32"`
		SignupEnabled    bool   `json:"signup_enabled"`
		RequireEmail     bool   `json:"require_email_verification"`
		Enable2FA        bool   `json:"enable_2fa"`
		SessionTimeout   int    `json:"session_timeout"`
		PasswordMinLength int   `json:"password_min_length"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Default values
	if req.SessionTimeout == 0 {
		req.SessionTimeout = 86400 // 24 hours
	}
	if req.PasswordMinLength == 0 {
		req.PasswordMinLength = 8
	}

	// Save security configuration
	configs := map[string]interface{}{
		"security.secret_key":         req.SecretKey,
		"auth.signup_enabled":         req.SignupEnabled,
		"auth.require_email_verification": req.RequireEmail,
		"auth.2fa_enabled":            req.Enable2FA,
		"auth.session_timeout":        req.SessionTimeout,
		"auth.password_min_length":    req.PasswordMinLength,
	}

	for key, value := range configs {
		h.saveConfig(key, value)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Security configuration saved",
		"next":    "features",
	})
}

func (h *SetupHandler) processFeaturesStep(c echo.Context) error {
	var req struct {
		SearchEnabled   bool `json:"search_enabled"`
		WebhookEnabled  bool `json:"webhook_enabled"`
		APIEnabled      bool `json:"api_enabled"`
		PublicGists     bool `json:"public_gists_enabled"`
		Organizations   bool `json:"organizations_enabled"`
		BackupEnabled   bool `json:"backup_enabled"`
		ImportEnabled   bool `json:"import_enabled"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// Save features configuration
	configs := map[string]interface{}{
		"features.search_enabled":        req.SearchEnabled,
		"features.webhook_enabled":       req.WebhookEnabled,
		"features.api_enabled":           req.APIEnabled,
		"features.public_gists_enabled":  req.PublicGists,
		"features.organizations_enabled": req.Organizations,
		"features.backup_enabled":        req.BackupEnabled,
		"features.import_enabled":        req.ImportEnabled,
	}

	for key, value := range configs {
		h.saveConfig(key, value)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Features configuration saved",
		"next":    "review",
	})
}

func (h *SetupHandler) processReviewStep(c echo.Context) error {
	// Mark setup as complete
	h.saveConfig("setup.completed", true)
	h.saveConfig("setup.completed_at", time.Now().Format(time.RFC3339))

	// Get all configuration for review
	var configs []models.SystemConfig
	h.db.Find(&configs)

	configMap := map[string]interface{}{}
	for _, config := range configs {
		configMap[config.Key] = config.Value
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Setup completed successfully!",
		"config":  configMap,
		"next_steps": []string{
			"Start using CasGists",
			"Create your first gist",
			"Invite team members",
			"Configure additional settings",
		},
	})
}

// Helper methods

func (h *SetupHandler) saveConfig(key string, value interface{}) error {
	var config models.SystemConfig
	err := h.db.Where("key = ?", key).First(&config).Error
	
	if err == gorm.ErrRecordNotFound {
		valueStr := fmt.Sprintf("%v", value)
		config = models.SystemConfig{
			Key:   key,
			Value: valueStr,
		}
		return h.db.Create(&config).Error
	} else if err != nil {
		return err
	} else {
		valueStr := fmt.Sprintf("%v", value)
		config.Value = valueStr
		return h.db.Save(&config).Error
	}
}

func (h *SetupHandler) performSystemChecks() map[string]bool {
	checks := map[string]bool{
		"database":      h.isDatabaseConfigured(),
		"storage":       h.isStorageConfigured(),
		"server":        h.isServerConfigured(),
		"email":         h.isEmailConfigured(),
		"security":      h.isSecurityConfigured(),
		"search":        h.isSearchConfigured(),
	}
	return checks
}

func (h *SetupHandler) isDatabaseConfigured() bool {
	// Check if database is properly configured
	var count int64
	h.db.Model(&models.SystemConfig{}).Where("key LIKE 'database.%'").Count(&count)
	return count >= 2 // At least type and connection info
}

func (h *SetupHandler) isStorageConfigured() bool {
	// Check if storage paths are configured
	var count int64
	h.db.Model(&models.SystemConfig{}).Where("key IN ('data_dir', 'git.repos_path')").Count(&count)
	return count >= 2
}

func (h *SetupHandler) isServerConfigured() bool {
	// Check if server settings are configured
	var count int64
	h.db.Model(&models.SystemConfig{}).Where("key IN ('server.url', 'server.port')").Count(&count)
	return count >= 2
}

func (h *SetupHandler) isEmailConfigured() bool {
	// Check if email is configured
	var config models.SystemConfig
	err := h.db.Where("key = 'email.enabled'").First(&config).Error
	if err != nil {
		return false
	}
	
	enabled := config.Value == "true"
	if !enabled {
		return true // Email is optional
	}
	
	// If enabled, check if properly configured
	var count int64
	h.db.Model(&models.SystemConfig{}).Where("key LIKE 'email.%'").Count(&count)
	return count >= 5
}

func (h *SetupHandler) isSecurityConfigured() bool {
	// Check if security settings are configured
	var count int64
	h.db.Model(&models.SystemConfig{}).Where("key = 'security.secret_key'").Count(&count)
	return count > 0
}

func (h *SetupHandler) isSearchConfigured() bool {
	// Search is configured by default
	return true
}

func (h *SetupHandler) calculateProgress() int {
	checks := h.performSystemChecks()
	completed := 0
	for _, check := range checks {
		if check {
			completed++
		}
	}
	return (completed * 100) / len(checks)
}

func (h *SetupHandler) initializeDefaultSettings() {
	defaults := map[string]interface{}{
		"auth.signup_enabled":     true,
		"auth.2fa_enabled":        true,
		"auth.session_timeout":    86400,
		"gist.max_files":          10,
		"gist.max_file_size":      10485760, // 10MB
		"user.max_gists":          1000,
		"search.enabled":          true,
		"webhook.enabled":         true,
		"email.enabled":           false,
		"backup.enabled":          true,
		"features.organizations_enabled": true,
		"features.public_gists_enabled":  true,
		"features.api_enabled":           true,
	}

	for key, value := range defaults {
		h.saveConfig(key, value)
	}
}

// RegisterRoutes registers setup routes
func (h *SetupHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/setup/status", h.GetStatus)
	g.GET("/setup/steps", h.GetSteps)
	g.POST("/setup/admin", h.CreateFirstAdmin)
	g.POST("/setup/step/:step", h.ProcessStep)
}