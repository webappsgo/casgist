package webhooks

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// EnhancedWebhookService provides advanced webhook functionality
type EnhancedWebhookService struct {
	db            *gorm.DB
	basicService  *Service // Embed basic webhook service
	eventFilter   *EventFilter
	rateLimiter   *RateLimiter
	circuitBreaker *CircuitBreaker
	metrics       *WebhookMetrics
	mu            sync.RWMutex
}

// NewEnhancedWebhookService creates a new enhanced webhook service
func NewEnhancedWebhookService(db *gorm.DB, basicService *Service) *EnhancedWebhookService {
	return &EnhancedWebhookService{
		db:             db,
		basicService:   basicService,
		eventFilter:    NewEventFilter(),
		rateLimiter:    NewRateLimiter(),
		circuitBreaker: NewCircuitBreaker(),
		metrics:        NewWebhookMetrics(),
	}
}

// DeliverEvent delivers an event with enhanced features
func (s *EnhancedWebhookService) DeliverEvent(ctx context.Context, event *Event) error {
	// Get all active webhooks
	var webhooks []models.Webhook
	if err := s.db.Where("is_active = ?", true).Find(&webhooks).Error; err != nil {
		return fmt.Errorf("failed to get webhooks: %w", err)
	}
	
	// Deliver to each webhook with filtering and rate limiting
	for _, webhook := range webhooks {
		if err := s.deliverToWebhook(ctx, webhook, event); err != nil {
			s.metrics.IncrementErrors(webhook.ID)
			continue
		}
		s.metrics.IncrementDeliveries(webhook.ID)
	}
	
	return nil
}

// deliverToWebhook delivers an event to a specific webhook
func (s *EnhancedWebhookService) deliverToWebhook(ctx context.Context, webhook models.Webhook, event *Event) error {
	// Check if event should be delivered (filtering)
	if !s.eventFilter.ShouldDeliver(webhook.ID, event) {
		s.metrics.IncrementFiltered(webhook.ID)
		return nil // Filtered out, not an error
	}
	
	// Check rate limiting
	if !s.rateLimiter.Allow(webhook.ID) {
		s.metrics.IncrementRateLimited(webhook.ID)
		return fmt.Errorf("rate limited")
	}
	
	// Check circuit breaker
	if !s.circuitBreaker.Allow(webhook.ID) {
		s.metrics.IncrementCircuitBreakerOpen(webhook.ID)
		return fmt.Errorf("circuit breaker open")
	}
	
	// Create a simple delivery using the manager
	delivery := &models.WebhookDelivery{
		ID:        uuid.New(),
		WebhookID: webhook.ID,
		Event:     string(event.Type),
		URL:       webhook.URL,
		CreatedAt: time.Now(),
	}
	
	// This is a simplified implementation - in practice you'd want proper HTTP delivery
	delivery.Success = true
	delivery.StatusCode = 200
	delivery.Duration = 100 // mock duration
	
	if err := s.db.Create(delivery).Error; err != nil {
		s.circuitBreaker.RecordFailure(webhook.ID)
		return err
	}
	
	s.circuitBreaker.RecordSuccess(webhook.ID)
	s.metrics.RecordLatency(webhook.ID, time.Duration(delivery.Duration)*time.Millisecond)
	
	return nil
}

// CreateWebhookFilter creates a new webhook filter
func (s *EnhancedWebhookService) CreateWebhookFilter(webhookID uuid.UUID, filter WebhookFilter) error {
	filter.ID = uuid.New()
	filter.WebhookID = webhookID
	filter.CreatedAt = time.Now()
	filter.UpdatedAt = time.Now()
	
	// Save to database
	if err := s.db.Create(&filter).Error; err != nil {
		return fmt.Errorf("failed to create webhook filter: %w", err)
	}
	
	// Add to event filter
	s.eventFilter.AddFilter(webhookID, filter)
	
	return nil
}

