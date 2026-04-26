package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DeliveryService handles webhook deliveries with retry logic
type DeliveryService struct {
	db         *gorm.DB
	client     *http.Client
	maxRetries int
	retryDelay time.Duration
}

// NewDeliveryService creates a new webhook delivery service
func NewDeliveryService(db *gorm.DB) *DeliveryService {
	return &DeliveryService{
		db: db,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxRetries: 4,
		retryDelay: 5 * time.Minute,
	}
}

// WebhookPayload represents the webhook payload structure
type WebhookPayload struct {
	Event     string                 `json:"event"`
	Timestamp time.Time             `json:"timestamp"`
	Server    map[string]interface{} `json:"server"`
	Data      map[string]interface{} `json:"data"`
}

// DeliverWebhook delivers a webhook with retry logic
func (d *DeliveryService) DeliverWebhook(ctx context.Context, webhook *models.Webhook, payload *WebhookPayload) error {
	// Serialize payload for storage
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	
	// Create delivery record
	delivery := &models.WebhookDelivery{
		ID:        uuid.New(),
		WebhookID: webhook.ID,
		Event:     payload.Event,
		Payload:   string(payloadJSON),
		URL:       webhook.URL,
		Attempts:  1,
		CreatedAt: time.Now(),
	}

	// Attempt delivery
	success, statusCode, responseBody, responseTime := d.attemptDelivery(ctx, webhook, payload)
	
	delivery.StatusCode = statusCode
	delivery.Error = responseBody
	delivery.Duration = responseTime
	delivery.Success = success

	if success {
		if err := d.db.Create(delivery).Error; err != nil {
			return fmt.Errorf("failed to save delivery record: %w", err)
		}
		return nil
	}

	// Schedule retry if not successful
	nextRetry := time.Now().Add(d.retryDelay)
	delivery.NextRetry = &nextRetry

	if err := d.db.Create(delivery).Error; err != nil {
		return fmt.Errorf("failed to save delivery record: %w", err)
	}

	return fmt.Errorf("webhook delivery failed, will retry")
}

// attemptDelivery attempts to deliver a webhook
func (d *DeliveryService) attemptDelivery(ctx context.Context, webhook *models.Webhook, payload *WebhookPayload) (bool, int, string, int64) {
	startTime := time.Now()

	// Serialize payload
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return false, 0, fmt.Sprintf("JSON marshal error: %v", err), 0
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewBuffer(payloadJSON))
	if err != nil {
		return false, 0, fmt.Sprintf("Request creation error: %v", err), 0
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "CasGists-Webhook/1.0")
	req.Header.Set("X-CasGists-Event", payload.Event)
	req.Header.Set("X-CasGists-Delivery", uuid.New().String())

	// Add HMAC signature if secret is provided
	if webhook.Secret != "" {
		signature := d.generateSignature(payloadJSON, webhook.Secret)
		req.Header.Set("X-CasGists-Signature-256", signature)
	}

	// Make request
	resp, err := d.client.Do(req)
	responseTime := time.Since(startTime).Milliseconds()

	if err != nil {
		return false, 0, fmt.Sprintf("HTTP error: %v", err), responseTime
	}
	defer resp.Body.Close()

	// Read response body (limited to prevent memory issues)
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	responseBody := string(buf[:n])

	// Consider 2xx status codes as success
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	return success, resp.StatusCode, responseBody, responseTime
}

// generateSignature generates HMAC-SHA256 signature for webhook payload
func (d *DeliveryService) generateSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))
	return "sha256=" + signature
}

