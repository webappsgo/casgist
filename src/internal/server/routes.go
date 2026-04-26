package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/casapps/casgist/src/internal/api/handlers"
	"github.com/casapps/casgist/src/internal/auth"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/labstack/echo/v4"
)

// setupRoutes configures all application routes
func (s *Server) setupRoutes() {
	// Create middleware
	authMiddleware := auth.NewMiddleware(s.auth)

	// Health check
	s.echo.GET("/health", s.handleHealth)
	s.echo.GET("/healthz", s.handleHealthz)

	// CLI script generation endpoint
	s.echo.GET("/cli", s.handleCLIScript)
	s.echo.GET("/cli.sh", s.handleCLIScript)

	// Static files (now handled in setupStaticRoutes)
	s.setupStaticRoutes()

	// Public routes
	s.echo.GET("/", s.handleHome)
	s.echo.GET("/login", s.handleLoginPage)
	s.echo.GET("/register", s.handleRegisterPage)

	// Web gist routes (with auth)
	s.echo.GET("/gists", s.handleGistListPage, authMiddleware.Auth())
	s.echo.GET("/gists/new", s.handleGistNewPage, authMiddleware.Auth())
	s.echo.GET("/gists/:id", s.handleGistViewPage)

	// Authentication routes
	authGroup := s.echo.Group("/auth")
	authGroup.POST("/login", s.handleLogin)
	authGroup.POST("/register", s.handleRegister)
	authGroup.POST("/logout", s.handleLogout, authMiddleware.Auth())
	authGroup.POST("/refresh", s.handleRefreshToken)
	authGroup.GET("/2fa/setup", s.handle2FASetup, authMiddleware.Auth())
	authGroup.POST("/2fa/verify", s.handle2FAVerify, authMiddleware.Auth())
	authGroup.POST("/2fa/disable", s.handle2FADisable, authMiddleware.Auth())

	// API v1 routes
	apiV1 := s.echo.Group("/api/v1")
	s.setupAPIv1Routes(apiV1)

	// Search routes
	searchHandler := handlers.NewSearchHandler(s.searchManager, s.config, s.db)
	searchHandler.RegisterRoutes(apiV1)

	// Gist API routes
	gistGroup := s.echo.Group("/api/gists", authMiddleware.Auth())
	gistGroup.GET("", s.handleGetGists)
	gistGroup.POST("", s.handleCreateGist)
	gistGroup.GET("/:id", s.handleGetGist)
	gistGroup.PUT("/:id", s.handleUpdateGist)
	gistGroup.DELETE("/:id", s.handleDeleteGist)
	gistGroup.POST("/:id/star", s.handleStarGist)
	gistGroup.DELETE("/:id/star", s.handleUnstarGist)
	gistGroup.GET("/:id/stars", s.handleGetStars)
	gistGroup.POST("/:id/fork", s.handleForkGist)
	gistGroup.GET("/:id/forks", s.handleGetForks)
	gistGroup.GET("/:id/comments", s.handleGetComments)
	gistGroup.POST("/:id/comments", s.handleCreateComment)

	// User routes
	userGroup := s.echo.Group("/users", authMiddleware.Auth())
	userGroup.GET("/me", s.handleGetCurrentUser)
	userGroup.PUT("/me", s.handleUpdateCurrentUser)
	userGroup.GET("/:username", s.handleGetUser)
	userGroup.POST("/:username/follow", s.handleFollowUser)
	userGroup.DELETE("/:username/follow", s.handleUnfollowUser)

	// Organization routes
	// orgGroup := s.echo.Group("/orgs", authMiddleware.Auth())
	// orgGroup.GET("", s.handleGetOrganizations)
	// orgGroup.POST("", s.handleCreateOrganization)
	// orgGroup.GET("/:org", s.handleGetOrganization)
	// orgGroup.PUT("/:org", s.handleUpdateOrganization)
	// orgGroup.DELETE("/:org", s.handleDeleteOrganization)
	// orgGroup.GET("/:org/members", s.handleGetOrgMembers)
	// orgGroup.POST("/:org/members/:username", s.handleAddOrgMember)
	// orgGroup.DELETE("/:org/members/:username", s.handleRemoveOrgMember)

	// Admin routes
	// adminGroup := s.echo.Group("/admin", authMiddleware.Auth(), authMiddleware.RequireAdmin())
	// adminGroup.GET("/dashboard", s.handleAdminDashboard)
	// adminGroup.GET("/users", s.handleAdminGetUsers)
	// adminGroup.GET("/system", s.handleAdminGetSystem)
	// adminGroup.GET("/audit", s.handleAdminGetAuditLogs)

	// Setup wizard routes (no auth required initially)
	setupGroup := s.echo.Group("/setup")
	setupGroup.GET("", s.handleSetupWizard)
	setupGroup.POST("/step/:step", s.handleSetupStep)
	setupGroup.GET("/status", s.handleSetupStatus)

	// TODO: Re-enable after implementing proper auth middleware
	// Webhook routes
	// webhookGroup := s.echo.Group("/webhooks", authMiddleware.Auth())
	// webhookGroup.GET("", s.handleGetWebhooks)
	// webhookGroup.POST("", s.handleCreateWebhook)
	// webhookGroup.GET("/:id", s.handleGetWebhook)
	// webhookGroup.PUT("/:id", s.handleUpdateWebhook)
	// webhookGroup.DELETE("/:id", s.handleDeleteWebhook)
	// webhookGroup.POST("/:id/test", s.handleTestWebhook)

	// Public gist viewing (short URLs)
	s.echo.GET("/g/:id", s.handlePublicGist, authMiddleware.OptionalAuth())
	s.echo.GET("/raw/:id/:file", s.handleRawFile, authMiddleware.OptionalAuth())

	// Catch-all for 404
	s.echo.RouteNotFound("/*", s.handle404)
}

