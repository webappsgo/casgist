package webhooks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

func setupWebhookTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// Auto migrate for tests
	err = db.AutoMigrate(
		&models.User{},
		&models.Webhook{},
		&models.WebhookDelivery{},
		&models.Gist{},
		&models.GistFile{},
		&models.Tag{},
	)
	require.NoError(t, err)

	return db
}

func TestWebhookService(t *testing.T) {
	db := setupWebhookTestDB(t)
	cfg := viper.New()
	cfg.Set("webhooks.enabled", true)
	cfg.Set("webhooks.timeout_seconds", 5)
	cfg.Set("webhooks.max_retries", 2)

	service := NewService(db, cfg)
	require.NotNil(t, service)

	// Create a test user
	user := &models.User{
		Username: "testuser",
		Email:    "test@example.com",
	}
	require.NoError(t, db.Create(user).Error)

	t.Run("CreateWebhook", func(t *testing.T) {
		input := CreateWebhookInput{
			URL:    "https://example.com/webhook",
			Events: []string{"gist.created", "gist.updated"},
			Secret: "test-secret",
		}

		webhook, err := service.CreateWebhook(user.ID, input)
		assert.NoError(t, err)
		assert.NotNil(t, webhook)
		assert.Equal(t, input.URL, webhook.URL)
		assert.Equal(t, input.Secret, webhook.Secret)
		assert.True(t, webhook.IsActive)
		assert.Equal(t, "application/json", webhook.ContentType)

		// Verify events are stored as JSON
		var events []string
		err = json.Unmarshal([]byte(webhook.Events), &events)
		assert.NoError(t, err)
		assert.Equal(t, input.Events, events)
	})

	t.Run("CreateWebhookInvalidEvents", func(t *testing.T) {
		input := CreateWebhookInput{
			URL:    "https://example.com/webhook",
			Events: []string{"invalid.event"},
		}

		_, err := service.CreateWebhook(user.ID, input)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid events")
	})

	t.Run("ListWebhooks", func(t *testing.T) {
		// Create multiple webhooks
		for i := 0; i < 3; i++ {
			input := CreateWebhookInput{
				URL:    "https://example.com/webhook" + string(rune(i)),
				Events: []string{"gist.created"},
			}
			_, err := service.CreateWebhook(user.ID, input)
			require.NoError(t, err)
		}

		webhooks, total, err := service.ListWebhooks(&user.ID, 1, 10)
		assert.NoError(t, err)
		assert.True(t, len(webhooks) >= 3)
		assert.True(t, total >= 3)
	})

	t.Run("UpdateWebhook", func(t *testing.T) {
		// Create webhook
		input := CreateWebhookInput{
			URL:    "https://example.com/webhook-original",
			Events: []string{"gist.created"},
		}

		webhook, err := service.CreateWebhook(user.ID, input)
		require.NoError(t, err)

		// Update webhook
		newURL := "https://example.com/webhook-updated"
		isActive := false
		updateInput := UpdateWebhookInput{
			URL:      &newURL,
			IsActive: &isActive,
		}

		updatedWebhook, err := service.UpdateWebhook(webhook.ID, &user.ID, updateInput)
		assert.NoError(t, err)
		assert.Equal(t, newURL, updatedWebhook.URL)
		assert.False(t, updatedWebhook.IsActive)
	})

	t.Run("DeleteWebhook", func(t *testing.T) {
		// Create webhook
		input := CreateWebhookInput{
			URL:    "https://example.com/webhook-delete",
			Events: []string{"gist.created"},
		}

		webhook, err := service.CreateWebhook(user.ID, input)
		require.NoError(t, err)

		// Delete webhook
		err = service.DeleteWebhook(webhook.ID, &user.ID)
		assert.NoError(t, err)

		// Verify it's deleted
		_, err = service.GetWebhook(webhook.ID, &user.ID)
		assert.Error(t, err)
	})

	t.Run("EventValidation", func(t *testing.T) {
		validEvents := []string{
			"gist.created",
			"gist.updated", 
			"gist.deleted",
			"gist.starred",
			"gist.forked",
			"user.created",
			"user.updated",
			"user.followed",
			"*",
		}

		for _, event := range validEvents {
			err := service.validateEvents([]string{event})
			assert.NoError(t, err, "Event %s should be valid", event)
		}

		// Test invalid event
		err := service.validateEvents([]string{"invalid.event"})
		assert.Error(t, err)
	})
}

