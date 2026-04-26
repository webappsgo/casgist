package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/cache"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/email"
	"github.com/casapps/casgist/src/internal/webhooks"
)

// GistService handles gist business logic
type GistService struct {
	db             *gorm.DB
	cfg            *viper.Viper
	cache          *cache.CacheManager
	webhookService *webhooks.Service
	emailService   *email.Service
}

// NewGistService creates a new gist service
func NewGistService(db *gorm.DB, cfg *viper.Viper, cacheManager *cache.CacheManager, emailService *email.Service) *GistService {
	return &GistService{
		db:             db,
		cfg:            cfg,
		cache:          cacheManager,
		webhookService: webhooks.NewService(db, cfg),
		emailService:   emailService,
	}
}

// CreateGistInput represents input for creating a gist
type CreateGistInput struct {
	Title       string
	Description string
	Visibility  models.Visibility
	Files       []CreateFileInput
	Tags        []string
	OrgID       *uuid.UUID
}

// CreateFileInput represents input for creating a file
type CreateFileInput struct {
	Filename string
	Content  string
	Language string
}

// CreateGist creates a new gist with files
func (s *GistService) CreateGist(userID uuid.UUID, input CreateGistInput) (*models.Gist, error) {
	// Validate input
	if len(input.Files) == 0 {
		return nil, errors.New("at least one file is required")
	}

	if input.Title == "" && len(input.Files) > 0 {
		input.Title = input.Files[0].Filename
	}

	// Start transaction
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Create gist
	gist := &models.Gist{
		UserID:         &userID,
		Title:          input.Title,
		Description:    input.Description,
		Visibility:     input.Visibility,
		OrganizationID: input.OrgID,
	}

	if err := tx.Create(gist).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to create gist: %w", err)
	}

	// Create files
	for _, fileInput := range input.Files {
		// Validate filename
		if err := s.ValidateFilename(fileInput.Filename); err != nil {
			tx.Rollback()
			return nil, err
		}

		// Detect language if not provided
		language := fileInput.Language
		if language == "" {
			language = s.DetectLanguage(fileInput.Filename)
		}

		file := &models.GistFile{
			GistID:   gist.ID,
			Filename: fileInput.Filename,
			Content:  fileInput.Content,
			Language: language,
			Size:     int64(len(fileInput.Content)),
			Lines:    countLines(fileInput.Content),
		}

		if err := tx.Create(file).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to create file: %w", err)
		}
	}

	// Add tags
	for _, tagName := range input.Tags {
		tagName = strings.TrimSpace(strings.ToLower(tagName))
		if tagName == "" {
			continue
		}

		// Find or create tag
		var tag models.Tag
		if err := tx.FirstOrCreate(&tag, models.Tag{Name: tagName}).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to create tag: %w", err)
		}

		// Create gist-tag association
		gistTag := &models.GistTag{
			GistID: gist.ID,
			TagID:  tag.ID,
		}
		if err := tx.Create(gistTag).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to associate tag: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Load full gist with associations
	if err := s.db.Preload("Files").Preload("Tags").First(gist, "id = ?", gist.ID).Error; err != nil {
		return nil, err
	}

	// Trigger webhook for gist creation
	if s.webhookService != nil {
		go s.webhookService.TriggerGistCreated(context.Background(), gist, userID)
	}

	return gist, nil
}

// UpdateGistInput represents input for updating a gist
type UpdateGistInput struct {
	Title       *string
	Description *string
	Visibility  *models.Visibility
	Files       []UpdateFileInput
	Tags        []string
}

// UpdateFileInput represents input for updating a file
type UpdateFileInput struct {
	ID       *uuid.UUID // nil for new files
	Filename string
	Content  string
	Language string
	Delete   bool
}