// Placeholder handlers - these would be implemented properly
func (s *Server) handleHome(c echo.Context) error {
	return c.Render(http.StatusOK, "home", map[string]interface{}{
		"Title": "Welcome",
		"Gists": []interface{}{}, // TODO: Get recent public gists
	})
}

func (s *Server) handleLogin(c echo.Context) error {
	handler := handlers.NewAuthHandler(s.db, s.auth, s.config)
	return handler.Login(c)
}

func (s *Server) handleRegister(c echo.Context) error {
	handler := handlers.NewAuthHandler(s.db, s.auth, s.config)
	return handler.Register(c)
}

func (s *Server) handleLogout(c echo.Context) error {
	handler := handlers.NewAuthHandler(s.db, s.auth, s.config)
	return handler.Logout(c)
}

func (s *Server) handleRefreshToken(c echo.Context) error {
	handler := handlers.NewAuthHandler(s.db, s.auth, s.config)
	return handler.RefreshToken(c)
}

func (s *Server) handle2FASetup(c echo.Context) error {
	// Get user from JWT token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get user from database
	var user models.User
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "User not found")
	}

	// Check if 2FA is already enabled
	if user.TwoFactorEnabled {
		return echo.NewHTTPError(http.StatusBadRequest, "2FA is already enabled")
	}

	// Generate TOTP setup
	totpService := auth.NewTOTPService("CasGists")
	setup, err := totpService.GenerateTOTP(user.Username)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to generate 2FA setup")
	}

	// Store secret temporarily (user must verify before enabling)
	if err := s.db.Model(&user).Update("two_factor_secret", setup.Secret).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save 2FA secret")
	}

	// Generate recovery codes
	recoveryCodes, err := auth.GenerateRecoveryCodes(8)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to generate recovery codes")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"secret":         setup.Secret,
		"url":            setup.URL,
		"qr_code":        setup.QRCode,
		"recovery_codes": recoveryCodes,
		"enabled":        false,
	})
}

func (s *Server) handle2FAVerify(c echo.Context) error {
	// Get user from JWT token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request body
	var req struct {
		Code string `json:"code" validate:"required"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body")
	}

	// Get user from database
	var user models.User
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "User not found")
	}

	// Check if user has a 2FA secret
	if user.TwoFactorSecret == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "2FA setup required first")
	}

	// Validate TOTP code
	totpService := auth.NewTOTPService("CasGists")
	if !totpService.ValidateTOTP(user.TwoFactorSecret, req.Code) {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid 2FA code")
	}

	// Enable 2FA for user
	if err := s.db.Model(&user).Update("two_factor_enabled", true).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to enable 2FA")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"enabled": true,
		"message": "2FA enabled successfully",
	})
}

func (s *Server) handle2FADisable(c echo.Context) error {
	// Get user from JWT token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request body
	var req struct {
		Password string `json:"password" validate:"required"`
		Code     string `json:"code" validate:"required"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body")
	}

	// Get user from database
	var user models.User
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "User not found")
	}

	// Check if 2FA is enabled
	if !user.TwoFactorEnabled {
		return echo.NewHTTPError(http.StatusBadRequest, "2FA is not enabled")
	}

	// Verify password
	if !auth.VerifyPassword(req.Password, user.PasswordHash) {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid password")
	}

	// Validate TOTP code
	totpService := auth.NewTOTPService("CasGists")
	if !totpService.ValidateTOTP(user.TwoFactorSecret, req.Code) {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid 2FA code")
	}

	// Disable 2FA
	updates := map[string]interface{}{
		"two_factor_enabled": false,
		"two_factor_secret":  "",
	}
	if err := s.db.Model(&user).Updates(updates).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to disable 2FA")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"enabled": false,
		"message": "2FA disabled successfully",
	})
}

func (s *Server) handleGetGists(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil) // Git operations optional
	return handler.List(c)
}

func (s *Server) handleCreateGist(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil) // Git operations optional
	return handler.Create(c)
}

func (s *Server) handleGetGist(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil) // Git operations optional
	return handler.Get(c)
}

func (s *Server) handleUpdateGist(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil) // Git operations optional
	return handler.Update(c)
}

func (s *Server) handleDeleteGist(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil) // Git operations optional
	return handler.Delete(c)
}

// Additional handlers
func (s *Server) handleStarGist(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil)
	return handler.Star(c)
}

func (s *Server) handleUnstarGist(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil)
	return handler.Unstar(c)
}

func (s *Server) handleForkGist(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil)
	return handler.Fork(c)
}

func (s *Server) handleGetStars(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil)
	return handler.GetStars(c)
}

func (s *Server) handleGetForks(c echo.Context) error {
	handler := handlers.NewGistHandler(s.db, s.config, nil)
	return handler.GetForks(c)
}

func (s *Server) handleGetComments(c echo.Context) error {
	gistID := c.Param("gistId")
	if gistID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Gist ID required")
	}

	// Verify gist exists and user has access
	var gist models.Gist
	if err := s.db.First(&gist, "id = ?", gistID).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Gist not found")
	}

	// Check visibility permissions
	userID, _ := s.auth.GetUserIDFromToken(c)
	if gist.Visibility == "private" && gist.UserID != nil && *gist.UserID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Get comments with user information
	var comments []models.GistComment
	if err := s.db.Preload("User").Where("gist_id = ? AND deleted_at IS NULL", gistID).
		Order("created_at ASC").Find(&comments).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get comments")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"comments": comments,
		"total":    len(comments),
	})
}

