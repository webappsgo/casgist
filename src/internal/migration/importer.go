package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/casapps/casgist/src/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ImportSource represents the source platform for migration
type ImportSource string

const (
	SourceGitHub   ImportSource = "github"
	SourceGitLab   ImportSource = "gitlab"
	SourceOpenGist ImportSource = "opengist"
)

// ImportOptions contains options for the import process
type ImportOptions struct {
	Source       ImportSource
	AccessToken  string
	BaseURL      string // For self-hosted instances
	UserID       uuid.UUID
	IncludeStars bool
	IncludeForks bool
	Limit        int
	DryRun       bool
}

// ImportResult contains the results of an import operation
type ImportResult struct {
	TotalGists     int
	ImportedGists  int
	FailedGists    int
	Errors         []ImportError
	ImportedIDs    []uuid.UUID
}

// ImportError represents an error during import
type ImportError struct {
	GistID      string
	Title       string
	Error       string
	Timestamp   time.Time
}

// Importer handles importing gists from various sources
type Importer struct {
	db      *gorm.DB
	client  *http.Client
	options ImportOptions
}

// NewImporter creates a new importer instance
func NewImporter(db *gorm.DB, options ImportOptions) *Importer {
	return &Importer{
		db:      db,
		client:  &http.Client{Timeout: 30 * time.Second},
		options: options,
	}
}

// Import performs the import operation
func (i *Importer) Import(ctx context.Context) (*ImportResult, error) {
	switch i.options.Source {
	case SourceGitHub:
		return i.importFromGitHub(ctx)
	case SourceGitLab:
		return i.importFromGitLab(ctx)
	case SourceOpenGist:
		return i.importFromOpenGist(ctx)
	default:
		return nil, fmt.Errorf("unsupported import source: %s", i.options.Source)
	}
}

// GitHub Import