// UpdateGist updates an existing gist
func (s *GistService) UpdateGist(gistID uuid.UUID, userID uuid.UUID, input UpdateGistInput) (*models.Gist, error) {
	// Load gist
	var gist models.Gist
	if err := s.db.First(&gist, "id = ? AND user_id = ?", gistID, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("gist not found")
		}
		return nil, err
	}

	// Start transaction
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Update gist fields
	if input.Title != nil {
		gist.Title = *input.Title
	}
	if input.Description != nil {
		gist.Description = *input.Description
	}
	if input.Visibility != nil {
		gist.Visibility = *input.Visibility
	}

	if err := tx.Save(&gist).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to update gist: %w", err)
	}

	// Update files if provided
	if len(input.Files) > 0 {
		// Process file updates
		for _, fileInput := range input.Files {
			if fileInput.Delete && fileInput.ID != nil {
				// Delete file
				if err := tx.Delete(&models.GistFile{}, "id = ? AND gist_id = ?", fileInput.ID, gistID).Error; err != nil {
					tx.Rollback()
					return nil, fmt.Errorf("failed to delete file: %w", err)
				}
			} else if fileInput.ID != nil {
				// Update existing file
				var file models.GistFile
				if err := tx.First(&file, "id = ? AND gist_id = ?", fileInput.ID, gistID).Error; err != nil {
					tx.Rollback()
					return nil, fmt.Errorf("file not found: %w", err)
				}

				file.Filename = fileInput.Filename
				file.Content = fileInput.Content
				file.Language = fileInput.Language
				if file.Language == "" {
					file.Language = s.DetectLanguage(file.Filename)
				}
				file.Size = int64(len(fileInput.Content))
				file.Lines = countLines(fileInput.Content)

				if err := tx.Save(&file).Error; err != nil {
					tx.Rollback()
					return nil, fmt.Errorf("failed to update file: %w", err)
				}
			} else {
				// Create new file
				if err := s.ValidateFilename(fileInput.Filename); err != nil {
					tx.Rollback()
					return nil, err
				}

				language := fileInput.Language
				if language == "" {
					language = s.DetectLanguage(fileInput.Filename)
				}

				file := &models.GistFile{
					GistID:   gistID,
					Filename: fileInput.Filename,
					Content:  fileInput.Content,
					Language: language,
					Size:     int64(len(fileInput.Content)),
					Lines:    countLines(fileInput.Content),
				}

				if err := tx.Create(file).Error; err != nil {
					tx.Rollback()
					return nil, fmt.Errorf("failed to create file: %w", err)
				}
			}
		}
	}

	// Update tags if provided
	if input.Tags != nil {
		// Remove existing tags
		if err := tx.Delete(&models.GistTag{}, "gist_id = ?", gistID).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to remove tags: %w", err)
		}

		// Add new tags
		for _, tagName := range input.Tags {
			tagName = strings.TrimSpace(strings.ToLower(tagName))
			if tagName == "" {
				continue
			}

			var tag models.Tag
			if err := tx.FirstOrCreate(&tag, models.Tag{Name: tagName}).Error; err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("failed to create tag: %w", err)
			}

			gistTag := &models.GistTag{
				GistID: gistID,
				TagID:  tag.ID,
			}
			if err := tx.Create(gistTag).Error; err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("failed to associate tag: %w", err)
			}
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Load full gist with associations
	if err := s.db.Preload("Files").Preload("Tags").Preload("User").First(&gist, "id = ?", gistID).Error; err != nil {
		return nil, err
	}

	// Invalidate cache after update
	if s.cache != nil {
		ctx := context.Background()
		cacheKey := cache.GistKey(gistID.String())
		s.cache.Delete(ctx, cacheKey)

		// Also invalidate user gists list cache
		userCacheKey := cache.UserGistsKey(userID.String())
		s.cache.Delete(ctx, userCacheKey)
	}

	// Trigger webhook for gist update
	if s.webhookService != nil {
		go s.webhookService.TriggerGistUpdated(context.Background(), &gist, userID)
	}

	return &gist, nil
}

// GetGist retrieves a gist by ID
func (s *GistService) GetGist(gistID uuid.UUID, userID *uuid.UUID) (*models.Gist, error) {
	cacheKey := cache.GistKey(gistID.String())
	ctx := context.Background()

	// Try to get from cache first
	if s.cache != nil {
		var cachedGist models.Gist
		if err := s.cache.GetJSON(ctx, cacheKey, &cachedGist); err == nil {
			// Validate visibility for cached gist
			if userID == nil && cachedGist.Visibility != models.VisibilityPublic {
				return nil, errors.New("gist not found")
			}
			if userID != nil && cachedGist.Visibility != models.VisibilityPublic && cachedGist.UserID != userID {
				return nil, errors.New("gist not found")
			}
			// Increment view count asynchronously
			go s.incrementViewCount(gistID)
			return &cachedGist, nil
		}
	}

	var gist models.Gist
	query := s.db.Preload("Files").Preload("Tags").Preload("User")

	// Check visibility
	if userID == nil {
		// Anonymous user can only see public gists
		query = query.Where("id = ? AND visibility = ?", gistID, models.VisibilityPublic)
	} else {
		// Authenticated user can see public and their own gists
		query = query.Where("id = ? AND (visibility = ? OR user_id = ?)", gistID, models.VisibilityPublic, *userID)
	}

	if err := query.First(&gist).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("gist not found")
		}
		return nil, err
	}

	// Cache the gist for future requests (only cache public gists or if specifically requested by owner)
	if s.cache != nil && (gist.Visibility == models.VisibilityPublic || (userID != nil && gist.UserID == userID)) {
		s.cache.SetJSON(ctx, cacheKey, &gist, cache.TTLMedium)
	}

	// Increment view count
	s.incrementViewCount(gistID)

	return &gist, nil
}

