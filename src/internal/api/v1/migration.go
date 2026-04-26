package v1

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/migration/opengist"
)

// MigrationHandler handles migration-related API endpoints
type MigrationHandler struct {
	db           *gorm.DB
	config       *Config
	activeJobs   map[string]*MigrationJob
	jobsMutex    sync.RWMutex
}

// MigrationJob represents an active migration job
type MigrationJob struct {
	ID           string                `json:"id"`
	Type         string                `json:"type"` // "opengist", "github", etc.
	Status       string                `json:"status"`
	SourceURL    string                `json:"source_url,omitempty"`
	Username     string                `json:"username,omitempty"`
	TotalItems   int                   `json:"total_items"`
	ProcessedItems int                `json:"processed_items"`
	ErrorCount   int                   `json:"error_count"`
	Errors       []string              `json:"errors"`
	Result       *opengist.MigrationResult `json:"result,omitempty"`
	StartTime    time.Time             `json:"start_time"`
	EndTime      *time.Time            `json:"end_time,omitempty"`
	ElapsedTime  string                `json:"elapsed_time"`
	CurrentOperation string            `json:"current_operation,omitempty"`
}

// Config represents configuration needed for migrations
type Config struct {
	DataDir string
}

// NewMigrationHandler creates a new migration handler
func NewMigrationHandler(db *gorm.DB, config *Config) *MigrationHandler {
	return &MigrationHandler{
		db:         db,
		config:     config,
		activeJobs: make(map[string]*MigrationJob),
	}
}

// RegisterMigrationRoutes registers migration-related routes
func (h *MigrationHandler) RegisterMigrationRoutes(g *echo.Group) {
	g.POST("/migration/opengist/test", h.TestOpenGistConnection)
	g.POST("/migration/opengist/dry-run", h.DryRunOpenGistMigration)
	g.POST("/migration/opengist/start", h.StartOpenGistMigration)
	g.GET("/migration/opengist/status/:id", h.GetMigrationStatus)
	g.GET("/migration/jobs", h.ListMigrationJobs)
	g.DELETE("/migration/jobs/:id", h.CancelMigrationJob)
}

// TestOpenGistConnectionRequest represents the request to test OpenGist connection
type TestOpenGistConnectionRequest struct {
	DatabaseType   string `json:"database_type" validate:"required,oneof=sqlite mysql postgresql"`
	DatabaseURL    string `json:"database_url" validate:"required"`
	RepositoryPath string `json:"repository_path" validate:"required"`
	TestConnection bool   `json:"test_connection"`
}

// TestOpenGistConnectionResponse represents the response from testing OpenGist connection
type TestOpenGistConnectionResponse struct {
	Success     bool   `json:"success"`
	Version     string `json:"version,omitempty"`
	UserCount   int64  `json:"user_count"`
	GistCount   int64  `json:"gist_count"`
	Message     string `json:"message,omitempty"`
	Error       string `json:"error,omitempty"`
}