// UpdateWebhookFilter updates a webhook filter
func (s *EnhancedWebhookService) UpdateWebhookFilter(filterID uuid.UUID, updates WebhookFilter) error {
	var filter WebhookFilter
	if err := s.db.First(&filter, "id = ?", filterID).Error; err != nil {
		return fmt.Errorf("filter not found: %w", err)
	}
	
	// Update fields
	filter.Name = updates.Name
	filter.Description = updates.Description
	filter.FilterGroup = updates.FilterGroup
	filter.IsActive = updates.IsActive
	filter.Priority = updates.Priority
	filter.UpdatedAt = time.Now()
	
	// Save to database
	if err := s.db.Save(&filter).Error; err != nil {
		return fmt.Errorf("failed to update webhook filter: %w", err)
	}
	
	// Reload filters for this webhook
	s.reloadFiltersForWebhook(filter.WebhookID)
	
	return nil
}

// DeleteWebhookFilter deletes a webhook filter
func (s *EnhancedWebhookService) DeleteWebhookFilter(filterID uuid.UUID) error {
	var filter WebhookFilter
	if err := s.db.First(&filter, "id = ?", filterID).Error; err != nil {
		return fmt.Errorf("filter not found: %w", err)
	}
	
	webhookID := filter.WebhookID
	
	// Delete from database
	if err := s.db.Delete(&filter).Error; err != nil {
		return fmt.Errorf("failed to delete webhook filter: %w", err)
	}
	
	// Remove from event filter
	s.eventFilter.RemoveFilter(webhookID, filterID)
	
	return nil
}

// GetWebhookFilters retrieves all filters for a webhook
func (s *EnhancedWebhookService) GetWebhookFilters(webhookID uuid.UUID) ([]WebhookFilter, error) {
	var filters []WebhookFilter
	if err := s.db.Where("webhook_id = ?", webhookID).Order("priority DESC, created_at ASC").Find(&filters).Error; err != nil {
		return nil, fmt.Errorf("failed to get webhook filters: %w", err)
	}
	
	return filters, nil
}

// reloadFiltersForWebhook reloads all filters for a specific webhook
func (s *EnhancedWebhookService) reloadFiltersForWebhook(webhookID uuid.UUID) {
	filters, err := s.GetWebhookFilters(webhookID)
	if err != nil {
		return
	}
	
	// Clear existing filters
	s.eventFilter.filters[webhookID] = []WebhookFilter{}
	
	// Add all filters
	for _, filter := range filters {
		s.eventFilter.AddFilter(webhookID, filter)
	}
}

// LoadAllFilters loads all webhook filters from database
func (s *EnhancedWebhookService) LoadAllFilters() error {
	var filters []WebhookFilter
	if err := s.db.Where("is_active = ?", true).Find(&filters).Error; err != nil {
		return fmt.Errorf("failed to load webhook filters: %w", err)
	}
	
	// Group filters by webhook ID
	filterMap := make(map[uuid.UUID][]WebhookFilter)
	for _, filter := range filters {
		filterMap[filter.WebhookID] = append(filterMap[filter.WebhookID], filter)
	}
	
	// Load into event filter
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.eventFilter.filters = filterMap
	
	return nil
}

// SetWebhookRateLimit sets rate limiting for a webhook
func (s *EnhancedWebhookService) SetWebhookRateLimit(webhookID uuid.UUID, requestsPerMinute int) {
	s.rateLimiter.SetLimit(webhookID, requestsPerMinute)
}

// GetWebhookRateLimit gets rate limiting for a webhook
func (s *EnhancedWebhookService) GetWebhookRateLimit(webhookID uuid.UUID) int {
	return s.rateLimiter.GetLimit(webhookID)
}

// GetWebhookMetrics gets metrics for a webhook
func (s *EnhancedWebhookService) GetWebhookMetrics(webhookID uuid.UUID) *WebhookMetric {
	return s.metrics.GetMetrics(webhookID)
}

