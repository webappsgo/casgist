package git

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"

	"github.com/casapps/casgist/src/internal/database/models"
)

// Operations handles Git operations for gists
type Operations struct {
	dataDir string
}

// NewOperations creates a new Git operations handler
func NewOperations(dataDir string) *Operations {
	return &Operations{
		dataDir: dataDir,
	}
}

// CreateGistRepository creates a Git repository for a gist
func (g *Operations) CreateGistRepository(gist *models.Gist) error {
	repoPath := filepath.Join(g.dataDir, gist.ID.String())
	
	// Create repository directory
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return fmt.Errorf("failed to create repository directory: %w", err)
	}
	
	// Initialize Git repository
	repo, err := git.PlainInit(repoPath, false)
	if err != nil {
		return fmt.Errorf("failed to initialize git repository: %w", err)
	}
	
	// Create initial commit with gist files
	if err := g.createInitialCommit(repo, gist); err != nil {
		return fmt.Errorf("failed to create initial commit: %w", err)
	}
	
	return nil
}

// UpdateGistRepository updates a Git repository with new changes
func (g *Operations) UpdateGistRepository(gist *models.Gist, commitMessage string) error {
	repoPath := filepath.Join(g.dataDir, gist.ID.String())
	
	// Open existing repository
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open git repository: %w", err)
	}
	
	// Get working tree
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get working tree: %w", err)
	}
	
	// Write updated files
	for _, file := range gist.Files {
		filePath := filepath.Join(repoPath, file.Filename)
		if err := os.WriteFile(filePath, []byte(file.Content), 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", file.Filename, err)
		}
		
		// Add file to staging area
		if _, err := worktree.Add(file.Filename); err != nil {
			return fmt.Errorf("failed to add file to staging: %w", err)
		}
	}
	
	// Create commit
	commit, err := worktree.Commit(commitMessage, &git.CommitOptions{
		Author: &object.Signature{
			Name:  gist.User.Username,
			Email: gist.User.Email,
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}
	
	// Update gist with latest commit hash
	obj, err := repo.CommitObject(commit)
	if err != nil {
		return fmt.Errorf("failed to get commit object: %w", err)
	}
	
	// Store commit hash in gist metadata (if needed)
	_ = obj.Hash.String()
	
	return nil
}

// GetGistHistory returns the commit history for a gist
func (g *Operations) GetGistHistory(gistID uuid.UUID, limit int) ([]*GitCommit, error) {
	repoPath := filepath.Join(g.dataDir, gistID.String())
	
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}
	
	// Get commit history
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD reference: %w", err)
	}
	
	commits, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit log: %w", err)
	}
	defer commits.Close()
	
	var history []*GitCommit
	count := 0
	
	err = commits.ForEach(func(c *object.Commit) error {
		if limit > 0 && count >= limit {
			return fmt.Errorf("limit reached") // Use error to break iteration
		}
		
		history = append(history, &GitCommit{
			Hash:    c.Hash.String(),
			Message: c.Message,
			Author:  c.Author.Name,
			Email:   c.Author.Email,
			Date:    c.Author.When,
		})
		
		count++
		return nil
	})
	
	if err != nil && err.Error() != "limit reached" {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}
	
	return history, nil
}

// DeleteGistRepository removes the Git repository for a gist
func (g *Operations) DeleteGistRepository(gistID uuid.UUID) error {
	repoPath := filepath.Join(g.dataDir, gistID.String())
	
	if err := os.RemoveAll(repoPath); err != nil {
		return fmt.Errorf("failed to delete repository: %w", err)
	}
	
	return nil
}

// GetGistContent retrieves the content of a gist at a specific commit
func (g *Operations) GetGistContent(gistID uuid.UUID, commitHash string) (map[string]string, error) {
	repoPath := filepath.Join(g.dataDir, gistID.String())
	
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}
	
	// Get commit object
	var hash plumbing.Hash
	if commitHash == "" {
		ref, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("failed to get HEAD reference: %w", err)
		}
		hash = ref.Hash()
	} else {
		// Parse commit hash
		hash = plumbing.NewHash(commitHash)
	}
	
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit object: %w", err)
	}
	
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get commit tree: %w", err)
	}
	
	// Extract files content
	files := make(map[string]string)
	err = tree.Files().ForEach(func(f *object.File) error {
		content, err := f.Contents()
		if err != nil {
			return err
		}
		files[f.Name] = content
		return nil
	})
	
	if err != nil {
		return nil, fmt.Errorf("failed to extract files: %w", err)
	}
	
	return files, nil
}

// createInitialCommit creates the initial commit for a gist
func (g *Operations) createInitialCommit(repo *git.Repository, gist *models.Gist) error {
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get working tree: %w", err)
	}
	
	// Write gist files to repository
	for _, file := range gist.Files {
		filePath := filepath.Join(worktree.Filesystem.Root(), file.Filename)
		if err := os.WriteFile(filePath, []byte(file.Content), 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", file.Filename, err)
		}
		
		// Add file to staging area
		if _, err := worktree.Add(file.Filename); err != nil {
			return fmt.Errorf("failed to add file to staging: %w", err)
		}
	}
	
	// Create initial commit
	message := "Initial commit"
	if gist.Title != "" {
		message = fmt.Sprintf("Create %s", gist.Title)
	}
	
	_, err = worktree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  gist.User.Username,
			Email: gist.User.Email,
			When:  time.Now(),
		},
	})
	
	if err != nil {
		return fmt.Errorf("failed to create initial commit: %w", err)
	}
	
	return nil
}

// GitCommit represents a Git commit
type GitCommit struct {
	Hash    string    `json:"hash"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	Date    time.Time `json:"date"`
}

// CloneRepository clones a repository from a remote URL
func (g *Operations) CloneRepository(gistID uuid.UUID, remoteURL string) error {
	repoPath := filepath.Join(g.dataDir, gistID.String())
	
	// Create directory
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return fmt.Errorf("failed to create repository directory: %w", err)
	}
	
	// Clone repository
	_, err := git.PlainClone(repoPath, false, &git.CloneOptions{
		URL: remoteURL,
	})
	
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}
	
	return nil
}

// GetRepositorySize returns the size of a repository in bytes
func (g *Operations) GetRepositorySize(gistID uuid.UUID) (int64, error) {
	repoPath := filepath.Join(g.dataDir, gistID.String())
	
	var size int64
	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	
	if err != nil {
		return 0, fmt.Errorf("failed to calculate repository size: %w", err)
	}
	
	return size, nil
}