// TestOpenGistConnection tests the connection to an OpenGist database
func (h *MigrationHandler) TestOpenGistConnection(c echo.Context) error {
	var req TestOpenGistConnectionRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Check if repository path exists
	if _, err := os.Stat(req.RepositoryPath); os.IsNotExist(err) {
		return c.JSON(http.StatusOK, TestOpenGistConnectionResponse{
			Success: false,
			Error:   "Repository path does not exist",
		})
	}

	// Connect to source database
	sourceDB, err := h.connectToDatabase(req.DatabaseType, req.DatabaseURL)
	if err != nil {
		return c.JSON(http.StatusOK, TestOpenGistConnectionResponse{
			Success: false,
			Error:   fmt.Sprintf("Database connection failed: %v", err),
		})
	}

	// Close connection when done
	defer func() {
		if sqlDB, err := sourceDB.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	// Verify it's an OpenGist database by checking for required tables
	requiredTables := []string{"users", "gists", "ssh_keys", "likes"}
	for _, table := range requiredTables {
		if !sourceDB.Migrator().HasTable(table) {
			return c.JSON(http.StatusOK, TestOpenGistConnectionResponse{
				Success: false,
				Error:   fmt.Sprintf("Required table '%s' not found. This doesn't appear to be an OpenGist database.", table),
			})
		}
	}

	// Count users and gists
	var userCount, gistCount int64
	sourceDB.Model(&opengist.OpenGistUser{}).Count(&userCount)
	sourceDB.Model(&opengist.OpenGistGist{}).Count(&gistCount)

	return c.JSON(http.StatusOK, TestOpenGistConnectionResponse{
		Success:   true,
		Version:   "1.x", // OpenGist doesn't store version in DB
		UserCount: userCount,
		GistCount: gistCount,
		Message:   "Successfully connected to OpenGist database",
	})
}

// StartOpenGistMigrationRequest represents the request to start OpenGist migration
type StartOpenGistMigrationRequest struct {
	DatabaseType        string `json:"database_type" validate:"required,oneof=sqlite mysql postgresql"`
	DatabaseURL         string `json:"database_url" validate:"required"`
	RepositoryPath      string `json:"repository_path" validate:"required"`
	ResetPasswords      bool   `json:"reset_passwords"`
	PreserveTimestamps  bool   `json:"preserve_timestamps"`
	MigrateSSHKeys      bool   `json:"migrate_ssh_keys"`
	MigratePrivateGists bool   `json:"migrate_private_gists"`
	BatchSize           int    `json:"batch_size"`
}

// StartOpenGistMigrationResponse represents the response from starting migration
type StartOpenGistMigrationResponse struct {
	MigrationID string `json:"migration_id"`
	Message     string `json:"message"`
}

// StartOpenGistMigration starts the OpenGist migration process
func (h *MigrationHandler) StartOpenGistMigration(c echo.Context) error {
	var req StartOpenGistMigrationRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Connect to source database
	sourceDB, err := h.connectToDatabase(req.DatabaseType, req.DatabaseURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Database connection failed: %v", err))
	}

	// Create migration job
	jobID := uuid.New().String()
	job := &MigrationJob{
		ID:               jobID,
		Type:             "opengist",
		Status:           "starting",
		StartTime:        time.Now(),
		TotalItems:       0,
		ProcessedItems:   0,
		ErrorCount:       0,
		Errors:           []string{},
		CurrentOperation: "Initializing migration...",
	}

	// Store job
	h.jobsMutex.Lock()
	h.activeJobs[jobID] = job
	h.jobsMutex.Unlock()

	// Start migration in background
	go h.runOpenGistMigration(jobID, sourceDB, &req)

	return c.JSON(http.StatusOK, StartOpenGistMigrationResponse{
		MigrationID: jobID,
		Message:     "Migration started successfully",
	})
}

// runOpenGistMigration runs the actual migration process
func (h *MigrationHandler) runOpenGistMigration(jobID string, sourceDB *gorm.DB, req *StartOpenGistMigrationRequest) {
	// Defer cleanup
	defer func() {
		if sqlDB, err := sourceDB.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	// Get job reference
	h.jobsMutex.Lock()
	job := h.activeJobs[jobID]
	h.jobsMutex.Unlock()

	if job == nil {
		return
	}

	// Update status
	job.Status = "running"
	job.CurrentOperation = "Setting up migration options..."

	// Set up migration options
	if req.BatchSize == 0 {
		req.BatchSize = 100
	}

	options := &opengist.MigrationOptions{
		SourceDB:            sourceDB,
		TargetDB:            h.db,
		RepositoryPath:      req.RepositoryPath,
		ResetPasswords:      req.ResetPasswords,
		PreserveTimestamps:  req.PreserveTimestamps,
		MigrateSSHKeys:      req.MigrateSSHKeys,
		MigratePrivateGists: req.MigratePrivateGists,
		BatchSize:           req.BatchSize,
		ProgressCallback: func(message string, current, total int) {
			job.CurrentOperation = message
			job.TotalItems = total
			job.ProcessedItems = current
			job.ElapsedTime = time.Since(job.StartTime).String()
		},
	}

	// Create migrator and run migration
	migrator := opengist.NewMigrator(options)
	
	result, err := migrator.Migrate()
	
	// Update job with results
	now := time.Now()
	job.EndTime = &now
	job.ElapsedTime = time.Since(job.StartTime).String()
	
	if err != nil {
		job.Status = "failed"
		job.ErrorCount++
		job.Errors = append(job.Errors, err.Error())
		job.CurrentOperation = fmt.Sprintf("Migration failed: %v", err)
	} else {
		job.Status = "completed"
		job.Result = result
		job.CurrentOperation = "Migration completed successfully"
		
		// Update counters from result
		if result != nil {
			job.ProcessedItems = result.UsersImported + result.GistsImported
			job.ErrorCount = len(result.Errors)
			
			// Convert errors to strings
			for _, migErr := range result.Errors {
				job.Errors = append(job.Errors, migErr.Error())
			}
		}
	}

	// Create migration record in database
	migrationRecord := &models.Migration{
		ID:           uuid.MustParse(jobID),
		Type:         "opengist",
		Status:       job.Status,
		SourceURL:    req.DatabaseURL,
		ItemsTotal:   job.TotalItems,
		ItemsProcessed: job.ProcessedItems,
		ErrorCount:   job.ErrorCount,
		StartedAt:    job.StartTime,
		CompletedAt:  job.EndTime,
	}

	if err := h.db.Create(migrationRecord).Error; err != nil {
		// Log error but don't fail the migration
		job.Errors = append(job.Errors, fmt.Sprintf("Failed to save migration record: %v", err))
	}
}

// DryRunOpenGistMigration performs a dry run of OpenGist migration
func (h *MigrationHandler) DryRunOpenGistMigration(c echo.Context) error {
	var req StartOpenGistMigrationRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request format")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Connect to source database
	sourceDB, err := h.connectToDatabase(req.DatabaseType, req.DatabaseURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Database connection failed: %v", err))
	}
	defer func() {
		if sqlDB, err := sourceDB.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	// Count items for dry run
	var userCount, gistCount, sshKeyCount, likeCount int64
	sourceDB.Model(&opengist.OpenGistUser{}).Count(&userCount)
	sourceDB.Model(&opengist.OpenGistGist{}).Count(&gistCount)
	sourceDB.Model(&opengist.OpenGistSSHKey{}).Count(&sshKeyCount)
	sourceDB.Model(&opengist.OpenGistLike{}).Count(&likeCount)

	// Filter private gists if not migrating them
	if !req.MigratePrivateGists {
		sourceDB.Model(&opengist.OpenGistGist{}).Where("private = ?", 0).Count(&gistCount)
	}

	dryRunResult := map[string]interface{}{
		"success": true,
		"summary": map[string]interface{}{
			"users_to_migrate":     userCount,
			"gists_to_migrate":     gistCount,
			"ssh_keys_to_migrate":  sshKeyCount,
			"likes_to_migrate":     likeCount,
			"estimated_duration":   "5-30 minutes (depending on data size)",
			"repository_path":      req.RepositoryPath,
			"reset_passwords":      req.ResetPasswords,
			"preserve_timestamps":  req.PreserveTimestamps,
			"migrate_ssh_keys":     req.MigrateSSHKeys,
			"migrate_private_gists": req.MigratePrivateGists,
		},
		"warnings": []string{},
	}

	// Add warnings based on configuration
	warnings := []string{}
	if req.ResetPasswords {
		warnings = append(warnings, "All user passwords will be reset and new passwords will be generated")
	}
	if !req.MigratePrivateGists {
		warnings = append(warnings, "Private gists will be skipped")
	}
	if req.MigrateSSHKeys {
		warnings = append(warnings, "SSH keys will be migrated but may need additional configuration")
	}

	dryRunResult["warnings"] = warnings

	return c.JSON(http.StatusOK, dryRunResult)
}

// GetMigrationStatus returns the status of a migration job
func (h *MigrationHandler) GetMigrationStatus(c echo.Context) error {
	jobID := c.Param("id")
	if jobID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Migration ID is required")
	}

	h.jobsMutex.RLock()
	job, exists := h.activeJobs[jobID]
	h.jobsMutex.RUnlock()

	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, "Migration job not found")
	}

	return c.JSON(http.StatusOK, job)
}