type GitHubGist struct {
	ID          string                 `json:"id"`
	Description string                 `json:"description"`
	Public      bool                   `json:"public"`
	Files       map[string]GitHubFile  `json:"files"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Owner       GitHubUser             `json:"owner"`
	HTMLURL     string                 `json:"html_url"`
}

type GitHubFile struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Language string `json:"language"`
	RawURL   string `json:"raw_url"`
	Size     int    `json:"size"`
	Content  string `json:"content"`
}

type GitHubUser struct {
	Login     string `json:"login"`
	ID        int    `json:"id"`
	AvatarURL string `json:"avatar_url"`
}

func (i *Importer) importFromGitHub(ctx context.Context) (*ImportResult, error) {
	result := &ImportResult{
		Errors:      []ImportError{},
		ImportedIDs: []uuid.UUID{},
	}

	// Determine API endpoint
	apiURL := "https://api.github.com/gists"
	if i.options.BaseURL != "" {
		apiURL = strings.TrimRight(i.options.BaseURL, "/") + "/api/v3/gists"
	}

	page := 1
	perPage := 100
	if i.options.Limit > 0 && i.options.Limit < perPage {
		perPage = i.options.Limit
	}

	for {
		// Build request URL
		url := fmt.Sprintf("%s?page=%d&per_page=%d", apiURL, page, perPage)
		
		// Create request
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return result, fmt.Errorf("failed to create request: %w", err)
		}

		// Add authentication
		if i.options.AccessToken != "" {
			req.Header.Set("Authorization", "token "+i.options.AccessToken)
		}
		req.Header.Set("Accept", "application/vnd.github.v3+json")

		// Execute request
		resp, err := i.client.Do(req)
		if err != nil {
			return result, fmt.Errorf("failed to fetch gists: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return result, fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
		}

		// Parse response
		var gists []GitHubGist
		if err := json.NewDecoder(resp.Body).Decode(&gists); err != nil {
			return result, fmt.Errorf("failed to decode response: %w", err)
		}

		// Import each gist
		for _, ghGist := range gists {
			result.TotalGists++

			if i.options.DryRun {
				continue
			}

			// Fetch full gist details
			fullGist, err := i.fetchGitHubGist(ctx, ghGist.ID)
			if err != nil {
				result.FailedGists++
				result.Errors = append(result.Errors, ImportError{
					GistID:    ghGist.ID,
					Title:     ghGist.Description,
					Error:     err.Error(),
					Timestamp: time.Now(),
				})
				continue
			}

			// Import the gist
			gistID, err := i.importGitHubGist(fullGist)
			if err != nil {
				result.FailedGists++
				result.Errors = append(result.Errors, ImportError{
					GistID:    ghGist.ID,
					Title:     ghGist.Description,
					Error:     err.Error(),
					Timestamp: time.Now(),
				})
				continue
			}

			result.ImportedGists++
			result.ImportedIDs = append(result.ImportedIDs, gistID)

			// Check limit
			if i.options.Limit > 0 && result.TotalGists >= i.options.Limit {
				return result, nil
			}
		}

		// Check if there are more pages
		if len(gists) < perPage {
			break
		}

		page++
	}

	return result, nil
}

func (i *Importer) fetchGitHubGist(ctx context.Context, gistID string) (*GitHubGist, error) {
	// Determine API endpoint
	apiURL := fmt.Sprintf("https://api.github.com/gists/%s", gistID)
	if i.options.BaseURL != "" {
		apiURL = fmt.Sprintf("%s/api/v3/gists/%s", strings.TrimRight(i.options.BaseURL, "/"), gistID)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	// Add authentication
	if i.options.AccessToken != "" {
		req.Header.Set("Authorization", "token "+i.options.AccessToken)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	// Execute request
	resp, err := i.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
	}

	// Parse response
	var gist GitHubGist
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return nil, err
	}

	return &gist, nil
}

func (i *Importer) importGitHubGist(ghGist *GitHubGist) (uuid.UUID, error) {
	// Create gist model
	gist := &models.Gist{
		ID:          uuid.New(),
		UserID:      i.options.UserID,
		Title:       ghGist.Description,
		Description: ghGist.Description,
		IsPublic:    ghGist.Public,
		CreatedAt:   ghGist.CreatedAt,
		UpdatedAt:   ghGist.UpdatedAt,
	}

	if gist.Title == "" {
		// Use first filename as title if no description
		for filename := range ghGist.Files {
			gist.Title = filename
			break
		}
	}

	// Start transaction
	tx := i.db.Begin()
	defer tx.Rollback()

	// Create gist
	if err := tx.Create(gist).Error; err != nil {
		return uuid.Nil, fmt.Errorf("failed to create gist: %w", err)
	}

	// Create files
	for filename, ghFile := range ghGist.Files {
		// Fetch file content if not included
		content := ghFile.Content
		if content == "" && ghFile.RawURL != "" {
			fileContent, err := i.fetchFileContent(ghFile.RawURL)
			if err != nil {
				return uuid.Nil, fmt.Errorf("failed to fetch file content: %w", err)
			}
			content = fileContent
		}

		file := &models.GistFile{
			ID:       uuid.New(),
			GistID:   gist.ID,
			Filename: filename,
			Content:  content,
			Language: ghFile.Language,
			Size:     int64(len(content)),
		}

		if err := tx.Create(file).Error; err != nil {
			return uuid.Nil, fmt.Errorf("failed to create file: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return uuid.Nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return gist.ID, nil
}

func (i *Importer) fetchFileContent(url string) (string, error) {
	resp, err := i.client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// GitLab Import

type GitLabSnippet struct {
	ID          int                    `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Visibility  string                 `json:"visibility"`
	Files       map[string]GitLabFile  `json:"files"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Author      GitLabUser             `json:"author"`
	WebURL      string                 `json:"web_url"`
}

type GitLabFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type GitLabUser struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	ID        int    `json:"id"`
	AvatarURL string `json:"avatar_url"`
}

func (i *Importer) importFromGitLab(ctx context.Context) (*ImportResult, error) {
	result := &ImportResult{
		Errors:      []ImportError{},
		ImportedIDs: []uuid.UUID{},
	}

	// Determine API endpoint
	apiURL := "https://gitlab.com/api/v4/snippets"
	if i.options.BaseURL != "" {
		apiURL = strings.TrimRight(i.options.BaseURL, "/") + "/api/v4/snippets"
	}

	page := 1
	perPage := 100
	if i.options.Limit > 0 && i.options.Limit < perPage {
		perPage = i.options.Limit
	}

	for {
		// Build request URL
		url := fmt.Sprintf("%s?page=%d&per_page=%d", apiURL, page, perPage)
		
		// Create request
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return result, fmt.Errorf("failed to create request: %w", err)
		}

		// Add authentication
		if i.options.AccessToken != "" {
			req.Header.Set("PRIVATE-TOKEN", i.options.AccessToken)
		}

		// Execute request
		resp, err := i.client.Do(req)
		if err != nil {
			return result, fmt.Errorf("failed to fetch snippets: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return result, fmt.Errorf("GitLab API error: %s - %s", resp.Status, string(body))
		}

		// Parse response
		var snippets []GitLabSnippet
		if err := json.NewDecoder(resp.Body).Decode(&snippets); err != nil {
			return result, fmt.Errorf("failed to decode response: %w", err)
		}

		// Import each snippet
		for _, snippet := range snippets {
			result.TotalGists++

			if i.options.DryRun {
				continue
			}

			// Import the snippet
			gistID, err := i.importGitLabSnippet(&snippet)
			if err != nil {
				result.FailedGists++
				result.Errors = append(result.Errors, ImportError{
					GistID:    fmt.Sprintf("%d", snippet.ID),
					Title:     snippet.Title,
					Error:     err.Error(),
					Timestamp: time.Now(),
				})
				continue
			}

			result.ImportedGists++
			result.ImportedIDs = append(result.ImportedIDs, gistID)

			// Check limit
			if i.options.Limit > 0 && result.TotalGists >= i.options.Limit {
				return result, nil
			}
		}

		// Check if there are more pages
		if len(snippets) < perPage {
			break
		}

		page++
	}

	return result, nil
}

func (i *Importer) importGitLabSnippet(snippet *GitLabSnippet) (uuid.UUID, error) {
	// Create gist model
	gist := &models.Gist{
		ID:          uuid.New(),
		UserID:      i.options.UserID,
		Title:       snippet.Title,
		Description: snippet.Description,
		IsPublic:    snippet.Visibility == "public",
		CreatedAt:   snippet.CreatedAt,
		UpdatedAt:   snippet.UpdatedAt,
	}

	// Start transaction
	tx := i.db.Begin()
	defer tx.Rollback()

	// Create gist
	if err := tx.Create(gist).Error; err != nil {
		return uuid.Nil, fmt.Errorf("failed to create gist: %w", err)
	}

	// Create files
	for filename, file := range snippet.Files {
		gistFile := &models.GistFile{
			ID:       uuid.New(),
			GistID:   gist.ID,
			Filename: filename,
			Content:  file.Content,
			Size:     int64(len(file.Content)),
		}

		// Detect language from filename
		gistFile.Language = detectLanguageFromFilename(filename)

		if err := tx.Create(gistFile).Error; err != nil {
			return uuid.Nil, fmt.Errorf("failed to create file: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return uuid.Nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return gist.ID, nil
}

// OpenGist Import

type OpenGistData struct {
	ID          string                    `json:"id"`
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	Private     int                       `json:"private"`
	Files       []OpenGistFile            `json:"files"`
	CreatedAt   string                    `json:"created_at"`
	UpdatedAt   string                    `json:"updated_at"`
	User        OpenGistUser              `json:"user"`
}

type OpenGistFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type OpenGistUser struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

func (i *Importer) importFromOpenGist(ctx context.Context) (*ImportResult, error) {
	result := &ImportResult{
		Errors:      []ImportError{},
		ImportedIDs: []uuid.UUID{},
	}

	// For OpenGist, we expect a JSON export file
	// This would typically be provided through an upload
	// For now, we'll implement the structure

	return result, fmt.Errorf("OpenGist import requires a JSON export file - not yet implemented")
}

// Helper functions

func detectLanguageFromFilename(filename string) string {
	// Simple language detection based on file extension
	parts := strings.Split(filename, ".")
	if len(parts) < 2 {
		return ""
	}

	ext := strings.ToLower(parts[len(parts)-1])
	
	languageMap := map[string]string{
		"go":     "Go",
		"js":     "JavaScript",
		"ts":     "TypeScript",
		"py":     "Python",
		"rb":     "Ruby",
		"java":   "Java",
		"cpp":    "C++",
		"c":      "C",
		"cs":     "C#",
		"php":    "PHP",
		"swift":  "Swift",
		"kotlin": "Kotlin",
		"rs":     "Rust",
		"sh":     "Shell",
		"sql":    "SQL",
		"html":   "HTML",
		"css":    "CSS",
		"json":   "JSON",
		"xml":    "XML",
		"yaml":   "YAML",
		"yml":    "YAML",
		"md":     "Markdown",
		"txt":    "Text",
	}

	if lang, ok := languageMap[ext]; ok {
		return lang
	}

	return ""
}