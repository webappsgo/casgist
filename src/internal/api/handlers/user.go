package handlers

import (
	"net/http"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/spf13/viper"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// UserHandler handles user-related endpoints
type UserHandler struct {
	db     *gorm.DB
	config *viper.Viper
}

// NewUserHandler creates a new user handler
func NewUserHandler(db *gorm.DB, config *viper.Viper) *UserHandler {
	return &UserHandler{
		db:     db,
		config: config,
	}
}

// Get returns a user by username
func (h *UserHandler) Get(c echo.Context) error {
	username := c.Param("username")
	if username == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username required")
	}

	var user models.User
	if err := h.db.Where("username = ?", username).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch user")
	}

	return c.JSON(http.StatusOK, UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		AvatarURL:   user.AvatarURL,
		IsAdmin:     user.IsAdmin,
	})
}

// GetCurrent returns the current user
func (h *UserHandler) GetCurrent(c echo.Context) error {
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	var user models.User
	if err := h.db.First(&user, userID).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch user")
	}

	return c.JSON(http.StatusOK, UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		AvatarURL:   user.AvatarURL,
		IsAdmin:     user.IsAdmin,
	})
}

// UpdateUserRequest represents a user update request
type UpdateUserRequest struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	Email       string `json:"email" validate:"email"`
}

// Update updates the current user
func (h *UserHandler) Update(c echo.Context) error {
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	var req UpdateUserRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Validate request
	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Fetch user
	var user models.User
	if err := h.db.First(&user, userID).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch user")
	}

	// Check if email is already taken by another user
	if req.Email != "" && req.Email != user.Email {
		var existingUser models.User
		if err := h.db.Where("email = ? AND id != ?", req.Email, userID).First(&existingUser).Error; err == nil {
			return echo.NewHTTPError(http.StatusConflict, "email already taken")
		}
	}

	// Update user
	if req.DisplayName != "" {
		user.DisplayName = req.DisplayName
	}
	if req.Bio != "" {
		user.Bio = req.Bio
	}
	if req.Email != "" {
		user.Email = req.Email
		user.EmailVerified = false // Reset email verification
	}

	if err := h.db.Save(&user).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update user")
	}

	return c.JSON(http.StatusOK, UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		AvatarURL:   user.AvatarURL,
		IsAdmin:     user.IsAdmin,
	})
}

// GetGists returns gists for a user
func (h *UserHandler) GetGists(c echo.Context) error {
	username := c.Param("username")
	if username == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username required")
	}

	// Find user
	var user models.User
	if err := h.db.Where("username = ?", username).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch user")
	}

	// Build query for user's gists
	query := h.db.Model(&models.Gist{}).
		Where("user_id = ?", user.ID).
		Preload("User").
		Preload("Files")

	// Check if current user can see private gists
	currentUserID, _ := c.Get("user_id").(uuid.UUID)
	if currentUserID != user.ID {
		// Different user, only show public gists
		query = query.Where("is_public = ?", true)
	}

	// Apply sorting
	sort := c.QueryParam("sort")
	switch sort {
	case "created":
		query = query.Order("created_at DESC")
	case "updated":
		query = query.Order("updated_at DESC")
	case "stars":
		query = query.Order("star_count DESC")
	default:
		query = query.Order("created_at DESC")
	}

	// Fetch gists
	var gists []models.Gist
	if err := query.Find(&gists).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gists")
	}

	// Create gist handler to use its response building methods
	gistHandler := &GistHandler{db: h.db, config: h.config}
	
	return c.JSON(http.StatusOK, map[string]interface{}{
		"user":  &UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
			IsAdmin:     user.IsAdmin,
		},
		"gists": gistHandler.buildGistListResponse(gists),
	})
}