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

	"github.com/casapps/casgist/src/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EventType represents the type of webhook event
type EventType string

const (
	EventGistCreated  EventType = "gist.created"
	EventGistUpdated  EventType = "gist.updated"
	EventGistDeleted  EventType = "gist.deleted"
	EventGistStarred  EventType = "gist.starred"
	EventGistForked   EventType = "gist.forked"
	EventCommentAdded EventType = "comment.added"
	EventUserCreated  EventType = "user.created"
	EventOrgCreated   EventType = "org.created"
	EventTeamCreated  EventType = "team.created"
)

// Event represents a webhook event
type Event struct {
	ID        string                 `json:"id"`
	Type      EventType              `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Actor     *EventActor            `json:"actor"`
	Data      map[string]interface{} `json:"data"`
}

// EventActor represents the user who triggered the event
type EventActor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Type     string `json:"type"` // user or system
}

// Payload represents the webhook payload
type Payload struct {
	Event     Event  `json:"event"`
	Signature string `json:"-"`
}

// Delivery represents a webhook delivery attempt
type Delivery struct {
	ID             uuid.UUID
	SubscriptionID uuid.UUID
	EventID        string
	URL            string
	Method         string
	Headers        map[string]string
	Payload        []byte
	ResponseStatus int
	ResponseBody   []byte
	Success        bool
	Attempts       int
	LastAttempt    time.Time
	NextAttempt    *time.Time
	Error          string
}

// Manager handles webhook operations
type Manager struct {
	db         *gorm.DB
	httpClient *http.Client
	queue      chan *Event
	workers    int
}

// NewManager creates a new webhook manager
func NewManager(db *gorm.DB, workers int) *Manager {
	if workers <= 0 {
		workers = 5
	}

	return &Manager{
		db: db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		queue:   make(chan *Event, 1000),
		workers: workers,
	}
}

// Start starts the webhook processing workers
func (m *Manager) Start(ctx context.Context) {
	for i := 0; i < m.workers; i++ {
		go m.worker(ctx)
	}
}

// TriggerEvent triggers a webhook event
func (m *Manager) TriggerEvent(eventType EventType, actor *models.User, data map[string]interface{}) error {
	event := &Event{
		ID:        uuid.New().String(),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	if actor != nil {
		event.Actor = &EventActor{
			ID:       actor.ID.String(),
			Username: actor.Username,
			Email:    actor.Email,
			Type:     "user",
		}
	} else {
		event.Actor = &EventActor{
			Type: "system",
		}
	}

	select {
	case m.queue <- event:
		return nil
	default:
		// Queue is full
		return fmt.Errorf("webhook queue is full")
	}
}

// worker processes webhook events
func (m *Manager) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-m.queue:
			m.processEvent(event)
		}
	}
}

// processEvent processes a single webhook event
func (m *Manager) processEvent(event *Event) {
	// Find all active subscriptions for this event type
	var subscriptions []models.Webhook
	if err := m.db.Where("is_active = ? AND (events LIKE ? OR events = ?)",
		true, "%"+string(event.Type)+"%", "*").
		Find(&subscriptions).Error; err != nil {
		// Log error
		return
	}

	// Send event to each subscription
	for _, subscription := range subscriptions {
		m.deliverWebhook(&subscription, event)
	}
}

// deliverWebhook delivers a webhook to a subscription
func (m *Manager) deliverWebhook(subscription *models.Webhook, event *Event) {
	// Create payload
	payload := Payload{Event: *event}
	
	// Marshal payload
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		m.recordDelivery(subscription, event, nil, err)
		return
	}

	// Create signature
	signature := m.createSignature(subscription.Secret, payloadBytes)

	// Create request - Always use POST for webhooks
	req, err := http.NewRequest("POST", subscription.URL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		m.recordDelivery(subscription, event, nil, err)
		return
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CasGists-Event", string(event.Type))
	req.Header.Set("X-CasGists-Signature", signature)
	req.Header.Set("X-CasGists-Signature-256", signature) // For compatibility
	req.Header.Set("X-CasGists-Delivery", event.ID)
	req.Header.Set("User-Agent", "CasGists-Webhook/1.0")

	// Add custom headers - webhook model doesn't have Headers field
	// Custom headers would need to be added to the model if required

	// Send request
	resp, err := m.httpClient.Do(req)
	if err != nil {
		m.recordDelivery(subscription, event, nil, err)
		m.scheduleRetry(subscription, event)
		return
	}
	defer resp.Body.Close()

	// Read response body (limited)
	responseBody := make([]byte, 1024*10) // 10KB limit
	n, _ := resp.Body.Read(responseBody)
	responseBody = responseBody[:n]

	// Record delivery
	m.recordDelivery(subscription, event, resp, nil)

	// Schedule retry if failed
	if resp.StatusCode >= 400 {
		m.scheduleRetry(subscription, event)
	}
}

// createSignature creates HMAC signature for webhook payload
func (m *Manager) createSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// ValidateSignature validates webhook signature
func ValidateSignature(secret, signature string, payload []byte) bool {
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	expectedMAC.Write(payload)
	expectedSignature := "sha256=" + hex.EncodeToString(expectedMAC.Sum(nil))
	
	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

// recordDelivery records a webhook delivery attempt
func (m *Manager) recordDelivery(subscription *models.Webhook, event *Event, resp *http.Response, err error) {
	delivery := &models.WebhookDelivery{
		ID:               uuid.New(),
		WebhookID:        subscription.ID,
		Event:            string(event.Type),
		Payload:          string(mustMarshal(event)),
		RequestHeaders:   "", // Headers would be added here if needed
		CreatedAt:        time.Now(),
	}

	if resp != nil {
		delivery.ResponseStatus = resp.StatusCode
		delivery.Success = resp.StatusCode >= 200 && resp.StatusCode < 300
		
		// Read limited response body
		body := make([]byte, 1024*10) // 10KB limit
		n, _ := resp.Body.Read(body)
		delivery.ResponseBody = string(body[:n])
		
		// Store response headers as JSON
		headers := make(map[string]string)
		for k, v := range resp.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}
		headersJSON, _ := json.Marshal(headers)
		delivery.ResponseHeaders = string(headersJSON)
	}

	if err != nil {
		delivery.Success = false
		delivery.Error = err.Error()
	}

	// Save delivery record
	m.db.Create(delivery)

	// Update subscription stats
	m.db.Model(subscription).Updates(map[string]interface{}{
		"last_delivered_at": time.Now(),
		"delivery_count":    gorm.Expr("delivery_count + ?", 1),
		"failure_count":     gorm.Expr("failure_count + ?", boolToInt(!delivery.Success)),
		"last_status":       delivery.ResponseStatus,
		"last_error":        delivery.Error,
	})
}

// scheduleRetry schedules a webhook retry
func (m *Manager) scheduleRetry(subscription *models.Webhook, event *Event) {
	// Simple exponential backoff
	// Retry after 1min, 5min, 15min, 1hr, then give up
	// This is a simplified version - in production you'd use a proper job queue
}

// Helper functions

func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CreateSubscription creates a new webhook subscription
func (m *Manager) CreateSubscription(userID uuid.UUID, url string, eventTypes []string, secret string) (*models.Webhook, error) {
	userIDPtr := &userID
	subscription := &models.Webhook{
		ID:          uuid.New(),
		UserID:      userIDPtr,
		URL:         url,
		ContentType: "application/json",
		Events:      joinEventTypes(eventTypes),
		Secret:      secret,
		IsActive:    true,
	}

	if err := m.db.Create(subscription).Error; err != nil {
		return nil, err
	}

	return subscription, nil
}

// UpdateSubscription updates a webhook subscription
func (m *Manager) UpdateSubscription(id uuid.UUID, updates map[string]interface{}) error {
	return m.db.Model(&models.Webhook{}).Where("id = ?", id).Updates(updates).Error
}

// DeleteSubscription deletes a webhook subscription
func (m *Manager) DeleteSubscription(id uuid.UUID) error {
	return m.db.Delete(&models.Webhook{}, id).Error
}

// TestWebhook sends a test webhook
func (m *Manager) TestWebhook(subscriptionID uuid.UUID) error {
	var subscription models.Webhook
	if err := m.db.First(&subscription, subscriptionID).Error; err != nil {
		return err
	}

	// Create test event
	event := &Event{
		ID:        uuid.New().String(),
		Type:      "test",
		Timestamp: time.Now(),
		Actor: &EventActor{
			Type: "system",
		},
		Data: map[string]interface{}{
			"message": "This is a test webhook from CasGists",
		},
	}

	// Deliver webhook
	m.deliverWebhook(&subscription, event)

	return nil
}

func joinEventTypes(types []string) string {
	if len(types) == 0 {
		return "*"
	}
	result := ""
	for i, t := range types {
		if i > 0 {
			result += ","
		}
		result += t
	}
	return result
}