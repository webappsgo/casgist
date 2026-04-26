package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/cache"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/email"
)

// UserService handles user business logic
type UserService struct {
	db           *gorm.DB
	cfg          *viper.Viper
	cache        *cache.CacheManager
	emailService *email.Service
}

// NewUserService creates a new user service
func NewUserService(db *gorm.DB, cfg *viper.Viper, cacheManager *cache.CacheManager, emailService *email.Service) *UserService {
	return &UserService{
		db:           db,
		cfg:          cfg,
		cache:        cacheManager,
		emailService: emailService,
	}
}

// CreateUser creates a new user with validation
func (s *UserService) CreateUser(user *models.User) error {
	// Validate username
	if err := s.ValidateUsername(user.Username); err != nil {
		return err
	}

	// Validate email
	if err := s.ValidateEmail(user.Email); err != nil {
		return err
	}

	// Check if username exists
	var count int64
	s.db.Model(&models.User{}).Where("username = ?", user.Username).Count(&count)
	if count > 0 {
		return errors.New("username already exists")
	}

	// Check if email exists
	s.db.Model(&models.User{}).Where("email = ?", user.Email).Count(&count)
	if count > 0 {
		return errors.New("email already exists")
	}

	// Create user
	if err := s.db.Create(user).Error; err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// Create user preferences with defaults
	preferences := &models.UserPreference{
		UserID: user.ID,
	}
	s.db.Create(preferences)

	// Create email preferences with defaults
	if s.emailService != nil {
		s.emailService.CreateEmailPreference(user.ID)

		// Send welcome email if enabled
		if s.cfg.GetBool("email.enabled") && s.cfg.GetBool("email.send_welcome") {
			go s.emailService.SendWelcomeEmail(user.Email, user.Username)
		}
	}

	return nil
}

// GetUserByID retrieves a user by ID
func (s *UserService) GetUserByID(userID uuid.UUID) (*models.User, error) {
	var user models.User
	if err := s.db.Preload("Preferences").First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByUsername retrieves a user by username
func (s *UserService) GetUserByUsername(username string) (*models.User, error) {
	cacheKey := cache.UserKey(username)
	ctx := context.Background()

	// Try to get from cache first
	if s.cache != nil {
		var cachedUser models.User
		if err := s.cache.GetJSON(ctx, cacheKey, &cachedUser); err == nil {
			return &cachedUser, nil
		}
	}

	var user models.User
	if err := s.db.Preload("Preferences").First(&user, "username = ?", username).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}

	// Cache the user for future requests
	if s.cache != nil {
		s.cache.SetJSON(ctx, cacheKey, &user, cache.TTLLong)
	}

	return &user, nil
}

// GetUserByEmail retrieves a user by email
func (s *UserService) GetUserByEmail(email string) (*models.User, error) {
	var user models.User
	if err := s.db.Preload("Preferences").First(&user, "email = ?", email).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}
	return &user, nil
}

// UpdateUser updates user information
func (s *UserService) UpdateUser(user *models.User) error {
	// Validate if username changed
	var existing models.User
	if err := s.db.First(&existing, "id = ?", user.ID).Error; err != nil {
		return errors.New("user not found")
	}

	if existing.Username != user.Username {
		if err := s.ValidateUsername(user.Username); err != nil {
			return err
		}

		// Check if new username is taken
		var count int64
		s.db.Model(&models.User{}).Where("username = ? AND id != ?", user.Username, user.ID).Count(&count)
		if count > 0 {
			return errors.New("username already exists")
		}
	}

	if existing.Email != user.Email {
		if err := s.ValidateEmail(user.Email); err != nil {
			return err
		}

		// Check if new email is taken
		var count int64
		s.db.Model(&models.User{}).Where("email = ? AND id != ?", user.Email, user.ID).Count(&count)
		if count > 0 {
			return errors.New("email already exists")
		}

		// Mark email as unverified if changed
		user.EmailVerified = false
	}

	if err := s.db.Save(user).Error; err != nil {
		return err
	}

	// Invalidate cache after update
	if s.cache != nil {
		ctx := context.Background()
		// Invalidate both old and new username caches
		oldUserCacheKey := cache.UserKey(existing.Username)
		newUserCacheKey := cache.UserKey(user.Username)
		s.cache.Delete(ctx, oldUserCacheKey)
		s.cache.Delete(ctx, newUserCacheKey)

		// Also invalidate user stats cache
		userStatsKey := cache.UserStatsKey(user.ID.String())
		s.cache.Delete(ctx, userStatsKey)
	}

	return nil
}