// incrementViewCount increments the view count for a gist
func (s *GistService) incrementViewCount(gistID uuid.UUID) {
	s.db.Model(&models.Gist{}).Where("id = ?", gistID).UpdateColumn("view_count", gorm.Expr("view_count + ?", 1))
}

// DeleteGist soft deletes a gist
func (s *GistService) DeleteGist(gistID uuid.UUID, userID uuid.UUID) error {
	result := s.db.Delete(&models.Gist{}, "id = ? AND user_id = ?", gistID, userID)
	if result.RowsAffected == 0 {
		return errors.New("gist not found")
	}

	// Trigger webhook for gist deletion
	if s.webhookService != nil {
		go s.webhookService.TriggerGistDeleted(context.Background(), gistID, userID)
	}

	// Invalidate cache after deletion
	if s.cache != nil {
		ctx := context.Background()
		cacheKey := cache.GistKey(gistID.String())
		s.cache.Delete(ctx, cacheKey)

		// Also invalidate user gists list cache
		userCacheKey := cache.UserGistsKey(userID.String())
		s.cache.Delete(ctx, userCacheKey)
	}

	return result.Error
}

// ListGists lists gists with filtering and pagination
type ListGistsOptions struct {
	UserID       *uuid.UUID
	OrgID        *uuid.UUID
	Visibility   *models.Visibility
	Tags         []string
	SearchQuery  string
	OrderBy      string
	Page         int
	Limit        int
	IncludeFiles bool
}