// ListMigrationJobs returns a list of migration jobs
func (h *MigrationHandler) ListMigrationJobs(c echo.Context) error {
	h.jobsMutex.RLock()
	jobs := make([]*MigrationJob, 0, len(h.activeJobs))
	for _, job := range h.activeJobs {
		jobs = append(jobs, job)
	}
	h.jobsMutex.RUnlock()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"jobs": jobs,
		"total": len(jobs),
	})
}

// CancelMigrationJob cancels a migration job
func (h *MigrationHandler) CancelMigrationJob(c echo.Context) error {
	jobID := c.Param("id")
	if jobID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Migration ID is required")
	}

	h.jobsMutex.Lock()
	job, exists := h.activeJobs[jobID]
	if exists && (job.Status == "running" || job.Status == "starting") {
		job.Status = "cancelled"
		job.CurrentOperation = "Migration cancelled by user"
		now := time.Now()
		job.EndTime = &now
		job.ElapsedTime = time.Since(job.StartTime).String()
	}
	h.jobsMutex.Unlock()

	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, "Migration job not found")
	}

	return c.JSON(http.StatusOK, map[string]string{
		"status": "cancelled",
		"message": "Migration job cancelled successfully",
	})
}

// connectToDatabase creates a database connection based on type and URL
func (h *MigrationHandler) connectToDatabase(dbType, dbURL string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	
	switch dbType {
	case "sqlite":
		dialector = sqlite.Open(dbURL)
	case "mysql":
		dialector = mysql.Open(dbURL)
	case "postgresql":
		dialector = postgres.Open(dbURL)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	config := &gorm.Config{
		Logger: nil, // Disable logging for migration connections
	}

	db, err := gorm.Open(dialector, config)
	if err != nil {
		return nil, err
	}

	// Test the connection
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	if err := sqlDB.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}