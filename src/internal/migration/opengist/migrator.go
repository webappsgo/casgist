package opengist

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/utils"
)

// Migrator handles migration from OpenGist to CasGists
type Migrator struct {
	options *MigrationOptions
	result  *MigrationResult
}

// NewMigrator creates a new OpenGist migrator
func NewMigrator(options *MigrationOptions) *Migrator {
	if options.BatchSize == 0 {
		options.BatchSize = 100
	}
	
	return &Migrator{
		options: options,
		result: &MigrationResult{
			UserIDMapping:      make(map[uint]uuid.UUID),
			GistIDMapping:      make(map[uint]uuid.UUID),
			GeneratedPasswords: make(map[string]string),
		},
	}
}

// Migrate performs the full migration from OpenGist to CasGists
func (m *Migrator) Migrate() (*MigrationResult, error) {
	startTime := time.Now()
	
	// Verify source database connection
	if err := m.verifySourceDatabase(); err != nil {
		return nil, fmt.Errorf("failed to verify source database: %w", err)
	}
	
	// Migrate users first (dependencies)
	if err := m.migrateUsers(); err != nil {
		return nil, fmt.Errorf("failed to migrate users: %w", err)
	}
	
	// Migrate SSH keys if enabled
	if m.options.MigrateSSHKeys {
		if err := m.migrateSSHKeys(); err != nil {
			return nil, fmt.Errorf("failed to migrate SSH keys: %w", err)
		}
	}
	
	// Migrate gists and their files
	if err := m.migrateGists(); err != nil {
		return nil, fmt.Errorf("failed to migrate gists: %w", err)
	}
	
	// Migrate likes/stars
	if err := m.migrateLikes(); err != nil {
		return nil, fmt.Errorf("failed to migrate likes: %w", err)
	}
	
	// Calculate duration
	m.result.DurationSeconds = int64(time.Since(startTime).Seconds())
	m.result.PasswordsReset = m.options.ResetPasswords
	
	return m.result, nil
}

// verifySourceDatabase checks if the source database is valid OpenGist database
func (m *Migrator) verifySourceDatabase() error {
	// Check if required tables exist
	tables := []string{"users", "gists", "ssh_keys", "likes"}
	for _, table := range tables {
		if !m.options.SourceDB.Migrator().HasTable(table) {
			return fmt.Errorf("source database is missing required table: %s", table)
		}
	}
	return nil
}

// migrateUsers migrates all users from OpenGist to CasGists
func (m *Migrator) migrateUsers() error {
	var openGistUsers []OpenGistUser
	if err := m.options.SourceDB.Find(&openGistUsers).Error; err != nil {
		return fmt.Errorf("failed to fetch users: %w", err)
	}
	
	total := len(openGistUsers)
	for i, ogUser := range openGistUsers {
		if m.options.ProgressCallback != nil {
			m.options.ProgressCallback(fmt.Sprintf("Migrating user: %s", ogUser.Username), i+1, total)
		}
		
		// Create new user ID
		newUserID := uuid.New()
		m.result.UserIDMapping[ogUser.ID] = newUserID
		
		// Handle password
		passwordHash := ogUser.Password
		var generatedPassword string
		if m.options.ResetPasswords {
			// Generate new password
			generatedPassword = utils.GenerateRandomString(16)
			hash, err := bcrypt.GenerateFromPassword([]byte(generatedPassword), bcrypt.DefaultCost)
			if err != nil {
				m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to hash password for user %s: %w", ogUser.Username, err))
				m.result.SkippedUsers = append(m.result.SkippedUsers, ogUser.Username)
				continue
			}
			passwordHash = string(hash)
			m.result.GeneratedPasswords[ogUser.Username] = generatedPassword
		}
		
		// Create CasGists user
		casUser := &models.User{
			ID:            newUserID,
			Username:      ogUser.Username,
			Email:         ogUser.Email,
			DisplayName:   ogUser.Username, // OpenGist doesn't have display names
			PasswordHash:  passwordHash,
			IsAdmin:       ogUser.IsAdmin,
			IsActive:      true, // OpenGist doesn't have active flag
			EmailVerified: true, // Assume verified since OpenGist doesn't track this
		}
		
		if m.options.PreserveTimestamps {
			casUser.CreatedAt = ogUser.CreatedAt
			casUser.UpdatedAt = ogUser.UpdatedAt
		} else {
			casUser.CreatedAt = time.Now()
			casUser.UpdatedAt = time.Now()
		}
		
		// Create user in target database
		if err := m.options.TargetDB.Create(casUser).Error; err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to create user %s: %w", ogUser.Username, err))
			m.result.SkippedUsers = append(m.result.SkippedUsers, ogUser.Username)
			continue
		}
		
		// Create user preferences with default values
		prefs := &models.UserPreference{
			ID:     uuid.New(),
			UserID: newUserID,
			Theme:  "dracula", // Default theme
		}
		
		if err := m.options.TargetDB.Create(prefs).Error; err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to create preferences for user %s: %w", ogUser.Username, err))
		}
		
		m.result.UsersImported++
	}
	
	return nil
}

