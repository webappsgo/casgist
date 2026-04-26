package handlers

import (
	"net/http"
	"strconv"

	"github.com/casapps/casgist/src/internal/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// OrganizationHandler handles organization-related endpoints
type OrganizationHandler struct {
	db     *gorm.DB
	config *viper.Viper
}

// NewOrganizationHandler creates a new organization handler
func NewOrganizationHandler(db *gorm.DB, config *viper.Viper) *OrganizationHandler {
	return &OrganizationHandler{
		db:     db,
		config: config,
	}
}

// List returns all organizations for the current user
func (h *OrganizationHandler) List(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Find organizations where user is a member
	var memberships []models.OrganizationUser
	if err := h.db.Where("user_id = ?", userID).
		Preload("Organization").
		Find(&memberships).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organizations")
	}

	// Extract organizations
	organizations := make([]models.Organization, len(memberships))
	for i, membership := range memberships {
		organizations[i] = membership.Organization
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"organizations": organizations,
		"total":        len(organizations),
	})
}

// Create creates a new organization
func (h *OrganizationHandler) Create(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request
	var req struct {
		Name        string `json:"name" validate:"required,min=3,max=100"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Website     string `json:"website"`
		IsPublic    bool   `json:"is_public"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Check if organization name already exists
	var count int64
	h.db.Model(&models.Organization{}).Where("name = ?", req.Name).Count(&count)
	if count > 0 {
		return echo.NewHTTPError(http.StatusConflict, "Organization name already exists")
	}

	// Create organization
	org := &models.Organization{
		ID:          uuid.New(),
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Website:     req.Website,
		IsPublic:    req.IsPublic,
	}

	// Start transaction
	tx := h.db.Begin()
	defer tx.Rollback()

	// Create organization
	if err := tx.Create(org).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create organization")
	}

	// Add owner as admin member
	member := &models.OrganizationUser{
		ID:             uuid.New(),
		OrganizationID: org.ID,
		UserID:         userID,
		Role:           "owner",
	}

	if err := tx.Create(member).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to add owner as member")
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to commit transaction")
	}

	// Return organization with basic info
	// (Owner info not directly available in this model structure)

	return c.JSON(http.StatusCreated, org)
}

// Get returns a specific organization
func (h *OrganizationHandler) Get(c echo.Context) error {
	// Get org name from URL
	orgName := c.Param("name")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).
		Preload("Owner").
		First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Check if user has access (public orgs are accessible to all)
	if !org.IsPublic {
		userID, ok := c.Get("user_id").(uuid.UUID)
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
		}

		// Check if user is a member
		var count int64
		h.db.Model(&models.OrganizationUser{}).
			Where("organization_id = ? AND user_id = ?", org.ID, userID).
			Count(&count)
		
		if count == 0 {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
	}

	return c.JSON(http.StatusOK, org)
}

