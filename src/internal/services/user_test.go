package services

import (
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// Auto migrate for tests
	err = db.AutoMigrate(
		&models.User{},
		&models.UserPreference{},
		&models.UserFollow{},
		&models.UserBlock{},
		&models.Gist{},
	)
	require.NoError(t, err)

	return db
}

func TestUserService(t *testing.T) {
	db := setupTestDB(t)
	cfg := viper.New()
	
	userService := NewUserService(db, cfg, nil, nil) // nil cache and email service for testing
	require.NotNil(t, userService)

	t.Run("CreateUser", func(t *testing.T) {
		user := &models.User{
			Username: "testuser",
			Email:    "test@example.com",
		}

		err := userService.CreateUser(user)
		assert.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, user.ID)

		// Try to create duplicate username
		user2 := &models.User{
			Username: "testuser",
			Email:    "test2@example.com",
		}
		err = userService.CreateUser(user2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username already exists")

		// Try to create duplicate email
		user3 := &models.User{
			Username: "testuser2",
			Email:    "test@example.com",
		}
		err = userService.CreateUser(user3)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "email already exists")
	})

	t.Run("ValidateUsername", func(t *testing.T) {
		// Valid usernames
		assert.NoError(t, userService.ValidateUsername("valid"))
		assert.NoError(t, userService.ValidateUsername("valid-user"))
		assert.NoError(t, userService.ValidateUsername("valid123"))
		assert.NoError(t, userService.ValidateUsername("Valid-User-123"))

		// Invalid usernames
		assert.Error(t, userService.ValidateUsername("ab")) // too short
		assert.Error(t, userService.ValidateUsername("a")) // too short
		assert.Error(t, userService.ValidateUsername("-invalid")) // starts with hyphen
		assert.Error(t, userService.ValidateUsername("invalid-")) // ends with hyphen
		assert.Error(t, userService.ValidateUsername("invalid--user")) // consecutive hyphens
		assert.Error(t, userService.ValidateUsername("invalid_user")) // underscore
		assert.Error(t, userService.ValidateUsername("invalid user")) // space
		assert.Error(t, userService.ValidateUsername("admin")) // reserved
		assert.Error(t, userService.ValidateUsername("root")) // reserved
	})

	t.Run("ValidateEmail", func(t *testing.T) {
		// Valid emails
		assert.NoError(t, userService.ValidateEmail("test@example.com"))
		assert.NoError(t, userService.ValidateEmail("user.name@domain.co.uk"))
		assert.NoError(t, userService.ValidateEmail("user+tag@example.org"))

		// Invalid emails
		assert.Error(t, userService.ValidateEmail("invalid")) // no @
		assert.Error(t, userService.ValidateEmail("@example.com")) // no local part
		assert.Error(t, userService.ValidateEmail("test@")) // no domain
		assert.Error(t, userService.ValidateEmail("test@invalid")) // no TLD
	})

	t.Run("GetUserByUsername", func(t *testing.T) {
		// Create a user first
		user := &models.User{
			Username: "gettest",
			Email:    "gettest@example.com",
		}
		err := userService.CreateUser(user)
		require.NoError(t, err)

		// Get the user
		foundUser, err := userService.GetUserByUsername("gettest")
		assert.NoError(t, err)
		assert.Equal(t, user.ID, foundUser.ID)
		assert.Equal(t, "gettest", foundUser.Username)

		// Try to get non-existent user
		_, err = userService.GetUserByUsername("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user not found")
	})

	t.Run("UpdateUser", func(t *testing.T) {
		// Create a user first
		user := &models.User{
			Username: "updatetest",
			Email:    "updatetest@example.com",
			Bio:      "Original bio",
		}
		err := userService.CreateUser(user)
		require.NoError(t, err)

		// Update user
		user.Bio = "Updated bio"
		user.Website = "https://example.com"
		err = userService.UpdateUser(user)
		assert.NoError(t, err)

		// Verify update
		updatedUser, err := userService.GetUserByUsername("updatetest")
		assert.NoError(t, err)
		assert.Equal(t, "Updated bio", updatedUser.Bio)
		assert.Equal(t, "https://example.com", updatedUser.Website)
	})

	t.Run("FollowUser", func(t *testing.T) {
		// Create two users
		user1 := &models.User{Username: "follower", Email: "follower@example.com"}
		user2 := &models.User{Username: "following", Email: "following@example.com"}
		
		require.NoError(t, userService.CreateUser(user1))
		require.NoError(t, userService.CreateUser(user2))

		// Follow user
		err := userService.FollowUser(user1.ID, user2.ID)
		assert.NoError(t, err)

		// Check if following
		isFollowing := userService.IsFollowing(user1.ID, user2.ID)
		assert.True(t, isFollowing)

		// Try to follow again (should fail)
		err = userService.FollowUser(user1.ID, user2.ID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already following")

		// Try to follow self (should fail)
		err = userService.FollowUser(user1.ID, user1.ID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot follow yourself")
	})

	t.Run("UnfollowUser", func(t *testing.T) {
		// Create two users
		user1 := &models.User{Username: "unfollower", Email: "unfollower@example.com"}
		user2 := &models.User{Username: "unfollowing", Email: "unfollowing@example.com"}
		
		require.NoError(t, userService.CreateUser(user1))
		require.NoError(t, userService.CreateUser(user2))

		// Follow first
		require.NoError(t, userService.FollowUser(user1.ID, user2.ID))

		// Unfollow
		err := userService.UnfollowUser(user1.ID, user2.ID)
		assert.NoError(t, err)

		// Check if not following
		isFollowing := userService.IsFollowing(user1.ID, user2.ID)
		assert.False(t, isFollowing)

		// Try to unfollow again (should fail)
		err = userService.UnfollowUser(user1.ID, user2.ID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not following")
	})

	t.Run("BlockUser", func(t *testing.T) {
		// Create two users
		user1 := &models.User{Username: "blocker", Email: "blocker@example.com"}
		user2 := &models.User{Username: "blocked", Email: "blocked@example.com"}
		
		require.NoError(t, userService.CreateUser(user1))
		require.NoError(t, userService.CreateUser(user2))

		// Follow first
		require.NoError(t, userService.FollowUser(user1.ID, user2.ID))
		require.NoError(t, userService.FollowUser(user2.ID, user1.ID))

		// Block user
		err := userService.BlockUser(user1.ID, user2.ID, "Spam")
		assert.NoError(t, err)

		// Check if blocked
		isBlocked := userService.IsBlocked(user1.ID, user2.ID)
		assert.True(t, isBlocked)

		// Check if follow relationships are removed
		assert.False(t, userService.IsFollowing(user1.ID, user2.ID))
		assert.False(t, userService.IsFollowing(user2.ID, user1.ID))

		// Try to block again (should fail)
		err = userService.BlockUser(user1.ID, user2.ID, "Spam")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already blocked")

		// Try to block self (should fail)
		err = userService.BlockUser(user1.ID, user1.ID, "Test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot block yourself")
	})

	t.Run("UnblockUser", func(t *testing.T) {
		// Create two users
		user1 := &models.User{Username: "unblocker", Email: "unblocker@example.com"}
		user2 := &models.User{Username: "unblocked", Email: "unblocked@example.com"}
		
		require.NoError(t, userService.CreateUser(user1))
		require.NoError(t, userService.CreateUser(user2))

		// Block first
		require.NoError(t, userService.BlockUser(user1.ID, user2.ID, "Test"))

		// Unblock
		err := userService.UnblockUser(user1.ID, user2.ID)
		assert.NoError(t, err)

		// Check if not blocked
		isBlocked := userService.IsBlocked(user1.ID, user2.ID)
		assert.False(t, isBlocked)

		// Try to unblock again (should fail)
		err = userService.UnblockUser(user1.ID, user2.ID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not blocked")
	})

	t.Run("GetUserStats", func(t *testing.T) {
		// Create a user
		user := &models.User{Username: "statstest", Email: "stats@example.com"}
		require.NoError(t, userService.CreateUser(user))

		// Get stats (should be zero for new user)
		stats, err := userService.GetUserStats(user.ID)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), stats["gists"])
		assert.Equal(t, int64(0), stats["public_gists"])
		assert.Equal(t, int64(0), stats["followers"])
		assert.Equal(t, int64(0), stats["following"])
	})
}