// ListGists returns a paginated list of gists
func (s *GistService) ListGists(opts ListGistsOptions, viewerID *uuid.UUID) ([]models.Gist, int64, error) {
	query := s.db.Model(&models.Gist{})

	// Visibility filter
	if viewerID == nil {
		// Anonymous users can only see public gists
		query = query.Where("visibility = ?", models.VisibilityPublic)
	} else if opts.UserID != nil && *opts.UserID == *viewerID {
		// User viewing their own gists - no visibility filter
	} else {
		// Other authenticated users can see public gists
		query = query.Where("visibility = ?", models.VisibilityPublic)
	}

	// User filter
	if opts.UserID != nil {
		query = query.Where("user_id = ?", *opts.UserID)
	}

	// Organization filter
	if opts.OrgID != nil {
		query = query.Where("organization_id = ?", *opts.OrgID)
	}

	// Visibility filter (explicit)
	if opts.Visibility != nil {
		query = query.Where("visibility = ?", *opts.Visibility)
	}

	// Tag filter
	if len(opts.Tags) > 0 {
		query = query.Joins("JOIN gist_tags ON gist_tags.gist_id = gists.id").
			Joins("JOIN tags ON tags.id = gist_tags.tag_id").
			Where("tags.name IN ?", opts.Tags).
			Group("gists.id").
			Having("COUNT(DISTINCT tags.id) = ?", len(opts.Tags))
	}

	// Search filter
	if opts.SearchQuery != "" {
		searchPattern := "%" + opts.SearchQuery + "%"
		query = query.Where("title LIKE ? OR description LIKE ?", searchPattern, searchPattern)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Order
	switch opts.OrderBy {
	case "stars":
		query = query.Order("star_count DESC")
	case "forks":
		query = query.Order("fork_count DESC")
	case "updated":
		query = query.Order("updated_at DESC")
	default:
		query = query.Order("created_at DESC")
	}

	// Pagination
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	offset := (opts.Page - 1) * opts.Limit
	query = query.Limit(opts.Limit).Offset(offset)

	// Preload associations
	query = query.Preload("User").Preload("Tags")
	if opts.IncludeFiles {
		query = query.Preload("Files")
	}

	// Execute query
	var gists []models.Gist
	if err := query.Find(&gists).Error; err != nil {
		return nil, 0, err
	}

	return gists, total, nil
}

// StarGist stars/unstars a gist
func (s *GistService) StarGist(gistID uuid.UUID, userID uuid.UUID, star bool) error {
	// Check if gist exists
	var gist models.Gist
	if err := s.db.First(&gist, "id = ?", gistID).Error; err != nil {
		return errors.New("gist not found")
	}

	if star {
		if s.IsStarred(gistID, userID) {
			return errors.New("already starred")
		}
		// Add star
		star := &models.GistStar{
			GistID: gistID,
			UserID: userID,
		}
		if err := s.db.Create(star).Error; err != nil {
			return err
		}

		// Update star count
		s.db.Model(&gist).UpdateColumn("star_count", gorm.Expr("star_count + ?", 1))
	} else {
		if !s.IsStarred(gistID, userID) {
			return errors.New("not starred")
		}
		// Remove star
		s.db.Delete(&models.GistStar{}, "gist_id = ? AND user_id = ?", gistID, userID)

		// Update star count
		s.db.Model(&gist).UpdateColumn("star_count", gorm.Expr("star_count - ?", 1))
	}

	// Load full gist and user for webhook and email notifications
	if s.webhookService != nil || (s.emailService != nil && star) {
		s.db.Preload("Files").Preload("Tags").Preload("User").First(&gist, "id = ?", gistID)
		var user models.User
		if s.db.First(&user, "id = ?", userID).Error == nil {
			// Send webhook
			if s.webhookService != nil {
				go s.webhookService.TriggerGistStarred(context.Background(), &gist, &user, star)
			}

			// Send email notification for starring (not unstarring)
			if s.emailService != nil && star && gist.User != nil && gist.UserID != nil && *gist.UserID != userID {
				go s.emailService.SendGistStarredNotification(
					*gist.UserID,
					gist.User.Email,
					gist.User.DisplayName,
					user.DisplayName,
					gist.Title,
					gist.ID,
				)
			}
		}
	}

	return nil
}

// IsStarred checks if a user has starred a gist
func (s *GistService) IsStarred(gistID uuid.UUID, userID uuid.UUID) bool {
	var count int64
	s.db.Model(&models.GistStar{}).Where("gist_id = ? AND user_id = ?", gistID, userID).Count(&count)
	return count > 0
}

// ForkGist creates a fork of a gist
func (s *GistService) ForkGist(gistID uuid.UUID, userID uuid.UUID) (*models.Gist, error) {
	// Load original gist with files
	var original models.Gist
	if err := s.db.Preload("Files").Preload("Tags").First(&original, "id = ?", gistID).Error; err != nil {
		return nil, errors.New("gist not found")
	}

	// Check if already forked
	var count int64
	s.db.Model(&models.Gist{}).Where("user_id = ? AND forked_from_id = ?", userID, gistID).Count(&count)
	if count > 0 {
		return nil, errors.New("already forked this gist")
	}

	// Create fork
	fork := &models.Gist{
		UserID:       &userID,
		Title:        original.Title,
		Description:  original.Description,
		Visibility:   original.Visibility,
		ForkedFromID: &gistID,
	}

	// Start transaction
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Create(fork).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to create fork: %w", err)
	}

	// Copy files
	for _, file := range original.Files {
		forkFile := &models.GistFile{
			GistID:   fork.ID,
			Filename: file.Filename,
			Content:  file.Content,
			Language: file.Language,
			Size:     file.Size,
			Lines:    file.Lines,
		}
		if err := tx.Create(forkFile).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to copy file: %w", err)
		}
	}

	// Copy tags
	for _, tag := range original.Tags {
		gistTag := &models.GistTag{
			GistID: fork.ID,
			TagID:  tag.ID,
		}
		if err := tx.Create(gistTag).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to copy tag: %w", err)
		}
	}

	// Update fork count
	tx.Model(&original).UpdateColumn("fork_count", gorm.Expr("fork_count + ?", 1))

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Load full fork
	if err := s.db.Preload("Files").Preload("Tags").Preload("User").First(fork, "id = ?", fork.ID).Error; err != nil {
		return nil, err
	}

	return fork, nil
}