func (s *Server) handleCreateComment(c echo.Context) error {
	gistID := c.Param("gistId")
	if gistID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Gist ID required")
	}

	// Get user from token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request body
	var req struct {
		Content string `json:"content" validate:"required"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body")
	}

	// Validate content length
	if len(req.Content) == 0 || len(req.Content) > 1000 {
		return echo.NewHTTPError(http.StatusBadRequest, "Comment content must be 1-1000 characters")
	}

	// Verify gist exists and user has access
	var gist models.Gist
	if err := s.db.First(&gist, "id = ?", gistID).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Gist not found")
	}

	// Check visibility permissions
	if gist.Visibility == "private" && gist.UserID != nil && *gist.UserID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Create comment
	comment := models.GistComment{
		GistID:  gist.ID,
		UserID:  userID,
		Content: req.Content,
	}

	if err := s.db.Create(&comment).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create comment")
	}

	// Load user info for response
	if err := s.db.Preload("User").First(&comment, comment.ID).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load comment")
	}

	return c.JSON(http.StatusCreated, comment)
}

func (s *Server) handleGetCurrentUser(c echo.Context) error {
	handler := handlers.NewUserHandler(s.db, s.config)
	return handler.GetCurrent(c)
}

func (s *Server) handleUpdateCurrentUser(c echo.Context) error {
	handler := handlers.NewUserHandler(s.db, s.config)
	return handler.Update(c)
}

func (s *Server) handleGetUser(c echo.Context) error {
	handler := handlers.NewUserHandler(s.db, s.config)
	return handler.Get(c)
}

func (s *Server) handleFollowUser(c echo.Context) error {
	targetUsername := c.Param("username")
	if targetUsername == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Username required")
	}

	// Get current user from token
	followerID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Find target user
	var targetUser models.User
	if err := s.db.First(&targetUser, "username = ?", targetUsername).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "User not found")
	}

	// Can't follow yourself
	if followerID == targetUser.ID {
		return echo.NewHTTPError(http.StatusBadRequest, "Cannot follow yourself")
	}

	// Check if already following
	var existingFollow models.UserFollow
	err = s.db.First(&existingFollow, "follower_id = ? AND following_id = ?", followerID, targetUser.ID).Error
	if err == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Already following this user")
	}

	// Create follow relationship
	follow := models.UserFollow{
		FollowerID:  followerID,
		FollowingID: targetUser.ID,
	}

	if err := s.db.Create(&follow).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to follow user")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"following": true,
		"message":   fmt.Sprintf("Now following %s", targetUser.Username),
	})
}

func (s *Server) handleUnfollowUser(c echo.Context) error {
	targetUsername := c.Param("username")
	if targetUsername == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Username required")
	}

	// Get current user from token
	followerID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Find target user
	var targetUser models.User
	if err := s.db.First(&targetUser, "username = ?", targetUsername).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "User not found")
	}

	// Delete follow relationship
	result := s.db.Where("follower_id = ? AND following_id = ?", followerID, targetUser.ID).Delete(&models.UserFollow{})
	if result.Error != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to unfollow user")
	}

	if result.RowsAffected == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "Not following this user")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"following": false,
		"message":   fmt.Sprintf("Unfollowed %s", targetUser.Username),
	})
}

func (s *Server) handleGetOrganizations(c echo.Context) error {
	// Get user from token (if authenticated)
	userID, err := s.auth.GetUserIDFromToken(c)
	var organizations []models.Organization

	if err != nil {
		// Public view - only show public organizations
		if err := s.db.Where("is_public = ?", true).Find(&organizations).Error; err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get organizations")
		}
	} else {
		// Authenticated user - show public orgs + user's private orgs
		query := `
			SELECT DISTINCT o.* FROM organizations o
			LEFT JOIN organization_members om ON o.id = om.organization_id
			WHERE o.is_public = true 
			   OR (om.user_id = ? AND o.deleted_at IS NULL)
			ORDER BY o.name
		`
		if err := s.db.Raw(query, userID).Scan(&organizations).Error; err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get organizations")
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"organizations": organizations,
		"total":         len(organizations),
	})
}

func (s *Server) handleCreateOrganization(c echo.Context) error {
	// Get user from token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request body
	var req struct {
		Name        string `json:"name" validate:"required"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Website     string `json:"website"`
		Location    string `json:"location"`
		IsPublic    bool   `json:"is_public"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body")
	}

	// Validate name
	if len(req.Name) < 3 || len(req.Name) > 39 {
		return echo.NewHTTPError(http.StatusBadRequest, "Organization name must be 3-39 characters")
	}

	// Check if organization name exists
	var existing models.Organization
	if err := s.db.Where("name = ?", req.Name).First(&existing).Error; err == nil {
		return echo.NewHTTPError(http.StatusConflict, "Organization name already exists")
	}

	// Create organization
	org := models.Organization{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Website:     req.Website,
		Location:    req.Location,
		IsPublic:    req.IsPublic,
	}

	if err := s.db.Create(&org).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create organization")
	}

	// Add creator as owner
	member := models.OrganizationMember{
		OrganizationID: org.ID,
		UserID:         userID,
		Role:           "owner",
	}

	if err := s.db.Create(&member).Error; err != nil {
		// If member creation fails, delete the organization
		s.db.Delete(&org)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create organization membership")
	}

	return c.JSON(http.StatusCreated, org)
}

func (s *Server) handleGetOrganization(c echo.Context) error {
	orgName := c.Param("name")
	if orgName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Organization name required")
	}

	// Find organization
	var org models.Organization
	if err := s.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
	}

	// Check visibility
	if !org.IsPublic {
		// Check if user is a member
		userID, err := s.auth.GetUserIDFromToken(c)
		if err != nil {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}

		var member models.OrganizationMember
		if err := s.db.Where("organization_id = ? AND user_id = ?", org.ID, userID).First(&member).Error; err != nil {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
	}

	// Get member count
	var memberCount int64
	s.db.Model(&models.OrganizationMember{}).Where("organization_id = ?", org.ID).Count(&memberCount)

	// Get gist count
	var gistCount int64
	s.db.Model(&models.Gist{}).Where("organization_id = ? AND deleted_at IS NULL", org.ID).Count(&gistCount)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"organization": org,
		"member_count": memberCount,
		"gist_count":   gistCount,
	})
}

func (s *Server) handleUpdateOrganization(c echo.Context) error {
	orgName := c.Param("name")
	if orgName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Organization name required")
	}

	// Get user from token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Find organization and check permissions
	var org models.Organization
	if err := s.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
	}

	// Check if user is admin/owner
	var member models.OrganizationMember
	if err := s.db.Where("organization_id = ? AND user_id = ? AND role IN (?, ?)",
		org.ID, userID, "admin", "owner").First(&member).Error; err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Parse request body
	var req struct {
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Website     string `json:"website"`
		Location    string `json:"location"`
		IsPublic    *bool  `json:"is_public"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body")
	}

	// Update fields
	updates := map[string]interface{}{}
	if req.DisplayName != "" {
		updates["display_name"] = req.DisplayName
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Website != "" {
		updates["website"] = req.Website
	}
	if req.Location != "" {
		updates["location"] = req.Location
	}
	if req.IsPublic != nil {
		updates["is_public"] = *req.IsPublic
	}

	if err := s.db.Model(&org).Updates(updates).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update organization")
	}

	return c.JSON(http.StatusOK, org)
}

