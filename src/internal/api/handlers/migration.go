package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/casapps/casgist/src/internal/migration"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// MigrationHandler handles import/export endpoints
type MigrationHandler struct {
	db     *gorm.DB
	config *viper.Viper
}

// NewMigrationHandler creates a new migration handler
func NewMigrationHandler(db *gorm.DB, config *viper.Viper) *MigrationHandler {
	return &MigrationHandler{
		db:     db,
		config: config,
	}
}

// Import handles gist import from various sources
func (h *MigrationHandler) Import(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request
	var req struct {
		Source       string `json:"source" validate:"required,oneof=github gitlab opengist"`
		AccessToken  string `json:"access_token"`
		BaseURL      string `json:"base_url"` // For self-hosted instances
		IncludeStars bool   `json:"include_stars"`
		IncludeForks bool   `json:"include_forks"`
		Limit        int    `json:"limit"`
		DryRun       bool   `json:"dry_run"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Create import options
	options := migration.ImportOptions{
		Source:       migration.ImportSource(req.Source),
		AccessToken:  req.AccessToken,
		BaseURL:      req.BaseURL,
		UserID:       userID,
		IncludeStars: req.IncludeStars,
		IncludeForks: req.IncludeForks,
		Limit:        req.Limit,
		DryRun:       req.DryRun,
	}

	// Create importer
	importer := migration.NewImporter(h.db, options)

	// Perform import
	ctx := context.Background()
	result, err := importer.Import(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Import failed: %v", err))
	}

	// Return result
	response := map[string]interface{}{
		"total_gists":    result.TotalGists,
		"imported_gists": result.ImportedGists,
		"failed_gists":   result.FailedGists,
		"errors":         result.Errors,
		"imported_ids":   result.ImportedIDs,
	}

	return c.JSON(http.StatusOK, response)
}

// Export handles gist export to various formats
func (h *MigrationHandler) Export(c echo.Context) error {
	// Get current user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Authentication required")
	}

	// Parse request
	var req struct {
		Format          string   `json:"format" validate:"required,oneof=json zip github gitlab"`
		GistIDs         []string `json:"gist_ids"` // Optional, export specific gists
		IncludePrivate  bool     `json:"include_private"`
		IncludeMetadata bool     `json:"include_metadata"`
	}

	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Convert gist IDs to UUIDs
	var gistIDs []uuid.UUID
	for _, idStr := range req.GistIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Invalid gist ID: %s", idStr))
		}
		gistIDs = append(gistIDs, id)
	}

	// Create export options
	options := migration.ExportOptions{
		Format:          migration.ExportFormat(req.Format),
		UserID:          userID,
		GistIDs:         gistIDs,
		IncludePrivate:  req.IncludePrivate,
		IncludeMetadata: req.IncludeMetadata,
		OutputPath:      fmt.Sprintf("/tmp/export-%s-%s", userID.String()[:8], req.Format),
	}

	// Create exporter
	exporter := migration.NewExporter(h.db, options)

	// Perform export
	result, err := exporter.Export()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Export failed: %v", err))
	}

	// For now, return the file path
	// In production, you'd want to stream the file or provide a download URL
	response := map[string]interface{}{
		"total_gists":    result.TotalGists,
		"exported_gists": result.ExportedGists,
		"output_file":    result.OutputFile,
		"size":           result.Size,
		"duration_ms":    result.Duration.Milliseconds(),
		"download_url":   fmt.Sprintf("/api/v1/migration/download/%s", uuid.New().String()),
	}

	return c.JSON(http.StatusOK, response)
}

// GetImportStatus returns the status of an import job
func (h *MigrationHandler) GetImportStatus(c echo.Context) error {
	// Get job ID from URL
	jobID := c.Param("id")

	// TODO: Implement job tracking for async imports
	// For now, return a placeholder response
	return c.JSON(http.StatusOK, map[string]interface{}{
		"job_id": jobID,
		"status": "completed",
		"message": "Import job tracking not yet implemented",
	})
}

// GetExportStatus returns the status of an export job
func (h *MigrationHandler) GetExportStatus(c echo.Context) error {
	// Get job ID from URL
	jobID := c.Param("id")

	// TODO: Implement job tracking for async exports
	// For now, return a placeholder response
	return c.JSON(http.StatusOK, map[string]interface{}{
		"job_id": jobID,
		"status": "completed",
		"message": "Export job tracking not yet implemented",
	})
}

// DownloadExport downloads an exported file
func (h *MigrationHandler) DownloadExport(c echo.Context) error {
	// Get download ID from URL
	_ = c.Param("id") // downloadID

	// TODO: Implement secure download mechanism
	// For now, return a placeholder response
	return echo.NewHTTPError(http.StatusNotImplemented, "Export download not yet implemented")
}

// GetImportFormats returns supported import formats
func (h *MigrationHandler) GetImportFormats(c echo.Context) error {
	formats := []map[string]interface{}{
		{
			"id":          "github",
			"name":        "GitHub Gists",
			"description": "Import gists from GitHub.com or GitHub Enterprise",
			"requires_auth": true,
			"auth_type":    "token",
			"supports_self_hosted": true,
		},
		{
			"id":          "gitlab",
			"name":        "GitLab Snippets",
			"description": "Import snippets from GitLab.com or self-hosted GitLab",
			"requires_auth": true,
			"auth_type":    "token",
			"supports_self_hosted": true,
		},
		{
			"id":          "opengist",
			"name":        "OpenGist",
			"description": "Import from OpenGist JSON export",
			"requires_auth": false,
			"auth_type":    "none",
			"supports_self_hosted": false,
		},
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"formats": formats,
	})
}

// GetExportFormats returns supported export formats
func (h *MigrationHandler) GetExportFormats(c echo.Context) error {
	formats := []map[string]interface{}{
		{
			"id":          "json",
			"name":        "JSON",
			"description": "Export to CasGists JSON format",
			"file_extension": ".json",
			"supports_metadata": true,
		},
		{
			"id":          "zip",
			"name":        "ZIP Archive",
			"description": "Export gists as a ZIP file with all files",
			"file_extension": ".zip",
			"supports_metadata": false,
		},
		{
			"id":          "github",
			"name":        "GitHub Format",
			"description": "Export in GitHub Gist API format",
			"file_extension": ".json",
			"supports_metadata": false,
		},
		{
			"id":          "gitlab",
			"name":        "GitLab Format",
			"description": "Export in GitLab Snippet API format",
			"file_extension": ".json",
			"supports_metadata": false,
		},
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"formats": formats,
	})
}

// RegisterRoutes registers migration routes
func (h *MigrationHandler) RegisterRoutes(g *echo.Group) {
	g.GET("/migration/import/formats", h.GetImportFormats)
	g.GET("/migration/export/formats", h.GetExportFormats)
	g.POST("/migration/import", h.Import)
	g.POST("/migration/export", h.Export)
	g.GET("/migration/import/:id/status", h.GetImportStatus)
	g.GET("/migration/export/:id/status", h.GetExportStatus)
	g.GET("/migration/download/:id", h.DownloadExport)
}