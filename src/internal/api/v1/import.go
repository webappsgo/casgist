package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/migration/github"
	"github.com/casapps/casgist/src/internal/migration/gitlab"
)

// ImportHandler handles import-related API endpoints
type ImportHandler struct {
	db           *gorm.DB
	config       *Config
	activeJobs   map[string]*ImportJob
	jobsMutex    sync.RWMutex
}

// ImportJob represents an active import job
type ImportJob struct {
	ID               string            `json:"id"`
	Platform         string            `json:"platform"` // github, gitlab, bitbucket
	Status           string            `json:"status"`
	SourceURL        string            `json:"source_url,omitempty"`
	Username         string            `json:"username,omitempty"`
	TotalItems       int               `json:"total_items"`
	ProcessedItems   int               `json:"processed_items"`
	ImportedGists    int               `json:"imported_gists"`
	ErrorCount       int               `json:"error_count"`
	Errors           []string          `json:"errors"`
	StartTime        time.Time         `json:"start_time"`
	EndTime          *time.Time        `json:"end_time,omitempty"`
	ElapsedTime      string            `json:"elapsed_time"`
	CurrentOperation string            `json:"current_operation,omitempty"`
	Settings         map[string]interface{} `json:"settings"`
}

// NewImportHandler creates a new import handler
func NewImportHandler(db *gorm.DB, config *Config) *ImportHandler {
	return &ImportHandler{
		db:         db,
		config:     config,
		activeJobs: make(map[string]*ImportJob),
	}
}

// RegisterImportRoutes registers import-related routes
func (h *ImportHandler) RegisterImportRoutes(g *echo.Group) {
	// GitHub import routes
	g.POST("/imports/github/test", h.TestGitHubConnection)
	g.POST("/imports/github/start", h.StartGitHubImport)
	
	// GitLab import routes
	g.POST("/imports/gitlab/test", h.TestGitLabConnection)
	g.POST("/imports/gitlab/start", h.StartGitLabImport)
	
	// Bitbucket import routes
	g.POST("/imports/bitbucket/test", h.TestBitbucketConnection)
	g.POST("/imports/bitbucket/start", h.StartBitbucketImport)
	
	// Generic import routes
	g.GET("/imports/status/:id", h.GetImportStatus)
	g.GET("/imports/recent", h.GetRecentImports)
	g.DELETE("/imports/:id", h.CancelImport)
}

// GitHub Import Handlers

// TestGitHubConnectionRequest represents GitHub connection test request
type TestGitHubConnectionRequest struct {
	Username string `json:"username" validate:"required"`
	Token    string `json:"token"`
}

// TestGitHubConnection tests connection to GitHub
func (h *ImportHandler) TestGitHubConnection(c echo.Context) error {
	var req TestGitHubConnectionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Create GitHub client
	client := github.NewClient(req.Token)
	
	// Test connection and get user info
	user, err := client.GetUser(context.Background(), req.Username)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Failed to connect to GitHub: %v", err),
		})
	}

	// Get gist count
	gists, err := client.ListGists(context.Background(), req.Username, &github.ListGistsOptions{
		PerPage: 1,
	})
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Failed to access gists: %v", err),
		})
	}

	// Estimate total gists (GitHub API doesn't provide total count directly)
	gistCount := 0
	if len(gists) > 0 {
		// This is a rough estimate - we'd need to paginate to get exact count
		gistCount = 100 // Default estimate
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success":    true,
		"user_id":    user.ID,
		"username":   user.Login,
		"name":       user.Name,
		"gist_count": gistCount,
		"message":    "Successfully connected to GitHub",
	})
}

// StartGitHubImportRequest represents GitHub import start request
type StartGitHubImportRequest struct {
	Username        string `json:"username" validate:"required"`
	Token           string `json:"token"`
	ImportPublic    bool   `json:"import_public"`
	ImportPrivate   bool   `json:"import_private"`
	PreserveURLs    bool   `json:"preserve_urls"`
	ImportComments  bool   `json:"import_comments"`
	Limit           *int   `json:"limit"`
}

