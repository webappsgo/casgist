package handlers

import (
	"net/http"

	"github.com/casapps/casgist/src/internal/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// TeamHandler handles team-related endpoints
type TeamHandler struct {
	db     *gorm.DB
	config *viper.Viper
}

// NewTeamHandler creates a new team handler
func NewTeamHandler(db *gorm.DB, config *viper.Viper) *TeamHandler {
	return &TeamHandler{
		db:     db,
		config: config,
	}
}

// List returns all teams for an organization
func (h *TeamHandler) List(c echo.Context) error {
	// Get org name from URL
	orgName := c.Param("org")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Check if user has access
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !org.IsPublic && ok {
		// Check if user is a member
		var count int64
		h.db.Model(&models.OrganizationUser{}).
			Where("organization_id = ? AND user_id = ?", org.ID, userID).
			Count(&count)
		
		if count == 0 {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
	} else if !org.IsPublic {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get teams
	var teams []models.Team
	if err := h.db.Where("organization_id = ?", org.ID).
		Order("name ASC").
		Find(&teams).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch teams")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"teams": teams,
		"total": len(teams),
	})
}

// Create creates a new team
func (h *TeamHandler) Create(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org name from URL
	orgName := c.Param("org")

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
		Name        string `json:"name" validate:"required,min=3,max=100"`
		Description string `json:"description"`
		Permission  string `json:"permission" validate:"required,oneof=read write admin"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Check if team name already exists in org
	var count int64
	h.db.Model(&models.Team{}).
		Where("organization_id = ? AND name = ?", org.ID, req.Name).
		Count(&count)
	if count > 0 {
		return echo.NewHTTPError(http.StatusConflict, "Team name already exists")
	}

	// Create team
	team := &models.Team{
		ID:             uuid.New(),
		OrganizationID: org.ID,
		Name:           req.Name,
		Description:    req.Description,
		Permission:     req.Permission,
	}

	if err := h.db.Create(team).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create team")
	}

	return c.JSON(http.StatusCreated, team)
}

// Get returns a specific team
func (h *TeamHandler) Get(c echo.Context) error {
	// Get org and team name from URL
	orgName := c.Param("org")
	teamName := c.Param("team")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Find team
	var team models.Team
	if err := h.db.Where("organization_id = ? AND name = ?", org.ID, teamName).
		First(&team).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Team not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch team")
	}

	// Check if user has access
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !org.IsPublic && ok {
		// Check if user is a member
		var count int64
		h.db.Model(&models.OrganizationUser{}).
			Where("organization_id = ? AND user_id = ?", org.ID, userID).
			Count(&count)
		
		if count == 0 {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
	} else if !org.IsPublic {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	return c.JSON(http.StatusOK, team)
}

// Update updates a team
func (h *TeamHandler) Update(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org and team name from URL
	orgName := c.Param("org")
	teamName := c.Param("team")

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

	// Find team
	var team models.Team
	if err := h.db.Where("organization_id = ? AND name = ?", org.ID, teamName).
		First(&team).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Team not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch team")
	}

	// Parse request
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Permission  string `json:"permission" validate:"omitempty,oneof=read write admin"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Update fields
	updates := map[string]interface{}{}
	if req.Name != "" && req.Name != team.Name {
		// Check if new name already exists
		var count int64
		h.db.Model(&models.Team{}).
			Where("organization_id = ? AND name = ? AND id != ?", org.ID, req.Name, team.ID).
			Count(&count)
		if count > 0 {
			return echo.NewHTTPError(http.StatusConflict, "Team name already exists")
		}
		updates["name"] = req.Name
	}
	if req.Description != team.Description {
		updates["description"] = req.Description
	}
	if req.Permission != "" && req.Permission != team.Permission {
		updates["permission"] = req.Permission
	}

	// Update team
	if len(updates) > 0 {
		if err := h.db.Model(&team).Updates(updates).Error; err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update team")
		}
	}

	// Reload team
	h.db.First(&team, team.ID)

	return c.JSON(http.StatusOK, team)
}

// Delete deletes a team
func (h *TeamHandler) Delete(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org and team name from URL
	orgName := c.Param("org")
	teamName := c.Param("team")

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

	// Find team
	var team models.Team
	if err := h.db.Where("organization_id = ? AND name = ?", org.ID, teamName).
		First(&team).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Team not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch team")
	}

	// Start transaction
	tx := h.db.Begin()
	defer tx.Rollback()

	// Delete all team members
	if err := tx.Where("team_id = ?", team.ID).Delete(&models.TeamMember{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete team members")
	}

	// Delete team
	if err := tx.Delete(&team).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete team")
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to commit transaction")
	}

	return c.NoContent(http.StatusNoContent)
}

