package services

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

func setupGistTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// Auto migrate for tests
	err = db.AutoMigrate(
		&models.User{},
		&models.Gist{},
		&models.GistFile{},
		&models.GistStar{},
		&models.Tag{},
		&models.GistTag{},
	)
	require.NoError(t, err)

	return db
}

func TestGistService(t *testing.T) {
	db := setupGistTestDB(t)
	cfg := viper.New()
	
	gistService := NewGistService(db, cfg, nil, nil) // nil cache and email service for testing
	require.NotNil(t, gistService)

	// Create a test user
	user := &models.User{
		Username: "testuser",
		Email:    "test@example.com",
	}
	require.NoError(t, db.Create(user).Error)

	t.Run("CreateGist", func(t *testing.T) {
		input := CreateGistInput{
			Title:       "Test Gist",
			Description: "This is a test gist",
			Visibility:  models.VisibilityPublic,
			Files: []CreateFileInput{
				{
					Filename: "hello.go",
					Content:  "package main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}",
					Language: "go",
				},
				{
					Filename: "README.md",
					Content:  "# Test Gist\n\nThis is a test.",
				},
			},
			Tags: []string{"golang", "test"},
		}

		gist, err := gistService.CreateGist(user.ID, input)
		assert.NoError(t, err)
		assert.NotNil(t, gist)
		assert.Equal(t, "Test Gist", gist.Title)
		assert.Equal(t, "This is a test gist", gist.Description)
		assert.Equal(t, models.VisibilityPublic, gist.Visibility)
		assert.Len(t, gist.Files, 2)
		assert.Len(t, gist.Tags, 2)

		// Check files
		for _, file := range gist.Files {
			assert.NotEqual(t, uuid.Nil, file.ID)
			assert.Equal(t, gist.ID, file.GistID)
			assert.True(t, file.Size > 0)
			assert.True(t, file.Lines > 0)
		}
	})

	t.Run("CreateGistNoFiles", func(t *testing.T) {
		input := CreateGistInput{
			Title:       "Empty Gist",
			Description: "This gist has no files",
			Visibility:  models.VisibilityPublic,
			Files:       []CreateFileInput{}, // No files
		}

		_, err := gistService.CreateGist(user.ID, input)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one file is required")
	})

	t.Run("ValidateFilename", func(t *testing.T) {
		// Valid filenames
		assert.NoError(t, gistService.ValidateFilename("hello.go"))
		assert.NoError(t, gistService.ValidateFilename("README.md"))
		assert.NoError(t, gistService.ValidateFilename("config.yaml"))
		assert.NoError(t, gistService.ValidateFilename("script.sh"))

		// Invalid filenames
		assert.Error(t, gistService.ValidateFilename("")) // empty
		assert.Error(t, gistService.ValidateFilename("../etc/passwd")) // path traversal
		assert.Error(t, gistService.ValidateFilename("file/with/slash")) // slash
		assert.Error(t, gistService.ValidateFilename("file\\backslash")) // backslash
		assert.Error(t, gistService.ValidateFilename("file\x00null")) // null byte
	})

	t.Run("DetectLanguage", func(t *testing.T) {
		// Test various file extensions
		assert.Equal(t, "go", gistService.DetectLanguage("main.go"))
		assert.Equal(t, "javascript", gistService.DetectLanguage("script.js"))
		assert.Equal(t, "python", gistService.DetectLanguage("script.py"))
		assert.Equal(t, "java", gistService.DetectLanguage("Main.java"))
		assert.Equal(t, "markdown", gistService.DetectLanguage("README.md"))
		assert.Equal(t, "json", gistService.DetectLanguage("package.json"))
		assert.Equal(t, "yaml", gistService.DetectLanguage("config.yaml"))
		assert.Equal(t, "dockerfile", gistService.DetectLanguage("Dockerfile"))
		assert.Equal(t, "makefile", gistService.DetectLanguage("Makefile"))
		assert.Equal(t, "text", gistService.DetectLanguage("unknown.xyz"))
	})

	t.Run("GetGist", func(t *testing.T) {
		// Create a gist first
		input := CreateGistInput{
			Title:      "Get Test Gist",
			Visibility: models.VisibilityPublic,
			Files: []CreateFileInput{
				{
					Filename: "test.txt",
					Content:  "Hello, World!",
				},
			},
		}

		createdGist, err := gistService.CreateGist(user.ID, input)
		require.NoError(t, err)

		// Get the gist
		gist, err := gistService.GetGist(createdGist.ID, &user.ID)
		assert.NoError(t, err)
		assert.Equal(t, createdGist.ID, gist.ID)
		assert.Equal(t, "Get Test Gist", gist.Title)

		// Try to get non-existent gist
		_, err = gistService.GetGist(uuid.New(), &user.ID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "gist not found")
	})

	t.Run("StarGist", func(t *testing.T) {
		// Create a gist
		input := CreateGistInput{
			Title:      "Star Test Gist",
			Visibility: models.VisibilityPublic,
			Files: []CreateFileInput{
				{Filename: "test.txt", Content: "test"},
			},
		}

		gist, err := gistService.CreateGist(user.ID, input)
		require.NoError(t, err)

		// Create another user to star the gist
		user2 := &models.User{Username: "starrer", Email: "starrer@example.com"}
		require.NoError(t, db.Create(user2).Error)

		// Star the gist
		err = gistService.StarGist(gist.ID, user2.ID, true)
		assert.NoError(t, err)

		// Check if starred
		isStarred := gistService.IsStarred(gist.ID, user2.ID)
		assert.True(t, isStarred)

		// Try to star again (should fail)
		err = gistService.StarGist(gist.ID, user2.ID, true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already starred")

		// Unstar the gist
		err = gistService.StarGist(gist.ID, user2.ID, false)
		assert.NoError(t, err)

		// Check if not starred
		isStarred = gistService.IsStarred(gist.ID, user2.ID)
		assert.False(t, isStarred)

		// Try to unstar again (should fail)
		err = gistService.StarGist(gist.ID, user2.ID, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not starred")
	})

	t.Run("ForkGist", func(t *testing.T) {
		// Create a gist
		input := CreateGistInput{
			Title:      "Fork Test Gist",
			Visibility: models.VisibilityPublic,
			Files: []CreateFileInput{
				{Filename: "original.txt", Content: "Original content"},
			},
			Tags: []string{"original", "test"},
		}

		originalGist, err := gistService.CreateGist(user.ID, input)
		require.NoError(t, err)

		// Create another user to fork the gist
		user2 := &models.User{Username: "forker", Email: "forker@example.com"}
		require.NoError(t, db.Create(user2).Error)

		// Fork the gist
		fork, err := gistService.ForkGist(originalGist.ID, user2.ID)
		assert.NoError(t, err)
		assert.NotNil(t, fork)
		assert.Equal(t, originalGist.Title, fork.Title)
		assert.Equal(t, &originalGist.ID, fork.ForkedFromID)
		assert.Equal(t, &user2.ID, fork.UserID)
		assert.Len(t, fork.Files, 1)
		assert.Len(t, fork.Tags, 2)

		// Try to fork again (should fail)
		_, err = gistService.ForkGist(originalGist.ID, user2.ID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already forked")
	})

	t.Run("UpdateGist", func(t *testing.T) {
		// Create a gist
		input := CreateGistInput{
			Title:      "Update Test Gist",
			Visibility: models.VisibilityPublic,
			Files: []CreateFileInput{
				{Filename: "original.txt", Content: "Original content"},
			},
		}

		gist, err := gistService.CreateGist(user.ID, input)
		require.NoError(t, err)

		// Update the gist
		newTitle := "Updated Test Gist"
		newDescription := "This gist has been updated"
		updateInput := UpdateGistInput{
			Title:       &newTitle,
			Description: &newDescription,
			Files: []UpdateFileInput{
				{
					ID:       &gist.Files[0].ID,
					Filename: "updated.txt",
					Content:  "Updated content",
				},
			},
		}

		updatedGist, err := gistService.UpdateGist(gist.ID, user.ID, updateInput)
		assert.NoError(t, err)
		assert.Equal(t, "Updated Test Gist", updatedGist.Title)
		assert.Equal(t, "This gist has been updated", updatedGist.Description)
		assert.Equal(t, "updated.txt", updatedGist.Files[0].Filename)
		assert.Equal(t, "Updated content", updatedGist.Files[0].Content)
	})

	t.Run("DeleteGist", func(t *testing.T) {
		// Create a gist
		input := CreateGistInput{
			Title:      "Delete Test Gist",
			Visibility: models.VisibilityPublic,
			Files: []CreateFileInput{
				{Filename: "delete.txt", Content: "To be deleted"},
			},
		}

		gist, err := gistService.CreateGist(user.ID, input)
		require.NoError(t, err)

		// Delete the gist
		err = gistService.DeleteGist(gist.ID, user.ID)
		assert.NoError(t, err)

		// Try to get deleted gist (should fail)
		_, err = gistService.GetGist(gist.ID, &user.ID)
		assert.Error(t, err)

		// Try to delete again (should fail)
		err = gistService.DeleteGist(gist.ID, user.ID)
		assert.Error(t, err)
	})

	t.Run("ListGists", func(t *testing.T) {
		// Create several gists
		for i := 0; i < 5; i++ {
			input := CreateGistInput{
				Title:      fmt.Sprintf("List Test Gist %d", i),
				Visibility: models.VisibilityPublic,
				Files: []CreateFileInput{
					{Filename: "test.txt", Content: "test content"},
				},
			}
			_, err := gistService.CreateGist(user.ID, input)
			require.NoError(t, err)
		}

		// List gists
		opts := ListGistsOptions{
			Page:  1,
			Limit: 3,
		}

		gists, total, err := gistService.ListGists(opts, &user.ID)
		assert.NoError(t, err)
		assert.True(t, len(gists) <= 3)
		assert.True(t, total >= 5) // At least the 5 we created
	})
}