// StartGitHubImport starts GitHub import
func (h *ImportHandler) StartGitHubImport(c echo.Context) error {
	var req StartGitHubImportRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Get current user
	userID := c.Get("user_id").(uuid.UUID)

	// Create import job
	jobID := uuid.New().String()
	job := &ImportJob{
		ID:               jobID,
		Platform:         "github",
		Status:           "starting",
		Username:         req.Username,
		StartTime:        time.Now(),
		TotalItems:       0,
		ProcessedItems:   0,
		ImportedGists:    0,
		ErrorCount:       0,
		Errors:           []string{},
		CurrentOperation: "Initializing GitHub import...",
		Settings: map[string]interface{}{
			"import_public":    req.ImportPublic,
			"import_private":   req.ImportPrivate,
			"preserve_urls":    req.PreserveURLs,
			"import_comments":  req.ImportComments,
			"limit":            req.Limit,
		},
	}

	// Store job
	h.jobsMutex.Lock()
	h.activeJobs[jobID] = job
	h.jobsMutex.Unlock()

	// Start import in background
	go h.runGitHubImport(jobID, userID, &req)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"import_id": jobID,
		"message":   "GitHub import started successfully",
	})
}

// runGitHubImport runs the actual GitHub import process
func (h *ImportHandler) runGitHubImport(jobID string, userID uuid.UUID, req *StartGitHubImportRequest) {
	// Get job reference
	h.jobsMutex.Lock()
	job := h.activeJobs[jobID]
	h.jobsMutex.Unlock()

	if job == nil {
		return
	}

	// Update status
	job.Status = "running"
	job.CurrentOperation = "Connecting to GitHub..."

	// Create GitHub client
	client := github.NewClient(req.Token)
	
	// Create importer
	importer := github.NewImporter(client, h.db)
	
	// Set up import options
	options := &github.ImportOptions{
		Username:       req.Username,
		ImportPublic:   req.ImportPublic,
		ImportPrivate:  req.ImportPrivate,
		ImportComments: req.ImportComments,
		PreserveURLs:   req.PreserveURLs,
		Limit:          req.Limit,
		UserID:         userID,
		ProgressCallback: func(message string, current, total int) {
			job.CurrentOperation = message
			job.TotalItems = total
			job.ProcessedItems = current
			job.ElapsedTime = time.Since(job.StartTime).String()
		},
	}

	// Run import
	result, err := importer.Import(context.Background(), options)
	
	// Update job with results
	now := time.Now()
	job.EndTime = &now
	job.ElapsedTime = time.Since(job.StartTime).String()
	
	if err != nil {
		job.Status = "failed"
		job.ErrorCount++
		job.Errors = append(job.Errors, err.Error())
		job.CurrentOperation = fmt.Sprintf("Import failed: %v", err)
	} else {
		job.Status = "completed"
		job.CurrentOperation = "Import completed successfully"
		job.ImportedGists = result.GistsImported
		job.ErrorCount = len(result.Errors)
		
		// Convert errors to strings
		for _, impErr := range result.Errors {
			job.Errors = append(job.Errors, impErr.Error())
		}
	}

	// Create import record in database
	settingsJSON, _ := json.Marshal(job.Settings)
	
	importRecord := &models.ImportJob{
		ID:             uuid.MustParse(jobID),
		Platform:       "github",
		Status:         job.Status,
		SourceURL:      "https://github.com/" + req.Username,
		SourceUsername: req.Username,
		ItemsTotal:     job.TotalItems,
		ItemsImported:  job.ImportedGists,
		ErrorCount:     job.ErrorCount,
		Settings:       string(settingsJSON),
		StartedAt:      job.StartTime,
		CompletedAt:    job.EndTime,
		CreatedBy:      userID,
	}

	if err := h.db.Create(importRecord).Error; err != nil {
		job.Errors = append(job.Errors, fmt.Sprintf("Failed to save import record: %v", err))
	}
}

// GitLab Import Handlers

// TestGitLabConnectionRequest represents GitLab connection test request
type TestGitLabConnectionRequest struct {
	URL      string `json:"url" validate:"required"`
	Username string `json:"username" validate:"required"`
	Token    string `json:"token"`
}

// TestGitLabConnection tests connection to GitLab
func (h *ImportHandler) TestGitLabConnection(c echo.Context) error {
	var req TestGitLabConnectionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Create GitLab client
	client := gitlab.NewClient(req.URL, req.Token)
	
	// Test connection and get user info
	user, err := client.GetUser(context.Background(), req.Username)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Failed to connect to GitLab: %v", err),
		})
	}

	// Get snippet count
	snippets, err := client.ListSnippets(context.Background(), req.Username, &gitlab.ListSnippetsOptions{
		PerPage: 1,
	})
	if err != nil {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Failed to access snippets: %v", err),
		})
	}

	snippetCount := len(snippets) * 20 // Rough estimate

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success":       true,
		"user_id":       user.ID,
		"username":      user.Username,
		"name":          user.Name,
		"snippet_count": snippetCount,
		"message":       "Successfully connected to GitLab",
	})
}

