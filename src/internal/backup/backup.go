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
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/google/uuid"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// Manager handles backup and restore operations
type Manager struct {
	db        *gorm.DB
	config    *viper.Viper
	dataDir   string
	gitDir    string
	uploadDir string
}

// NewManager creates a new backup manager
func NewManager(db *gorm.DB, config *viper.Viper) *Manager {
	return &Manager{
		db:        db,
		config:    config,
		dataDir:   config.GetString("paths.data"),
		gitDir:    filepath.Join(config.GetString("paths.data"), "git"),
		uploadDir: filepath.Join(config.GetString("paths.data"), "uploads"),
	}
}

// CreateBackup creates a backup of the CasGists instance
func (m *Manager) CreateBackup(ctx context.Context, options BackupOptions) (*BackupResult, error) {
	result := &BackupResult{
		StartTime: time.Now(),
		ID:        uuid.New().String(),
	}

	// Validate options
	if options.OutputPath == "" {
		options.OutputPath = filepath.Join(m.dataDir, "backups", 
			fmt.Sprintf("casgists-backup-%s.tar.gz", time.Now().Format("20060102-150405")))
	}

	// Ensure backup directory exists
	backupDir := filepath.Dir(options.OutputPath)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Create temporary directory for staging
	tempDir, err := os.MkdirTemp("", "casgists-backup-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Export database
	if err := m.exportDatabase(ctx, tempDir); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Database export error: %v", err))
		return result, err
	}
	result.DatabaseExported = true

	// Export git repositories if requested
	if options.IncludeGitRepos {
		if err := m.exportGitRepos(ctx, tempDir); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Git repos export error: %v", err))
		} else {
			result.GitReposExported = true
		}
	}

	// Export attachments if requested
	if options.IncludeAttachments {
		if err := m.exportAttachments(ctx, tempDir); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Attachments export error: %v", err))
		} else {
			result.AttachmentsExported = true
		}
	}

	// Create metadata
	metadata := m.createMetadata()
	if err := m.writeMetadata(tempDir, metadata); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Metadata write error: %v", err))
	}

	// Create archive
	if err := m.createArchive(tempDir, options.OutputPath, options.EncryptionKey); err != nil {
		return result, fmt.Errorf("failed to create archive: %w", err)
	}

	// Get file info
	if info, err := os.Stat(options.OutputPath); err == nil {
		result.Size = info.Size()
		result.OutputPath = options.OutputPath
	}

	result.EndTime = time.Now()
	result.Success = len(result.Errors) == 0

	return result, nil
}