// DeleteUser soft deletes a user
func (s *UserService) DeleteUser(userID uuid.UUID) error {
	return s.db.Delete(&models.User{}, "id = ?", userID).Error
}

// ValidateUsername validates username format
func (s *UserService) ValidateUsername(username string) error {
	if len(username) < 3 || len(username) > 39 {
		return errors.New("username must be between 3 and 39 characters")
	}

	// Check pattern: alphanumeric with hyphens, but not starting/ending with hyphen
	if !isValidUsername(username) {
		return errors.New("username can only contain letters, numbers, and hyphens")
	}

	// Check blacklist
	blacklist := []string{"admin", "administrator", "root", "system", "api", "www", "mail", "ftp"}
	lowerUsername := strings.ToLower(username)
	for _, blocked := range blacklist {
		if lowerUsername == blocked {
			return fmt.Errorf("username '%s' is reserved", username)
		}
	}

	return nil
}

// ValidateEmail validates email format
func (s *UserService) ValidateEmail(email string) error {
	if len(email) > 255 {
		return errors.New("email must be less than 255 characters")
	}

	// Basic email validation
	parts := strings.Split(email, "@")
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return errors.New("invalid email format")
	}

	// Check domain has at least one dot
	if !strings.Contains(parts[1], ".") {
		return errors.New("invalid email domain")
	}

	return nil
}

// GetUserStats returns statistics for a user
func (s *UserService) GetUserStats(userID uuid.UUID) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Count gists
	var gistCount int64
	s.db.Model(&models.Gist{}).Where("user_id = ? AND deleted_at IS NULL", userID).Count(&gistCount)
	stats["gists"] = gistCount

	// Count public gists
	var publicGistCount int64
	s.db.Model(&models.Gist{}).Where("user_id = ? AND visibility = ? AND deleted_at IS NULL", userID, "public").Count(&publicGistCount)
	stats["public_gists"] = publicGistCount

	// Count followers
	var followerCount int64
	s.db.Model(&models.UserFollow{}).Where("following_id = ?", userID).Count(&followerCount)
	stats["followers"] = followerCount

	// Count following
	var followingCount int64
	s.db.Model(&models.UserFollow{}).Where("follower_id = ?", userID).Count(&followingCount)
	stats["following"] = followingCount

	// Count stars received
	var starCount int64
	s.db.Table("gist_stars").
		Joins("JOIN gists ON gist_stars.gist_id = gists.id").
		Where("gists.user_id = ?", userID).
		Count(&starCount)
	stats["stars_received"] = starCount

	// Count organizations
	var orgCount int64
	s.db.Model(&models.OrganizationMember{}).Where("user_id = ?", userID).Count(&orgCount)
	stats["organizations"] = orgCount

	return stats, nil
}

// FollowUser creates a follow relationship
func (s *UserService) FollowUser(followerID, followingID uuid.UUID) error {
	if followerID == followingID {
		return errors.New("cannot follow yourself")
	}
	if s.IsFollowing(followerID, followingID) {
		return errors.New("already following this user")
	}

	follow := &models.UserFollow{
		FollowerID:  followerID,
		FollowingID: followingID,
	}

	if err := s.db.Create(follow).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return errors.New("already following this user")
		}
		return err
	}

	// Send email notification to the user being followed
	if s.emailService != nil {
		var followerUser, followingUser models.User
		if s.db.First(&followerUser, "id = ?", followerID).Error == nil &&
			s.db.First(&followingUser, "id = ?", followingID).Error == nil {
			go s.emailService.SendUserFollowedNotification(
				followingID,
				followingUser.Email,
				followingUser.DisplayName,
				followerUser.DisplayName,
				followerID,
			)
		}
	}

	return nil
}

