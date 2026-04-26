package compliance

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/models"
)

// GDPRService handles GDPR compliance operations
type GDPRService struct {
	db           *gorm.DB
	exportDir    string
	auditService *AuditService
}

// NewGDPRService creates a new GDPR service
func NewGDPRService(db *gorm.DB, exportDir string, auditService *AuditService) *GDPRService {
	return &GDPRService{
		db:           db,
		exportDir:    exportDir,
		auditService: auditService,
	}
}

// RequestDataExport creates a GDPR data export request
func (g *GDPRService) RequestDataExport(userID uuid.UUID, requestorIP string) (*models.GDPRExportRequest, error) {
	// Check if user has a pending or processing export request
	var existingRequest models.GDPRExportRequest
	err := g.db.Where("user_id = ? AND status IN ('pending', 'processing')", userID).First(&existingRequest).Error
	if err == nil {
		return nil, fmt.Errorf("user already has a pending export request")
	} else if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check existing requests: %w", err)
	}
	
	// Create new export request
	exportRequest := &models.GDPRExportRequest{
		ID:        uuid.New(),
		UserID:    userID,
		Status:    "pending",
		ExpiresAt: timePtr(time.Now().AddDate(0, 0, 30)), // Expires in 30 days
		CreatedAt: time.Now(),
	}
	
	if err := g.db.Create(exportRequest).Error; err != nil {
		return nil, fmt.Errorf("failed to create export request: %w", err)
	}
	
	// Log the request
	g.auditService.LogCompliance(userID, "GDPR", "data_export_requested", "user_data", map[string]interface{}{
		"export_request_id": exportRequest.ID,
		"requestor_ip":      requestorIP,
	}, "user_right_to_access", 30)
	
	// Process export asynchronously
	go g.processDataExport(exportRequest)
	
	return exportRequest, nil
}

// RequestDataDeletion creates a GDPR data deletion request
func (g *GDPRService) RequestDataDeletion(userID uuid.UUID, reason, requestorIP string) (*models.GDPRDeletionRequest, error) {
	// Check if user has a pending or processing deletion request
	var existingRequest models.GDPRDeletionRequest
	err := g.db.Where("user_id = ? AND status IN ('pending', 'processing')", userID).First(&existingRequest).Error
	if err == nil {
		return nil, fmt.Errorf("user already has a pending deletion request")
	} else if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check existing requests: %w", err)
	}
	
	// Create new deletion request
	deletionRequest := &models.GDPRDeletionRequest{
		ID:             uuid.New(),
		UserID:         userID,
		Status:         "pending",
		DeletionReason: reason,
		CreatedAt:      time.Now(),
	}
	
	if err := g.db.Create(deletionRequest).Error; err != nil {
		return nil, fmt.Errorf("failed to create deletion request: %w", err)
	}
	
	// Log the request
	g.auditService.LogCompliance(userID, "GDPR", "data_deletion_requested", "user_data", map[string]interface{}{
		"deletion_request_id": deletionRequest.ID,
		"reason":              reason,
		"requestor_ip":        requestorIP,
	}, "user_right_to_erasure", 0)
	
	return deletionRequest, nil
}

// ProcessDataDeletion processes a GDPR data deletion request
func (g *GDPRService) ProcessDataDeletion(requestID uuid.UUID, processorID uuid.UUID) error {
	// Get deletion request
	var request models.GDPRDeletionRequest
	if err := g.db.Preload("User").First(&request, "id = ?", requestID).Error; err != nil {
		return fmt.Errorf("deletion request not found: %w", err)
	}
	
	if request.Status != "pending" {
		return fmt.Errorf("deletion request is not in pending status")
	}
	
	// Update status to processing
	request.Status = "processing"
	request.ProcessedByID = &processorID
	if err := g.db.Save(&request).Error; err != nil {
		return fmt.Errorf("failed to update deletion request: %w", err)
	}
	
	// Log processing start
	g.auditService.LogCompliance(request.UserID, "GDPR", "data_deletion_processing", "user_data", map[string]interface{}{
		"deletion_request_id": requestID,
		"processor_id":        processorID,
	}, "user_right_to_erasure", 0)
	
	// Perform deletion
	if err := g.deleteUserData(request.UserID, requestID); err != nil {
		// Mark as failed
		request.Status = "failed"
		g.db.Save(&request)
		return fmt.Errorf("failed to delete user data: %w", err)
	}
	
	// Mark as completed
	now := time.Now()
	request.Status = "completed"
	request.CompletedAt = &now
	if err := g.db.Save(&request).Error; err != nil {
		return fmt.Errorf("failed to update deletion request status: %w", err)
	}
	
	// Log completion
	g.auditService.LogCompliance(request.UserID, "GDPR", "data_deletion_completed", "user_data", map[string]interface{}{
		"deletion_request_id": requestID,
		"processor_id":        processorID,
	}, "user_right_to_erasure", 0)
	
	return nil
}

