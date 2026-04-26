package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/casapps/casgist/src/internal/backup"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// BackupHandler handles backup and restore endpoints
type BackupHandler struct {
	db      *gorm.DB
	config  *viper.Viper
	manager *backup.Manager
}

// NewBackupHandler creates a new backup handler
func NewBackupHandler(db *gorm.DB, config *viper.Viper) *BackupHandler {
	return &BackupHandler{
		db:      db,
		config:  config,
		manager: backup.NewManager(db, config),
	}
}

// CreateBackup creates a new backup
func (h *BackupHandler) CreateBackup(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Parse request
	var req struct {
		IncludeGitRepos    bool   `json:"include_git_repos"`
		IncludeAttachments bool   `json:"include_attachments"`
		IncludeLogs        bool   `json:"include_logs"`
		IncludeConfigs     bool   `json:"include_configs"`
		EncryptionKey      string `json:"encryption_key"`
		Description        string `json:"description"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// Create backup ID
	backupID := uuid.New().String()
	
	// Set backup path
	backupDir := h.config.GetString("backup.directory")
	if backupDir == "" {
		backupDir = filepath.Join(h.config.GetString("paths.data"), "backups")
	}
	
	// Ensure backup directory exists
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create backup directory")
	}

	filename := fmt.Sprintf("casgists-backup-%s-%s.tar.gz", 
		time.Now().Format("20060102-150405"), backupID[:8])
	outputPath := filepath.Join(backupDir, filename)

	// Create backup options
	options := backup.BackupOptions{
		IncludeGitRepos:    req.IncludeGitRepos,
		IncludeAttachments: req.IncludeAttachments,
		IncludeLogs:        req.IncludeLogs,
		IncludeConfigs:     req.IncludeConfigs,
		EncryptionKey:      req.EncryptionKey,
		OutputPath:         outputPath,
	}

	// Create backup in background
	go func() {
		ctx := c.Request().Context()
		result, err := h.manager.CreateBackup(ctx, options)
		
		// Store backup record in database
		backupRecord := map[string]interface{}{
			"id":          backupID,
			"filename":    filename,
			"path":        outputPath,
			"size":        result.Size,
			"description": req.Description,
			"created_at":  time.Now(),
			"success":     result.Success,
			"errors":      result.Errors,
		}
		
		// You could store this in a backups table
		if err != nil {
			backupRecord["error"] = err.Error()
		}
		
		// Log or store the backup record
		// h.db.Table("backups").Create(backupRecord)
	}()

	return c.JSON(http.StatusAccepted, map[string]interface{}{
		"backup_id": backupID,
		"status":    "in_progress",
		"message":   "Backup creation started",
	})
}

// RestoreBackup restores from a backup file
func (h *BackupHandler) RestoreBackup(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	// Parse request
	var req struct {
		BackupID            string `json:"backup_id"`
		BackupPath          string `json:"backup_path"`
		OverwriteExisting   bool   `json:"overwrite_existing"`
		RestoreUsers        bool   `json:"restore_users"`
		RestoreGists        bool   `json:"restore_gists"`
		RestoreOrgs         bool   `json:"restore_orgs"`
		RestoreWebhooks     bool   `json:"restore_webhooks"`
		RestoreConfig       bool   `json:"restore_config"`
		RestoreGitRepos     bool   `json:"restore_git_repos"`
		SkipValidation      bool   `json:"skip_validation"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// Determine backup path
	backupPath := req.BackupPath
	if backupPath == "" && req.BackupID != "" {
		// Look up backup by ID
		backupDir := h.config.GetString("backup.directory")
		if backupDir == "" {
			backupDir = filepath.Join(h.config.GetString("paths.data"), "backups")
		}
		
		// Find backup file
		pattern := filepath.Join(backupDir, fmt.Sprintf("*-%s.tar.gz", req.BackupID[:8]))
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			backupPath = matches[0]
		}
	}

	if backupPath == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Backup file not found")
	}

	// Check if file exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return echo.NewHTTPError(http.StatusNotFound, "Backup file not found")
	}

	// Create restore options
	options := backup.RestoreOptions{
		BackupPath:        backupPath,
		OverwriteExisting: req.OverwriteExisting,
		RestoreUsers:      req.RestoreUsers,
		RestoreGists:      req.RestoreGists,
		RestoreOrgs:       req.RestoreOrgs,
		RestoreWebhooks:   req.RestoreWebhooks,
		RestoreConfig:     req.RestoreConfig,
		RestoreGitRepos:   req.RestoreGitRepos,
		SkipValidation:    req.SkipValidation,
	}

	// Perform restore
	ctx := c.Request().Context()
	result, err := h.manager.RestoreBackup(ctx, options)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Restore failed: %v", err))
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success":           result.Success,
		"restored_users":    result.RestoredUsers,
		"restored_gists":    result.RestoredGists,
		"restored_files":    result.RestoredFiles,
		"restored_orgs":     result.RestoredOrgs,
		"restored_webhooks": result.RestoredWebhooks,
		"skipped_items":     result.SkippedItems,
		"errors":            result.Errors,
		"backup_metadata":   result.BackupMetadata,
	})
}