func (s *Server) handleDeleteOrganization(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Delete organization not implemented"})
}

func (s *Server) handleGetOrgMembers(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Get org members not implemented"})
}

func (s *Server) handleAddOrgMember(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Add org member not implemented"})
}

func (s *Server) handleRemoveOrgMember(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Remove org member not implemented"})
}

func (s *Server) handleAdminDashboard(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Admin dashboard not implemented"})
}

func (s *Server) handleAdminGetUsers(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Admin get users not implemented"})
}

func (s *Server) handleAdminGetSystem(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Admin get system not implemented"})
}

func (s *Server) handleAdminGetAuditLogs(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Admin get audit logs not implemented"})
}

func (s *Server) handleSetupWizard(c echo.Context) error {
	handler := handlers.NewSetupHandler(s.db, s.config, s.auth)

	// Check if setup is already completed - GetStatus handles the response directly
	// So we don't need to check status here, just continue to render template

	// If this is an HTML request, render the wizard template
	if c.Request().Header.Get("Accept") != "application/json" &&
		!strings.Contains(c.Request().Header.Get("Content-Type"), "application/json") {

		// Create step data for template - simplified for now
		steps := []map[string]interface{}{
			{
				"StepNumber":  1,
				"Title":       "Welcome",
				"Description": "System requirements check",
				"IsActive":    true,
				"IsComplete":  false,
			},
			{
				"StepNumber":  2,
				"Title":       "Admin Account",
				"Description": "Create first administrator",
				"IsActive":    false,
				"IsComplete":  false,
			},
			{
				"StepNumber":  3,
				"Title":       "Database",
				"Description": "Configure database connection",
				"IsActive":    false,
				"IsComplete":  false,
			},
			{
				"StepNumber":  4,
				"Title":       "Storage",
				"Description": "Configure data storage",
				"IsActive":    false,
				"IsComplete":  false,
			},
			{
				"StepNumber":  5,
				"Title":       "Server",
				"Description": "Server and network settings",
				"IsActive":    false,
				"IsComplete":  false,
			},
			{
				"StepNumber":  6,
				"Title":       "Email",
				"Description": "Email notifications (optional)",
				"IsActive":    false,
				"IsComplete":  false,
			},
			{
				"StepNumber":  7,
				"Title":       "Security",
				"Description": "Security and authentication",
				"IsActive":    false,
				"IsComplete":  false,
			},
			{
				"StepNumber":  8,
				"Title":       "Complete",
				"Description": "Finish setup",
				"IsActive":    false,
				"IsComplete":  false,
			},
		}

		return c.Render(http.StatusOK, "wizard", map[string]interface{}{
			"title": "Setup Wizard",
			"steps": steps,
		})
	}

	// Return JSON status for API requests
	return handler.GetStatus(c)
}

func (s *Server) handleSetupStep(c echo.Context) error {
	handler := handlers.NewSetupHandler(s.db, s.config, s.auth)
	return handler.ProcessStep(c)
}

func (s *Server) handleSetupStatus(c echo.Context) error {
	handler := handlers.NewSetupHandler(s.db, s.config, s.auth)
	return handler.GetStatus(c)
}