// exportDatabase exports the database to JSON files
func (m *Manager) exportDatabase(ctx context.Context, outputDir string) error {
	dbDir := filepath.Join(outputDir, "database")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return err
	}

	// Export users
	var users []models.User
	if err := m.db.Find(&users).Error; err != nil {
		return fmt.Errorf("failed to export users: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "users.json"), users); err != nil {
		return err
	}

	// Export gists and files
	var gists []models.Gist
	if err := m.db.Preload("Files").Find(&gists).Error; err != nil {
		return fmt.Errorf("failed to export gists: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "gists.json"), gists); err != nil {
		return err
	}

	// Export organizations
	var orgs []models.Organization
	if err := m.db.Find(&orgs).Error; err != nil {
		return fmt.Errorf("failed to export organizations: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "organizations.json"), orgs); err != nil {
		return err
	}

	// Export organization members
	var orgMembers []models.OrganizationMember
	if err := m.db.Find(&orgMembers).Error; err != nil {
		return fmt.Errorf("failed to export organization members: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "organization_members.json"), orgMembers); err != nil {
		return err
	}

	// Export gist comments
	var gistComments []models.GistComment
	if err := m.db.Find(&gistComments).Error; err != nil {
		return fmt.Errorf("failed to export gist comments: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "gist_comments.json"), gistComments); err != nil {
		return err
	}

	// Export gist stars
	var gistStars []models.GistStar
	if err := m.db.Find(&gistStars).Error; err != nil {
		return fmt.Errorf("failed to export gist stars: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "gist_stars.json"), gistStars); err != nil {
		return err
	}

	// Export webhooks
	var webhooks []models.Webhook
	if err := m.db.Find(&webhooks).Error; err != nil {
		return fmt.Errorf("failed to export webhooks: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "webhooks.json"), webhooks); err != nil {
		return err
	}

	// Export system configs
	var configs []models.SystemConfig
	if err := m.db.Find(&configs).Error; err != nil {
		return fmt.Errorf("failed to export system configs: %w", err)
	}
	if err := m.writeJSON(filepath.Join(dbDir, "system_configs.json"), configs); err != nil {
		return err
	}

	return nil
}

// writeJSON writes data to a JSON file
func (m *Manager) writeJSON(filename string, data interface{}) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// exportGitRepos exports git repositories
func (m *Manager) exportGitRepos(ctx context.Context, outputDir string) error {
	gitDir := filepath.Join(outputDir, "git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		return err
	}

	// Copy git directory
	return copyDir(m.gitDir, gitDir)
}

// exportAttachments exports uploaded attachments
func (m *Manager) exportAttachments(ctx context.Context, outputDir string) error {
	attachDir := filepath.Join(outputDir, "attachments")
	if err := os.MkdirAll(attachDir, 0755); err != nil {
		return err
	}

	// Copy uploads directory
	return copyDir(m.uploadDir, attachDir)
}

// createMetadata creates backup metadata
func (m *Manager) createMetadata() *BackupMetadata {
	var userCount, gistCount, orgCount int64
	m.db.Model(&models.User{}).Count(&userCount)
	m.db.Model(&models.Gist{}).Count(&gistCount)
	m.db.Model(&models.Organization{}).Count(&orgCount)

	hostname, _ := os.Hostname()

	return &BackupMetadata{
		Version:        "1.0",
		CreatedAt:      time.Now(),
		CasGistVersion: m.config.GetString("version"),
		DatabaseType:   m.config.GetString("database.type"),
		TotalUsers:     userCount,
		TotalGists:     gistCount,
		TotalOrgs:      orgCount,
		Hostname:       hostname,
		Encrypted:      false,
	}
}

// writeMetadata writes metadata to file
func (m *Manager) writeMetadata(outputDir string, metadata *BackupMetadata) error {
	metadataFile := filepath.Join(outputDir, "backup-metadata.json")
	return m.writeJSON(metadataFile, metadata)
}

// createArchive creates a tar.gz archive
func (m *Manager) createArchive(sourceDir, outputPath, encryptionKey string) error {
	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	// Create gzip writer
	gzipWriter := gzip.NewWriter(outFile)
	defer gzipWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	// Walk through source directory
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}

		// Update header name
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		header.Name = relPath

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// If it's a file, write its content
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}

		return nil
	})
}

// ReadBackupMetadata reads metadata from a backup file
func (m *Manager) ReadBackupMetadata(backupPath string) (*BackupMetadata, error) {
	backupFile, err := os.Open(backupPath)
	if err != nil {
		return nil, err
	}
	defer backupFile.Close()

	var reader io.Reader = backupFile

	// Handle compression
	if filepath.Ext(backupPath) == ".gz" {
		gzReader, err := gzip.NewReader(backupFile)
		if err != nil {
			return nil, err
		}
		defer gzReader.Close()
		reader = gzReader
	}

	tarReader := tar.NewReader(reader)

	// Look for backup-metadata.json
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if header.Name == "backup-metadata.json" {
			var metadata BackupMetadata
			decoder := json.NewDecoder(tarReader)
			if err := decoder.Decode(&metadata); err != nil {
				return nil, err
			}
			return &metadata, nil
		}
	}

	return nil, fmt.Errorf("metadata not found in backup")
}

// Helper function to copy directories
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate destination path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		// Create directories
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Copy files
		return copyFile(path, dstPath)
	})
}

// Helper function to copy files
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}