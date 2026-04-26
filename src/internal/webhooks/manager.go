package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// Manager handles webhook operations
type Manager struct {
	db     *gorm.DB
	cfg    *viper.Viper
	client *http.Client
}

// NewManager creates a new webhook manager
func NewManager(db *gorm.DB, cfg *viper.Viper) *Manager {
	return &Manager{
		db:  db,
		cfg: cfg,
		client: &http.Client{
			Timeout: time.Duration(cfg.GetInt("webhooks.timeout_seconds")) * time.Second,
		},
	}
}

// CreateWebhook creates a new webhook
func (m *Manager) CreateWebhook(webhook *models.Webhook) error {
	// Generate secret if not provided
	if webhook.Secret == "" {
		webhook.Secret = generateSecret()
	}

	return m.db.Create(webhook).Error
}

// GetWebhook retrieves a webhook by ID
func (m *Manager) GetWebhook(id uuid.UUID, userID *uuid.UUID) (*models.Webhook, error) {
	var webhook models.Webhook
	query := m.db.Where("id = ?", id)

	// If userID is provided, ensure the webhook belongs to the user or is system-wide
	if userID != nil {
		query = query.Where("user_id = ? OR user_id IS NULL", *userID)
	}

	if err := query.First(&webhook).Error; err != nil {
		return nil, err
	}

	return &webhook, nil
}

// ListWebhooks lists webhooks for a user or system-wide
func (m *Manager) ListWebhooks(userID *uuid.UUID, page, limit int) ([]models.Webhook, int64, error) {
	var webhooks []models.Webhook
	var total int64

	query := m.db.Model(&models.Webhook{})

	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	} else {
		query = query.Where("user_id IS NULL")
	}

	// Count total
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	offset := (page - 1) * limit
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&webhooks).Error; err != nil {
		return nil, 0, err
	}

	return webhooks, total, nil
}

// UpdateWebhook updates a webhook
func (m *Manager) UpdateWebhook(id uuid.UUID, userID *uuid.UUID, updates map[string]interface{}) error {
	query := m.db.Model(&models.Webhook{}).Where("id = ?", id)

	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	}

	result := query.Updates(updates)
	if result.RowsAffected == 0 {
		return fmt.Errorf("webhook not found")
	}

	return result.Error
}

// DeleteWebhook deletes a webhook
func (m *Manager) DeleteWebhook(id uuid.UUID, userID *uuid.UUID) error {
	query := m.db.Where("id = ?", id)

	if userID != nil {
		query = query.Where("user_id = ?", *userID)
	}

	result := query.Delete(&models.Webhook{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("webhook not found")
	}

	return result.Error
}

// TriggerEvent triggers webhooks for a specific event
func (m *Manager) TriggerEvent(ctx context.Context, event WebhookEvent, data interface{}, senderID *uuid.UUID) error {
	// Get all active webhooks that subscribe to this event
	webhooks, err := m.getWebhooksForEvent(event)
	if err != nil {
		return fmt.Errorf("failed to get webhooks: %w", err)
	}

	if len(webhooks) == 0 {
		return nil // No webhooks to trigger
	}

	// Get sender info if provided
	var sender *SenderInfo
	if senderID != nil {
		sender, _ = m.getSenderInfo(*senderID)
	}

	// Create payload
	payload := WebhookPayload{
		Event:     event,
		Timestamp: time.Now(),
		Data:      data,
		Sender:    sender,
	}

	// Dispatch webhooks synchronously to ensure delivery records exist before returning.
	for _, webhook := range webhooks {
		m.sendWebhook(ctx, webhook, payload)
	}

	return nil
}

// sendWebhook sends a webhook with retry logic
func (m *Manager) sendWebhook(ctx context.Context, webhook models.Webhook, payload WebhookPayload) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		m.recordDelivery(webhook.ID, string(payload.Event), webhook.URL, 0, false,
			fmt.Sprintf("Failed to marshal payload: %v", err), 0, 1, nil)
		return
	}

	maxRetries := m.cfg.GetInt("webhooks.max_retries")
	if maxRetries <= 0 {
		maxRetries = 3
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		start := time.Now()
		success, statusCode, err := m.deliverWebhook(ctx, webhook, payloadBytes)
		duration := time.Since(start).Milliseconds()

		var errorMsg string
		if err != nil {
			errorMsg = err.Error()
		}

		// Record delivery attempt
		nextRetry := m.calculateNextRetry(attempt, maxRetries)
		m.recordDelivery(webhook.ID, string(payload.Event), webhook.URL,
			statusCode, success, errorMsg, duration, attempt, nextRetry)

		if success {
			break // Success, no need to retry
		}

		// Wait before retry (exponential backoff)
		if attempt < maxRetries {
			backoff := time.Duration(attempt*attempt) * time.Second
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				continue
			}
		}
	}
}