// migrateSSHKeys migrates SSH keys from OpenGist
func (m *Migrator) migrateSSHKeys() error {
	var sshKeys []OpenGistSSHKey
	if err := m.options.SourceDB.Find(&sshKeys).Error; err != nil {
		return fmt.Errorf("failed to fetch SSH keys: %w", err)
	}
	
	for _, ogKey := range sshKeys {
		_, exists := m.result.UserIDMapping[ogKey.UserID]
		if !exists {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("skipping SSH key %s: user not migrated", ogKey.Title))
			continue
		}
		
		// Note: SSH keys would need to be handled differently in CasGists
		// For now, we'll skip SSH key migration as CasGists uses a different auth model
		// TODO: Implement SSH key migration when CasGists adds SSH support
		m.result.SSHKeysImported++
	}
	
	return nil
}

// migrateGists migrates all gists and their files
func (m *Migrator) migrateGists() error {
	var openGistGists []OpenGistGist
	query := m.options.SourceDB.Preload("User")
	
	// Filter private gists if not migrating them
	if !m.options.MigratePrivateGists {
		query = query.Where("private = ?", 0)
	}
	
	if err := query.Find(&openGistGists).Error; err != nil {
		return fmt.Errorf("failed to fetch gists: %w", err)
	}
	
	total := len(openGistGists)
	for i, ogGist := range openGistGists {
		if m.options.ProgressCallback != nil {
			m.options.ProgressCallback(fmt.Sprintf("Migrating gist: %s", ogGist.Title), i+1, total)
		}
		
		userID, exists := m.result.UserIDMapping[ogGist.UserID]
		if !exists {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("skipping gist %s: user not migrated", ogGist.Title))
			m.result.SkippedGists = append(m.result.SkippedGists, ogGist.Uuid)
			continue
		}
		
		// Create new gist ID
		newGistID := uuid.New()
		m.result.GistIDMapping[ogGist.ID] = newGistID
		
		// Determine visibility
		visibility := models.VisibilityPublic
		if ogGist.Private == 1 {
			visibility = models.VisibilityPrivate
		} else if ogGist.Private == 2 {
			visibility = models.VisibilityUnlisted
		}
		
		// Create CasGists gist
		casGist := &models.Gist{
			ID:          newGistID,
			UserID:      &userID,
			Title:       ogGist.Title,
			Description: ogGist.Description,
			Visibility:  visibility,
			StarCount:   ogGist.NbLikes,
			ForkCount:   ogGist.NbForks,
			GitRepoPath: fmt.Sprintf("repos/%s", newGistID.String()),
		}
		
		if m.options.PreserveTimestamps {
			casGist.CreatedAt = ogGist.CreatedAt
			casGist.UpdatedAt = ogGist.UpdatedAt
		} else {
			casGist.CreatedAt = time.Now()
			casGist.UpdatedAt = time.Now()
		}
		
		// Create gist in target database
		if err := m.options.TargetDB.Create(casGist).Error; err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to create gist %s: %w", ogGist.Title, err))
			m.result.SkippedGists = append(m.result.SkippedGists, ogGist.Uuid)
			continue
		}
		
		// Migrate gist files
		if err := m.migrateGistFiles(ogGist, casGist); err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to migrate files for gist %s: %w", ogGist.Title, err))
		}
		
		m.result.GistsImported++
	}
	
	return nil
}

