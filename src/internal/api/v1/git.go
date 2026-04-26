package v1

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/casapps/casgist/src/internal/git"
)

// GitHandler handles Git-related API endpoints
type GitHandler struct {
	gitService *git.Service
}

// NewGitHandler creates a new Git handler
func NewGitHandler(gitService *git.Service) *GitHandler {
	return &GitHandler{
		gitService: gitService,
	}
}

// GetGistHistory godoc
// @Summary Get gist commit history
// @Description Retrieve the commit history for a gist
// @Tags git
// @Accept json
// @Produce json
// @Param id path string true "Gist ID"
// @Param limit query int false "Number of commits to return (default: 10)"
// @Success 200 {array} git.Commit
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gists/{id}/history [get]
func (h *GitHandler) GetGistHistory(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid gist ID",
		})
	}
	
	// Parse limit parameter
	limit := 10 // default
	if limitStr := c.QueryParam("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	
	// Get commit history
	history, err := h.gitService.GetGistHistory(gistID, limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to get gist history",
		})
	}
	
	return c.JSON(http.StatusOK, history)
}

// CreateGistVersion godoc
// @Summary Create a new gist version
// @Description Create a new version/revision of a gist
// @Tags git
// @Accept json
// @Produce json
// @Param id path string true "Gist ID"
// @Param request body CreateVersionRequest true "Version creation request"
// @Success 200 {object} CreateVersionResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gists/{id}/versions [post]
func (h *GitHandler) CreateGistVersion(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid gist ID",
		})
	}
	
	// Parse request body
	var req CreateVersionRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid request body",
		})
	}
	
	// Validate message
	if req.Message == "" {
		req.Message = "Update gist"
	}
	
	// Create version
	commitHash, err := h.gitService.CreateGistVersion(gistID, req.Message)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to create version",
		})
	}
	
	return c.JSON(http.StatusOK, CreateVersionResponse{
		CommitHash: commitHash,
		Message:    req.Message,
	})
}

// GetGistBranches godoc
// @Summary Get gist branches
// @Description List all branches in a gist repository
// @Tags git
// @Accept json
// @Produce json
// @Param id path string true "Gist ID"
// @Success 200 {array} string
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gists/{id}/branches [get]
func (h *GitHandler) GetGistBranches(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid gist ID",
		})
	}
	
	// Get branches
	branches, err := h.gitService.GetGistBranches(gistID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to get branches",
		})
	}
	
	return c.JSON(http.StatusOK, branches)
}

// CreateGistBranch godoc
// @Summary Create a new gist branch
// @Description Create a new branch in a gist repository
// @Tags git
// @Accept json
// @Produce json
// @Param id path string true "Gist ID"
// @Param request body CreateBranchRequest true "Branch creation request"
// @Success 201 {object} MessageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gists/{id}/branches [post]
func (h *GitHandler) CreateGistBranch(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid gist ID",
		})
	}
	
	// Parse request body
	var req CreateBranchRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid request body",
		})
	}
	
	// Validate branch name
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "branch name is required",
		})
	}
	
	// Create branch
	if err := h.gitService.CreateGistBranch(gistID, req.Name); err != nil {
		return c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to create branch",
		})
	}
	
	return c.JSON(http.StatusCreated, MessageResponse{
		Message: "branch created successfully",
	})
}

// SyncGistFiles godoc
// @Summary Sync gist files
// @Description Synchronize database files with Git repository
// @Tags git
// @Accept json
// @Produce json
// @Param id path string true "Gist ID"
// @Success 200 {object} MessageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gists/{id}/sync [post]
func (h *GitHandler) SyncGistFiles(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid gist ID",
		})
	}
	
	// Sync files
	if err := h.gitService.SyncGistFiles(gistID); err != nil {
		return c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to sync files",
		})
	}
	
	return c.JSON(http.StatusOK, MessageResponse{
		Message: "files synchronized successfully",
	})
}

// GetRepositorySize godoc
// @Summary Get repository size
// @Description Get the size of a gist's Git repository
// @Tags git
// @Accept json
// @Produce json
// @Param id path string true "Gist ID"
// @Success 200 {object} RepositorySizeResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /gists/{id}/size [get]
func (h *GitHandler) GetRepositorySize(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "invalid gist ID",
		})
	}
	
	// Get repository size
	size, err := h.gitService.GetRepositorySize(gistID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "failed to get repository size",
		})
	}
	
	return c.JSON(http.StatusOK, RepositorySizeResponse{
		Size:      size,
		SizeHuman: formatBytes(size),
	})
}

// Request/Response types

type ErrorResponse struct {
	Error string `json:"error"`
}

type MessageResponse struct {
	Message string `json:"message"`
}

type CreateVersionRequest struct {
	Message string `json:"message"`
}

type CreateVersionResponse struct {
	CommitHash string `json:"commit_hash"`
	Message    string `json:"message"`
}

type CreateBranchRequest struct {
	Name string `json:"name"`
}

type RepositorySizeResponse struct {
	Size      int64  `json:"size"`
	SizeHuman string `json:"size_human"`
}

// formatBytes formats byte size in human readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}