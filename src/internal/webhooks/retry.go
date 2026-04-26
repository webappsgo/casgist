package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// RetryConfig defines retry behavior for webhook deliveries
type RetryConfig struct {
	MaxAttempts     int           // Maximum number of retry attempts
	InitialDelay    time.Duration // Initial delay before first retry
	MaxDelay        time.Duration // Maximum delay between retries
	BackoffFactor   float64       // Exponential backoff factor
	RetryableStatus []int         // HTTP status codes that trigger retry
}

// DefaultRetryConfig returns the default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:   5,
		InitialDelay:  1 * time.Second,
		MaxDelay:      5 * time.Minute,
		BackoffFactor: 2.0,
		RetryableStatus: []int{
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout,
		},
	}
}

// RetryableWebhookService handles webhook delivery with retry logic
type RetryableWebhookService struct {
	db          *gorm.DB
	client      *http.Client
	retryConfig *RetryConfig
	retryQueue  chan *RetryTask
	workers     int
}

// RetryTask represents a webhook delivery that needs to be retried
type RetryTask struct {
	WebhookID   uuid.UUID
	DeliveryID  uuid.UUID
	URL         string
	Secret      string
	AttemptNum  int
	NextRetryAt *time.Time
}

// NewRetryableWebhookService creates a new webhook service with retry capability
func NewRetryableWebhookService(db *gorm.DB, workers int) *RetryableWebhookService {
	if workers <= 0 {
		workers = 5
	}

	return &RetryableWebhookService{
		db: db,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		retryConfig: DefaultRetryConfig(),
		retryQueue:  make(chan *RetryTask, 1000),
		workers:     workers,
	}
}

// Start begins processing retry tasks
func (s *RetryableWebhookService) Start(ctx context.Context) {
	// Start worker goroutines
	for i := 0; i < s.workers; i++ {
		go s.worker(ctx)
	}

	// Start retry scheduler
	go s.scheduler(ctx)
}

// worker processes retry tasks from the queue
func (s *RetryableWebhookService) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-s.retryQueue:
			if task != nil {
				s.processRetry(ctx, task)
			}
		}
	}
}

// scheduler checks for deliveries that need to be retried
func (s *RetryableWebhookService) scheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scheduleRetries()
		}
	}
}

// scheduleRetries finds failed deliveries that should be retried
func (s *RetryableWebhookService) scheduleRetries() {
	var deliveries []models.WebhookDelivery

	// Find failed deliveries that haven't exceeded max attempts
	s.db.Where("success = ? AND attempts < ? AND next_retry IS NOT NULL AND next_retry <= ?",
		false, s.retryConfig.MaxAttempts, time.Now()).
		Find(&deliveries)

	for _, delivery := range deliveries {
		// Get webhook details
		var webhook models.Webhook
		if err := s.db.First(&webhook, "id = ?", delivery.WebhookID).Error; err != nil {
			continue
		}

		// Create retry task
		task := &RetryTask{
			WebhookID:   webhook.ID,
			DeliveryID:  delivery.ID,
			URL:         webhook.URL,
			Secret:      webhook.Secret,
			AttemptNum:  delivery.Attempts + 1,
			NextRetryAt: delivery.NextRetry,
		}

		// Queue for retry
		select {
		case s.retryQueue <- task:
		default:
			// Queue is full, skip this retry
		}
	}
}

// DeliverWithRetry delivers a webhook event with retry logic
func (s *RetryableWebhookService) DeliverWithRetry(ctx context.Context, webhook *models.Webhook, event *Event) error {
	if webhook == nil {
		return fmt.Errorf("webhook is required")
	}
	if event == nil {
		return fmt.Errorf("event is required")
	}
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	payloadBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook event: %w", err)
	}

	delivery := &models.WebhookDelivery{
		ID:        uuid.New(),
		WebhookID: webhook.ID,
		Event:     string(event.Type),
		URL:       webhook.URL,
		Payload:   string(payloadBytes),
		Attempts:  1,
	}

	success, statusCode, responseBody, duration := s.attemptDelivery(ctx, webhook, payloadBytes, event.Type, delivery.ID)

	delivery.Success = success
	delivery.StatusCode = statusCode
	delivery.Duration = duration.Milliseconds()
	if !success && responseBody != "" {
		delivery.Error = responseBody
	}

	if !success && s.shouldRetry(statusCode) {
		nextRetry := s.calculateNextRetryTime(1)
		delivery.NextRetry = &nextRetry
	}

	if err := s.db.Create(delivery).Error; err != nil {
		return fmt.Errorf("failed to save delivery record: %w", err)
	}

	if !success && s.shouldRetry(statusCode) {
		task := &RetryTask{
			WebhookID:   webhook.ID,
			DeliveryID:  delivery.ID,
			URL:         webhook.URL,
			Secret:      webhook.Secret,
			AttemptNum:  2,
			NextRetryAt: delivery.NextRetry,
		}

		select {
		case s.retryQueue <- task:
		default:
			// Queue is full, skip this retry
		}
	}

	return nil
}