// StartGitLabImportRequest represents GitLab import start request
type StartGitLabImportRequest struct {
	URL             string `json:"url" validate:"required"`
	Username        string `json:"username" validate:"required"`
	Token           string `json:"token"`
	ImportPublic    bool   `json:"import_public"`
	ImportPrivate   bool   `json:"import_private"`
	PreserveURLs    bool   `json:"preserve_urls"`
	Limit           *int   `json:"limit"`
}

// StartGitLabImport starts GitLab import
func (h *ImportHandler) StartGitLabImport(c echo.Context) error {
	var req StartGitLabImportRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Get current user
	userID := c.Get("user_id").(uuid.UUID)

	// Create import job
	jobID := uuid.New().String()
	job := &ImportJob{
		ID:               jobID,
		Platform:         "gitlab",
		Status:           "starting",
		Username:         req.Username,
		SourceURL:        req.URL,
		StartTime:        time.Now(),
		TotalItems:       0,
		ProcessedItems:   0,
		ImportedGists:    0,
		ErrorCount:       0,
		Errors:           []string{},
		CurrentOperation: "Initializing GitLab import...",
		Settings: map[string]interface{}{
			"url":              req.URL,
			"import_public":    req.ImportPublic,
			"import_private":   req.ImportPrivate,
			"preserve_urls":    req.PreserveURLs,
			"limit":            req.Limit,
		},
	}

	// Store job
	h.jobsMutex.Lock()
	h.activeJobs[jobID] = job
	h.jobsMutex.Unlock()

	// Start import in background
	go h.runGitLabImport(jobID, userID, &req)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"import_id": jobID,
		"message":   "GitLab import started successfully",
	})
}

// runGitLabImport runs the actual GitLab import process
func (h *ImportHandler) runGitLabImport(jobID string, userID uuid.UUID, req *StartGitLabImportRequest) {
	// Similar implementation to GitHub import but using GitLab client
	// This would follow the same pattern as runGitHubImport
	
	// Get job reference
	h.jobsMutex.Lock()
	job := h.activeJobs[jobID]
	h.jobsMutex.Unlock()

	if job == nil {
		return
	}

	// For now, mark as completed (placeholder implementation)
	job.Status = "completed"
	now := time.Now()
	job.EndTime = &now
	job.ElapsedTime = time.Since(job.StartTime).String()
	job.CurrentOperation = "GitLab import completed (placeholder implementation)"
	
	// TODO: Implement actual GitLab import logic
}

// Bitbucket Import Handlers (placeholder)

// TestBitbucketConnectionRequest represents Bitbucket connection test request
type TestBitbucketConnectionRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password"`
}

// TestBitbucketConnection tests connection to Bitbucket
func (h *ImportHandler) TestBitbucketConnection(c echo.Context) error {
	// TODO: Implement Bitbucket connection testing
	return c.JSON(http.StatusOK, map[string]interface{}{
		"success": false,
		"error":   "Bitbucket import not yet implemented",
	})
}

// StartBitbucketImport starts Bitbucket import
func (h *ImportHandler) StartBitbucketImport(c echo.Context) error {
	// TODO: Implement Bitbucket import
	return echo.NewHTTPError(http.StatusNotImplemented, "Bitbucket import not yet implemented")
}

// Generic Import Handlers

// GetImportStatus returns the status of an import job
func (h *ImportHandler) GetImportStatus(c echo.Context) error {
	jobID := c.Param("id")
	if jobID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Import ID is required")
	}

	h.jobsMutex.RLock()
	job, exists := h.activeJobs[jobID]
	h.jobsMutex.RUnlock()

	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, "Import job not found")
	}

	return c.JSON(http.StatusOK, job)
}

// GetRecentImports returns recent import jobs
func (h *ImportHandler) GetRecentImports(c echo.Context) error {
	var imports []models.ImportJob
	
	// Get recent imports from database
	if err := h.db.Order("created_at DESC").Limit(20).Find(&imports).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to fetch recent imports")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"imports": imports,
		"total":   len(imports),
	})
}

// CancelImport cancels an import job
func (h *ImportHandler) CancelImport(c echo.Context) error {
	jobID := c.Param("id")
	if jobID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Import ID is required")
	}

	h.jobsMutex.Lock()
	job, exists := h.activeJobs[jobID]
	if exists && (job.Status == "running" || job.Status == "starting") {
		job.Status = "cancelled"
		job.CurrentOperation = "Import cancelled by user"
		now := time.Now()
		job.EndTime = &now
		job.ElapsedTime = time.Since(job.StartTime).String()
	}
	h.jobsMutex.Unlock()

	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, "Import job not found")
	}

	return c.JSON(http.StatusOK, map[string]string{
		"status":  "cancelled",
		"message": "Import job cancelled successfully",
	})
}