func (s *Server) handleGetWebhooks(c echo.Context) error {
	// Get user from token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get webhooks for user or organization
	var webhooks []models.Webhook
	query := s.db.Where("user_id = ?", userID)

	// Check if organization context
	if orgName := c.QueryParam("org"); orgName != "" {
		var org models.Organization
		if err := s.db.Where("name = ?", orgName).First(&org).Error; err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}

		// Check if user is member
		var member models.OrganizationMember
		if err := s.db.Where("organization_id = ? AND user_id = ?", org.ID, userID).First(&member).Error; err != nil {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}

		query = s.db.Where("organization_id = ?", org.ID)
	}

	if err := query.Find(&webhooks).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get webhooks")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"webhooks": webhooks,
		"total":    len(webhooks),
	})
}

func (s *Server) handleCreateWebhook(c echo.Context) error {
	// Get user from token
	userID, err := s.auth.GetUserIDFromToken(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request body
	var req struct {
		URL    string   `json:"url" validate:"required"`
		Secret string   `json:"secret"`
		Events []string `json:"events" validate:"required"`
		Org    string   `json:"organization"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body")
	}

	// Validate URL
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid webhook URL")
	}

	// Convert events to JSON string
	eventsJSON := strings.Join(req.Events, ",")

	// Create webhook
	webhook := models.Webhook{
		URL:      req.URL,
		Secret:   req.Secret,
		Events:   eventsJSON,
		UserID:   &userID,
		IsActive: true,
	}

	// Check for organization context
	if req.Org != "" {
		var org models.Organization
		if err := s.db.Where("name = ?", req.Org).First(&org).Error; err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}

		// Check permissions
		var member models.OrganizationMember
		if err := s.db.Where("organization_id = ? AND user_id = ? AND role IN (?, ?)",
			org.ID, userID, "admin", "owner").First(&member).Error; err != nil {
			return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
		}

		// Note: Current webhook model doesn't support organization ownership
		// This would need to be added to the model if organization webhooks are needed
		webhook.UserID = &userID
	}

	if err := s.db.Create(&webhook).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create webhook")
	}

	return c.JSON(http.StatusCreated, webhook)
}

func (s *Server) handleGetWebhook(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Get webhook not implemented"})
}

func (s *Server) handleUpdateWebhook(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Update webhook not implemented"})
}

func (s *Server) handleDeleteWebhook(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Delete webhook not implemented"})
}

func (s *Server) handleTestWebhook(c echo.Context) error {
	return c.JSON(http.StatusNotImplemented, map[string]string{"message": "Test webhook not implemented"})
}

func (s *Server) handlePublicGist(c echo.Context) error {
	gistID := c.Param("gistId")
	if gistID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Gist ID required")
	}

	// Find gist with files and user info
	var gist models.Gist
	if err := s.db.Preload("Files").Preload("User").Where("id = ? AND visibility = ? AND deleted_at IS NULL", gistID, "public").First(&gist).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Public gist not found")
	}

	// Increment view count
	go func() {
		s.db.Model(&gist).UpdateColumn("view_count", gist.ViewCount+1)
	}()

	return c.JSON(http.StatusOK, gist)
}

func (s *Server) handleRawFile(c echo.Context) error {
	gistID := c.Param("gistId")
	filename := c.Param("filename")
	if gistID == "" || filename == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Gist ID and filename required")
	}

	// Find gist and check visibility
	var gist models.Gist
	if err := s.db.First(&gist, "id = ?", gistID).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Gist not found")
	}

	// Check access permissions
	if gist.Visibility == "private" {
		userID, err := s.auth.GetUserIDFromToken(c)
		if err != nil || gist.UserID == nil || *gist.UserID != userID {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
	}

	// Find the specific file
	var file models.GistFile
	if err := s.db.Where("gist_id = ? AND filename = ?", gistID, filename).First(&file).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "File not found")
	}

	// Set content type based on file extension
	contentType := "text/plain; charset=utf-8"
	switch {
	case strings.HasSuffix(filename, ".json"):
		contentType = "application/json; charset=utf-8"
	case strings.HasSuffix(filename, ".xml"):
		contentType = "application/xml; charset=utf-8"
	case strings.HasSuffix(filename, ".html"):
		contentType = "text/html; charset=utf-8"
	case strings.HasSuffix(filename, ".css"):
		contentType = "text/css; charset=utf-8"
	case strings.HasSuffix(filename, ".js"):
		contentType = "application/javascript; charset=utf-8"
	}

	c.Response().Header().Set("Content-Type", contentType)
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))

	return c.String(http.StatusOK, file.Content)
}

// setupAPIv1Routes configures API v1 routes
func (s *Server) setupAPIv1Routes(g *echo.Group) {
	// Create handlers
	authHandler := handlers.NewAuthHandler(s.db, s.auth, s.config)
	gistHandler := handlers.NewGistHandler(s.db, s.config, nil) // Git operations optional
	userHandler := handlers.NewUserHandler(s.db, s.config)
	orgHandler := handlers.NewOrganizationHandler(s.db, s.config)
	teamHandler := handlers.NewTeamHandler(s.db, s.config)
	adminHandler := handlers.NewAdminHandler(s.db, s.config)
	setupHandler := handlers.NewSetupHandler(s.db, s.config, s.auth)
	migrationHandler := handlers.NewMigrationHandler(s.db, s.config)
	webhookHandler := handlers.NewWebhookHandler(s.db, s.config, s.webhookManager)
	backupHandler := handlers.NewBackupHandler(s.db, s.config)
	complianceHandler := handlers.NewComplianceHandler(s.db, s.config)
	offlineHandler := handlers.NewOfflineHandler(s.db)

	// Create middleware
	authMiddleware := auth.NewMiddleware(s.auth)

	// Health endpoints
	g.GET("/health", s.handleHealth)
	g.GET("/healthz", s.handleHealthz)

	// Auth endpoints
	g.POST("/auth/login", authHandler.Login)
	g.POST("/auth/register", authHandler.Register)
	g.POST("/auth/refresh", authHandler.RefreshToken)
	g.POST("/auth/logout", authHandler.Logout, authMiddleware.Auth())

	// Gist endpoints
	g.GET("/gists", gistHandler.List, authMiddleware.OptionalAuth())
	g.POST("/gists", gistHandler.Create, authMiddleware.Auth())
	g.GET("/gists/:id", gistHandler.Get, authMiddleware.OptionalAuth())
	g.PUT("/gists/:id", gistHandler.Update, authMiddleware.Auth())
	g.DELETE("/gists/:id", gistHandler.Delete, authMiddleware.Auth())

	// User endpoints
	g.GET("/users/:username", userHandler.Get, authMiddleware.OptionalAuth())
	g.GET("/users/:username/gists", userHandler.GetGists, authMiddleware.OptionalAuth())
	g.GET("/user", userHandler.GetCurrent, authMiddleware.Auth())
	g.PUT("/user", userHandler.Update, authMiddleware.Auth())

	// Organization endpoints
	orgHandler.RegisterRoutes(g)

	// Team endpoints
	teamHandler.RegisterRoutes(g)

	// Admin endpoints (protected by middleware)
	adminHandler.RegisterRoutes(g)

	// Setup endpoints
	setupHandler.RegisterRoutes(g)

	// Migration endpoints (protected by auth)
	migrationHandler.RegisterRoutes(g)

	// Webhook endpoints (protected by auth)
	webhookHandler.RegisterRoutes(g)

	// Backup endpoints (protected by admin middleware)
	backupHandler.RegisterRoutes(g)

	// Compliance endpoints
	complianceHandler.RegisterRoutes(g)

	// Offline/PWA endpoints
	offlineHandler.RegisterRoutes(s.echo.Group(""))
}

// Health check handler
func (s *Server) handleHealth(c echo.Context) error {
	health := map[string]interface{}{
		"status":  "healthy",
		"version": s.config.GetString("version"),
		"uptime":  s.getUptime(),
	}

	// Check database
	if err := s.db.Exec("SELECT 1").Error; err != nil {
		health["status"] = "unhealthy"
		health["database"] = "error"
	} else {
		health["database"] = "ok"
	}

	status := http.StatusOK
	if health["status"] == "unhealthy" {
		status = http.StatusServiceUnavailable
	}

	return c.JSON(status, health)
}

// Enhanced health check handler
func (s *Server) handleHealthz(c echo.Context) error {
	// Calculate uptime
	uptime := time.Since(s.startTime)
	days := int(uptime.Hours() / 24)
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60

	uptimeStr := ""
	if days > 0 {
		uptimeStr = fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	} else if hours > 0 {
		uptimeStr = fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		uptimeStr = fmt.Sprintf("%dm", minutes)
	}

	// Get version from config or default
	version := s.config.GetString("version")
	if version == "" {
		version = "1.0.0"
	}

	// Initialize health response
	healthz := map[string]interface{}{
		"status":    "healthy",
		"version":   version,
		"uptime":    uptimeStr,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"components": map[string]interface{}{
			"database": "healthy",
			"storage":  "healthy",
			"search":   "healthy",
			"git":      "healthy",
			"email":    "disabled",
		},
		"metrics": map[string]interface{}{
			"total_users":           0,
			"total_gists":           0,
			"public_gists":          0,
			"requests_per_minute":   0,
			"average_response_time": "0ms",
			"storage_used":          "0B",
			"storage_available":     "0B",
			"active_connections":    0,
		},
		"features": map[string]interface{}{
			"registration":    "enabled",
			"organizations":   "enabled",
			"social_features": "enabled",
			"search":          "sqlite",
		},
	}

	// Check database health
	if err := s.db.Exec("SELECT 1").Error; err != nil {
		healthz["status"] = "unhealthy"
		healthz["components"].(map[string]interface{})["database"] = "unhealthy"
	} else {
		// Get actual metrics from database
		var userCount int64
		var gistCount int64
		var publicGistCount int64

		s.db.Table("users").Count(&userCount)
		s.db.Table("gists").Count(&gistCount)
		s.db.Table("gists").Where("visibility = ?", "public").Count(&publicGistCount)

		healthz["metrics"].(map[string]interface{})["total_users"] = userCount
		healthz["metrics"].(map[string]interface{})["total_gists"] = gistCount
		healthz["metrics"].(map[string]interface{})["public_gists"] = publicGistCount
	}

	// Check storage health (check if data directory is accessible)
	if s.pathConfig != nil {
		storagePath := s.pathConfig.GetStoragePath()
		if _, err := os.Stat(storagePath); err != nil {
			healthz["components"].(map[string]interface{})["storage"] = "unhealthy"
			healthz["status"] = "degraded"
		} else {
			// Get disk usage info
			if usage, err := s.getDiskUsage(storagePath); err == nil {
				healthz["metrics"].(map[string]interface{})["storage_used"] = s.formatBytes(usage.Used)
				healthz["metrics"].(map[string]interface{})["storage_available"] = s.formatBytes(usage.Available)
			}
		}
	}

	// Check search backend
	if s.searchManager != nil {
		searchBackend := "sqlite"
		if s.config.GetBool("redis.enabled") {
			searchBackend = "redis"
			// For now, assume Redis is healthy if enabled
			// TODO: Add proper Redis health check through cache manager
		}
		healthz["features"].(map[string]interface{})["search"] = searchBackend
	}

	// Check email service
	if s.emailService != nil && s.config.GetBool("email.enabled") {
		healthz["components"].(map[string]interface{})["email"] = "healthy"
	}

	// Check feature flags
	healthz["features"].(map[string]interface{})["registration"] = s.boolToEnabled(s.config.GetBool("features.registration"))
	healthz["features"].(map[string]interface{})["organizations"] = s.boolToEnabled(s.config.GetBool("features.organizations"))
	healthz["features"].(map[string]interface{})["social_features"] = s.boolToEnabled(s.config.GetBool("features.social_features"))

	// Determine HTTP status
	status := http.StatusOK
	if healthz["status"] == "unhealthy" {
		status = http.StatusServiceUnavailable
	} else if healthz["status"] == "degraded" {
		status = http.StatusOK // Still return 200 for degraded
	}

	return c.JSON(status, healthz)
}

// handleCLIScript generates a dynamic POSIX-compliant shell script for CLI operations
func (s *Server) handleCLIScript(c echo.Context) error {
	// Get server URL from request or config
	serverURL := s.config.GetString("server.url")
	if serverURL == "" {
		// Construct URL from request
		scheme := "http"
		if c.Request().TLS != nil {
			scheme = "https"
		}
		serverURL = fmt.Sprintf("%s://%s", scheme, c.Request().Host)
	}

	// Generate the POSIX-compliant shell script
	script := fmt.Sprintf(`#!/bin/sh
# CasGists CLI - POSIX-compliant shell script
# Generated dynamically by CasGists server
# Version: %s

CASGISTS_URL="${CASGISTS_URL:-%s}"
CASGISTS_TOKEN="${CASGISTS_TOKEN:-}"

# Colors for output (POSIX-compliant)
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    BLUE='\033[0;34m'
    NC='\033[0m' # No Color
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    NC=''
fi

# Print error message
error() {
    printf "${RED}Error: $1${NC}\n" >&2
}

# Print success message
success() {
    printf "${GREEN}✓ $1${NC}\n"
}

# Print info message
info() {
    printf "${BLUE}→ $1${NC}\n"
}

# Check for required commands
check_requirements() {
    if ! command -v curl >/dev/null 2>&1; then
        error "curl is required but not installed"
        exit 1
    fi
}

# Make API request
api_request() {
    method="$1"
    endpoint="$2"
    data="$3"

    if [ -z "$CASGISTS_TOKEN" ]; then
        error "CASGISTS_TOKEN environment variable not set"
        printf "Please set your API token: export CASGISTS_TOKEN=your-token\n"
        exit 1
    fi

    if [ -n "$data" ]; then
        curl -s -X "$method" \
            -H "Authorization: Bearer $CASGISTS_TOKEN" \
            -H "Content-Type: application/json" \
            -d "$data" \
            "$CASGISTS_URL/api/v1$endpoint"
    else
        curl -s -X "$method" \
            -H "Authorization: Bearer $CASGISTS_TOKEN" \
            "$CASGISTS_URL/api/v1$endpoint"
    fi
}

# Login command
cmd_login() {
    printf "Username: "
    read -r username
    printf "Password: "
    stty -echo
    read -r password
    stty echo
    printf "\n"

    response=$(curl -s -X POST \
        -H "Content-Type: application/json" \
        -d "{\"username\":\"$username\",\"password\":\"$password\"}" \
        "$CASGISTS_URL/api/v1/auth/login")

    token=$(echo "$response" | grep -o '"token":"[^"]*' | cut -d'"' -f4)

    if [ -n "$token" ]; then
        success "Login successful!"
        info "Add this to your shell configuration:"
        printf "export CASGISTS_TOKEN=$token\n"
    else
        error "Login failed"
        exit 1
    fi
}

# List gists
cmd_list() {
    info "Fetching gists..."
    response=$(api_request GET "/gists")

    if [ -z "$response" ]; then
        error "Failed to fetch gists"
        exit 1
    fi

    # Simple JSON parsing for POSIX shell
    echo "$response" | grep -o '"title":"[^"]*' | cut -d'"' -f4 | while read -r title; do
        printf "• %%s\n" "$title"
    done
}

# Create gist
cmd_create() {
    if [ -z "$1" ]; then
        error "Usage: $0 create <file> [description]"
        exit 1
    fi

    file="$1"
    description="${2:-}"

    if [ ! -f "$file" ]; then
        error "File not found: $file"
        exit 1
    fi

    filename=$(basename "$file")
    content=$(cat "$file" | sed 's/"/\\"/g' | sed ':a;N;$!ba;s/\n/\\n/g')

    data="{
        \"title\":\"$filename\",
        \"description\":\"$description\",
        \"files\":[{
            \"filename\":\"$filename\",
            \"content\":\"$content\"
        }],
        \"private\":false
    }"

    info "Creating gist..."
    response=$(api_request POST "/gists" "$data")

    if echo "$response" | grep -q '"id"'; then
        success "Gist created successfully!"
        id=$(echo "$response" | grep -o '"id":"[^"]*' | cut -d'"' -f4)
        printf "View at: $CASGISTS_URL/gist/$id\n"
    else
        error "Failed to create gist"
        exit 1
    fi
}

