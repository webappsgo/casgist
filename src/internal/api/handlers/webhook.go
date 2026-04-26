package handlers

import (
	"net/http"
	"strconv"

	"github.com/casapps/casgist/src/internal/models"
	"github.com/casapps/casgist/src/internal/webhook"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// WebhookHandler handles webhook-related endpoints
type WebhookHandler struct {
	db      *gorm.DB
	config  *viper.Viper
	manager *webhook.Manager
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(db *gorm.DB, config *viper.Viper, manager *webhook.Manager) *WebhookHandler {
	return &WebhookHandler{
		db:      db,
		config:  config,
		manager: manager,
	}
}

// List returns all webhooks for the current user
func (h *WebhookHandler) List(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Build query
	query := h.db.Model(&models.Webhook{}).
		Where("user_id = ?", userID)

	// Count total
	var total int64
	query.Count(&total)

	// Get webhooks
	var webhooks []models.Webhook
	offset := (page - 1) * limit
	if err := query.
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&webhooks).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch webhooks")
	}

	// Hide secrets
	for i := range webhooks {
		webhooks[i].Secret = ""
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"webhooks": webhooks,
		"total":    total,
		"page":     page,
		"limit":    limit,
		"pages":    (total + int64(limit) - 1) / int64(limit),
	})
}

// Create creates a new webhook subscription
func (h *WebhookHandler) Create(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Check webhook limit
	var count int64
	h.db.Model(&models.Webhook{}).Where("user_id = ?", userID).Count(&count)
	maxWebhooks := h.config.GetInt64("webhook.max_per_user")
	if maxWebhooks > 0 && count >= maxWebhooks {
		return echo.NewHTTPError(http.StatusBadRequest, "Webhook limit reached")
	}

	// Parse request
	var req struct {
		URL         string   `json:"url" validate:"required,url,https"`
		EventTypes  []string `json:"event_types" validate:"required,min=1"`
		Secret      string   `json:"secret" validate:"required,min=16"`
		ContentType string   `json:"content_type"`
		InsecureSSL bool     `json:"insecure_ssl"`
		IsActive    bool     `json:"is_active"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Validate event types
	validEventTypes := []string{
		string(webhook.EventGistCreated),
		string(webhook.EventGistUpdated),
		string(webhook.EventGistDeleted),
		string(webhook.EventGistStarred),
		string(webhook.EventGistForked),
		string(webhook.EventCommentAdded),
		string(webhook.EventUserCreated),
		string(webhook.EventOrgCreated),
		string(webhook.EventTeamCreated),
		"*", // All events
	}

	for _, eventType := range req.EventTypes {
		valid := false
		for _, validType := range validEventTypes {
			if eventType == validType {
				valid = true
				break
			}
		}
		if !valid {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid event type: "+eventType)
		}
	}

	// Create webhook subscription
	sub, err := h.manager.CreateSubscription(userID, req.URL, req.EventTypes, req.Secret)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create webhook")
	}

	// Update additional fields
	updates := map[string]interface{}{
		"is_active": req.IsActive,
	}
	
	if req.ContentType != "" {
		updates["content_type"] = req.ContentType
	}
	
	updates["insecure_ssl"] = req.InsecureSSL

	h.db.Model(sub).Updates(updates)

	// Hide secret in response
	sub.Secret = ""

	return c.JSON(http.StatusCreated, sub)
}

// Get returns a specific webhook
func (h *WebhookHandler) Get(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get webhook ID from URL
	webhookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid webhook ID")
	}

	// Find webhook
	var wh models.Webhook
	if err := h.db.Where("id = ? AND user_id = ?", webhookID, userID).First(&wh).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Webhook not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch webhook")
	}

	// Hide secret
	wh.Secret = ""

	return c.JSON(http.StatusOK, wh)
}

// Update updates a webhook subscription
func (h *WebhookHandler) Update(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get webhook ID from URL
	webhookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid webhook ID")
	}

	// Find webhook
	var wh models.Webhook
	if err := h.db.Where("id = ? AND user_id = ?", webhookID, userID).First(&wh).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Webhook not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch webhook")
	}

	// Parse request
	var req struct {
		URL         string   `json:"url" validate:"omitempty,url,https"`
		EventTypes  []string `json:"event_types"`
		Secret      string   `json:"secret" validate:"omitempty,min=16"`
		ContentType string   `json:"content_type"`
		InsecureSSL *bool    `json:"insecure_ssl"`
		IsActive    *bool    `json:"is_active"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Build updates
	updates := map[string]interface{}{}

	if req.URL != "" {
		updates["url"] = req.URL
	}
	if len(req.EventTypes) > 0 {
		// Validate event types
		eventTypesStr := ""
		for i, et := range req.EventTypes {
			if i > 0 {
				eventTypesStr += ","
			}
			eventTypesStr += et
		}
		updates["events"] = eventTypesStr
	}
	if req.Secret != "" {
		updates["secret"] = req.Secret
	}
	if req.ContentType != "" {
		updates["content_type"] = req.ContentType
	}
	if req.InsecureSSL != nil {
		updates["insecure_ssl"] = *req.InsecureSSL
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}

	// Update webhook
	if err := h.manager.UpdateSubscription(webhookID, updates); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update webhook")
	}

	// Reload webhook
	h.db.First(&wh, webhookID)

	// Hide secret
	wh.Secret = ""

	return c.JSON(http.StatusOK, wh)
}

