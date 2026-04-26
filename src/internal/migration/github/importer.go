package github

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/utils"
)

// Importer handles importing gists from GitHub
type Importer struct {
	client   *Client
	db       *gorm.DB
	options  *ImportOptions
	result   *ImportResult
	urlRules []URLTransformRule
	baseURL  string
}

// NewImporter creates a new GitHub importer
func NewImporter(client *Client, db *gorm.DB) *Importer {
	return &Importer{
		client:   client,
		db:       db,
		result:   &ImportResult{
			URLMappings: make(map[string]string),
			Errors:      []error{},
		},
	}
}

// ImportOptions contains options for GitHub import
type ImportOptions struct {
	Username         string
	ImportPublic     bool
	ImportPrivate    bool
	ImportComments   bool
	PreserveURLs     bool
	Limit            *int
	MaxGists         int
	RateLimitDelay   int
	UserID           uuid.UUID
	ProgressCallback func(message string, current, total int)
}

// ImportResult contains the results of an import
type ImportResult struct {
	GistsImported    int
	FilesImported    int
	CommentsImported int
	Errors           []error
	URLMappings      map[string]string
	SkippedGists     []string
}

// Import performs the GitHub gist import
func (i *Importer) Import(ctx context.Context, options *ImportOptions) (*ImportResult, error) {
	i.options = options
	
	// Get authenticated user if no username specified
	if i.options.Username == "" {
		user, err := i.client.GetAuthenticatedUser(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get authenticated user: %w", err)
		}
		i.options.Username = user.Login
	}
	
	// Get the user from the provided UserID
	var casUser models.User
	if err := i.db.First(&casUser, "id = ?", i.options.UserID).Error; err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	
	// Import user's gists
	if err := i.importUserGists(ctx, &casUser); err != nil {
		return nil, fmt.Errorf("failed to import user gists: %w", err)
	}
	return i.result, nil
}

// ensureUser ensures the GitHub user exists in CasGists
func (i *Importer) ensureUser(ghUser *GitHubUser) (*models.User, error) {
	if ghUser == nil {
		return nil, fmt.Errorf("GitHub user is nil")
	}
	
	var user models.User
	
	// Check if user already exists
	err := i.db.Where("username = ?", ghUser.Login).First(&user).Error
	if err == nil {
		return &user, nil // User already exists
	}
	
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check user existence: %w", err)
	}
	
	// Create new user
	password := utils.GenerateSecurePassword(16)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	
	user = models.User{
		ID:            uuid.New(),
		Username:      ghUser.Login,
		Email:         fmt.Sprintf("%s@github.import", ghUser.Login), // Placeholder email
		DisplayName:   ghUser.Login,
		PasswordHash:  string(hash),
		IsActive:      true,
		EmailVerified: true,
		AvatarURL:     ghUser.AvatarURL,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	
	if err := i.db.Create(&user).Error; err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}
	
	// Create user preferences
	prefs := &models.UserPreference{
		ID:     uuid.New(),
		UserID: user.ID,
		Theme:  "dracula",
	}
	
	if err := i.db.Create(prefs).Error; err != nil {
		return nil, fmt.Errorf("failed to create user preferences: %w", err)
	}
	
	return &user, nil
}