# Search gists
cmd_search() {
    if [ -z "$1" ]; then
        error "Usage: $0 search <query>"
        exit 1
    fi

    query="$1"
    info "Searching for: $query"

    response=$(api_request GET "/search?q=$(echo "$query" | sed 's/ /%%20/g')")

    if [ -z "$response" ]; then
        error "Search failed"
        exit 1
    fi

    # Parse and display results
    echo "$response" | grep -o '"title":"[^"]*' | cut -d'"' -f4 | while read -r title; do
        printf "• %%s\n" "$title"
    done
}

# Show help
cmd_help() {
    cat << EOF
CasGists CLI - Command-line interface for CasGists

Usage: $0 <command> [arguments]

Commands:
    login              Login to CasGists
    list               List your gists
    create <file>      Create a new gist from file
    search <query>     Search public gists
    help               Show this help message

Environment Variables:
    CASGISTS_URL       Server URL (default: $CASGISTS_URL)
    CASGISTS_TOKEN     API authentication token

Examples:
    $0 login
    $0 create example.py "Python example"
    $0 search "javascript"
    $0 list

For more information, visit: $CASGISTS_URL/docs
EOF
}

# Main command dispatcher
main() {
    check_requirements

    case "${1:-help}" in
        login)
            cmd_login
            ;;
        list)
            cmd_list
            ;;
        create)
            shift
            cmd_create "$@"
            ;;
        search)
            shift
            cmd_search "$@"
            ;;
        help|--help|-h)
            cmd_help
            ;;
        *)
            error "Unknown command: $1"
            cmd_help
            exit 1
            ;;
    esac
}