// Delete deletes a webhook subscription
func (h *WebhookHandler) Delete(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get webhook ID from URL
	webhookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid webhook ID")
	}

	// Verify ownership
	var wh models.Webhook
	if err := h.db.Where("id = ? AND user_id = ?", webhookID, userID).First(&wh).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Webhook not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch webhook")
	}

	// Delete webhook
	if err := h.manager.DeleteSubscription(webhookID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete webhook")
	}

	return c.NoContent(http.StatusNoContent)
}

// Test sends a test webhook
func (h *WebhookHandler) Test(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get webhook ID from URL
	webhookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid webhook ID")
	}

	// Verify ownership
	var wh models.Webhook
	if err := h.db.Where("id = ? AND user_id = ?", webhookID, userID).First(&wh).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Webhook not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch webhook")
	}

	// Send test webhook
	if err := h.manager.TestWebhook(webhookID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to send test webhook")
	}

	return c.JSON(http.StatusOK, map[string]string{
		"message": "Test webhook sent successfully",
	})
}

// GetDeliveries returns webhook delivery history
func (h *WebhookHandler) GetDeliveries(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get webhook ID from URL
	webhookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid webhook ID")
	}

	// Verify ownership
	var wh models.Webhook
	if err := h.db.Where("id = ? AND user_id = ?", webhookID, userID).First(&wh).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "Webhook not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch webhook")
	}

	// Parse pagination
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Get deliveries
	var deliveries []models.WebhookDelivery
	offset := (page - 1) * limit
	
	query := h.db.Model(&models.WebhookDelivery{}).
		Where("webhook_id = ?", webhookID)

	// Count total
	var total int64
	query.Count(&total)

	if err := query.
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&deliveries).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch deliveries")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"deliveries": deliveries,
		"total":      total,
		"page":       page,
		"limit":      limit,
		"pages":      (total + int64(limit) - 1) / int64(limit),
	})
}

// GetEventTypes returns available webhook event types
func (h *WebhookHandler) GetEventTypes(c echo.Context) error {
	eventTypes := []map[string]string{
		{
			"type":        string(webhook.EventGistCreated),
			"description": "Triggered when a new gist is created",
		},
		{
			"type":        string(webhook.EventGistUpdated),
			"description": "Triggered when a gist is updated",
		},
		{
			"type":        string(webhook.EventGistDeleted),
			"description": "Triggered when a gist is deleted",
		},
		{
			"type":        string(webhook.EventGistStarred),
			"description": "Triggered when a gist is starred",
		},
		{
			"type":        string(webhook.EventGistForked),
			"description": "Triggered when a gist is forked",
		},
		{
			"type":        string(webhook.EventCommentAdded),
			"description": "Triggered when a comment is added to a gist",
		},
		{
			"type":        string(webhook.EventUserCreated),
			"description": "Triggered when a new user is created",
		},
		{
			"type":        string(webhook.EventOrgCreated),
			"description": "Triggered when a new organization is created",
		},
		{
			"type":        string(webhook.EventTeamCreated),
			"description": "Triggered when a new team is created",
		},
		{
			"type":        "*",
			"description": "All events",
		},
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"event_types": eventTypes,
	})
}

// RegisterRoutes registers webhook routes
func (h *WebhookHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/webhooks", h.List)
	g.POST("/webhooks", h.Create)
	g.GET("/webhooks/event-types", h.GetEventTypes)
	g.GET("/webhooks/:id", h.Get)
	g.PUT("/webhooks/:id", h.Update)
	g.DELETE("/webhooks/:id", h.Delete)
	g.POST("/webhooks/:id/test", h.Test)
	g.GET("/webhooks/:id/deliveries", h.GetDeliveries)
}