// importUserGists imports gists for a specific user
func (i *Importer) importUserGists(ctx context.Context, casUser *models.User) error {
	page := 1
	totalImported := 0
	
	for {
		// Check rate limit
		if err := i.checkRateLimit(ctx); err != nil {
			return err
		}
		
		// Fetch gists page
		gists, err := i.client.ListGists(ctx, i.options.Username, &ListGistsOptions{
			PerPage: 100,
			Page:    page,
		})
		if err != nil {
			return fmt.Errorf("failed to list gists (page %d): %w", page, err)
		}
		
		if len(gists) == 0 {
			break // No more gists
		}
		
		// Import each gist
		for _, ghGist := range gists {
			// Check max gists limit
			if i.options.MaxGists > 0 && totalImported >= i.options.MaxGists {
				return nil
			}
			
			// Skip private gists if not importing them
			if !ghGist.Public && !i.options.ImportPrivate {
				continue
			}
			
			// Get full gist details
			fullGist, err := i.client.GetGist(ctx, ghGist.ID)
			if err != nil {
				i.result.Errors = append(i.result.Errors, fmt.Errorf("failed to get gist %s: %w", ghGist.ID, err))
				i.result.SkippedGists = append(i.result.SkippedGists, ghGist.ID)
				continue
			}
			
			// Import the gist
			if err := i.importGist(ctx, fullGist, casUser); err != nil {
				i.result.Errors = append(i.result.Errors, fmt.Errorf("failed to import gist %s: %w", ghGist.ID, err))
				i.result.SkippedGists = append(i.result.SkippedGists, ghGist.ID)
				continue
			}
			
			totalImported++
			
			// Progress callback
			if i.options.ProgressCallback != nil {
				i.options.ProgressCallback(fmt.Sprintf("Imported gist: %s", ghGist.Description), totalImported, i.options.MaxGists)
			}
			
			// Rate limit delay
			time.Sleep(time.Duration(i.options.RateLimitDelay) * time.Millisecond)
		}
		
		page++
	}
	
	return nil
}

// importStarredGists imports starred gists
func (i *Importer) importStarredGists(ctx context.Context, casUser *models.User) error {
	page := 1
	
	for {
		// Check rate limit
		if err := i.checkRateLimit(ctx); err != nil {
			return err
		}
		
		// Fetch starred gists page
		gists, err := i.client.ListStarredGists(ctx, page)
		if err != nil {
			return fmt.Errorf("failed to list starred gists (page %d): %w", page, err)
		}
		
		if len(gists) == 0 {
			break // No more gists
		}
		
		// Process each starred gist
		for _, ghGist := range gists {
			// Check if gist already exists
			var existingGist models.Gist
			err := i.db.Where("import_id = ?", ghGist.ID).First(&existingGist).Error
			if err == nil {
				// Gist exists, just star it
				star := &models.GistStar{
					ID:        uuid.New(),
					UserID:    casUser.ID,
					GistID:    existingGist.ID,
					CreatedAt: time.Now(),
				}
				
				if err := i.db.Create(star).Error; err != nil {
					i.result.Errors = append(i.result.Errors, fmt.Errorf("failed to star gist %s: %w", ghGist.ID, err))
				}
				continue
			}
			
			// Import the gist if it doesn't exist
			// (This would import from other users, creating placeholder accounts)
			// Skip for now to keep it simple
		}
		
		page++
		
		// Rate limit delay
		time.Sleep(time.Duration(i.options.RateLimitDelay) * time.Millisecond)
	}
	
	return nil
}

// importGist imports a single gist
func (i *Importer) importGist(ctx context.Context, ghGist *GitHubGist, casUser *models.User) error {
	// Create CasGists gist
	gistID := uuid.New()
	visibility := models.VisibilityPublic
	if !ghGist.Public {
		visibility = models.VisibilityPrivate
	}
	
	userID := casUser.ID
	casGist := &models.Gist{
		ID:          gistID,
		UserID:      &userID,
		Title:       i.generateTitle(ghGist),
		Description: ghGist.Description,
		Visibility:  visibility,
		ImportID:    ghGist.ID, // Store GitHub ID for reference
		ImportURL:   ghGist.HTMLURL,
		GitRepoPath: fmt.Sprintf("repos/%s", gistID.String()),
		CreatedAt:   ghGist.CreatedAt,
		UpdatedAt:   ghGist.UpdatedAt,
	}
	
	// Create gist
	if err := i.db.Create(casGist).Error; err != nil {
		return fmt.Errorf("failed to create gist: %w", err)
	}
	
	// Import files
	for _, ghFile := range ghGist.Files {
		if err := i.importGistFile(ghFile, casGist); err != nil {
			i.result.Errors = append(i.result.Errors, fmt.Errorf("failed to import file %s: %w", ghFile.Filename, err))
			continue
		}
		i.result.FilesImported++
	}
	
	// Import comments if requested
	if i.options.ImportComments && ghGist.Comments > 0 {
		if err := i.importGistComments(ctx, ghGist.ID, casGist); err != nil {
			i.result.Errors = append(i.result.Errors, fmt.Errorf("failed to import comments: %w", err))
		}
	}
	
	// Store URL mapping
	newURL := fmt.Sprintf("%s/u/%s/%s", i.baseURL, casUser.Username, gistID.String())
	i.result.URLMappings[ghGist.HTMLURL] = newURL
	
	// Transform URLs in description
	if casGist.Description != "" {
		casGist.Description = i.transformURLs(casGist.Description)
		i.db.Save(casGist)
	}
	
	i.result.GistsImported++
	return nil
}

