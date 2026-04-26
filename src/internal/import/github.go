package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
)

// GitHubImporter handles importing gists from GitHub
type GitHubImporter struct {
	client *http.Client
	token  string
}

// NewGitHubImporter creates a new GitHub importer
func NewGitHubImporter(token string) *GitHubImporter {
	return &GitHubImporter{
		client: &http.Client{Timeout: 30 * time.Second},
		token:  token,
	}
}

// GitHubGist represents a GitHub gist from the API
type GitHubGist struct {
	ID          string                `json:"id"`
	Description string                `json:"description"`
	Public      bool                  `json:"public"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
	Files       map[string]GitHubFile `json:"files"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// GitHubFile represents a file within a GitHub gist
type GitHubFile struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Language string `json:"language"`
	RawURL   string `json:"raw_url"`
	Size     int64  `json:"size"`
	Content  string `json:"content"`
}

// ListGists lists all gists for the authenticated user
func (g *GitHubImporter) ListGists(ctx context.Context) ([]GitHubGist, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/gists", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", g.token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var gists []GitHubGist
	if err := json.NewDecoder(resp.Body).Decode(&gists); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return gists, nil
}

// GetGist gets a specific gist with full content
func (g *GitHubImporter) GetGist(ctx context.Context, gistID string) (*GitHubGist, error) {
	url := fmt.Sprintf("https://api.github.com/gists/%s", gistID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", g.token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var gist GitHubGist
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &gist, nil
}

// ConvertToCasGist converts a GitHub gist to CasGists format
func (g *GitHubImporter) ConvertToCasGist(ghGist *GitHubGist, targetUserID uuid.UUID) (*models.Gist, error) {
	// Create gist
	gist := &models.Gist{
		ID:          uuid.New(),
		Name:        ghGist.ID, // Use GitHub ID as name initially
		Title:       ghGist.Description,
		Description: ghGist.Description,
		Visibility:  models.VisibilityPrivate, // Import as private by default
		UserID:      &targetUserID,
		GitRepoPath: fmt.Sprintf("import/github/%s", ghGist.ID),
		CreatedAt:   ghGist.CreatedAt,
		UpdatedAt:   ghGist.UpdatedAt,
	}

	// Set visibility based on GitHub public setting
	if ghGist.Public {
		gist.Visibility = models.VisibilityPublic
	}

	// Convert files
	var files []models.GistFile
	for _, file := range ghGist.Files {
		gistFile := models.GistFile{
			ID:       uuid.New(),
			GistID:   gist.ID,
			Filename: file.Filename,
			Content:  file.Content,
			Size:     file.Size,
			Language: file.Language,
		}
		files = append(files, gistFile)
	}

	gist.Files = files
	return gist, nil
}

// ImportGists imports all gists from GitHub for a user
func (g *GitHubImporter) ImportGists(ctx context.Context, targetUserID uuid.UUID) ([]*models.Gist, []error) {
	// Get list of gists
	githubGists, err := g.ListGists(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to list GitHub gists: %w", err)}
	}

	var convertedGists []*models.Gist
	var errors []error

	// Convert each gist
	for _, ghGist := range githubGists {
		// Get full gist content
		fullGist, err := g.GetGist(ctx, ghGist.ID)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to get gist %s: %w", ghGist.ID, err))
			continue
		}

		// Convert to CasGists format
		casGist, err := g.ConvertToCasGist(fullGist, targetUserID)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to convert gist %s: %w", ghGist.ID, err))
			continue
		}

		convertedGists = append(convertedGists, casGist)

		// Rate limiting - be nice to GitHub API
		time.Sleep(100 * time.Millisecond)
	}

	return convertedGists, errors
}

// ValidateToken validates a GitHub API token
func (g *GitHubImporter) ValidateToken(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", g.token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid GitHub token")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	return nil
}