// GetExportRequest retrieves an export request
func (g *GDPRService) GetExportRequest(requestID uuid.UUID) (*models.GDPRExportRequest, error) {
	var request models.GDPRExportRequest
	if err := g.db.Preload("User").First(&request, "id = ?", requestID).Error; err != nil {
		return nil, fmt.Errorf("export request not found: %w", err)
	}
	return &request, nil
}

// GetUserExportRequests gets all export requests for a user
func (g *GDPRService) GetUserExportRequests(userID uuid.UUID) ([]models.GDPRExportRequest, error) {
	var requests []models.GDPRExportRequest
	if err := g.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&requests).Error; err != nil {
		return nil, fmt.Errorf("failed to get export requests: %w", err)
	}
	return requests, nil
}

// GetUserDeletionRequests gets all deletion requests for a user
func (g *GDPRService) GetUserDeletionRequests(userID uuid.UUID) ([]models.GDPRDeletionRequest, error) {
	var requests []models.GDPRDeletionRequest
	if err := g.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&requests).Error; err != nil {
		return nil, fmt.Errorf("failed to get deletion requests: %w", err)
	}
	return requests, nil
}

// processDataExport processes a GDPR data export request
func (g *GDPRService) processDataExport(request *models.GDPRExportRequest) {
	// Update status to processing
	request.Status = "processing"
	g.db.Save(request)
	
	// Log processing start
	g.auditService.LogCompliance(request.UserID, "GDPR", "data_export_processing", "user_data", map[string]interface{}{
		"export_request_id": request.ID,
	}, "user_right_to_access", 30)
	
	// Export user data
	exportPath, err := g.exportUserData(request.UserID, request.ID)
	if err != nil {
		request.Status = "failed"
		g.db.Save(request)
		return
	}
	
	// Update request with export file path
	now := time.Now()
	request.Status = "completed"
	request.ExportFilePath = exportPath
	request.CompletedAt = &now
	g.db.Save(request)
	
	// Log completion
	g.auditService.LogCompliance(request.UserID, "GDPR", "data_export_completed", "user_data", map[string]interface{}{
		"export_request_id": request.ID,
		"export_file":       exportPath,
	}, "user_right_to_access", 30)
}

// exportUserData exports all user data to a ZIP file
func (g *GDPRService) exportUserData(userID uuid.UUID, requestID uuid.UUID) (string, error) {
	// Ensure export directory exists
	if err := os.MkdirAll(g.exportDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create export directory: %w", err)
	}
	
	// Create export file
	exportPath := filepath.Join(g.exportDir, fmt.Sprintf("user_export_%s_%s.zip", userID, requestID))
	zipFile, err := os.Create(exportPath)
	if err != nil {
		return "", fmt.Errorf("failed to create export file: %w", err)
	}
	defer zipFile.Close()
	
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()
	
	// Export user profile
	if err := g.exportUserProfile(zipWriter, userID); err != nil {
		return "", fmt.Errorf("failed to export user profile: %w", err)
	}
	
	// Export user gists
	if err := g.exportUserGists(zipWriter, userID); err != nil {
		return "", fmt.Errorf("failed to export user gists: %w", err)
	}
	
	// Export user activity
	if err := g.exportUserActivity(zipWriter, userID); err != nil {
		return "", fmt.Errorf("failed to export user activity: %w", err)
	}
	
	// Export user preferences
	if err := g.exportUserPreferences(zipWriter, userID); err != nil {
		return "", fmt.Errorf("failed to export user preferences: %w", err)
	}
	
	return exportPath, nil
}

