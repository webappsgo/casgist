package git

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/casapps/casgist/src/internal/models"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/google/uuid"
	"github.com/spf13/viper"
)

// Service handles Git operations for gists
type Service struct {
	config     *viper.Viper
	repoPath   string
}

// NewService creates a new Git service
func NewService(cfg *viper.Viper) *Service {
	repoPath := cfg.GetString("git.repo_path")
	if repoPath == "" {
		repoPath = cfg.GetString("data_dir") + "/repositories"
	}

	return &Service{
		config:   cfg,
		repoPath: repoPath,
	}
}

// CommitInfo represents git commit information (alias for compatibility)
type CommitInfo = Commit

// FileDiff represents a file diff
type FileDiff struct {
	Filename string
	Action   string
	Patch    string
}

// Diff represents a diff between commits
type Diff struct {
	FromCommit string
	ToCommit   string
	Files      []*FileDiff
}

// RepoStats represents repository statistics
type RepoStats struct {
	CommitCount int
	SizeBytes   int64
}

// InitRepository initializes a Git repository for a gist
func (s *Service) InitRepository(gistID uuid.UUID) error {
	repoDir := filepath.Join(s.repoPath, gistID.String())
	
	// Create repository directory
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return fmt.Errorf("failed to create repository directory: %w", err)
	}

	// Initialize bare repository
	_, err := git.PlainInit(repoDir, true)
	if err != nil {
		return fmt.Errorf("failed to initialize git repository: %w", err)
	}

	return nil
}

// InitializeGistRepo creates a Git repository for a gist (alias)
func (s *Service) InitializeGistRepo(gist *models.Gist) error {
	return s.InitRepository(gist.ID)
}

// CommitChanges commits changes to a gist repository
func (s *Service) CommitChanges(gistID uuid.UUID, message string) error {
	repoDir := filepath.Join(s.repoPath, gistID.String())
	
	// Open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	repo, err := git.Open(storage, fs)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Get working tree
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get working tree: %w", err)
	}

	// Add all files
	if _, err := worktree.Add("."); err != nil {
		return fmt.Errorf("failed to add files to git: %w", err)
	}

	// Create commit
	_, err = worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "CasGists",
			Email: "noreply@casgists.local",
			When:  time.Now(),
		},
		Committer: &object.Signature{
			Name:  "CasGists",
			Email: "noreply@casgists.local",
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	return nil
}

// CreateCommit creates a commit with the gist files
func (s *Service) CreateCommit(gist *models.Gist, message string, author *models.User) error {
	repoDir := filepath.Join(s.repoPath, gist.ID.String())
	
	// Open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	repo, err := git.Open(storage, fs)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Get working tree
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get working tree: %w", err)
	}

	// Create/update files
	for _, file := range gist.Files {
		filePath := file.Filename
		
		// Create directories if needed
		dir := filepath.Dir(filePath)
		if dir != "." {
			if err := fs.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		// Write file content
		f, err := fs.Create(filePath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", filePath, err)
		}
		
		if _, err := f.Write([]byte(file.Content)); err != nil {
			f.Close()
			return fmt.Errorf("failed to write file %s: %w", filePath, err)
		}
		f.Close()

		// Add file to git
		if _, err := worktree.Add(filePath); err != nil {
			return fmt.Errorf("failed to add file %s to git: %w", filePath, err)
		}
	}

	// Create commit
	commit, err := worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  author.DisplayName,
			Email: author.Email,
			When:  time.Now(),
		},
		Committer: &object.Signature{
			Name:  author.DisplayName,
			Email: author.Email,
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	// TODO: Store commit hash in gist metadata when field is added
	_ = commit.String() // Prevent unused variable error

	return nil
}

// GetGistHistory returns git history for a gist
func (s *Service) GetGistHistory(gistID uuid.UUID, limit int) ([]CommitInfo, error) {
	commits, err := s.GetCommitHistory(gistID.String(), limit)
	if err != nil {
		return nil, err
	}
	
	var result []CommitInfo
	for _, commit := range commits {
		result = append(result, CommitInfo{
			Hash:      commit.Hash,
			Message:   commit.Message,
			Author:    commit.Author,
			Email:     commit.Email,
			Timestamp: commit.Timestamp,
		})
	}
	
	return result, nil
}

// GetCommitHistory returns the commit history for a gist
func (s *Service) GetCommitHistory(gistID string, limit int) ([]*Commit, error) {
	repoDir := filepath.Join(s.repoPath, gistID)
	
	// Open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	repo, err := git.Open(storage, fs)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Get commit iterator
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	commitIter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit log: %w", err)
	}
	defer commitIter.Close()

	var commits []*Commit
	count := 0
	
	err = commitIter.ForEach(func(c *object.Commit) error {
		if limit > 0 && count >= limit {
			return fmt.Errorf("limit reached") // Use error to break iteration
		}

		commit := &Commit{
			Hash:      c.Hash.String(),
			Message:   c.Message,
			Author:    c.Author.Name,
			Email:     c.Author.Email,
			Timestamp: c.Author.When,
		}
		
		commits = append(commits, commit)
		count++
		return nil
	})
	
	if err != nil && err.Error() != "limit reached" {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}

	return commits, nil
}

