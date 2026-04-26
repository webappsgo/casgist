package importer

import (
	"context"
	"fmt"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
)

// ForgejoImporter handles importing gists from Forgejo
// Since Forgejo is a Gitea fork with compatible API, we embed GiteaImporter
type ForgejoImporter struct {
	*GiteaImporter
}

// NewForgejoImporter creates a new Forgejo importer
func NewForgejoImporter(token, baseURL string) *ForgejoImporter {
	return &ForgejoImporter{
		GiteaImporter: NewGiteaImporter(token, baseURL),
	}
}

// ConvertToCasGist converts a Forgejo gist to CasGists format
func (f *ForgejoImporter) ConvertToCasGist(forgejoGist *GiteaGist, targetUserID uuid.UUID) (*models.Gist, error) {
	// Use Gitea converter but adjust tags and import info
	gist, err := f.GiteaImporter.ConvertToCasGist(forgejoGist, targetUserID)
	if err != nil {
		return nil, err
	}

	// Update for Forgejo specifics
	gist.TagsString = "forgejo,imported"
	gist.ImportID = fmt.Sprintf("forgejo:%d", forgejoGist.ID)
	gist.GitRepoPath = fmt.Sprintf("import/forgejo/%d", forgejoGist.ID)

	return gist, nil
}

// ImportGists imports all gists from Forgejo for a user
func (f *ForgejoImporter) ImportGists(ctx context.Context, targetUserID uuid.UUID) ([]*models.Gist, []error) {
	// Get gists using Gitea API compatibility
	gists, err := f.GiteaImporter.ListGists(ctx)
	if err != nil {
		return nil, []error{err}
	}

	var convertedGists []*models.Gist
	var errors []error

	// Convert each gist with Forgejo-specific conversion
	for _, gist := range gists {
		fullGist, err := f.GiteaImporter.GetGist(ctx, gist.ID)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		// Use Forgejo-specific converter
		casGist, err := f.ConvertToCasGist(fullGist, targetUserID)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		convertedGists = append(convertedGists, casGist)
	}

	return convertedGists, errors
}