// exportUserProfile exports user profile data
func (g *GDPRService) exportUserProfile(zipWriter *zip.Writer, userID uuid.UUID) error {
	var user models.User
	if err := g.db.First(&user, "id = ?", userID).Error; err != nil {
		return err
	}
	
	// Sanitize sensitive data for export
	exportUser := struct {
		ID            uuid.UUID `json:"id"`
		Username      string    `json:"username"`
		Email         string    `json:"email"`
		DisplayName   string    `json:"display_name"`
		Bio           string    `json:"bio"`
		Location      string    `json:"location"`
		Website       string    `json:"website"`
		Company       string    `json:"company"`
		AvatarURL     string    `json:"avatar_url"`
		IsActive      bool      `json:"is_active"`
		EmailVerified bool      `json:"email_verified"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
	}{
		ID:            user.ID,
		Username:      user.Username,
		Email:         user.Email,
		DisplayName:   user.DisplayName,
		Bio:           user.Bio,
		Location:      "", // user.Location field not available
		Website:       "", // user.Website field not available  
		Company:       "", // user.Company field not available
		AvatarURL:     user.AvatarURL,
		IsActive:      user.IsActive,
		EmailVerified: user.EmailVerified,
		CreatedAt:     user.CreatedAt,
		UpdatedAt:     user.UpdatedAt,
	}
	
	return g.addJSONToZip(zipWriter, "profile.json", exportUser)
}

// exportUserGists exports user gists
func (g *GDPRService) exportUserGists(zipWriter *zip.Writer, userID uuid.UUID) error {
	var gists []models.Gist
	if err := g.db.Preload("Files").Where("user_id = ?", userID).Find(&gists).Error; err != nil {
		return err
	}
	
	return g.addJSONToZip(zipWriter, "gists.json", gists)
}

// exportUserActivity exports user activity data
func (g *GDPRService) exportUserActivity(zipWriter *zip.Writer, userID uuid.UUID) error {
	var stars []models.Star
	if err := g.db.Where("user_id = ?", userID).Find(&stars).Error; err != nil {
		return err
	}
	
	var comments []models.Comment
	if err := g.db.Where("user_id = ?", userID).Find(&comments).Error; err != nil {
		return err
	}
	
	activity := struct {
		Stars    []models.Star    `json:"stars"`
		Comments []models.Comment `json:"comments"`
	}{
		Stars:    stars,
		Comments: comments,
	}
	
	return g.addJSONToZip(zipWriter, "activity.json", activity)
}

// exportUserPreferences exports user preferences
func (g *GDPRService) exportUserPreferences(zipWriter *zip.Writer, userID uuid.UUID) error {
	var prefs models.UserPreference
	if err := g.db.Where("user_id = ?", userID).First(&prefs).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil // No preferences to export
		}
		return err
	}
	
	return g.addJSONToZip(zipWriter, "preferences.json", prefs)
}

// addJSONToZip adds JSON data to zip file
func (g *GDPRService) addJSONToZip(zipWriter *zip.Writer, filename string, data interface{}) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	
	writer, err := zipWriter.Create(filename)
	if err != nil {
		return err
	}
	
	_, err = writer.Write(jsonData)
	return err
}

// deleteUserData deletes all user data (GDPR right to erasure)
func (g *GDPRService) deleteUserData(userID uuid.UUID, requestID uuid.UUID) error {
	// Start transaction
	tx := g.db.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()
	
	// Delete user gists and related data
	if err := tx.Where("user_id = ?", userID).Delete(&models.GistFile{}).Error; err != nil {
		return fmt.Errorf("failed to delete gist files: %w", err)
	}
	
	if err := tx.Where("user_id = ?", userID).Delete(&models.Star{}).Error; err != nil {
		return fmt.Errorf("failed to delete gist stars: %w", err)
	}
	
	if err := tx.Where("user_id = ?", userID).Delete(&models.Comment{}).Error; err != nil {
		return fmt.Errorf("failed to delete gist comments: %w", err)
	}
	
	if err := tx.Where("user_id = ?", userID).Delete(&models.Gist{}).Error; err != nil {
		return fmt.Errorf("failed to delete gists: %w", err)
	}
	
	// Delete user preferences
	if err := tx.Where("user_id = ?", userID).Delete(&models.UserPreference{}).Error; err != nil {
		return fmt.Errorf("failed to delete user preferences: %w", err)
	}
	
	// Delete webhooks
	if err := tx.Where("user_id = ?", userID).Delete(&models.Webhook{}).Error; err != nil {
		return fmt.Errorf("failed to delete webhooks: %w", err)
	}
	
	// Delete custom domains
	if err := tx.Where("user_id = ?", userID).Delete(&models.CustomDomain{}).Error; err != nil {
		return fmt.Errorf("failed to delete custom domains: %w", err)
	}
	
	// Anonymize audit logs (keep for legal compliance but remove PII)
	if err := tx.Model(&models.AuditLog{}).Where("user_id = ?", userID).Updates(map[string]interface{}{
		"user_id": nil,
		"details": fmt.Sprintf(`{"anonymized": true, "deletion_request_id": "%s"}`, requestID),
	}).Error; err != nil {
		return fmt.Errorf("failed to anonymize audit logs: %w", err)
	}
	
	// Finally, delete the user
	if err := tx.Delete(&models.User{}, userID).Error; err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	
	// Commit transaction
	return tx.Commit().Error
}

// CleanupExpiredExports removes expired export files
func (g *GDPRService) CleanupExpiredExports() error {
	var expiredRequests []models.GDPRExportRequest
	if err := g.db.Where("status = 'completed' AND expires_at < ?", time.Now()).Find(&expiredRequests).Error; err != nil {
		return fmt.Errorf("failed to find expired exports: %w", err)
	}
	
	for _, request := range expiredRequests {
		// Delete export file
		if request.ExportFilePath != "" {
			if err := os.Remove(request.ExportFilePath); err != nil && !os.IsNotExist(err) {
				fmt.Printf("Warning: failed to delete export file %s: %v\n", request.ExportFilePath, err)
			}
		}
		
		// Delete request record
		if err := g.db.Delete(&request).Error; err != nil {
			fmt.Printf("Warning: failed to delete export request %s: %v\n", request.ID, err)
		}
	}
	
	return nil
}

// GetDataProcessingAgreement returns the current data processing agreement text
func (g *GDPRService) GetDataProcessingAgreement() *DataProcessingAgreement {
	return &DataProcessingAgreement{
		Version:     "1.0",
		LastUpdated: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Content: `DATA PROCESSING AGREEMENT

This agreement governs how CasGists processes your personal data in compliance with GDPR.

1. DATA COLLECTION
We collect personal data you provide when creating an account, including:
- Username and email address
- Profile information (optional)
- Content you create (gists, comments)

2. LAWFUL BASIS
We process your data based on:
- Performance of contract (providing the service)
- Legitimate interests (service improvement, security)
- Consent (optional features like analytics)

3. DATA RETENTION
- Account data: Retained while account is active
- Gists and content: Retained per your privacy settings
- Audit logs: Retained for 7 years for legal compliance

4. YOUR RIGHTS
Under GDPR, you have the right to:
- Access your personal data
- Rectify inaccurate data
- Erase your data (right to be forgotten)
- Restrict processing
- Data portability
- Object to processing

5. DATA TRANSFERS
Your data is processed within the EU/EEA. Any transfers outside are protected by adequate safeguards.

6. CONTACT
For data protection queries, contact: privacy@casgists.com`,
	}
}

// DataProcessingAgreement contains the DPA text
type DataProcessingAgreement struct {
	Version     string    `json:"version"`
	LastUpdated time.Time `json:"last_updated"`
	Content     string    `json:"content"`
}

// UpdateConsent updates user consent preferences
func (g *GDPRService) UpdateConsent(ctx context.Context, userID uuid.UUID, consentType string, granted bool) error {
	// Store consent as user preference
	var pref models.UserPreference
	err := g.db.Where("user_id = ? AND key = ?", userID, "consent_"+consentType).First(&pref).Error
	
	if err == gorm.ErrRecordNotFound {
		// Create new preference
		pref = models.UserPreference{
			ID:     uuid.New(),
			UserID: userID,
			Key:    "consent_" + consentType,
			Value:  fmt.Sprintf("%t", granted),
		}
		if err := g.db.Create(&pref).Error; err != nil {
			return fmt.Errorf("failed to create consent preference: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to query consent preference: %w", err)
	} else {
		// Update existing preference
		pref.Value = fmt.Sprintf("%t", granted)
		if err := g.db.Save(&pref).Error; err != nil {
			return fmt.Errorf("failed to update consent preference: %w", err)
		}
	}
	
	// Log consent update
	g.auditService.LogCompliance(userID, "GDPR", "consent_updated", consentType, map[string]interface{}{
		"consent_type": consentType,
		"granted":      granted,
	}, "user_consent", 0)
	
	return nil
}

// timePtr returns a pointer to the time value
func timePtr(t time.Time) *time.Time {
	return &t
}