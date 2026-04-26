package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
)

// GitLabImporter handles importing snippets from GitLab
type GitLabImporter struct {
	client   *http.Client
	token    string
	baseURL  string
}

// NewGitLabImporter creates a new GitLab importer
func NewGitLabImporter(token, baseURL string) *GitLabImporter {
	if baseURL == "" {
		baseURL = "https://gitlab.com"
	}
	
	return &GitLabImporter{
		client:  &http.Client{Timeout: 30 * time.Second},
		token:   token,
		baseURL: strings.TrimSuffix(baseURL, "/"),
	}
}

// GitLabSnippet represents a GitLab snippet from the API
type GitLabSnippet struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"` // private, internal, public
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	FileName    string    `json:"file_name"`
	Content     string    `json:"content"`
	WebURL      string    `json:"web_url"`
	Author      struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"author"`
}

// ListSnippets lists all snippets for the authenticated user
func (g *GitLabImporter) ListSnippets(ctx context.Context) ([]GitLabSnippet, error) {
	url := fmt.Sprintf("%s/api/v4/snippets", g.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", g.token))
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned status %d", resp.StatusCode)
	}

	var snippets []GitLabSnippet
	if err := json.NewDecoder(resp.Body).Decode(&snippets); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return snippets, nil
}

// GetSnippet gets a specific snippet with full content
func (g *GitLabImporter) GetSnippet(ctx context.Context, snippetID int) (*GitLabSnippet, error) {
	url := fmt.Sprintf("%s/api/v4/snippets/%d", g.baseURL, snippetID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", g.token))

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned status %d", resp.StatusCode)
	}

	var snippet GitLabSnippet
	if err := json.NewDecoder(resp.Body).Decode(&snippet); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &snippet, nil
}

// ConvertToCasGist converts a GitLab snippet to CasGists format
func (g *GitLabImporter) ConvertToCasGist(snippet *GitLabSnippet, targetUserID uuid.UUID) (*models.Gist, error) {
	// Map GitLab visibility to CasGists visibility
	var visibility models.Visibility
	switch snippet.Visibility {
	case "public":
		visibility = models.VisibilityPublic
	case "internal":
		visibility = models.VisibilityUnlisted
	case "private":
		visibility = models.VisibilityPrivate
	default:
		visibility = models.VisibilityPrivate
	}

	// Create gist
	gist := &models.Gist{
		ID:          uuid.New(),
		Name:        fmt.Sprintf("gitlab-%d", snippet.ID),
		Title:       snippet.Title,
		Description: snippet.Description,
		Visibility:  visibility,
		UserID:      &targetUserID,
		GitRepoPath: fmt.Sprintf("import/gitlab/%d", snippet.ID),
		TagsString:  "gitlab,imported",
		ImportID:    fmt.Sprintf("gitlab:%d", snippet.ID),
		ImportURL:   snippet.WebURL,
		CreatedAt:   snippet.CreatedAt,
		UpdatedAt:   snippet.UpdatedAt,
	}

	// Create file from snippet content
	if snippet.FileName != "" && snippet.Content != "" {
		file := models.GistFile{
			ID:       uuid.New(),
			GistID:   gist.ID,
			Filename: snippet.FileName,
			Content:  snippet.Content,
			Size:     int64(len(snippet.Content)),
		}
		gist.Files = []models.GistFile{file}
	}

	return gist, nil
}

// ImportGists imports all snippets from GitLab for a user
func (g *GitLabImporter) ImportGists(ctx context.Context, targetUserID uuid.UUID) ([]*models.Gist, []error) {
	// Get list of snippets
	gitlabSnippets, err := g.ListSnippets(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to list GitLab snippets: %w", err)}
	}

	var convertedGists []*models.Gist
	var errors []error

	// Convert each snippet
	for _, snippet := range gitlabSnippets {
		// Get full snippet content
		fullSnippet, err := g.GetSnippet(ctx, snippet.ID)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to get snippet %d: %w", snippet.ID, err))
			continue
		}

		// Convert to CasGists format
		casGist, err := g.ConvertToCasGist(fullSnippet, targetUserID)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to convert snippet %d: %w", snippet.ID, err))
			continue
		}

		convertedGists = append(convertedGists, casGist)

		// Rate limiting - be nice to GitLab API
		time.Sleep(200 * time.Millisecond)
	}

	return convertedGists, errors
}

// ValidateToken validates a GitLab API token
func (g *GitLabImporter) ValidateToken(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v4/user", g.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", g.token))

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid GitLab token")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitLab API returned status %d", resp.StatusCode)
	}

	return nil
}