// importGistFile imports a single file from a gist
func (i *Importer) importGistFile(ghFile GitHubFile, casGist *models.Gist) error {
	// Transform URLs in content
	content := i.transformURLs(ghFile.Content)
	
	gistFile := &models.GistFile{
		ID:        uuid.New(),
		GistID:    casGist.ID,
		Filename:  ghFile.Filename,
		Content:   content,
		Language:  strings.ToLower(ghFile.Language),
		Size:      int64(ghFile.Size),
		CreatedAt: casGist.CreatedAt,
		UpdatedAt: casGist.UpdatedAt,
	}
	
	return i.db.Create(gistFile).Error
}

// importGistComments imports comments for a gist
func (i *Importer) importGistComments(ctx context.Context, ghGistID string, casGist *models.Gist) error {
	comments, err := i.client.GetGistComments(ctx, ghGistID)
	if err != nil {
		return err
	}
	
	for _, ghComment := range comments {
		// Ensure comment author exists
		commentUser, err := i.ensureUser(&ghComment.User)
		if err != nil {
			i.result.Errors = append(i.result.Errors, fmt.Errorf("failed to ensure comment user: %w", err))
			continue
		}
		
		// Transform URLs in comment body
		body := i.transformURLs(ghComment.Body)
		
		comment := &models.GistComment{
			ID:        uuid.New(),
			UserID:    commentUser.ID,
			GistID:    casGist.ID,
			Content:   body,
			CreatedAt: ghComment.CreatedAt,
			UpdatedAt: ghComment.UpdatedAt,
		}
		
		if err := i.db.Create(comment).Error; err != nil {
			i.result.Errors = append(i.result.Errors, fmt.Errorf("failed to create comment: %w", err))
			continue
		}
		
		i.result.CommentsImported++
	}
	
	return nil
}

// generateTitle generates a title for gists without descriptions
func (i *Importer) generateTitle(ghGist *GitHubGist) string {
	if ghGist.Description != "" {
		// Truncate long descriptions
		if len(ghGist.Description) > 100 {
			return ghGist.Description[:97] + "..."
		}
		return ghGist.Description
	}
	
	// Use first filename if no description
	for filename := range ghGist.Files {
		return fmt.Sprintf("Gist: %s", filename)
	}
	
	return "Untitled Gist"
}

// transformURLs transforms GitHub URLs to CasGists URLs
func (i *Importer) transformURLs(content string) string {
	transformed := content
	
	// Apply each transformation rule
	for _, rule := range i.urlRules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue
		}
		transformed = re.ReplaceAllString(transformed, rule.Replacement)
	}
	
	// Apply custom URL mappings
	for oldURL, newURL := range i.result.URLMappings {
		transformed = strings.ReplaceAll(transformed, oldURL, newURL)
	}
	
	return transformed
}

// checkRateLimit checks and handles GitHub API rate limits
func (i *Importer) checkRateLimit(ctx context.Context) error {
	remaining, resetTime, err := i.client.GetRateLimit(ctx)
	if err != nil {
		return fmt.Errorf("failed to check rate limit: %w", err)
	}
	
	// If we're running low on requests, wait until reset
	if remaining < 10 {
		waitTime := time.Until(resetTime)
		if i.options.ProgressCallback != nil {
			i.options.ProgressCallback(fmt.Sprintf("Rate limit low, waiting %v", waitTime), 0, 0)
		}
		time.Sleep(waitTime)
	}
	
	return nil
}