// ListBackups lists available backups
func (h *BackupHandler) ListBackups(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	backupDir := h.config.GetString("backup.directory")
	if backupDir == "" {
		backupDir = filepath.Join(h.config.GetString("paths.data"), "backups")
	}

	// Find all backup files
	pattern := filepath.Join(backupDir, "casgists-backup-*.tar.gz")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to list backups")
	}

	backups := []map[string]interface{}{}
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}

		// Extract backup ID from filename
		filename := filepath.Base(file)
		var backupID string
		if len(filename) > 32 {
			backupID = filename[len(filename)-44 : len(filename)-7] // Extract UUID portion
		}

		backup := map[string]interface{}{
			"id":         backupID,
			"filename":   filename,
			"path":       file,
			"size":       info.Size(),
			"created_at": info.ModTime(),
		}

		// Try to read metadata
		metadata, err := h.manager.ReadBackupMetadata(file)
		if err == nil {
			backup["metadata"] = metadata
		}

		backups = append(backups, backup)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"backups": backups,
		"total":   len(backups),
	})
}

// GetBackupInfo returns information about a specific backup
func (h *BackupHandler) GetBackupInfo(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	backupID := c.Param("id")
	
	// Find backup file
	backupDir := h.config.GetString("backup.directory")
	if backupDir == "" {
		backupDir = filepath.Join(h.config.GetString("paths.data"), "backups")
	}
	
	pattern := filepath.Join(backupDir, fmt.Sprintf("*-%s*.tar.gz", backupID))
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "Backup not found")
	}

	backupPath := matches[0]
	
	// Get file info
	info, err := os.Stat(backupPath)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get backup info")
	}

	// Read metadata
	metadata, err := h.manager.ReadBackupMetadata(backupPath)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to read backup metadata")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"id":         backupID,
		"filename":   filepath.Base(backupPath),
		"path":       backupPath,
		"size":       info.Size(),
		"created_at": info.ModTime(),
		"metadata":   metadata,
	})
}

// DeleteBackup deletes a backup file
func (h *BackupHandler) DeleteBackup(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	backupID := c.Param("id")
	
	// Find backup file
	backupDir := h.config.GetString("backup.directory")
	if backupDir == "" {
		backupDir = filepath.Join(h.config.GetString("paths.data"), "backups")
	}
	
	pattern := filepath.Join(backupDir, fmt.Sprintf("*-%s*.tar.gz", backupID))
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "Backup not found")
	}

	// Delete file
	if err := os.Remove(matches[0]); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete backup")
	}

	return c.NoContent(http.StatusNoContent)
}

// DownloadBackup streams a backup file for download
func (h *BackupHandler) DownloadBackup(c echo.Context) error {
	// Check if user is admin
	isAdmin, _ := c.Get("is_admin").(bool)
	if !isAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
	}

	backupID := c.Param("id")
	
	// Find backup file
	backupDir := h.config.GetString("backup.directory")
	if backupDir == "" {
		backupDir = filepath.Join(h.config.GetString("paths.data"), "backups")
	}
	
	pattern := filepath.Join(backupDir, fmt.Sprintf("*-%s*.tar.gz", backupID))
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "Backup not found")
	}

	backupPath := matches[0]
	filename := filepath.Base(backupPath)

	// Set headers for download
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.Response().Header().Set("Content-Type", "application/gzip")

	return c.File(backupPath)
}

// RegisterRoutes registers backup routes
func (h *BackupHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/backup", h.ListBackups)
	g.POST("/backup", h.CreateBackup)
	g.POST("/backup/restore", h.RestoreBackup)
	g.GET("/backup/:id", h.GetBackupInfo)
	g.DELETE("/backup/:id", h.DeleteBackup)
	g.GET("/backup/:id/download", h.DownloadBackup)
}