// GetMembers returns all members of a team
func (h *TeamHandler) GetMembers(c echo.Context) error {
	// Get org and team name from URL
	orgName := c.Param("org")
	teamName := c.Param("team")

	// Find organization
	var org models.Organization
	if err := h.db.Where("name = ?", orgName).First(&org).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Organization not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch organization")
	}

	// Find team
	var team models.Team
	if err := h.db.Where("organization_id = ? AND name = ?", org.ID, teamName).
		First(&team).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Team not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch team")
	}

	// Check if user has access
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !org.IsPublic && ok {
		// Check if user is a member
		var count int64
		h.db.Model(&models.OrganizationUser{}).
			Where("organization_id = ? AND user_id = ?", org.ID, userID).
			Count(&count)
		
		if count == 0 {
			return echo.NewHTTPError(http.StatusForbidden, "Access denied")
		}
	} else if !org.IsPublic {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get team members
	var members []models.TeamMember
	if err := h.db.Where("team_id = ?", team.ID).
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
			"joined_at": member.CreatedAt,
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"members": response,
		"total":   len(response),
	})
}

// AddMember adds a member to a team
func (h *TeamHandler) AddMember(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org, team and username from URL
	orgName := c.Param("org")
	teamName := c.Param("team")
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

	// Find team
	var team models.Team
	if err := h.db.Where("organization_id = ? AND name = ?", org.ID, teamName).
		First(&team).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Team not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch team")
	}

	// Find user to add
	var userToAdd models.User
	if err := h.db.Where("username = ?", username).First(&userToAdd).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch user")
	}

	// Check if user is an org member first
	var orgMemberCount int64
	h.db.Model(&models.OrganizationUser{}).
		Where("organization_id = ? AND user_id = ?", org.ID, userToAdd.ID).
		Count(&orgMemberCount)
	
	if orgMemberCount == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "User must be an organization member first")
	}

	// Check if user is already a team member
	var existingCount int64
	h.db.Model(&models.TeamMember{}).
		Where("team_id = ? AND user_id = ?", team.ID, userToAdd.ID).
		Count(&existingCount)
	
	if existingCount > 0 {
		return echo.NewHTTPError(http.StatusConflict, "User is already a team member")
	}

	// Create team membership
	teamMember := &models.TeamMember{
		ID:     uuid.New(),
		TeamID: team.ID,
		UserID: userToAdd.ID,
	}

	if err := h.db.Create(teamMember).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to add team member")
	}

	// Load user details
	h.db.Model(teamMember).Association("User").Find(&teamMember.User)

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"user": map[string]interface{}{
			"id":           teamMember.User.ID,
			"username":     teamMember.User.Username,
			"display_name": teamMember.User.DisplayName,
			"avatar_url":   teamMember.User.AvatarURL,
		},
		"joined_at": teamMember.CreatedAt,
	})
}

// RemoveMember removes a member from a team
func (h *TeamHandler) RemoveMember(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get org, team and username from URL
	orgName := c.Param("org")
	teamName := c.Param("team")
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

	// Find team
	var team models.Team
	if err := h.db.Where("organization_id = ? AND name = ?", org.ID, teamName).
		First(&team).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Team not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch team")
	}

	// Find user to remove
	var userToRemove models.User
	if err := h.db.Where("username = ?", username).First(&userToRemove).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch user")
	}

	// Remove team member
	if err := h.db.Where("team_id = ? AND user_id = ?", team.ID, userToRemove.ID).
		Delete(&models.TeamMember{}).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to remove team member")
	}

	return c.NoContent(http.StatusNoContent)
}

// RegisterRoutes registers team routes
func (h *TeamHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/orgs/:org/teams", h.List)
	g.POST("/orgs/:org/teams", h.Create)
	g.GET("/orgs/:org/teams/:team", h.Get)
	g.PATCH("/orgs/:org/teams/:team", h.Update)
	g.DELETE("/orgs/:org/teams/:team", h.Delete)
	g.GET("/orgs/:org/teams/:team/members", h.GetMembers)
	g.PUT("/orgs/:org/teams/:team/members/:username", h.AddMember)
	g.DELETE("/orgs/:org/teams/:team/members/:username", h.RemoveMember)
}