func TestWebhookManager(t *testing.T) {
	db := setupWebhookTestDB(t)
	cfg := viper.New()
	cfg.Set("webhooks.enabled", true)
	cfg.Set("webhooks.timeout_seconds", 5)
	cfg.Set("webhooks.max_retries", 1)

	manager := NewManager(db, cfg)
	require.NotNil(t, manager)

	// Create test user
	user := &models.User{
		Username: "testuser",
		Email:    "test@example.com",
	}
	require.NoError(t, db.Create(user).Error)

	t.Run("CreateAndGetWebhook", func(t *testing.T) {
		webhook := &models.Webhook{
			UserID:      &user.ID,
			URL:         "https://example.com/webhook",
			Events:      `["gist.created"]`,
			IsActive:    true,
			ContentType: "application/json",
		}

		err := manager.CreateWebhook(webhook)
		assert.NoError(t, err)
		assert.NotEmpty(t, webhook.Secret)

		// Get webhook
		retrieved, err := manager.GetWebhook(webhook.ID, &user.ID)
		assert.NoError(t, err)
		assert.Equal(t, webhook.URL, retrieved.URL)
	})

	t.Run("WebhookSubscriptionToEvent", func(t *testing.T) {
		webhook := models.Webhook{
			Events: `["gist.created", "gist.updated"]`,
		}

		// Test specific events
		assert.True(t, manager.webhookSubscribesToEvent(webhook, EventGistCreated))
		assert.True(t, manager.webhookSubscribesToEvent(webhook, EventGistUpdated))
		assert.False(t, manager.webhookSubscribesToEvent(webhook, EventGistDeleted))

		// Test wildcard
		webhookWildcard := models.Webhook{
			Events: `["*"]`,
		}
		assert.True(t, manager.webhookSubscribesToEvent(webhookWildcard, EventGistCreated))
		assert.True(t, manager.webhookSubscribesToEvent(webhookWildcard, EventUserCreated))
	})

	t.Run("TriggerEvent", func(t *testing.T) {
		// Create webhook
		webhook := &models.Webhook{
			UserID:      &user.ID,
			URL:         "https://httpbin.org/post", // This will fail but we can test the flow
			Events:      `["gist.created"]`,
			IsActive:    true,
			ContentType: "application/json",
		}
		require.NoError(t, manager.CreateWebhook(webhook))

		// Trigger event
		testData := map[string]string{
			"test": "data",
		}

		err := manager.TriggerEvent(context.Background(), EventGistCreated, testData, &user.ID)
		assert.NoError(t, err)

		// Wait a bit for async processing
		time.Sleep(100 * time.Millisecond)

		// Check that delivery was recorded
		deliveries, total, err := manager.GetDeliveries(webhook.ID, 1, 10)
		assert.NoError(t, err)
		assert.True(t, total > 0)
		assert.True(t, len(deliveries) > 0)
	})
}

func TestWebhookEventData(t *testing.T) {
	cfg := viper.New()
	cfg.Set("server.url", "https://example.com")

	service := &Service{cfg: cfg}

	t.Run("GistToEventData", func(t *testing.T) {
		gist := &models.Gist{
			ID:          uuid.New(),
			Title:       "Test Gist",
			Description: "Test Description",
			Visibility:  models.VisibilityPublic,
			Files: []models.GistFile{
				{Filename: "test.go"},
				{Filename: "README.md"},
			},
			Tags: []models.Tag{
				{Name: "golang"},
				{Name: "test"},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		eventData := service.gistToEventData(gist)
		assert.Equal(t, gist.ID, eventData.ID)
		assert.Equal(t, gist.Title, eventData.Title)
		assert.Equal(t, gist.Description, eventData.Description)
		assert.Equal(t, string(gist.Visibility), eventData.Visibility)
		assert.Equal(t, 2, eventData.FilesCount)
		assert.Contains(t, eventData.URL, gist.ID.String())
		assert.Len(t, eventData.Tags, 2)
		assert.Contains(t, eventData.Tags, "golang")
		assert.Contains(t, eventData.Tags, "test")
	})

	t.Run("UserToEventData", func(t *testing.T) {
		user := &models.User{
			ID:          uuid.New(),
			Username:    "testuser",
			DisplayName: "Test User",
			Bio:         "Test Bio",
			Location:    "Test Location",
			Website:     "https://example.com",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		eventData := service.userToEventData(user)
		assert.Equal(t, user.ID, eventData.ID)
		assert.Equal(t, user.Username, eventData.Username)
		assert.Equal(t, user.DisplayName, eventData.DisplayName)
		assert.Equal(t, user.Bio, eventData.Bio)
		assert.Equal(t, user.Location, eventData.Location)
		assert.Equal(t, user.Website, eventData.Website)
	})
}