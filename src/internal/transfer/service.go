package transfer

import (
	"context"
	"fmt"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Service handles gist transfer operations
type Service struct {
	db *gorm.DB
}

// NewService creates a new transfer service
func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// TransferRequest represents a transfer request
type TransferRequest struct {
	GistID           uuid.UUID  `json:"gist_id" validate:"required"`
	ToUserID         *uuid.UUID `json:"to_user_id"`
	ToOrganizationID *uuid.UUID `json:"to_organization_id"`
	Message          string     `json:"message"`
	TransferReason   string     `json:"transfer_reason"`
	PreserveHistory  bool       `json:"preserve_history"`
	NotifyWatchers   bool       `json:"notify_watchers"`
}

// CreateTransferRequest creates a new transfer request
func (s *Service) CreateTransferRequest(ctx context.Context, requesterID uuid.UUID, req *TransferRequest) (*models.TransferRequest, error) {
	// Validate request
	if req.ToUserID == nil && req.ToOrganizationID == nil {
		return nil, fmt.Errorf("transfer destination required")
	}
	if req.ToUserID != nil && req.ToOrganizationID != nil {
		return nil, fmt.Errorf("cannot transfer to both user and organization")
	}

	// Get the gist and verify ownership
	var gist models.Gist
	if err := s.db.First(&gist, "id = ?", req.GistID).Error; err != nil {
		return nil, fmt.Errorf("gist not found: %w", err)
	}

	// Check if requester owns the gist or is org admin
	var canTransfer bool
	if gist.UserID != nil && *gist.UserID == requesterID {
		canTransfer = true
	} else if gist.OrganizationID != nil {
		// Check if requester is org admin/owner
		var member models.OrganizationMember
		err := s.db.Where("organization_id = ? AND user_id = ? AND role IN (?, ?)",
			gist.OrganizationID, requesterID, "admin", "owner").First(&member).Error
		canTransfer = (err == nil)
	}

	if !canTransfer {
		return nil, fmt.Errorf("insufficient permissions to transfer this gist")
	}

	// Create transfer request
	transferReq := &models.TransferRequest{
		ID:                 uuid.New(),
		GistID:             req.GistID,
		FromUserID:         gist.UserID,
		FromOrganizationID: gist.OrganizationID,
		ToUserID:           req.ToUserID,
		ToOrganizationID:   req.ToOrganizationID,
		RequestedByID:      requesterID,
		Status:             models.TransferStatusPending,
		Message:            req.Message,
		TransferReason:     models.TransferReason(req.TransferReason),
		PreserveHistory:    req.PreserveHistory,
		NotifyWatchers:     req.NotifyWatchers,
		ExpiresAt:          time.Now().Add(7 * 24 * time.Hour), // 7 days
	}

	if err := s.db.Create(transferReq).Error; err != nil {
		return nil, fmt.Errorf("failed to create transfer request: %w", err)
	}

	return transferReq, nil
}

// ProcessTransferRequest processes a transfer request (accept/reject)
func (s *Service) ProcessTransferRequest(ctx context.Context, requestID uuid.UUID, userID uuid.UUID, action string) error {
	// Get transfer request
	var req models.TransferRequest
	if err := s.db.Preload("Gist").First(&req, "id = ?", requestID).Error; err != nil {
		return fmt.Errorf("transfer request not found: %w", err)
	}

	// Check if user can process this request
	var canProcess bool
	if req.ToUserID != nil && *req.ToUserID == userID {
		canProcess = true
	} else if req.ToOrganizationID != nil {
		// Check if user is org admin/owner
		var member models.OrganizationMember
		err := s.db.Where("organization_id = ? AND user_id = ? AND role IN (?, ?)",
			req.ToOrganizationID, userID, "admin", "owner").First(&member).Error
		canProcess = (err == nil)
	}

	if !canProcess {
		return fmt.Errorf("insufficient permissions to process this transfer")
	}

	// Check if request is still valid
	if req.Status != models.TransferStatusPending {
		return fmt.Errorf("transfer request already processed")
	}
	if time.Now().After(req.ExpiresAt) {
		req.Status = models.TransferStatusExpired
		s.db.Save(&req)
		return fmt.Errorf("transfer request has expired")
	}

	// Process based on action
	switch action {
	case "accept":
		return s.executeTransfer(ctx, &req, userID)
	case "reject":
		req.Status = models.TransferStatusRejected
		req.ProcessedByID = &userID
		now := time.Now()
		req.ProcessedAt = &now
		return s.db.Save(&req).Error
	default:
		return fmt.Errorf("invalid action: %s", action)
	}
}

// executeTransfer performs the actual gist transfer
func (s *Service) executeTransfer(ctx context.Context, req *models.TransferRequest, processorID uuid.UUID) error {
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Get the gist
	var gist models.Gist
	if err := tx.First(&gist, "id = ?", req.GistID).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("gist not found: %w", err)
	}

	// Store original ownership for history
	originalUserID := gist.UserID
	originalOrgID := gist.OrganizationID

	// Update gist ownership
	gist.UserID = req.ToUserID
	gist.OrganizationID = req.ToOrganizationID

	if err := tx.Save(&gist).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to update gist ownership: %w", err)
	}

	// Update transfer request status
	req.Status = models.TransferStatusCompleted
	req.ProcessedByID = &processorID
	now := time.Now()
	req.ProcessedAt = &now

	if err := tx.Save(req).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to update transfer request: %w", err)
	}

	// Create transfer history record
	history := &models.TransferHistory{
		ID:                     uuid.New(),
		GistID:                 req.GistID,
		TransferRequestID:      &req.ID,
		PreviousUserID:         originalUserID,
		PreviousOrganizationID: originalOrgID,
		NewUserID:              req.ToUserID,
		NewOrganizationID:      req.ToOrganizationID,
		TransferredByID:        processorID,
		TransferredAt:          now,
		TransferReason:         req.TransferReason,
		Notes:                  req.Message,
	}

	if err := tx.Create(history).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to create transfer history: %w", err)
	}

	return tx.Commit().Error
}

// ListTransferRequests lists transfer requests for a user or organization
func (s *Service) ListTransferRequests(userID uuid.UUID, orgIDs []uuid.UUID, status string) ([]models.TransferRequest, error) {
	query := s.db.Preload("Gist").Preload("FromUser").Preload("ToUser").
		Where("(to_user_id = ? OR requested_by_id = ?)", userID, userID)

	// Add organization requests
	if len(orgIDs) > 0 {
		query = query.Or("to_organization_id IN ? OR from_organization_id IN ?", orgIDs, orgIDs)
	}

	// Filter by status if provided
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var requests []models.TransferRequest
	if err := query.Order("created_at DESC").Find(&requests).Error; err != nil {
		return nil, err
	}

	return requests, nil
}

// GetTransferHistory gets transfer history for a gist
func (s *Service) GetTransferHistory(gistID uuid.UUID) ([]models.TransferHistory, error) {
	var history []models.TransferHistory
	if err := s.db.Preload("PreviousUser").Preload("NewUser").
		Preload("TransferredBy").Where("gist_id = ?", gistID).
		Order("transferred_at DESC").Find(&history).Error; err != nil {
		return nil, err
	}

	return history, nil
}
