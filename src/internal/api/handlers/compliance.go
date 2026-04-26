package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/casapps/casgist/src/internal/compliance"
	"github.com/casapps/casgist/src/internal/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// ComplianceHandler handles compliance-related endpoints
type ComplianceHandler struct {
	db          *gorm.DB
	config      *viper.Viper
	gdprService *compliance.GDPRService
	auditService *compliance.AuditService
}

// NewComplianceHandler creates a new compliance handler
func NewComplianceHandler(db *gorm.DB, config *viper.Viper) *ComplianceHandler {
	exportDir := config.GetString("paths.gdpr_exports")
	if exportDir == "" {
		exportDir = "./data/gdpr_exports"
	}
	
	auditService := compliance.NewAuditService(db)
	gdprService := compliance.NewGDPRService(db, exportDir, auditService)
	
	return &ComplianceHandler{
		db:           db,
		config:       config,
		gdprService:  gdprService,
		auditService: auditService,
	}
}

// GetDataProcessingAgreement returns the current DPA
func (h *ComplianceHandler) GetDataProcessingAgreement(c echo.Context) error {
	dpa := h.gdprService.GetDataProcessingAgreement()
	return c.JSON(http.StatusOK, dpa)
}

// RequestDataExport handles GDPR data export requests
func (h *ComplianceHandler) RequestDataExport(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get client IP
	clientIP := c.RealIP()

	// Create export request
	exportRequest, err := h.gdprService.RequestDataExport(userID, clientIP)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"export_request": exportRequest,
		"message": "Your data export request has been submitted and will be processed within 30 days as required by GDPR.",
	})
}

// GetExportRequests returns user's export requests
func (h *ComplianceHandler) GetExportRequests(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	requests, err := h.gdprService.GetUserExportRequests(userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch export requests")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"export_requests": requests,
	})
}

// DownloadExport downloads a completed data export
func (h *ComplianceHandler) DownloadExport(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get request ID
	requestID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request ID")
	}

	// Get export request
	exportRequest, err := h.gdprService.GetExportRequest(requestID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Export request not found")
	}

	// Verify ownership
	if exportRequest.UserID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "Access denied")
	}

	// Check if export is completed
	if exportRequest.Status != "completed" {
		return echo.NewHTTPError(http.StatusBadRequest, "Export is not ready yet")
	}

	// Check if export hasn't expired
	if exportRequest.ExpiresAt != nil && exportRequest.ExpiresAt.Before(time.Now()) {
		return echo.NewHTTPError(http.StatusGone, "Export has expired")
	}

	// Set headers for download
	c.Response().Header().Set("Content-Type", "application/zip")
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"casgists-data-export-%s.zip\"", userID.String()[:8]))

	return c.File(exportRequest.ExportFilePath)
}

// RequestDataDeletion handles GDPR data deletion requests
func (h *ComplianceHandler) RequestDataDeletion(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request
	var req struct {
		Reason   string `json:"reason" validate:"required,min=10"`
		Confirm  bool   `json:"confirm" validate:"required"`
		Password string `json:"password" validate:"required"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if !req.Confirm {
		return echo.NewHTTPError(http.StatusBadRequest, "Deletion must be confirmed")
	}

	// TODO: Verify password
	// This would require injecting auth service

	// Get client IP
	clientIP := c.RealIP()

	// Create deletion request
	deletionRequest, err := h.gdprService.RequestDataDeletion(userID, req.Reason, clientIP)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"deletion_request": deletionRequest,
		"message": "Your data deletion request has been submitted. Your account and all associated data will be permanently deleted within 30 days.",
	})
}

// GetDeletionRequests returns user's deletion requests
func (h *ComplianceHandler) GetDeletionRequests(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	requests, err := h.gdprService.GetUserDeletionRequests(userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch deletion requests")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"deletion_requests": requests,
	})
}

// GetConsent returns user's consent settings
func (h *ComplianceHandler) GetConsent(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Get consent types from preferences
	consents := map[string]bool{
		"marketing_emails": false,
		"analytics":        false,
		"third_party_sharing": false,
		"necessary_cookies": true, // Always true - required for service
		"functional_cookies": true,
		"performance_cookies": false,
	}
	
	// Load user consent preferences
	var prefs []models.UserPreference
	if err := h.db.Where("user_id = ? AND key LIKE ?", userID, "consent_%").Find(&prefs).Error; err == nil {
		for _, pref := range prefs {
			consentType := strings.TrimPrefix(pref.Key, "consent_")
			if pref.Value == "true" {
				consents[consentType] = true
			} else {
				consents[consentType] = false
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"user_id": userID,
		"consents": consents,
	})
}

// UpdateConsent updates user's consent settings
func (h *ComplianceHandler) UpdateConsent(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request
	var req map[string]bool
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// Update each consent type
	// TODO: Implement actual consent storage
	for consentType, granted := range req {
		_ = h.gdprService.UpdateConsent(c.Request().Context(), userID, consentType, granted)
	}

	// Log consent update
	h.auditService.LogCompliance(userID, "GDPR", "consent_updated", "user_preferences", 
		map[string]interface{}{"consents": req}, "user_consent", 0)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Consent preferences updated",
		"consents": req,
	})
}

// GetAuditLogs returns audit logs (admin only)
func (h *ComplianceHandler) GetAuditLogs(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Parse filters
	filters := compliance.AuditLogFilters{
		Page:  1,
		Limit: 50,
	}

	// Get query parameters
	if userID := c.QueryParam("user_id"); userID != "" {
		if id, err := uuid.Parse(userID); err == nil {
			filters.UserID = &id
		}
	}

	if action := c.QueryParam("action"); action != "" {
		filters.Action = action
	}

	if resourceType := c.QueryParam("resource_type"); resourceType != "" {
		filters.ResourceType = resourceType
	}

	// Get audit logs
	response, err := h.auditService.GetAuditLogs(filters)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch audit logs")
	}

	return c.JSON(http.StatusOK, response)
}

// ProcessDeletionRequest processes a deletion request (admin only)
func (h *ComplianceHandler) ProcessDeletionRequest(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Get admin user ID
	adminID, _ := c.Get("user_id").(uuid.UUID)

	// Get request ID
	requestID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request ID")
	}

	// Process deletion
	if err := h.gdprService.ProcessDataDeletion(requestID, adminID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to process deletion: %v", err))
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Deletion request processed successfully",
	})
}

// RegisterRoutes registers compliance routes
func (h *ComplianceHandler) RegisterRoutes(g *echo.Group) {
	// Public compliance endpoints
	g.GET("/compliance/dpa", h.GetDataProcessingAgreement)
	
	// User compliance endpoints (require auth)
	g.POST("/compliance/gdpr/export", h.RequestDataExport)
	g.GET("/compliance/gdpr/exports", h.GetExportRequests)
	g.GET("/compliance/gdpr/export/:id/download", h.DownloadExport)
	
	g.POST("/compliance/gdpr/delete", h.RequestDataDeletion)
	g.GET("/compliance/gdpr/deletions", h.GetDeletionRequests)
	
	g.GET("/compliance/consent", h.GetConsent)
	g.PUT("/compliance/consent", h.UpdateConsent)
	
	// Admin compliance endpoints
	g.GET("/compliance/audit", h.GetAuditLogs)
	g.POST("/compliance/gdpr/deletion/:id/process", h.ProcessDeletionRequest)
}