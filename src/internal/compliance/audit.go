package compliance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/models"
)

// AuditService handles comprehensive audit logging
type AuditService struct {
	db *gorm.DB
}

// NewAuditService creates a new audit service
func NewAuditService(db *gorm.DB) *AuditService {
	return &AuditService{
		db: db,
	}
}

// LogAction logs a general audit action
func (a *AuditService) LogAction(userID *uuid.UUID, action, resourceType, resourceID string, details map[string]interface{}, req *http.Request) error {
	auditLog := &models.AuditLog{
		ID:           uuid.New(),
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Success:      true,
		CreatedAt:    time.Now(),
	}
	
	// Add request context if provided
	if req != nil {
		auditLog.IPAddress = getClientIP(req)
		auditLog.UserAgent = req.UserAgent()
	}
	
	// Serialize details to JSON
	if details != nil {
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("failed to marshal audit details: %w", err)
		}
		auditLog.Metadata = string(detailsJSON)
	}
	
	return a.db.Create(auditLog).Error
}

// LogFailedAction logs a failed action
func (a *AuditService) LogFailedAction(userID *uuid.UUID, action, resourceType, resourceID string, errorMsg string, req *http.Request) error {
	auditLog := &models.AuditLog{
		ID:           uuid.New(),
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Success:      false,
		ErrorMessage: errorMsg,
		CreatedAt:    time.Now(),
	}
	
	// Add request context if provided
	if req != nil {
		auditLog.IPAddress = getClientIP(req)
		auditLog.UserAgent = req.UserAgent()
	}
	
	return a.db.Create(auditLog).Error
}

// LogCompliance logs compliance-specific actions
func (a *AuditService) LogCompliance(userID uuid.UUID, complianceType, action, dataType string, details map[string]interface{}, legalBasis string, retentionDays int) error {
	// Store compliance logs as regular audit logs with specific metadata
	complianceDetails := map[string]interface{}{
		"compliance_type": complianceType,
		"data_type":       dataType,
		"legal_basis":     legalBasis,
		"retention_days":  retentionDays,
	}
	
	// Merge with provided details
	for k, v := range details {
		complianceDetails[k] = v
	}
	
	return a.LogAction(&userID, fmt.Sprintf("compliance.%s", action), "compliance", "", complianceDetails, nil)
}

// GetAuditLogs retrieves audit logs with filtering and pagination
func (a *AuditService) GetAuditLogs(filters AuditLogFilters) (*AuditLogResponse, error) {
	query := a.db.Model(&models.AuditLog{}).Preload("User")
	
	// Apply filters
	if filters.UserID != nil {
		query = query.Where("user_id = ?", *filters.UserID)
	}
	if filters.Action != "" {
		query = query.Where("action = ?", filters.Action)
	}
	if filters.ResourceType != "" {
		query = query.Where("resource_type = ?", filters.ResourceType)
	}
	if filters.ResourceID != "" {
		query = query.Where("resource_id = ?", filters.ResourceID)
	}
	if !filters.StartDate.IsZero() {
		query = query.Where("created_at >= ?", filters.StartDate)
	}
	if !filters.EndDate.IsZero() {
		query = query.Where("created_at <= ?", filters.EndDate)
	}
	if filters.Success != nil {
		query = query.Where("success = ?", *filters.Success)
	}
	if filters.IPAddress != "" {
		query = query.Where("ip_address = ?", filters.IPAddress)
	}
	
	// Count total matching records
	var total int64
	query.Count(&total)
	
	// Apply pagination
	offset := (filters.Page - 1) * filters.Limit
	query = query.Order("created_at DESC").Limit(filters.Limit).Offset(offset)
	
	// Execute query
	var logs []models.AuditLog
	if err := query.Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch audit logs: %w", err)
	}
	
	return &AuditLogResponse{
		Logs:       logs,
		TotalCount: int(total),
		Page:       filters.Page,
		PageSize:   filters.Limit,
		TotalPages: int((total + int64(filters.Limit) - 1) / int64(filters.Limit)),
	}, nil
}

// GetUserActivity gets recent activity for a specific user
func (a *AuditService) GetUserActivity(userID uuid.UUID, limit int) ([]models.AuditLog, error) {
	var logs []models.AuditLog
	err := a.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
		
	return logs, err
}

// GetResourceHistory gets audit history for a specific resource
func (a *AuditService) GetResourceHistory(resourceType, resourceID string, limit int) ([]models.AuditLog, error) {
	var logs []models.AuditLog
	err := a.db.Where("resource_type = ? AND resource_id = ?", resourceType, resourceID).
		Order("created_at DESC").
		Limit(limit).
		Preload("User").
		Find(&logs).Error
		
	return logs, err
}

// CleanupOldLogs removes audit logs older than retention period
func (a *AuditService) CleanupOldLogs(retentionDays int) error {
	cutoffDate := time.Now().AddDate(0, 0, -retentionDays)
	
	// Don't delete compliance-related logs
	return a.db.Where("created_at < ? AND action NOT LIKE ?", cutoffDate, "compliance.%").
		Delete(&models.AuditLog{}).Error
}

// Helper function to extract client IP
func getClientIP(r *http.Request) string {
	// Check X-Real-IP header
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return realIP
	}
	
	// Check X-Forwarded-For header
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		// Take the first IP in the chain
		ips := strings.Split(forwardedFor, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}
	
	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// AuditLogFilters defines filters for querying audit logs
type AuditLogFilters struct {
	UserID       *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   string
	StartDate    time.Time
	EndDate      time.Time
	Success      *bool
	IPAddress    string
	Page         int
	Limit        int
}

// AuditLogResponse represents the response for audit log queries
type AuditLogResponse struct {
	Logs       []models.AuditLog `json:"logs"`
	TotalCount int               `json:"total_count"`
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
	TotalPages int               `json:"total_pages"`
}

// LogRequest logs HTTP request details from Echo context
func (a *AuditService) LogRequest(c echo.Context, userID *uuid.UUID, action string) error {
	details := map[string]interface{}{
		"method":     c.Request().Method,
		"path":       c.Request().URL.Path,
		"query":      c.Request().URL.RawQuery,
		"request_id": c.Response().Header().Get(echo.HeaderXRequestID),
	}
	
	return a.LogAction(userID, action, "http_request", c.Request().URL.Path, details, c.Request())
}