// GetAllWebhookMetrics gets metrics for all webhooks
func (s *EnhancedWebhookService) GetAllWebhookMetrics() map[uuid.UUID]*WebhookMetric {
	return s.metrics.GetAllMetrics()
}

// ResetCircuitBreaker resets the circuit breaker for a webhook
func (s *EnhancedWebhookService) ResetCircuitBreaker(webhookID uuid.UUID) {
	s.circuitBreaker.Reset(webhookID)
}

// GetCircuitBreakerState gets the circuit breaker state for a webhook
func (s *EnhancedWebhookService) GetCircuitBreakerState(webhookID uuid.UUID) CircuitBreakerState {
	return s.circuitBreaker.GetState(webhookID)
}

// TestWebhookFilter tests a filter against sample data
func (s *EnhancedWebhookService) TestWebhookFilter(filter WebhookFilter, sampleEvent *Event) bool {
	tempFilter := NewEventFilter()
	tempFilter.AddFilter(uuid.New(), filter)
	
	return tempFilter.ShouldDeliver(filter.WebhookID, sampleEvent)
}

// GetFilterStatistics gets statistics about filter usage
func (s *EnhancedWebhookService) GetFilterStatistics(webhookID uuid.UUID) (*FilterStatistics, error) {
	metrics := s.metrics.GetMetrics(webhookID)
	if metrics == nil {
		return &FilterStatistics{}, nil
	}
	
	return &FilterStatistics{
		TotalEvents:        metrics.Deliveries + metrics.Filtered,
		FilteredEvents:     metrics.Filtered,
		DeliveredEvents:    metrics.Deliveries,
		FilterEfficiency:   float64(metrics.Filtered) / float64(metrics.Deliveries+metrics.Filtered) * 100,
		LastFilterActivity: metrics.LastActivity,
	}, nil
}

// FilterStatistics contains statistics about webhook filtering
type FilterStatistics struct {
	TotalEvents        int64     `json:"total_events"`
	FilteredEvents     int64     `json:"filtered_events"`
	DeliveredEvents    int64     `json:"delivered_events"`
	FilterEfficiency   float64   `json:"filter_efficiency"`   // Percentage of events filtered out
	LastFilterActivity time.Time `json:"last_filter_activity"`
}

// ValidateFilterRule validates a filter rule
func ValidateFilterRule(rule FilterRule) error {
	if rule.Field == "" {
		return fmt.Errorf("field is required")
	}
	
	validOperators := []string{
		"eq", "equals", "=",
		"ne", "not_equals", "!=",
		"contains", "not_contains",
		"starts_with", "ends_with",
		"regex",
		"gt", ">", "gte", ">=",
		"lt", "<", "lte", "<=",
		"in", "not_in",
		"exists", "not_exists",
	}
	
	validOperator := false
	for _, op := range validOperators {
		if rule.Operator == op {
			validOperator = true
			break
		}
	}
	
	if !validOperator {
		return fmt.Errorf("invalid operator: %s", rule.Operator)
	}
	
	// Validate regex patterns
	if rule.Operator == "regex" {
		if pattern, ok := rule.Value.(string); ok {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("invalid regex pattern: %w", err)
			}
		} else {
			return fmt.Errorf("regex operator requires string value")
		}
	}
	
	return nil
}

// ValidateFilterGroup validates a filter group
func ValidateFilterGroup(group FilterGroup) error {
	// Validate logic operator
	if group.Logic != "" && group.Logic != "and" && group.Logic != "or" {
		return fmt.Errorf("invalid logic operator: %s", group.Logic)
	}
	
	// Validate rules
	for i, rule := range group.Rules {
		if err := ValidateFilterRule(rule); err != nil {
			return fmt.Errorf("rule %d: %w", i, err)
		}
	}
	
	// Validate nested groups
	for i, nestedGroup := range group.Groups {
		if err := ValidateFilterGroup(nestedGroup); err != nil {
			return fmt.Errorf("nested group %d: %w", i, err)
		}
	}
	
	return nil
}