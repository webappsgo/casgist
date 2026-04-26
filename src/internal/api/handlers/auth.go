package handlers

import (
	"net/http"
	"time"

	"github.com/casapps/casgist/src/internal/auth"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/spf13/viper"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// AuthHandler handles authentication endpoints
type AuthHandler struct {
	db          *gorm.DB
	authService *auth.AuthService
	totpService *auth.TOTPService
	config      *viper.Viper
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(db *gorm.DB, authService *auth.AuthService, config *viper.Viper) *AuthHandler {
	return &AuthHandler{
		db:          db,
		authService: authService,
		totpService: auth.NewTOTPService("CasGists"),
		config:      config,
	}
}

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
	TOTPCode string `json:"totp_code,omitempty"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	ExpiresAt     time.Time `json:"expires_at"`
	User          *UserResponse `json:"user"`
	Require2FA    bool      `json:"require_2fa,omitempty"`
}

// UserResponse represents a user in API responses
type UserResponse struct {
	ID            uuid.UUID `json:"id"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	DisplayName   string    `json:"display_name"`
	AvatarURL     string    `json:"avatar_url"`
	IsAdmin       bool      `json:"is_admin"`
}

// Login handles user login
func (h *AuthHandler) Login(c echo.Context) error {
	var req LoginRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Validate request
	// TODO: Fix validator
	// if err := c.Validate(req); err != nil {
	// 	return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	// }

	// Find user by username or email
	var user models.User
	if err := h.db.Where("username = ? OR email = ?", req.Username, req.Username).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "database error")
	}

	// Check if user is active
	if !user.IsActive {
		return echo.NewHTTPError(http.StatusUnauthorized, "account is disabled")
	}

	// Verify password
	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}

	// Check 2FA if enabled
	if user.TwoFactorEnabled {
		if req.TOTPCode == "" {
			return c.JSON(http.StatusOK, LoginResponse{
				Require2FA: true,
			})
		}

		// Verify TOTP code
		if !h.totpService.ValidateTOTP(user.TwoFactorSecret, req.TOTPCode) {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid 2FA code")
		}
	}

	// Create session
	session := &models.Session{
		UserID:       user.ID,
		IPAddress:    c.RealIP(),
		UserAgent:    c.Request().UserAgent(),
		ExpiresAt:    time.Now().Add(time.Duration(h.config.GetInt("security.session_timeout")) * time.Second),
		LastUsedAt:   time.Now(),
	}

	if err := h.db.Create(session).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create session")
	}

	// Generate tokens
	tokenPair, err := h.authService.GenerateTokenPair(&user, session.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate tokens")
	}

	// Update session with tokens
	session.Token = tokenPair.AccessToken
	session.RefreshToken = tokenPair.RefreshToken
	if err := h.db.Save(session).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to save session")
	}

	// Update last login
	user.LastLoginAt = &session.CreatedAt
	// Update IP in session
	// Note: User model doesn't have LastLoginIP field
	h.db.Save(&user)

	return c.JSON(http.StatusOK, LoginResponse{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		User: &UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
			IsAdmin:     user.IsAdmin,
		},
	})
}

// RegisterRequest represents a registration request
type RegisterRequest struct {
	Username string `json:"username" validate:"required,min=3,max=32"`
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

// Register handles user registration
func (h *AuthHandler) Register(c echo.Context) error {
	var req RegisterRequest
	if err := c.Bind(&req); err != nil {
		// Log the actual error for debugging
		c.Logger().Errorf("Bind error: %v", err)
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: " + err.Error())
	}

	// Validate request
	// TODO: Fix validator
	// if err := c.Validate(req); err != nil {
	// 	return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	// }

	// Check if user already exists
	var existingUser models.User
	if err := h.db.Where("username = ? OR email = ?", req.Username, req.Email).First(&existingUser).Error; err == nil {
		if existingUser.Username == req.Username {
			return echo.NewHTTPError(http.StatusConflict, "username already taken")
		}
		return echo.NewHTTPError(http.StatusConflict, "email already registered")
	}

	// Hash password
	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to hash password")
	}

	// Create user
	user := models.User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hashedPassword,
		IsActive:     true,
		DisplayName:  req.Username,
	}

	if err := h.db.Create(&user).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create user")
	}

	// Create session
	session := &models.Session{
		UserID:       user.ID,
		IPAddress:    c.RealIP(),
		UserAgent:    c.Request().UserAgent(),
		ExpiresAt:    time.Now().Add(time.Duration(h.config.GetInt("security.session_timeout")) * time.Second),
		LastUsedAt:   time.Now(),
	}

	if err := h.db.Create(session).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create session")
	}

	// Generate tokens
	tokenPair, err := h.authService.GenerateTokenPair(&user, session.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate tokens")
	}

	// Update session with tokens
	session.Token = tokenPair.AccessToken
	session.RefreshToken = tokenPair.RefreshToken
	if err := h.db.Save(session).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to save session")
	}

	return c.JSON(http.StatusCreated, LoginResponse{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		User: &UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
			IsAdmin:     user.IsAdmin,
		},
	})
}

// RefreshTokenRequest represents a token refresh request
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// RefreshToken handles token refresh
func (h *AuthHandler) RefreshToken(c echo.Context) error {
	var req RefreshTokenRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Find session by refresh token
	var session models.Session
	if err := h.db.Where("refresh_token = ?", req.RefreshToken).First(&session).Error; err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid refresh token")
	}

	// Check if session is expired
	if session.ExpiresAt.Before(time.Now()) {
		return echo.NewHTTPError(http.StatusUnauthorized, "refresh token expired")
	}

	// Get user
	var user models.User
	if err := h.db.First(&user, session.UserID).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "user not found")
	}

	// Generate new tokens
	tokenPair, err := h.authService.GenerateTokenPair(&user, session.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate tokens")
	}

	// Update session
	session.Token = tokenPair.AccessToken
	session.RefreshToken = tokenPair.RefreshToken
	session.LastUsedAt = time.Now()
	if err := h.db.Save(&session).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update session")
	}

	return c.JSON(http.StatusOK, LoginResponse{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresAt:    tokenPair.ExpiresAt,
		User: &UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
			IsAdmin:     user.IsAdmin,
		},
	})
}

// Logout handles user logout
func (h *AuthHandler) Logout(c echo.Context) error {
	// Get session ID from context
	sessionID, ok := c.Get("session_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid session")
	}

	// Delete session
	if err := h.db.Delete(&models.Session{}, sessionID).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete session")
	}

	return c.NoContent(http.StatusNoContent)
}