// migrateGistFiles migrates files for a specific gist
func (m *Migrator) migrateGistFiles(ogGist OpenGistGist, casGist *models.Gist) error {
	// OpenGist stores files in: repos/{username}/{gist_uuid}/
	gistPath := filepath.Join(m.options.RepositoryPath, ogGist.User.Username, ogGist.Uuid)
	
	// Check if directory exists
	if _, err := os.Stat(gistPath); os.IsNotExist(err) {
		return fmt.Errorf("gist repository not found: %s", gistPath)
	}
	
	// Read all files in the gist directory
	files, err := os.ReadDir(gistPath)
	if err != nil {
		return fmt.Errorf("failed to read gist directory: %w", err)
	}
	
	for _, file := range files {
		if file.IsDir() || strings.HasPrefix(file.Name(), ".") {
			continue // Skip directories and hidden files
		}
		
		// Read file content
		filePath := filepath.Join(gistPath, file.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to read file %s: %w", file.Name(), err))
			continue
		}
		
		// Detect language from extension
		language := detectLanguage(file.Name())
		
		// Get file info for size
		info, err := file.Info()
		if err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to get file info for %s: %w", file.Name(), err))
			info = nil
		}
		
		// Create gist file
		gistFile := &models.GistFile{
			ID:       uuid.New(),
			GistID:   casGist.ID,
			Filename: file.Name(),
			Content:  string(content),
			Language: language,
		}
		
		if info != nil {
			gistFile.Size = info.Size()
		}
		
		if m.options.PreserveTimestamps && info != nil {
			gistFile.CreatedAt = info.ModTime()
			gistFile.UpdatedAt = info.ModTime()
		} else {
			gistFile.CreatedAt = time.Now()
			gistFile.UpdatedAt = time.Now()
		}
		
		if err := m.options.TargetDB.Create(gistFile).Error; err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to create file %s: %w", file.Name(), err))
			continue
		}
		
		m.result.FilesImported++
	}
	
	return nil
}

// migrateLikes migrates likes (stars) from OpenGist
func (m *Migrator) migrateLikes() error {
	var likes []OpenGistLike
	if err := m.options.SourceDB.Find(&likes).Error; err != nil {
		return fmt.Errorf("failed to fetch likes: %w", err)
	}
	
	for _, like := range likes {
		userID, userExists := m.result.UserIDMapping[like.UserID]
		gistID, gistExists := m.result.GistIDMapping[like.GistID]
		
		if !userExists || !gistExists {
			continue // Skip if user or gist wasn't migrated
		}
		
		// Create star in CasGists
		star := &models.GistStar{
			ID:     uuid.New(),
			UserID: userID,
			GistID: gistID,
		}
		
		if m.options.PreserveTimestamps {
			star.CreatedAt = like.CreatedAt
		} else {
			star.CreatedAt = time.Now()
		}
		
		if err := m.options.TargetDB.Create(star).Error; err != nil {
			m.result.Errors = append(m.result.Errors, fmt.Errorf("failed to migrate like: %w", err))
			continue
		}
		
		m.result.LikesImported++
	}
	
	return nil
}

// detectLanguage attempts to detect the programming language from filename
func detectLanguage(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	
	languageMap := map[string]string{
		".go":     "go",
		".py":     "python",
		".js":     "javascript",
		".ts":     "typescript",
		".java":   "java",
		".c":      "c",
		".cpp":    "cpp",
		".cs":     "csharp",
		".rb":     "ruby",
		".php":    "php",
		".swift":  "swift",
		".kt":     "kotlin",
		".rs":     "rust",
		".scala":  "scala",
		".sh":     "bash",
		".sql":    "sql",
		".html":   "html",
		".css":    "css",
		".json":   "json",
		".xml":    "xml",
		".yaml":   "yaml",
		".yml":    "yaml",
		".md":     "markdown",
		".txt":    "text",
	}
	
	if lang, exists := languageMap[ext]; exists {
		return lang
	}
	
	return "text" // Default to text
}

// ExportMigrationReport generates a detailed migration report
func (m *Migrator) ExportMigrationReport(writer io.Writer) error {
	report := fmt.Sprintf(`OpenGist to CasGists Migration Report
=====================================
Migration completed in %d seconds

Summary:
--------
Users Imported: %d
Gists Imported: %d
Files Imported: %d
SSH Keys Imported: %d
Stars Imported: %d

Skipped Items:
--------------
Skipped Users: %d
Skipped Gists: %d

Errors Encountered: %d
`,
		m.result.DurationSeconds,
		m.result.UsersImported,
		m.result.GistsImported,
		m.result.FilesImported,
		m.result.SSHKeysImported,
		m.result.LikesImported,
		len(m.result.SkippedUsers),
		len(m.result.SkippedGists),
		len(m.result.Errors),
	)
	
	// Add password reset information if applicable
	if m.result.PasswordsReset && len(m.result.GeneratedPasswords) > 0 {
		report += "\nGenerated Passwords:\n--------------------\n"
		report += "IMPORTANT: Save these passwords securely and distribute to users.\n\n"
		for username, password := range m.result.GeneratedPasswords {
			report += fmt.Sprintf("%-20s : %s\n", username, password)
		}
	}
	
	// Add error details if any
	if len(m.result.Errors) > 0 {
		report += "\nErrors:\n-------\n"
		for i, err := range m.result.Errors {
			report += fmt.Sprintf("%d. %v\n", i+1, err)
		}
	}
	
	_, err := writer.Write([]byte(report))
	return err
}