// UnfollowUser removes a follow relationship
func (s *UserService) UnfollowUser(followerID, followingID uuid.UUID) error {
	result := s.db.Delete(&models.UserFollow{}, "follower_id = ? AND following_id = ?", followerID, followingID)
	if result.RowsAffected == 0 {
		return errors.New("not following this user")
	}
	return result.Error
}

// IsFollowing checks if one user follows another
func (s *UserService) IsFollowing(followerID, followingID uuid.UUID) bool {
	var count int64
	s.db.Model(&models.UserFollow{}).
		Where("follower_id = ? AND following_id = ?", followerID, followingID).
		Count(&count)
	return count > 0
}

// GetFollowers returns a user's followers
func (s *UserService) GetFollowers(userID uuid.UUID, limit, offset int) ([]models.User, error) {
	var followers []models.User
	err := s.db.Table("users").
		Joins("JOIN user_follows ON users.id = user_follows.follower_id").
		Where("user_follows.following_id = ?", userID).
		Limit(limit).
		Offset(offset).
		Find(&followers).Error
	return followers, err
}

// GetFollowing returns users that a user follows
func (s *UserService) GetFollowing(userID uuid.UUID, limit, offset int) ([]models.User, error) {
	var following []models.User
	err := s.db.Table("users").
		Joins("JOIN user_follows ON users.id = user_follows.following_id").
		Where("user_follows.follower_id = ?", userID).
		Limit(limit).
		Offset(offset).
		Find(&following).Error
	return following, err
}

// BlockUser creates a block relationship
func (s *UserService) BlockUser(blockerID, blockedID uuid.UUID, reason string) error {
	if blockerID == blockedID {
		return errors.New("cannot block yourself")
	}
	if s.IsBlocked(blockerID, blockedID) {
		return errors.New("user already blocked")
	}

	// Remove any follow relationships
	s.db.Delete(&models.UserFollow{}, "follower_id = ? AND following_id = ?", blockerID, blockedID)
	s.db.Delete(&models.UserFollow{}, "follower_id = ? AND following_id = ?", blockedID, blockerID)

	block := &models.UserBlock{
		BlockerID: blockerID,
		BlockedID: blockedID,
		Reason:    reason,
	}

	if err := s.db.Create(block).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			return errors.New("user already blocked")
		}
		return err
	}

	return nil
}

// UnblockUser removes a block relationship
func (s *UserService) UnblockUser(blockerID, blockedID uuid.UUID) error {
	result := s.db.Delete(&models.UserBlock{}, "blocker_id = ? AND blocked_id = ?", blockerID, blockedID)
	if result.RowsAffected == 0 {
		return errors.New("user not blocked")
	}
	return result.Error
}

// IsBlocked checks if one user has blocked another
func (s *UserService) IsBlocked(blockerID, blockedID uuid.UUID) bool {
	var count int64
	s.db.Model(&models.UserBlock{}).
		Where("blocker_id = ? AND blocked_id = ?", blockerID, blockedID).
		Count(&count)
	return count > 0
}

// isValidUsername checks if username matches required pattern
func isValidUsername(username string) bool {
	if len(username) == 0 {
		return false
	}

	// Must start and end with alphanumeric
	if !isAlphanumeric(username[0]) || !isAlphanumeric(username[len(username)-1]) {
		return false
	}

	// Check all characters
	for i, char := range username {
		if !isAlphanumeric(byte(char)) && char != '-' {
			return false
		}
		// No consecutive hyphens
		if char == '-' && i > 0 && username[i-1] == '-' {
			return false
		}
	}

	return true
}

func isAlphanumeric(char byte) bool {
	return (char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9')
}
