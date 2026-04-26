package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotActive      = errors.New("user account is not active")
	ErrInvalidToken       = errors.New("invalid token")
	ErrTokenExpired       = errors.New("token has expired")
	ErrUserNotFound       = errors.New("user not found")
)

// AuthService handles authentication operations
type AuthService struct {
	secretKey []byte
	issuer    string
}

// NewAuthService creates a new authentication service
func NewAuthService(secretKey string, issuer string) *AuthService {
	return &AuthService{
		secretKey: []byte(secretKey),
		issuer:    issuer,
	}
}

// Claims represents JWT claims
type Claims struct {
	UserID    uuid.UUID `json:"user_id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	IsAdmin   bool      `json:"is_admin"`
	SessionID uuid.UUID `json:"session_id"`
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// HashPassword hashes a plain text password
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// CheckPasswordHash compares a password with its hash
func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateTokenPair generates access and refresh tokens
func (a *AuthService) GenerateTokenPair(user *models.User, sessionID uuid.UUID) (*TokenPair, error) {
	now := time.Now()
	
	// Access token claims (15 minutes)
	accessClaims := Claims{
		UserID:    user.ID,
		Username:  user.Username,
		Email:     user.Email,
		IsAdmin:   user.IsAdmin,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    a.issuer,
			Subject:   user.ID.String(),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
	}
	
	// Create access token
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(a.secretKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}
	
	// Generate refresh token
	refreshToken, err := a.generateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}
	
	return &TokenPair{
		AccessToken:  accessTokenString,
		RefreshToken: refreshToken,
		ExpiresAt:    accessClaims.ExpiresAt.Time,
	}, nil
}

// ValidateToken validates a JWT token
func (a *AuthService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.secretKey, nil
	})
	
	if err != nil {
		return nil, err
	}
	
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	
	return nil, ErrInvalidToken
}

// generateRefreshToken generates a secure random refresh token
func (a *AuthService) generateRefreshToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// GetUserIDFromToken extracts user ID from JWT token in request
func (a *AuthService) GetUserIDFromToken(c interface{}) (uuid.UUID, error) {
	// This would typically get the token from echo.Context
	// For now, return a placeholder implementation
	// In real implementation, would extract from Authorization header
	
	// Type assert to echo.Context
	if ctx, ok := c.(interface{ Get(string) interface{} }); ok {
		if userID := ctx.Get("user_id"); userID != nil {
			if id, ok := userID.(uuid.UUID); ok {
				return id, nil
			}
			if idStr, ok := userID.(string); ok {
				return uuid.Parse(idStr)
			}
		}
	}
	
	return uuid.Nil, ErrInvalidToken
}

// VerifyPassword verifies a password against its hash
func VerifyPassword(password, hash string) bool {
	return CheckPasswordHash(password, hash)
}

// GenerateSecureToken generates a secure random token
func GenerateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}