# Run main function
main "$@"
`, s.config.GetString("version"), serverURL)

	// Set appropriate headers for shell script
	c.Response().Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Response().Header().Set("Content-Disposition", "inline; filename=\"casgists-cli.sh\"")

	return c.String(http.StatusOK, script)
}

// Handle 404 errors
func (s *Server) handle404(c echo.Context) error {
	// Check if this is an API request
	if isAPIRequest(c) {
		return echo.NewHTTPError(http.StatusNotFound, "endpoint not found")
	}

	// Return HTML 404 page
	return c.Render(http.StatusNotFound, "404", map[string]interface{}{
		"Title": "Page Not Found",
	})
}

// Helper function to check if request is for API
func isAPIRequest(c echo.Context) bool {
	path := c.Path()
	return c.Request().Header.Get("Accept") == "application/json" ||
		c.Request().Header.Get("Content-Type") == "application/json" ||
		strings.HasPrefix(path, "/api")
}

// getUptime returns the server uptime
func (s *Server) getUptime() string {
	// This would be implemented to track actual uptime
	return "0h 0m"
}

// Web page handlers
func (s *Server) handleLoginPage(c echo.Context) error {
	return c.Render(http.StatusOK, "login", map[string]interface{}{
		"Title": "Login",
	})
}

func (s *Server) handleRegisterPage(c echo.Context) error {
	return c.Render(http.StatusOK, "register", map[string]interface{}{
		"Title": "Register",
	})
}

func (s *Server) handleGistListPage(c echo.Context) error {
	// TODO: Get user from auth context
	// TODO: Get gists from database

	// For now, just render with empty gists
	// Git operations are optional so we can still show the page
	return c.Render(http.StatusOK, "gist_list", map[string]interface{}{
		"Title":  "My Gists",
		"Gists":  []interface{}{}, // TODO: Fetch actual gists
		"Filter": c.QueryParam("visibility"),
	})
}

func (s *Server) handleGistNewPage(c echo.Context) error {
	return c.Render(http.StatusOK, "gist_new", map[string]interface{}{
		"Title": "New Gist",
	})
}

func (s *Server) handleGistViewPage(c echo.Context) error {
	// TODO: Get gist by ID
	// TODO: Check permissions
	// TODO: Get comments

	gistID := c.Param("id")

	// Placeholder data
	gist := map[string]interface{}{
		"ID":          gistID,
		"Title":       "Example Gist",
		"Description": "This is an example gist",
		"Visibility":  "public",
		"StarCount":   0,
		"CreatedAt":   time.Now(),
		"UpdatedAt":   time.Now(),
		"User": map[string]interface{}{
			"Username":  "example",
			"AvatarURL": "/static/img/default-avatar.png",
		},
		"Files": []interface{}{
			map[string]interface{}{
				"ID":       "1",
				"Filename": "example.go",
				"Language": "go",
				"Content":  "package main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}",
			},
		},
	}

	return c.Render(http.StatusOK, "gist_view", map[string]interface{}{
		"Title":     "View Gist",
		"Gist":      gist,
		"Comments":  []interface{}{},
		"IsOwner":   false,
		"IsStarred": false,
	})
}

// DiskUsage represents disk usage statistics
type DiskUsage struct {
	Total     uint64
	Available uint64
	Used      uint64
}

// getDiskUsage gets disk usage statistics for a path
func (s *Server) getDiskUsage(path string) (*DiskUsage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}

	usage := &DiskUsage{
		Total:     stat.Blocks * uint64(stat.Bsize),
		Available: stat.Bavail * uint64(stat.Bsize),
		Used:      (stat.Blocks - stat.Bfree) * uint64(stat.Bsize),
	}

	return usage, nil
}

// formatBytes formats bytes to human-readable format
func (s *Server) formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// boolToEnabled converts a boolean to "enabled" or "disabled" string
func (s *Server) boolToEnabled(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}