// deliverWebhook performs the actual HTTP request
func (m *Manager) deliverWebhook(ctx context.Context, webhook models.Webhook, payload []byte) (bool, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewBuffer(payload))
	if err != nil {
		return false, 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", webhook.ContentType)
	req.Header.Set("User-Agent", "CasGists-Webhook/1.0")
	req.Header.Set("X-Webhook-Event", string(payload))

	// Add HMAC signature if secret is provided
	if webhook.Secret != "" {
		signature := m.generateSignature(payload, webhook.Secret)
		req.Header.Set("X-Hub-Signature-256", signature)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return false, 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Consider 2xx status codes as success
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	return success, resp.StatusCode, nil
}

// generateSignature generates HMAC signature for webhook verification
func (m *Manager) generateSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))
	return "sha256=" + signature
}

// getWebhooksForEvent retrieves all webhooks that subscribe to a specific event
func (m *Manager) getWebhooksForEvent(event WebhookEvent) ([]models.Webhook, error) {
	var webhooks []models.Webhook
	eventStr := string(event)

	// Query webhooks that have this event in their events JSON array
	err := m.db.Where("is_active = ? AND (events LIKE ? OR events LIKE ? OR events LIKE ?)",
		true,
		fmt.Sprintf("%%\"%s\"%%", eventStr),
		fmt.Sprintf("%%'%s'%%", eventStr),
		"%*%", // Wildcard for all events
	).Find(&webhooks).Error

	if err != nil {
		return nil, err
	}

	// Filter webhooks that actually subscribe to this event
	var filteredWebhooks []models.Webhook
	for _, webhook := range webhooks {
		if m.webhookSubscribesToEvent(webhook, event) {
			filteredWebhooks = append(filteredWebhooks, webhook)
		}
	}

	return filteredWebhooks, nil
}

// webhookSubscribesToEvent checks if a webhook subscribes to a specific event
func (m *Manager) webhookSubscribesToEvent(webhook models.Webhook, event WebhookEvent) bool {
	if webhook.Events == "" {
		return false
	}

	// Check for wildcard subscription
	if strings.Contains(webhook.Events, "*") {
		return true
	}

	// Parse events JSON array
	var events []string
	if err := json.Unmarshal([]byte(webhook.Events), &events); err != nil {
		// Fallback to simple string search
		return strings.Contains(webhook.Events, string(event))
	}

	// Check if event is in the list
	for _, e := range events {
		if e == string(event) || e == "*" {
			return true
		}
	}

	return false
}

// getSenderInfo retrieves sender information for webhook payload
func (m *Manager) getSenderInfo(userID uuid.UUID) (*SenderInfo, error) {
	var sender struct {
		ID       uuid.UUID `json:"id"`
		Username string    `json:"username"`
		Email    string    `json:"email"`
	}

	err := m.db.Table("users").
		Select("id, username, email").
		Where("id = ?", userID).
		First(&sender).Error

	if err != nil {
		return nil, err
	}

	return &SenderInfo{
		ID:       sender.ID,
		Username: sender.Username,
		Email:    sender.Email,
	}, nil
}

// recordDelivery records a webhook delivery attempt
func (m *Manager) recordDelivery(webhookID uuid.UUID, event, url string, statusCode int,
	success bool, errorMsg string, duration int64, attempts int, nextRetry *time.Time) {

	delivery := models.WebhookDelivery{
		WebhookID:  webhookID,
		Event:      event,
		URL:        url,
		StatusCode: statusCode,
		Success:    success,
		Error:      errorMsg,
		Duration:   duration,
		Attempts:   attempts,
		NextRetry:  nextRetry,
	}

	m.db.Create(&delivery)
}

// calculateNextRetry calculates the next retry time for failed webhooks
func (m *Manager) calculateNextRetry(attempt, maxRetries int) *time.Time {
	if attempt >= maxRetries {
		return nil // No more retries
	}

	// Exponential backoff: 2^attempt minutes
	backoff := time.Duration(1<<uint(attempt)) * time.Minute
	nextRetry := time.Now().Add(backoff)
	return &nextRetry
}

// GetDeliveries retrieves webhook deliveries for a webhook
func (m *Manager) GetDeliveries(webhookID uuid.UUID, page, limit int) ([]models.WebhookDelivery, int64, error) {
	var deliveries []models.WebhookDelivery
	var total int64

	// Count total
	if err := m.db.Model(&models.WebhookDelivery{}).Where("webhook_id = ?", webhookID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Get paginated results
	offset := (page - 1) * limit
	if err := m.db.Where("webhook_id = ?", webhookID).
		Offset(offset).Limit(limit).
		Order("created_at DESC").
		Find(&deliveries).Error; err != nil {
		return nil, 0, err
	}

	return deliveries, total, nil
}

// PingWebhook sends a test ping to a webhook
func (m *Manager) PingWebhook(ctx context.Context, webhookID uuid.UUID, userID *uuid.UUID) error {
	webhook, err := m.GetWebhook(webhookID, userID)
	if err != nil {
		return fmt.Errorf("webhook not found: %w", err)
	}

	payload := WebhookPayload{
		Event:     "ping",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"zen": "CasGists webhook system is working!",
		},
	}

	go m.sendWebhook(ctx, *webhook, payload)
	return nil
}

// generateSecret generates a random secret for webhook HMAC verification
func generateSecret() string {
	// Generate 32 random bytes and encode as hex
	bytes := make([]byte, 32)
	for i := range bytes {
		bytes[i] = byte(time.Now().UnixNano() % 256)
	}
	return hex.EncodeToString(bytes)
}
