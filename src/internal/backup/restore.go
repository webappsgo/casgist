package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
)

// RestoreResult contains the result of a restore operation
type RestoreResult struct {
	Success             bool
	RestoredUsers       int
	RestoredGists       int
	RestoredFiles       int
	RestoredOrgs        int
	RestoredWebhooks    int
	SkippedItems        int
	Errors              []string
	BackupMetadata      *BackupMetadata
	UserMapping         map[uuid.UUID]uuid.UUID // Old ID -> New ID mapping
}

// RestoreOptions contains options for restore operations
type RestoreOptions struct {
	BackupPath          string
	OverwriteExisting   bool
	RestoreUsers        bool
	RestoreGists        bool
	RestoreOrgs         bool
	RestoreWebhooks     bool
	RestoreConfig       bool
	RestoreGitRepos     bool
	SkipValidation      bool
	UserMapping         map[uuid.UUID]uuid.UUID // Old ID -> New ID mapping
}

// RestoreBackup restores data from a backup file
func (m *Manager) RestoreBackup(ctx context.Context, options RestoreOptions) (*RestoreResult, error) {
	result := &RestoreResult{
		UserMapping: make(map[uuid.UUID]uuid.UUID),
	}

	// Open backup file
	file, err := os.Open(options.BackupPath)
	if err != nil {
		return result, fmt.Errorf("failed to open backup file: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return result, fmt.Errorf("failed to read gzip: %w", err)
	}
	defer gzipReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzipReader)

	// First pass: read metadata
	metadata, err := m.extractMetadata(tarReader)
	if err != nil {
		return result, fmt.Errorf("failed to extract metadata: %w", err)
	}
	result.BackupMetadata = metadata

	// Validate backup compatibility
	if !options.SkipValidation {
		if err := m.validateBackup(metadata); err != nil {
			return result, fmt.Errorf("backup validation failed: %w", err)
		}
	}

	// Reset tar reader for second pass
	file.Seek(0, 0)
	gzipReader, _ = gzip.NewReader(file)
	defer gzipReader.Close()
	tarReader = tar.NewReader(gzipReader)

	// Second pass: restore data
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Error reading tar: %v", err))
			continue
		}

		switch {
		case header.Name == "backup-metadata.json":
			continue // Already processed

		case header.Name == "database/users.json" && options.RestoreUsers:
			count, err := m.restoreUsers(ctx, tarReader, options.OverwriteExisting, result.UserMapping)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("User restore error: %v", err))
			} else {
				result.RestoredUsers = count
			}

		case header.Name == "database/gists.json" && options.RestoreGists:
			count, err := m.restoreGists(ctx, tarReader, options.OverwriteExisting, result.UserMapping)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Gist restore error: %v", err))
			} else {
				result.RestoredGists = count
			}

		case header.Name == "database/organizations.json" && options.RestoreOrgs:
			count, err := m.restoreOrganizations(ctx, tarReader, options.OverwriteExisting)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Organization restore error: %v", err))
			} else {
				result.RestoredOrgs = count
			}

		case header.Name == "database/webhooks.json" && options.RestoreWebhooks:
			count, err := m.restoreWebhooks(ctx, tarReader, options.OverwriteExisting, result.UserMapping)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Webhook restore error: %v", err))
			} else {
				result.RestoredWebhooks = count
			}

		case strings.HasPrefix(header.Name, "git/") && options.RestoreGitRepos:
			if err := m.restoreGitRepo(header, tarReader); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Git repo restore error: %v", err))
			} else {
				result.RestoredFiles++
			}

		case strings.HasPrefix(header.Name, "attachments/"):
			if err := m.restoreAttachment(header, tarReader); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Attachment restore error: %v", err))
			} else {
				result.RestoredFiles++
			}

		default:
			result.SkippedItems++
		}
	}

	result.Success = len(result.Errors) == 0
	return result, nil
}

// extractMetadata extracts metadata from the backup
func (m *Manager) extractMetadata(tarReader *tar.Reader) (*BackupMetadata, error) {
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("metadata not found in backup")
		}
		if err != nil {
			return nil, err
		}

		if header.Name == "backup-metadata.json" {
			var metadata BackupMetadata
			if err := json.NewDecoder(tarReader).Decode(&metadata); err != nil {
				return nil, err
			}
			return &metadata, nil
		}
	}
}

// validateBackup validates backup compatibility
func (m *Manager) validateBackup(metadata *BackupMetadata) error {
	// Check version compatibility
	if metadata.Version != "1.0" {
		return fmt.Errorf("unsupported backup version: %s", metadata.Version)
	}

	// Check database compatibility
	currentDBType := m.config.GetString("database.type")
	if metadata.DatabaseType != "" && metadata.DatabaseType != currentDBType {
		return fmt.Errorf("database type mismatch: backup=%s, current=%s", 
			metadata.DatabaseType, currentDBType)
	}

	return nil
}