// CloneRepository clones a gist repository
func (s *Service) CloneRepository(gistID uuid.UUID, destination string) error {
	sourceDir := filepath.Join(s.repoPath, gistID.String())
	
	// Create destination directory
	if err := os.MkdirAll(destination, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Clone repository
	_, err := git.PlainClone(destination, false, &git.CloneOptions{
		URL: sourceDir,
	})
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	return nil
}

// CloneGist creates a clone/fork of a gist
func (s *Service) CloneGist(sourceGist, targetGist *models.Gist) error {
	sourceDir := filepath.Join(s.repoPath, sourceGist.ID.String())
	targetDir := filepath.Join(s.repoPath, targetGist.ID.String())
	
	// Create target directory
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Clone repository
	_, err := git.PlainClone(targetDir, false, &git.CloneOptions{
		URL: sourceDir,
	})
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}

	return nil
}

// CreateGistVersion creates a new version of a gist
func (s *Service) CreateGistVersion(gistID uuid.UUID, message string) (string, error) {
	if err := s.CommitChanges(gistID, message); err != nil {
		return "", err
	}
	
	// Get latest commit hash
	commits, err := s.GetCommitHistory(gistID.String(), 1)
	if err != nil {
		return "", err
	}
	
	if len(commits) > 0 {
		return commits[0].Hash, nil
	}
	
	return "", fmt.Errorf("no commits found")
}

// GetGistBranches returns list of branches for a gist
func (s *Service) GetGistBranches(gistID uuid.UUID) ([]string, error) {
	repoDir := filepath.Join(s.repoPath, gistID.String())
	
	// Open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	repo, err := git.Open(storage, fs)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Get branches
	refs, err := repo.References()
	if err != nil {
		return nil, fmt.Errorf("failed to get references: %w", err)
	}
	defer refs.Close()

	var branches []string
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsBranch() {
			branches = append(branches, ref.Name().Short())
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate branches: %w", err)
	}

	// If no branches found, default to main
	if len(branches) == 0 {
		branches = []string{"main"}
	}

	return branches, nil
}

// CreateGistBranch creates a new branch for a gist
func (s *Service) CreateGistBranch(gistID uuid.UUID, branchName string) error {
	repoDir := filepath.Join(s.repoPath, gistID.String())
	
	// Open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	repo, err := git.Open(storage, fs)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Get current HEAD
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	// Create new branch reference
	branchRef := plumbing.NewBranchReferenceName(branchName)
	ref := plumbing.NewHashReference(branchRef, head.Hash())
	
	if err := repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

// SyncGistFiles synchronizes gist files with git repository
func (s *Service) SyncGistFiles(gistID uuid.UUID) error {
	// This is typically handled by CreateCommit, but can be implemented
	// for more complex synchronization scenarios
	return s.CommitChanges(gistID, "Sync gist files")
}

// GetRepositorySize returns the size of a gist repository
func (s *Service) GetRepositorySize(gistID uuid.UUID) (int64, error) {
	stats, err := s.GetRepoStats(gistID.String())
	if err != nil {
		return 0, err
	}
	return stats.SizeBytes, nil
}

// GetRepoStats returns statistics about the repository
func (s *Service) GetRepoStats(gistID string) (*RepoStats, error) {
	repoDir := filepath.Join(s.repoPath, gistID)
	
	// Open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	repo, err := git.Open(storage, fs)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Count commits
	ref, err := repo.Head()
	if err != nil {
		return &RepoStats{CommitCount: 0}, nil // Empty repository
	}

	commitIter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit log: %w", err)
	}
	defer commitIter.Close()

	commitCount := 0
	err = commitIter.ForEach(func(c *object.Commit) error {
		commitCount++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to count commits: %w", err)
	}

	// Get repository size
	var totalSize int64
	err = filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to calculate repository size: %w", err)
	}

	return &RepoStats{
		CommitCount: commitCount,
		SizeBytes:   totalSize,
	}, nil
}

// GetFileContent returns the content of a file at a specific commit
func (s *Service) GetFileContent(gistID, filename, commitHash string) (string, error) {
	repoDir := filepath.Join(s.repoPath, gistID)
	
	// Open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	repo, err := git.Open(storage, fs)
	if err != nil {
		return "", fmt.Errorf("failed to open repository: %w", err)
	}

	// Get commit object
	hash := plumbing.NewHash(commitHash)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return "", fmt.Errorf("failed to get commit: %w", err)
	}

	// Get tree
	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("failed to get tree: %w", err)
	}

	// Get file
	file, err := tree.File(filename)
	if err != nil {
		return "", fmt.Errorf("failed to get file: %w", err)
	}

	content, err := file.Contents()
	if err != nil {
		return "", fmt.Errorf("failed to get file contents: %w", err)
	}

	return content, nil
}

// DeleteGistRepo removes the Git repository for a gist
func (s *Service) DeleteGistRepo(gistID string) error {
	repoDir := filepath.Join(s.repoPath, gistID)
	
	if err := os.RemoveAll(repoDir); err != nil {
		return fmt.Errorf("failed to delete repository: %w", err)
	}

	return nil
}

// ValidateGistRepo checks if a gist repository exists and is valid
func (s *Service) ValidateGistRepo(gistID string) error {
	repoDir := filepath.Join(s.repoPath, gistID)
	
	// Check if directory exists
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		return fmt.Errorf("repository does not exist")
	}

	// Try to open repository
	fs := osfs.New(repoDir)
	storage := filesystem.NewStorage(fs, nil)
	
	_, err := git.Open(storage, fs)
	if err != nil {
		return fmt.Errorf("invalid git repository: %w", err)
	}

	return nil
}