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

// GiteaImporter handles importing gists from Gitea
type GiteaImporter struct {
	client   *http.Client
	token    string
	baseURL  string
}

// NewGiteaImporter creates a new Gitea importer
func NewGiteaImporter(token, baseURL string) *GiteaImporter {
	return &GiteaImporter{
		client:  &http.Client{Timeout: 30 * time.Second},
		token:   token,
		baseURL: strings.TrimSuffix(baseURL, "/"),
	}
}

// GiteaGist represents a Gitea gist from the API
type GiteaGist struct {
	ID          int64                  `json:"id"`
	Description string                 `json:"description"`
	Public      bool                   `json:"public"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
	Files       map[string]GiteaFile  `json:"files"`
	Owner       struct {
		Username string `json:"username"`
		FullName string `json:"full_name"`
	} `json:"owner"`
	HTMLURL string `json:"html_url"`
}

// GiteaFile represents a file within a Gitea gist
type GiteaFile struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Language string `json:"language"`
	RawURL   string `json:"raw_url"`
	Size     int64  `json:"size"`
	Content  string `json:"content"`
}

// ListGists lists all gists for the authenticated user
func (g *GiteaImporter) ListGists(ctx context.Context) ([]GiteaGist, error) {
	url := fmt.Sprintf("%s/api/v1/user/gists", g.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", g.token))
	req.Header.Set("Accept", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gitea API returned status %d", resp.StatusCode)
	}

	var gists []GiteaGist
	if err := json.NewDecoder(resp.Body).Decode(&gists); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return gists, nil
}

// GetGist gets a specific gist with full content
func (g *GiteaImporter) GetGist(ctx context.Context, gistID int64) (*GiteaGist, error) {
	url := fmt.Sprintf("%s/api/v1/gists/%d", g.baseURL, gistID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", g.token))
	req.Header.Set("Accept", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gitea API returned status %d", resp.StatusCode)
	}

	var gist GiteaGist
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &gist, nil
}

// ConvertToCasGist converts a Gitea gist to CasGists format
func (g *GiteaImporter) ConvertToCasGist(giteaGist *GiteaGist, targetUserID uuid.UUID) (*models.Gist, error) {
	// Create gist
	gist := &models.Gist{
		ID:          uuid.New(),
		Name:        fmt.Sprintf("gitea-%d", giteaGist.ID),
		Title:       giteaGist.Description,
		Description: giteaGist.Description,
		Visibility:  models.VisibilityPrivate, // Import as private by default
		UserID:      &targetUserID,
		GitRepoPath: fmt.Sprintf("import/gitea/%d", giteaGist.ID),
		TagsString:  "gitea,imported",
		ImportID:    fmt.Sprintf("gitea:%d", giteaGist.ID),
		ImportURL:   giteaGist.HTMLURL,
		CreatedAt:   giteaGist.CreatedAt,
		UpdatedAt:   giteaGist.UpdatedAt,
	}

	// Set visibility based on Gitea public setting
	if giteaGist.Public {
		gist.Visibility = models.VisibilityPublic
	}

	// Convert files
	var files []models.GistFile
	for _, file := range giteaGist.Files {
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

// ImportGists imports all gists from Gitea for a user
func (g *GiteaImporter) ImportGists(ctx context.Context, targetUserID uuid.UUID) ([]*models.Gist, []error) {
	// Get list of gists
	giteaGists, err := g.ListGists(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to list Gitea gists: %w", err)}
	}

	var convertedGists []*models.Gist
	var errors []error

	// Convert each gist
	for _, gist := range giteaGists {
		// Get full gist content
		fullGist, err := g.GetGist(ctx, gist.ID)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to get gist %d: %w", gist.ID, err))
			continue
		}

		// Convert to CasGists format
		casGist, err := g.ConvertToCasGist(fullGist, targetUserID)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to convert gist %d: %w", gist.ID, err))
			continue
		}

		convertedGists = append(convertedGists, casGist)

		// Rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return convertedGists, errors
}

// ValidateToken validates a Gitea API token
func (g *GiteaImporter) ValidateToken(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/user", g.baseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", g.token))

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid Gitea token")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Gitea API returned status %d", resp.StatusCode)
	}

	return nil
}