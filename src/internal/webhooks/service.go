package webhooks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// Service provides webhook functionality
type Service struct {
	manager *Manager
	cfg     *viper.Viper
}

// NewService creates a new webhook service
func NewService(db *gorm.DB, cfg *viper.Viper) *Service {
	return &Service{
		manager: NewManager(db, cfg),
		cfg:     cfg,
	}
}

// CreateWebhookInput represents input for creating a webhook
type CreateWebhookInput struct {
	URL         string   `json:"url" validate:"required,url"`
	Events      []string `json:"events" validate:"required,min=1"`
	Secret      string   `json:"secret,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
}

// UpdateWebhookInput represents input for updating a webhook
type UpdateWebhookInput struct {
	URL         *string  `json:"url,omitempty" validate:"omitempty,url"`
	Events      []string `json:"events,omitempty" validate:"omitempty,min=1"`
	Secret      *string  `json:"secret,omitempty"`
	ContentType *string  `json:"content_type,omitempty"`
	IsActive    *bool    `json:"is_active,omitempty"`
}

// CreateWebhook creates a new webhook for a user
func (s *Service) CreateWebhook(userID uuid.UUID, input CreateWebhookInput) (*models.Webhook, error) {
	// Validate events
	if err := s.validateEvents(input.Events); err != nil {
		return nil, fmt.Errorf("invalid events: %w", err)
	}

	// Convert events to JSON
	eventsJSON, err := json.Marshal(input.Events)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal events: %w", err)
	}

	contentType := input.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	webhook := &models.Webhook{
		UserID:      &userID,
		URL:         input.URL,
		Secret:      input.Secret,
		Events:      string(eventsJSON),
		IsActive:    true,
		ContentType: contentType,
	}

	if err := s.manager.CreateWebhook(webhook); err != nil {
		return nil, fmt.Errorf("failed to create webhook: %w", err)
	}

	return webhook, nil
}

// GetWebhook retrieves a webhook by ID
func (s *Service) GetWebhook(id uuid.UUID, userID *uuid.UUID) (*models.Webhook, error) {
	return s.manager.GetWebhook(id, userID)
}

// ListWebhooks lists webhooks for a user
func (s *Service) ListWebhooks(userID *uuid.UUID, page, limit int) ([]models.Webhook, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	return s.manager.ListWebhooks(userID, page, limit)
}

// UpdateWebhook updates a webhook
func (s *Service) UpdateWebhook(id uuid.UUID, userID *uuid.UUID, input UpdateWebhookInput) (*models.Webhook, error) {
	updates := make(map[string]interface{})

	if input.URL != nil {
		updates["url"] = *input.URL
	}

	if input.Events != nil {
		if err := s.validateEvents(input.Events); err != nil {
			return nil, fmt.Errorf("invalid events: %w", err)
		}
		eventsJSON, err := json.Marshal(input.Events)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal events: %w", err)
		}
		updates["events"] = string(eventsJSON)
	}

	if input.Secret != nil {
		updates["secret"] = *input.Secret
	}

	if input.ContentType != nil {
		updates["content_type"] = *input.ContentType
	}

	if input.IsActive != nil {
		updates["is_active"] = *input.IsActive
	}

	if err := s.manager.UpdateWebhook(id, userID, updates); err != nil {
		return nil, fmt.Errorf("failed to update webhook: %w", err)
	}

	return s.manager.GetWebhook(id, userID)
}

// DeleteWebhook deletes a webhook
func (s *Service) DeleteWebhook(id uuid.UUID, userID *uuid.UUID) error {
	if err := s.manager.DeleteWebhook(id, userID); err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}
	return nil
}

// GetDeliveries retrieves webhook deliveries
func (s *Service) GetDeliveries(webhookID uuid.UUID, userID *uuid.UUID, page, limit int) ([]models.WebhookDelivery, int64, error) {
	// Verify webhook ownership
	_, err := s.manager.GetWebhook(webhookID, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("webhook not found: %w", err)
	}

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	return s.manager.GetDeliveries(webhookID, page, limit)
}

// PingWebhook sends a test ping to a webhook
func (s *Service) PingWebhook(ctx context.Context, webhookID uuid.UUID, userID *uuid.UUID) error {
	return s.manager.PingWebhook(ctx, webhookID, userID)
}

// Event trigger methods for integration with other services

// TriggerGistCreated triggers webhooks when a gist is created
func (s *Service) TriggerGistCreated(ctx context.Context, gist *models.Gist, senderID uuid.UUID) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := s.gistToEventData(gist)
	return s.manager.TriggerEvent(ctx, EventGistCreated, data, &senderID)
}

// TriggerGistUpdated triggers webhooks when a gist is updated
func (s *Service) TriggerGistUpdated(ctx context.Context, gist *models.Gist, senderID uuid.UUID) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := s.gistToEventData(gist)
	return s.manager.TriggerEvent(ctx, EventGistUpdated, data, &senderID)
}

// TriggerGistDeleted triggers webhooks when a gist is deleted
func (s *Service) TriggerGistDeleted(ctx context.Context, gistID uuid.UUID, senderID uuid.UUID) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := map[string]interface{}{
		"id": gistID,
	}
	return s.manager.TriggerEvent(ctx, EventGistDeleted, data, &senderID)
}

// TriggerGistStarred triggers webhooks when a gist is starred/unstarred
func (s *Service) TriggerGistStarred(ctx context.Context, gist *models.Gist, user *models.User, starred bool) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := StarEventData{
		Gist:    s.gistToEventData(gist),
		Starrer: s.userToEventData(user),
	}

	action := "starred"
	if !starred {
		action = "unstarred"
	}

	payload := map[string]interface{}{
		"action": action,
		"data":   data,
	}

	return s.manager.TriggerEvent(ctx, EventGistStarred, payload, &user.ID)
}

// TriggerGistForked triggers webhooks when a gist is forked
func (s *Service) TriggerGistForked(ctx context.Context, originalGist, forkedGist *models.Gist, forker *models.User) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := ForkEventData{
		OriginalGist: s.gistToEventData(originalGist),
		ForkedGist:   s.gistToEventData(forkedGist),
		Forker:       s.userToEventData(forker),
	}

	return s.manager.TriggerEvent(ctx, EventGistForked, data, &forker.ID)
}

// TriggerUserCreated triggers webhooks when a user is created
func (s *Service) TriggerUserCreated(ctx context.Context, user *models.User) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := s.userToEventData(user)
	return s.manager.TriggerEvent(ctx, EventUserCreated, data, &user.ID)
}

// TriggerUserUpdated triggers webhooks when a user is updated
func (s *Service) TriggerUserUpdated(ctx context.Context, user *models.User) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := s.userToEventData(user)
	return s.manager.TriggerEvent(ctx, EventUserUpdated, data, &user.ID)
}

// TriggerUserFollowed triggers webhooks when a user follows another user
func (s *Service) TriggerUserFollowed(ctx context.Context, follower, following *models.User) error {
	if !s.isWebhooksEnabled() {
		return nil
	}

	data := FollowEventData{
		Follower:  s.userToEventData(follower),
		Following: s.userToEventData(following),
	}

	return s.manager.TriggerEvent(ctx, EventUserFollowed, data, &follower.ID)
}

// Helper methods

// validateEvents validates the list of webhook events
func (s *Service) validateEvents(events []string) error {
	validEvents := map[string]bool{
		string(EventGistCreated):  true,
		string(EventGistUpdated):  true,
		string(EventGistDeleted):  true,
		string(EventGistStarred):  true,
		string(EventGistForked):   true,
		string(EventUserCreated):  true,
		string(EventUserUpdated):  true,
		string(EventUserFollowed): true,
		"*": true, // Wildcard for all events
	}

	for _, event := range events {
		if !validEvents[event] {
			return fmt.Errorf("invalid event: %s", event)
		}
	}

	return nil
}

// isWebhooksEnabled checks if webhooks are enabled in configuration
func (s *Service) isWebhooksEnabled() bool {
	return s.cfg.GetBool("webhooks.enabled")
}

// gistToEventData converts a gist model to event data
func (s *Service) gistToEventData(gist *models.Gist) GistEventData {
	var tags []string
	for _, tag := range gist.Tags {
		tags = append(tags, tag.Name)
	}

	return GistEventData{
		ID:          gist.ID,
		Title:       gist.Title,
		Description: gist.Description,
		Visibility:  string(gist.Visibility),
		URL:         fmt.Sprintf("%s/gists/%s", s.cfg.GetString("server.url"), gist.ID),
		FilesCount:  len(gist.Files),
		Tags:        tags,
		CreatedAt:   gist.CreatedAt,
		UpdatedAt:   gist.UpdatedAt,
	}
}

// userToEventData converts a user model to event data
func (s *Service) userToEventData(user *models.User) UserEventData {
	return UserEventData{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Bio:         user.Bio,
		Location:    user.Location,
		Website:     user.Website,
		CreatedAt:   user.CreatedAt,
		UpdatedAt:   user.UpdatedAt,
	}
}