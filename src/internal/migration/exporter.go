package migration

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/casapps/casgist/src/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ExportFormat represents the export format
type ExportFormat string

const (
	FormatJSON       ExportFormat = "json"
	FormatZip        ExportFormat = "zip"
	FormatGitHub     ExportFormat = "github"
	FormatGitLab     ExportFormat = "gitlab"
)

// ExportOptions contains options for the export process
type ExportOptions struct {
	Format          ExportFormat
	UserID          uuid.UUID
	GistIDs         []uuid.UUID // If empty, export all user's gists
	IncludePrivate  bool
	IncludeMetadata bool
	OutputPath      string
}

// ExportResult contains the results of an export operation
type ExportResult struct {
	TotalGists     int
	ExportedGists  int
	OutputFile     string
	Size           int64
	Duration       time.Duration
}

// Exporter handles exporting gists to various formats
type Exporter struct {
	db      *gorm.DB
	options ExportOptions
}

// NewExporter creates a new exporter instance
func NewExporter(db *gorm.DB, options ExportOptions) *Exporter {
	return &Exporter{
		db:      db,
		options: options,
	}
}

// Export performs the export operation
func (e *Exporter) Export() (*ExportResult, error) {
	startTime := time.Now()
	
	switch e.options.Format {
	case FormatJSON:
		return e.exportToJSON(startTime)
	case FormatZip:
		return e.exportToZip(startTime)
	case FormatGitHub:
		return e.exportToGitHubFormat(startTime)
	case FormatGitLab:
		return e.exportToGitLabFormat(startTime)
	default:
		return nil, fmt.Errorf("unsupported export format: %s", e.options.Format)
	}
}

// JSON Export

type JSONExport struct {
	ExportDate time.Time           `json:"export_date"`
	Version    string              `json:"version"`
	User       *JSONUser           `json:"user"`
	Gists      []JSONGist          `json:"gists"`
}

type JSONUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type JSONGist struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	IsPublic    bool           `json:"is_public"`
	Language    string         `json:"language"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Files       []JSONFile     `json:"files"`
	Metadata    *GistMetadata  `json:"metadata,omitempty"`
}

type JSONFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
	Language string `json:"language"`
	Size     int64  `json:"size"`
}

type GistMetadata struct {
	Stars    int      `json:"stars"`
	Forks    int      `json:"forks"`
	Comments int      `json:"comments"`
	Tags     []string `json:"tags"`
}

func (e *Exporter) exportToJSON(startTime time.Time) (*ExportResult, error) {
	result := &ExportResult{}

	// Fetch user
	var user models.User
	if err := e.db.First(&user, e.options.UserID).Error; err != nil {
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}

	// Build export data
	export := JSONExport{
		ExportDate: time.Now(),
		Version:    "1.0",
		User: &JSONUser{
			ID:          user.ID.String(),
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
		},
		Gists: []JSONGist{},
	}

	// Fetch gists
	gists, err := e.fetchGists()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gists: %w", err)
	}

	result.TotalGists = len(gists)

	// Convert gists to JSON format
	for _, gist := range gists {
		jsonGist := JSONGist{
			ID:          gist.ID.String(),
			Title:       gist.Title,
			Description: gist.Description,
			IsPublic:    gist.IsPublic,
			Language:    gist.Language,
			CreatedAt:   gist.CreatedAt,
			UpdatedAt:   gist.UpdatedAt,
			Files:       []JSONFile{},
		}

		// Add files
		for _, file := range gist.Files {
			jsonFile := JSONFile{
				Filename: file.Filename,
				Content:  file.Content,
				Language: file.Language,
				Size:     file.Size,
			}
			jsonGist.Files = append(jsonGist.Files, jsonFile)
		}

		// Add metadata if requested
		if e.options.IncludeMetadata {
			metadata := &GistMetadata{}
			
			// Count stars
			var starCount int64
			e.db.Model(&models.Star{}).Where("gist_id = ?", gist.ID).Count(&starCount)
			metadata.Stars = int(starCount)
			
			// Count comments
			var commentCount int64
			e.db.Model(&models.Comment{}).Where("gist_id = ?", gist.ID).Count(&commentCount)
			metadata.Comments = int(commentCount)
			
			// TODO: Add forks and tags when implemented
			
			jsonGist.Metadata = metadata
		}

		export.Gists = append(export.Gists, jsonGist)
		result.ExportedGists++
	}

	// Write to file
	outputPath := e.options.OutputPath
	if outputPath == "" {
		outputPath = fmt.Sprintf("casgists-export-%s.json", time.Now().Format("20060102-150405"))
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(export); err != nil {
		return nil, fmt.Errorf("failed to write JSON: %w", err)
	}

	// Get file size
	stat, _ := file.Stat()
	result.Size = stat.Size()
	result.OutputFile = outputPath
	result.Duration = time.Since(startTime)

	return result, nil
}

// Zip Export

func (e *Exporter) exportToZip(startTime time.Time) (*ExportResult, error) {
	result := &ExportResult{}

	// Fetch gists
	gists, err := e.fetchGists()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gists: %w", err)
	}

	result.TotalGists = len(gists)

	// Create output file
	outputPath := e.options.OutputPath
	if outputPath == "" {
		outputPath = fmt.Sprintf("casgists-export-%s.zip", time.Now().Format("20060102-150405"))
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Create zip writer
	w := zip.NewWriter(file)
	defer w.Close()

	// Add metadata file
	metaFile, err := w.Create("metadata.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create metadata file: %w", err)
	}

	metadata := map[string]interface{}{
		"export_date": time.Now(),
		"version":     "1.0",
		"gist_count":  len(gists),
	}
	json.NewEncoder(metaFile).Encode(metadata)

	// Export each gist
	for _, gist := range gists {
		// Create directory for gist
		gistDir := fmt.Sprintf("%s-%s", gist.ID.String()[:8], sanitizeFilename(gist.Title))

		// Add gist metadata
		gistMetaFile, err := w.Create(filepath.Join(gistDir, "gist.json"))
		if err != nil {
			continue
		}

		gistMeta := map[string]interface{}{
			"id":          gist.ID.String(),
			"title":       gist.Title,
			"description": gist.Description,
			"is_public":   gist.IsPublic,
			"created_at":  gist.CreatedAt,
			"updated_at":  gist.UpdatedAt,
		}
		json.NewEncoder(gistMetaFile).Encode(gistMeta)

		// Add files
		for _, file := range gist.Files {
			f, err := w.Create(filepath.Join(gistDir, file.Filename))
			if err != nil {
				continue
			}
			
			if _, err := io.WriteString(f, file.Content); err != nil {
				continue
			}
		}

		result.ExportedGists++
	}

	// Get file size
	stat, _ := file.Stat()
	result.Size = stat.Size()
	result.OutputFile = outputPath
	result.Duration = time.Since(startTime)

	return result, nil
}

// GitHub Format Export

func (e *Exporter) exportToGitHubFormat(startTime time.Time) (*ExportResult, error) {
	// Export in GitHub Gist API format
	result := &ExportResult{}

	// Fetch gists
	gists, err := e.fetchGists()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gists: %w", err)
	}

	result.TotalGists = len(gists)

	// Convert to GitHub format
	var githubGists []map[string]interface{}

	for _, gist := range gists {
		ghGist := map[string]interface{}{
			"description": gist.Description,
			"public":      gist.IsPublic,
			"created_at":  gist.CreatedAt.Format(time.RFC3339),
			"updated_at":  gist.UpdatedAt.Format(time.RFC3339),
			"files":       map[string]interface{}{},
		}

		// Add files
		files := ghGist["files"].(map[string]interface{})
		for _, file := range gist.Files {
			files[file.Filename] = map[string]interface{}{
				"content":  file.Content,
				"language": file.Language,
				"size":     file.Size,
			}
		}

		githubGists = append(githubGists, ghGist)
		result.ExportedGists++
	}

	// Write to file
	outputPath := e.options.OutputPath
	if outputPath == "" {
		outputPath = fmt.Sprintf("casgists-github-export-%s.json", time.Now().Format("20060102-150405"))
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(githubGists); err != nil {
		return nil, fmt.Errorf("failed to write JSON: %w", err)
	}

	// Get file size
	stat, _ := file.Stat()
	result.Size = stat.Size()
	result.OutputFile = outputPath
	result.Duration = time.Since(startTime)

	return result, nil
}

// GitLab Format Export

func (e *Exporter) exportToGitLabFormat(startTime time.Time) (*ExportResult, error) {
	// Export in GitLab Snippet API format
	result := &ExportResult{}

	// Fetch gists
	gists, err := e.fetchGists()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch gists: %w", err)
	}

	result.TotalGists = len(gists)

	// Convert to GitLab format
	var gitlabSnippets []map[string]interface{}

	for _, gist := range gists {
		visibility := "private"
		if gist.IsPublic {
			visibility = "public"
		}

		glSnippet := map[string]interface{}{
			"title":       gist.Title,
			"description": gist.Description,
			"visibility":  visibility,
			"created_at":  gist.CreatedAt.Format(time.RFC3339),
			"updated_at":  gist.UpdatedAt.Format(time.RFC3339),
			"files":       []map[string]interface{}{},
		}

		// Add files
		files := []map[string]interface{}{}
		for _, file := range gist.Files {
			files = append(files, map[string]interface{}{
				"path":    file.Filename,
				"content": file.Content,
			})
		}
		glSnippet["files"] = files

		gitlabSnippets = append(gitlabSnippets, glSnippet)
		result.ExportedGists++
	}

	// Write to file
	outputPath := e.options.OutputPath
	if outputPath == "" {
		outputPath = fmt.Sprintf("casgists-gitlab-export-%s.json", time.Now().Format("20060102-150405"))
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(gitlabSnippets); err != nil {
		return nil, fmt.Errorf("failed to write JSON: %w", err)
	}

	// Get file size
	stat, _ := file.Stat()
	result.Size = stat.Size()
	result.OutputFile = outputPath
	result.Duration = time.Since(startTime)

	return result, nil
}

// Helper methods

func (e *Exporter) fetchGists() ([]models.Gist, error) {
	query := e.db.Model(&models.Gist{}).
		Preload("Files").
		Where("user_id = ?", e.options.UserID).
		Where("deleted_at IS NULL")

	// Filter by specific gist IDs if provided
	if len(e.options.GistIDs) > 0 {
		query = query.Where("id IN ?", e.options.GistIDs)
	}

	// Filter private gists if not included
	if !e.options.IncludePrivate {
		query = query.Where("is_public = ?", true)
	}

	var gists []models.Gist
	if err := query.Find(&gists).Error; err != nil {
		return nil, err
	}

	return gists, nil
}

func sanitizeFilename(name string) string {
	// Remove or replace characters that are problematic in filenames
	replacements := map[rune]string{
		'/':  "-",
		'\\': "-",
		':':  "-",
		'*':  "-",
		'?':  "-",
		'"':  "'",
		'<':  "-",
		'>':  "-",
		'|':  "-",
	}

	result := ""
	for _, r := range name {
		if replacement, ok := replacements[r]; ok {
			result += replacement
		} else {
			result += string(r)
		}
	}

	// Limit length
	if len(result) > 50 {
		result = result[:50]
	}

	return result
}