// Update updates an organization
func (h *OrganizationHandler) Update(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org name from URL
	orgName := c.Param("name")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Check if user has admin permissions
	var member models.OrganizationUser
	if err := h.db.Where("organization_id = ? AND user_id = ?", org.ID, userID).
		First(&member).Error; err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	if member.Role != "owner" && member.Role != "admin" {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Parse request
	var req struct {
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Website     string `json:"website"`
		IsPublic    *bool  `json:"is_public"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
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
	if req.IsPublic != nil {
		updates["is_public"] = *req.IsPublic
	}

	// Update organization
	if err := h.db.Model(&org).Updates(updates).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update organization")
	}

	// Reload organization
	h.db.First(&org, org.ID)

	return c.JSON(http.StatusOK, org)
}

// Delete deletes an organization
func (h *OrganizationHandler) Delete(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org name from URL
	orgName := c.Param("name")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Check if user is the owner
	var ownerMember models.OrganizationUser
	if err := h.db.Where("organization_id = ? AND user_id = ? AND role = ?", org.ID, userID, "owner").
		First(&ownerMember).Error; err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Only the owner can delete an organization")
	}

	// Start transaction
	tx := h.db.Begin()
	defer tx.Rollback()

	// Delete all gists belonging to the organization
	if err := tx.Where("organization_id = ?", org.ID).Delete(&models.Gist{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete organization gists")
	}

	// Delete all team members through teams
	var teams []models.Team
	tx.Where("organization_id = ?", org.ID).Find(&teams)
	for _, team := range teams {
		if err := tx.Where("team_id = ?", team.ID).Delete(&models.TeamMember{}).Error; err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete team members")
		}
	}

	// Delete all teams
	if err := tx.Where("organization_id = ?", org.ID).Delete(&models.Team{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete teams")
	}

	// Delete all organization members
	if err := tx.Where("organization_id = ?", org.ID).Delete(&models.OrganizationUser{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete organization members")
	}

	// Delete organization
	if err := tx.Delete(&org).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete organization")
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to commit transaction")
	}

	return c.NoContent(http.StatusNoContent)
}

// GetMembers returns all members of an organization
func (h *OrganizationHandler) GetMembers(c echo.Context) error {
	// Get org name from URL
	orgName := c.Param("name")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Check if user has access
	if !org.IsPublic {
		userID, ok := c.Get("user_id").(uuid.UUID)
		if !ok {
			return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
		}

		// Check if user is a member
		var count int64
		h.db.Model(&models.OrganizationUser{}).
			Where("organization_id = ? AND user_id = ?", org.ID, userID).
			Count(&count)
		
		if count == 0 {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
	}

	// Get members
	var members []models.OrganizationUser
	if err := h.db.Where("organization_id = ?", org.ID).
		Preload("User").
		Find(&members).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch members")
	}

	// Convert to response format
	response := make([]map[string]interface{}, len(members))
	for i, member := range members {
		response[i] = map[string]interface{}{
			"user": map[string]interface{}{
				"id":           member.User.ID,
				"username":     member.User.Username,
				"display_name": member.User.DisplayName,
				"avatar_url":   member.User.AvatarURL,
			},
			"role":      member.Role,
			"joined_at": member.CreatedAt,
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"members": response,
		"total":   len(response),
	})
}

// AddMember adds a member to an organization
func (h *OrganizationHandler) AddMember(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org name and username from URL
	orgName := c.Param("name")
	username := c.Param("username")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Check if user has admin permissions
	var member models.OrganizationUser
	if err := h.db.Where("organization_id = ? AND user_id = ?", org.ID, userID).
		First(&member).Error; err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	if member.Role != "owner" && member.Role != "admin" {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Find user to add
	var userToAdd models.User
	if err := h.db.Where("username = ?", username).First(&userToAdd).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch user")
	}

	// Check if user is already a member
	var existingCount int64
	h.db.Model(&models.OrganizationUser{}).
		Where("organization_id = ? AND user_id = ?", org.ID, userToAdd.ID).
		Count(&existingCount)
	
	if existingCount > 0 {
		return echo.NewHTTPError(http.StatusConflict, "User is already a member")
	}

	// Parse request for role
	var req struct {
		Role string `json:"role" validate:"required,oneof=member admin"`
	}

	if err := c.Bind(&req); err != nil {
		req.Role = "member" // Default to member
	}

	// Create membership
	newMember := &models.OrganizationUser{
		ID:             uuid.New(),
		OrganizationID: org.ID,
		UserID:         userToAdd.ID,
		Role:           req.Role,
	}

	if err := h.db.Create(newMember).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to add member")
	}

	// Load user details
	h.db.Model(newMember).Association("User").Find(&newMember.User)

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"user": map[string]interface{}{
			"id":           newMember.User.ID,
			"username":     newMember.User.Username,
			"display_name": newMember.User.DisplayName,
			"avatar_url":   newMember.User.AvatarURL,
		},
		"role":      newMember.Role,
		"joined_at": newMember.CreatedAt,
	})
}

// RemoveMember removes a member from an organization
func (h *OrganizationHandler) RemoveMember(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org name and username from URL
	orgName := c.Param("name")
	username := c.Param("username")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Find user to remove
	var userToRemove models.User
	if err := h.db.Where("username = ?", username).First(&userToRemove).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch user")
	}

	// Check if user has permissions
	var currentMember models.OrganizationUser
	if err := h.db.Where("organization_id = ? AND user_id = ?", org.ID, userID).
		First(&currentMember).Error; err != nil {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Only owner and admin can remove members
	// Members can remove themselves
	canRemove := currentMember.Role == "owner" || 
		currentMember.Role == "admin" || 
		userID == userToRemove.ID

	if !canRemove {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	// Check if user to remove is the owner
	var targetMember models.OrganizationUser
	if err := h.db.Where("organization_id = ? AND user_id = ?", org.ID, userToRemove.ID).
		First(&targetMember).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Member not found")
	}
	
	if targetMember.Role == "owner" {
		return echo.NewHTTPError(http.StatusBadRequest, "Cannot remove the organization owner")
	}

	// Remove member
	if err := h.db.Where("organization_id = ? AND user_id = ?", org.ID, userToRemove.ID).
		Delete(&models.OrganizationUser{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to remove member")
	}

	return c.NoContent(http.StatusNoContent)
}

// GetGists returns all gists for an organization
func (h *OrganizationHandler) GetGists(c echo.Context) error {
	// Get org name from URL
	orgName := c.Param("name")

	// Parse pagination
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Build query
	query := h.db.Model(&models.Gist{}).
		Where("organization_id = ?", org.ID)

	// Check if user has access to private gists
	userID, authenticated := c.Get("user_id").(uuid.UUID)
	if org.IsPublic || !authenticated {
		// Only show public gists
		query = query.Where("is_public = ?", true)
	} else {
		// Check if user is a member
		var memberCount int64
		h.db.Model(&models.OrganizationUser{}).
			Where("organization_id = ? AND user_id = ?", org.ID, userID).
			Count(&memberCount)
		
		if memberCount == 0 {
			// Not a member, only show public gists
			query = query.Where("is_public = ?", true)
		}
		// If member, show all gists
	}

	// Count total
	var total int64
	query.Count(&total)

	// Get gists
	var gists []models.Gist
	offset := (page - 1) * limit
	if err := query.
		Preload("User").
		Preload("Files").
		Order("updated_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&gists).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch gists")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"gists": gists,
		"total": total,
		"page":  page,
		"limit": limit,
		"pages": (total + int64(limit) - 1) / int64(limit),
	})
}

// RegisterRoutes registers organization routes
func (h *OrganizationHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/orgs", h.List)
	g.POST("/orgs", h.Create)
	g.GET("/orgs/:name", h.Get)
	g.PATCH("/orgs/:name", h.Update)
	g.DELETE("/orgs/:name", h.Delete)
	g.GET("/orgs/:name/members", h.GetMembers)
	g.PUT("/orgs/:name/members/:username", h.AddMember)
	g.DELETE("/orgs/:name/members/:username", h.RemoveMember)
	g.GET("/orgs/:name/gists", h.GetGists)
}