// ValidateFilename validates a filename
func (s *GistService) ValidateFilename(filename string) error {
	if filename == "" {
		return errors.New("filename cannot be empty")
	}

	if len(filename) > 255 {
		return errors.New("filename too long")
	}

	// Check for invalid characters
	invalidChars := []string{"..", "/", "\\", "\x00"}
	for _, char := range invalidChars {
		if strings.Contains(filename, char) {
			return fmt.Errorf("filename contains invalid character: %s", char)
		}
	}

	return nil
}

// DetectLanguage detects programming language from filename
func (s *GistService) DetectLanguage(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))

	languageMap := map[string]string{
		".go":         "go",
		".js":         "javascript",
		".ts":         "typescript",
		".py":         "python",
		".rb":         "ruby",
		".java":       "java",
		".c":          "c",
		".cpp":        "cpp",
		".cc":         "cpp",
		".h":          "c",
		".hpp":        "cpp",
		".cs":         "csharp",
		".php":        "php",
		".rs":         "rust",
		".swift":      "swift",
		".kt":         "kotlin",
		".scala":      "scala",
		".r":          "r",
		".m":          "objective-c",
		".pl":         "perl",
		".sh":         "bash",
		".ps1":        "powershell",
		".sql":        "sql",
		".html":       "html",
		".htm":        "html",
		".css":        "css",
		".scss":       "scss",
		".sass":       "sass",
		".less":       "less",
		".xml":        "xml",
		".json":       "json",
		".yaml":       "yaml",
		".yml":        "yaml",
		".toml":       "toml",
		".ini":        "ini",
		".cfg":        "ini",
		".conf":       "conf",
		".md":         "markdown",
		".markdown":   "markdown",
		".rst":        "restructuredtext",
		".tex":        "latex",
		".vim":        "vim",
		".lua":        "lua",
		".dart":       "dart",
		".elm":        "elm",
		".clj":        "clojure",
		".ex":         "elixir",
		".exs":        "elixir",
		".erl":        "erlang",
		".hrl":        "erlang",
		".fs":         "fsharp",
		".fsx":        "fsharp",
		".ml":         "ocaml",
		".mli":        "ocaml",
		".pas":        "pascal",
		".pp":         "pascal",
		".hs":         "haskell",
		".lhs":        "haskell",
		".jl":         "julia",
		".nim":        "nim",
		".nims":       "nim",
		".cr":         "crystal",
		".d":          "d",
		".zig":        "zig",
		".v":          "v",
		".vb":         "vbnet",
		".bas":        "vbnet",
		".f":          "fortran",
		".f90":        "fortran",
		".f95":        "fortran",
		".cob":        "cobol",
		".cbl":        "cobol",
		".asm":        "assembly",
		".s":          "assembly",
		".proto":      "protobuf",
		".gradle":     "gradle",
		".groovy":     "groovy",
		".dockerfile": "dockerfile",
		".tf":         "terraform",
		".hcl":        "hcl",
		".vue":        "vue",
		".jsx":        "javascript",
		".tsx":        "typescript",
		".svelte":     "svelte",
		".astro":      "astro",
		".prisma":     "prisma",
		".graphql":    "graphql",
		".gql":        "graphql",
	}

	// Check by extension
	if lang, ok := languageMap[ext]; ok {
		return lang
	}

	// Check by full filename
	basename := strings.ToLower(filepath.Base(filename))
	switch basename {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	case "rakefile":
		return "ruby"
	case "gemfile":
		return "ruby"
	case "guardfile":
		return "ruby"
	case "podfile":
		return "ruby"
	case "thorfile":
		return "ruby"
	case "vagrantfile":
		return "ruby"
	case "berksfile":
		return "ruby"
	case "appraisals":
		return "ruby"
	case "cakefile":
		return "coffeescript"
	case "package.json":
		return "json"
	case "composer.json":
		return "json"
	case "tsconfig.json":
		return "json"
	case ".gitignore":
		return "gitignore"
	case ".dockerignore":
		return "dockerignore"
	case ".env":
		return "dotenv"
	case "requirements.txt":
		return "text"
	case "pipfile":
		return "toml"
	case "cargo.toml":
		return "toml"
	case "go.mod":
		return "gomod"
	case "go.sum":
		return "gosum"
	}

	// Default to text
	return "text"
}

// countLines counts the number of lines in a string
func countLines(s string) int {
	if s == "" {
		return 0
	}
	lines := 1
	for _, c := range s {
		if c == '\n' {
			lines++
		}
	}
	return lines
}