// processRetry processes a single retry task
func (s *RetryableWebhookService) processRetry(ctx context.Context, task *RetryTask) {
	if task == nil {
		return
	}

	if task.NextRetryAt != nil {
		waitDuration := time.Until(*task.NextRetryAt)
		if waitDuration > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(waitDuration):
			}
		}
	}

	var delivery models.WebhookDelivery
	if err := s.db.First(&delivery, "id = ?", task.DeliveryID).Error; err != nil {
		return
	}

	payloadBytes := []byte(delivery.Payload)
	var eventType WebhookEvent
	if delivery.Event != "" {
		eventType = WebhookEvent(delivery.Event)
	}

	success, statusCode, responseBody, duration := s.attemptDelivery(
		ctx,
		&models.Webhook{ID: task.WebhookID, URL: task.URL, Secret: task.Secret},
		payloadBytes,
		eventType,
		delivery.ID,
	)

	delivery.Attempts = task.AttemptNum
	delivery.Success = success
	delivery.StatusCode = statusCode
	delivery.Duration = duration.Milliseconds()
	if !success && responseBody != "" {
		delivery.Error = responseBody
	} else if success {
		delivery.Error = ""
	}

	if !success && task.AttemptNum < s.retryConfig.MaxAttempts && s.shouldRetry(statusCode) {
		nextRetry := s.calculateNextRetryTime(task.AttemptNum)
		delivery.NextRetry = &nextRetry

		nextTask := &RetryTask{
			WebhookID:   task.WebhookID,
			DeliveryID:  task.DeliveryID,
			URL:         task.URL,
			Secret:      task.Secret,
			AttemptNum:  task.AttemptNum + 1,
			NextRetryAt: delivery.NextRetry,
		}

		select {
		case s.retryQueue <- nextTask:
		default:
			// Queue full
		}
	} else {
		delivery.NextRetry = nil
	}

	s.db.Save(&delivery)
}

// attemptDelivery attempts to deliver a webhook event
func (s *RetryableWebhookService) attemptDelivery(
	ctx context.Context,
	webhook *models.Webhook,
	payload []byte,
	eventType WebhookEvent,
	deliveryID uuid.UUID,
) (bool, int, string, time.Duration) {
	startTime := time.Now()

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewReader(payload))
	if err != nil {
		return false, 0, err.Error(), time.Since(startTime)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "CasGists-Webhook/1.0")
	req.Header.Set("X-CasGists-Event", string(eventType))
	req.Header.Set("X-CasGists-Delivery", deliveryID.String())

	// Add HMAC signature if secret is configured
	if webhook.Secret != "" {
		signature := s.generateSignature(payload, webhook.Secret)
		req.Header.Set("X-CasGists-Signature", signature)
		req.Header.Set("X-CasGists-Signature-256", signature)
	}

	// Send request
	resp, err := s.client.Do(req)
	duration := time.Since(startTime)

	if err != nil {
		return false, 0, err.Error(), duration
	}
	defer resp.Body.Close()

	// Read response body (limit to 1MB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return false, resp.StatusCode, err.Error(), duration
	}

	// Check if successful (2xx status)
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	return success, resp.StatusCode, string(body), duration
}

// generateSignature generates HMAC-SHA256 signature for payload
func (s *RetryableWebhookService) generateSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// shouldRetry determines if a delivery should be retried based on status code
func (s *RetryableWebhookService) shouldRetry(statusCode int) bool {
	for _, code := range s.retryConfig.RetryableStatus {
		if statusCode == code {
			return true
		}
	}
	return false
}

// calculateNextRetryTime calculates the next retry time using exponential backoff
func (s *RetryableWebhookService) calculateNextRetryTime(attemptNum int) time.Time {
	// Calculate delay with exponential backoff
	delay := s.retryConfig.InitialDelay
	for i := 1; i < attemptNum; i++ {
		delay = time.Duration(float64(delay) * s.retryConfig.BackoffFactor)
		if delay > s.retryConfig.MaxDelay {
			delay = s.retryConfig.MaxDelay
			break
		}
	}

	// Add some jitter (±10%)
	jitter := time.Duration(float64(delay) * 0.1)
	delay = delay + time.Duration(time.Now().UnixNano()%int64(jitter*2)) - jitter

	return time.Now().Add(delay)
}

// GetDeliveryHistory gets the delivery history for a webhook
func (s *RetryableWebhookService) GetDeliveryHistory(webhookID uuid.UUID, limit, offset int) ([]models.WebhookDelivery, error) {
	var deliveries []models.WebhookDelivery

	err := s.db.Where("webhook_id = ?", webhookID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&deliveries).Error

	return deliveries, err
}

// RetryDelivery manually retries a specific delivery
func (s *RetryableWebhookService) RetryDelivery(deliveryID uuid.UUID) error {
	var delivery models.WebhookDelivery
	if err := s.db.First(&delivery, "id = ?", deliveryID).Error; err != nil {
		return fmt.Errorf("delivery not found: %w", err)
	}

	var webhook models.Webhook
	if err := s.db.First(&webhook, "id = ?", delivery.WebhookID).Error; err != nil {
		return fmt.Errorf("webhook not found: %w", err)
	}

	// Queue for immediate retry
	now := time.Now()
	task := &RetryTask{
		WebhookID:   webhook.ID,
		DeliveryID:  delivery.ID,
		URL:         webhook.URL,
		Secret:      webhook.Secret,
		AttemptNum:  delivery.Attempts + 1,
		NextRetryAt: &now,
	}

	select {
	case s.retryQueue <- task:
		return nil
	default:
		return fmt.Errorf("retry queue is full")
	}
}