// RetryFailedDeliveries retries failed webhook deliveries
func (d *DeliveryService) RetryFailedDeliveries(ctx context.Context) error {
	// Get deliveries that need retry
	var deliveries []models.WebhookDelivery
	if err := d.db.Where("success = ? AND attempts < ? AND next_retry <= ?", 
		false, d.maxRetries, time.Now()).Find(&deliveries).Error; err != nil {
		return fmt.Errorf("failed to get failed deliveries: %w", err)
	}

	for _, delivery := range deliveries {
		// Get webhook configuration
		var webhook models.Webhook
		if err := d.db.Where("id = ?", delivery.WebhookID).First(&webhook).Error; err != nil {
			continue // Skip if webhook is deleted
		}

		if !webhook.IsActive {
			continue // Skip if webhook is disabled
		}

		// Attempt redelivery
		var payload WebhookPayload
		if err := json.Unmarshal([]byte(delivery.Payload), &payload); err != nil {
			continue // Skip malformed payload
		}

		success, statusCode, responseBody, responseTime := d.attemptDelivery(ctx, &webhook, &payload)
		
		// Update delivery record
		delivery.Attempts++
		delivery.StatusCode = statusCode
		delivery.Error = responseBody
		delivery.Duration = responseTime
		delivery.Success = success

		if success {
			// Successful delivery - no more retries needed
			delivery.NextRetry = nil
		} else if delivery.Attempts < d.maxRetries {
			// Calculate next retry with exponential backoff
			delay := d.retryDelay * time.Duration(delivery.Attempts*delivery.Attempts)
			nextRetry := time.Now().Add(delay)
			delivery.NextRetry = &nextRetry
		} else {
			// Max retries reached
			delivery.NextRetry = nil
		}

		d.db.Save(&delivery)
	}

	return nil
}

// PublishEvent publishes an event to all subscribed webhooks
func (d *DeliveryService) PublishEvent(ctx context.Context, eventType string, data map[string]interface{}) error {
	// Get active webhooks that subscribe to this event
	var webhooks []models.Webhook
	if err := d.db.Where("is_active = ? AND events LIKE ?", true, "%"+eventType+"%").Find(&webhooks).Error; err != nil {
		return fmt.Errorf("failed to get webhooks: %w", err)
	}

	if len(webhooks) == 0 {
		return nil // No subscribers
	}

	// Create payload
	payload := &WebhookPayload{
		Event:     eventType,
		Timestamp: time.Now(),
		Server: map[string]interface{}{
			"name":    "CasGists",
			"version": "1.0.0",
			"url":     "", // Would be filled from config
		},
		Data: data,
	}

	// Deliver to all webhooks asynchronously
	for _, webhook := range webhooks {
		webhook := webhook // Capture for goroutine
		go func() {
			if err := d.DeliverWebhook(context.Background(), &webhook, payload); err != nil {
				// Log error but don't fail the entire operation
				fmt.Printf("Webhook delivery failed: %v\n", err)
			}
		}()
	}

	return nil
}

// EventData structures for different event types
type GistEventData struct {
	Gist         map[string]interface{} `json:"gist"`
	User         map[string]interface{} `json:"user"`
	Organization map[string]interface{} `json:"organization,omitempty"`
}

type UserEventData struct {
	User      map[string]interface{} `json:"user"`
	TargetUser map[string]interface{} `json:"target_user,omitempty"`
}

type CommentEventData struct {
	Comment      map[string]interface{} `json:"comment"`
	Gist         map[string]interface{} `json:"gist"`
	User         map[string]interface{} `json:"user"`
}

// Helper methods to publish specific events
func (d *DeliveryService) PublishGistCreated(ctx context.Context, gist *models.Gist) error {
	data := GistEventData{
		Gist: map[string]interface{}{
			"id":          gist.ID.String(),
			"title":       gist.Title,
			"description": gist.Description,
			"visibility":  string(gist.Visibility),
			"created_at":  gist.CreatedAt,
			"updated_at":  gist.UpdatedAt,
		},
	}

	if gist.User != nil {
		data.User = map[string]interface{}{
			"id":           gist.User.ID.String(),
			"username":     gist.User.Username,
			"display_name": gist.User.DisplayName,
		}
	}

	return d.PublishEvent(ctx, "gist.created", map[string]interface{}(data.Gist))
}

func (d *DeliveryService) PublishGistUpdated(ctx context.Context, gist *models.Gist) error {
	// Similar to PublishGistCreated but for update event
	return d.PublishEvent(ctx, "gist.updated", map[string]interface{}{
		"gist_id": gist.ID.String(),
		"title":   gist.Title,
	})
}

func (d *DeliveryService) PublishGistDeleted(ctx context.Context, gistID uuid.UUID) error {
	return d.PublishEvent(ctx, "gist.deleted", map[string]interface{}{
		"gist_id": gistID.String(),
	})
}