// restoreUsers restores user data
func (m *Manager) restoreUsers(ctx context.Context, reader io.Reader, overwrite bool, mapping map[uuid.UUID]uuid.UUID) (int, error) {
	var users []models.User
	if err := json.NewDecoder(reader).Decode(&users); err != nil {
		return 0, err
	}

	count := 0
	for _, user := range users {
		oldID := user.ID
		
		// Check if user exists
		var existing models.User
		err := m.db.Where("email = ?", user.Email).First(&existing).Error
		
		if err == nil {
			// User exists
			if overwrite {
				user.ID = existing.ID
				if err := m.db.Save(&user).Error; err != nil {
					return count, err
				}
			}
			mapping[oldID] = existing.ID
		} else {
			// Create new user
			user.ID = uuid.New()
			mapping[oldID] = user.ID
			if err := m.db.Create(&user).Error; err != nil {
				return count, err
			}
		}
		count++
	}

	return count, nil
}

// restoreGists restores gist data
func (m *Manager) restoreGists(ctx context.Context, reader io.Reader, overwrite bool, userMapping map[uuid.UUID]uuid.UUID) (int, error) {
	var gists []models.Gist
	if err := json.NewDecoder(reader).Decode(&gists); err != nil {
		return 0, err
	}

	count := 0
	for _, gist := range gists {
		// Map user ID
		if gist.UserID != nil {
			if newUserID, ok := userMapping[*gist.UserID]; ok {
				gist.UserID = &newUserID
			}
		}

		// Generate new ID
		gist.ID = uuid.New()

		// Update file IDs
		for i := range gist.Files {
			gist.Files[i].ID = uuid.New()
			gist.Files[i].GistID = gist.ID
		}

		if err := m.db.Create(&gist).Error; err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

// restoreOrganizations restores organization data
func (m *Manager) restoreOrganizations(ctx context.Context, reader io.Reader, overwrite bool) (int, error) {
	var orgs []models.Organization
	if err := json.NewDecoder(reader).Decode(&orgs); err != nil {
		return 0, err
	}

	count := 0
	for _, org := range orgs {
		// Check if org exists
		var existing models.Organization
		err := m.db.Where("name = ?", org.Name).First(&existing).Error
		
		if err == nil && !overwrite {
			continue // Skip existing
		}

		if err == nil {
			org.ID = existing.ID
			if err := m.db.Save(&org).Error; err != nil {
				return count, err
			}
		} else {
			org.ID = uuid.New()
			if err := m.db.Create(&org).Error; err != nil {
				return count, err
			}
		}
		count++
	}

	return count, nil
}

// restoreWebhooks restores webhook data
func (m *Manager) restoreWebhooks(ctx context.Context, reader io.Reader, overwrite bool, userMapping map[uuid.UUID]uuid.UUID) (int, error) {
	var webhooks []models.Webhook
	if err := json.NewDecoder(reader).Decode(&webhooks); err != nil {
		return 0, err
	}

	count := 0
	for _, webhook := range webhooks {
		// Map user ID
		if webhook.UserID != nil {
			if newUserID, ok := userMapping[*webhook.UserID]; ok {
				webhook.UserID = &newUserID
			}
		}

		// Generate new ID
		webhook.ID = uuid.New()

		if err := m.db.Create(&webhook).Error; err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

// restoreGitRepo restores a git repository
func (m *Manager) restoreGitRepo(header *tar.Header, reader io.Reader) error {
	// Remove "git/" prefix
	relPath := strings.TrimPrefix(header.Name, "git/")
	fullPath := filepath.Join(m.gitDir, relPath)

	// Create directory if needed
	if header.Typeflag == tar.TypeDir {
		return os.MkdirAll(fullPath, os.FileMode(header.Mode))
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}

	// Create file
	file, err := os.Create(fullPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Copy content
	_, err = io.Copy(file, reader)
	return err
}

// restoreAttachment restores an uploaded attachment
func (m *Manager) restoreAttachment(header *tar.Header, reader io.Reader) error {
	// Remove "attachments/" prefix
	relPath := strings.TrimPrefix(header.Name, "attachments/")
	fullPath := filepath.Join(m.uploadDir, relPath)

	// Create directory if needed
	if header.Typeflag == tar.TypeDir {
		return os.MkdirAll(fullPath, os.FileMode(header.Mode))
	}

	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}

	// Create file
	file, err := os.Create(fullPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Copy content
	_, err = io.Copy(file, reader)
	return err
}