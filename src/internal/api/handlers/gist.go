package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/spf13/viper"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// GistHandler handles gist-related endpoints
type GistHandler struct {
	db        *gorm.DB
	config    *viper.Viper
	gitOps    GitOperations
}

// GitOperations interface for git operations
type GitOperations interface {
	InitializeGistRepo(gist *models.Gist, files []models.GistFile, author *models.User) error
	UpdateGistFiles(gist *models.Gist, files []models.GistFile, author *models.User, message string) error
	DeleteGistRepo(gist *models.Gist) error
}

// NewGistHandler creates a new gist handler
func NewGistHandler(db *gorm.DB, config *viper.Viper, gitOps GitOperations) *GistHandler {
	return &GistHandler{
		db:     db,
		config: config,
		gitOps: gitOps,
	}
}

// CreateGistRequest represents a gist creation request
type CreateGistRequest struct {
	Title       string              `json:"title" validate:"required"`
	Description string              `json:"description"`
	Visibility  string              `json:"visibility"` // public, private, unlisted
	Files       []CreateFileRequest `json:"files" validate:"required,min=1"`
}

// CreateFileRequest represents a file in a gist creation request
type CreateFileRequest struct {
	Filename string `json:"filename" validate:"required"`
	Content  string `json:"content"`
	Language string `json:"language"`
}

// GistResponse represents a gist in API responses
type GistResponse struct {
	ID          uuid.UUID       `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Visibility  string          `json:"visibility"`
	ViewCount   int             `json:"view_count"`
	StarCount   int             `json:"star_count"`
	ForkCount   int             `json:"fork_count"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	User        *UserResponse   `json:"user"`
	Files       []FileResponse  `json:"files"`
}

// FileResponse represents a file in API responses
type FileResponse struct {
	ID        uuid.UUID `json:"id"`
	Filename  string    `json:"filename"`
	Language  string    `json:"language"`
	Content   string    `json:"content"`
	Size      int64     `json:"size"`
	LineCount int64     `json:"line_count"`
}

// Create creates a new gist
func (h *GistHandler) Create(c echo.Context) error {
	var req CreateGistRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Validate request
	if err := c.Validate(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Get user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	// Validate visibility
	visibility := models.VisibilityPrivate
	if req.Visibility == "public" {
		visibility = models.VisibilityPublic
	} else if req.Visibility == "unlisted" {
		visibility = models.VisibilityUnlisted
	}

	// Create gist
	gist := models.Gist{
		ID:          uuid.New(),
		UserID:      &userID,
		Title:       req.Title,
		Description: req.Description,
		Visibility:  visibility,
		GitRepoPath: uuid.New().String(), // Placeholder for git repo path
	}

	// Create files
	for _, fileReq := range req.Files {
		file := models.GistFile{
			ID:       uuid.New(),
			Filename: fileReq.Filename,
			Content:  fileReq.Content,
			Language: fileReq.Language,
			Size:     int64(len(fileReq.Content)),
			Lines:    countLines(fileReq.Content),
		}
		gist.Files = append(gist.Files, file)
	}

	// Save to database
	if err := h.db.Create(&gist).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create gist")
	}

	// Load user
	var user models.User
	h.db.First(&user, userID)

	// Initialize Git repository if gitOps is available
	if h.gitOps != nil {
		if err := h.gitOps.InitializeGistRepo(&gist, gist.Files, &user); err != nil {
			// Log error but don't fail the request
			// Git operations are optional for basic functionality
			c.Logger().Errorf("Failed to initialize git repo for gist %s: %v", gist.ID, err)
		}
	}

	// Return response
	return c.JSON(http.StatusCreated, h.buildGistResponse(&gist, &user))
}

// List returns a list of gists
func (h *GistHandler) List(c echo.Context) error {
	// Parse pagination parameters
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// Build query
	query := h.db.Model(&models.Gist{}).Preload("User").Preload("Files")

	// Filter by user if specified
	if username := c.QueryParam("username"); username != "" {
		var user models.User
		if err := h.db.Where("username = ?", username).First(&user).Error; err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		query = query.Where("user_id = ?", user.ID)
	}

	// Filter by visibility
	if c.Get("user_id") == nil {
		// Not authenticated, only show public gists
		query = query.Where("visibility = ?", models.VisibilityPublic)
	} else {
		// Authenticated
		if visibility := c.QueryParam("visibility"); visibility != "" {
			switch visibility {
			case "public":
				query = query.Where("visibility = ?", models.VisibilityPublic)
			case "private":
				userID := c.Get("user_id").(uuid.UUID)
				query = query.Where("visibility = ? AND user_id = ?", models.VisibilityPrivate, userID)
			case "unlisted":
				query = query.Where("visibility = ?", models.VisibilityUnlisted)
			}
		}
	}

	// Count total
	var total int64
	query.Count(&total)

	// Apply sorting
	sort := c.QueryParam("sort")
	switch sort {
	case "created":
		query = query.Order("created_at DESC")
	case "updated":
		query = query.Order("updated_at DESC")
	case "stars":
		query = query.Order("star_count DESC")
	default:
		query = query.Order("created_at DESC")
	}

	// Apply pagination
	offset := (page - 1) * limit
	query = query.Offset(offset).Limit(limit)

	// Fetch gists
	var gists []models.Gist
	if err := query.Find(&gists).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gists")
	}

	// Build response
	response := map[string]interface{}{
		"gists": h.buildGistListResponse(gists),
		"pagination": map[string]interface{}{
			"page":  page,
			"limit": limit,
			"total": total,
			"pages": (total + int64(limit) - 1) / int64(limit),
		},
	}

	return c.JSON(http.StatusOK, response)
}

// Get returns a single gist
func (h *GistHandler) Get(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Fetch gist
	var gist models.Gist
	if err := h.db.Preload("User").Preload("Files").First(&gist, gistID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "gist not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gist")
	}

	// Check visibility
	userID, _ := c.Get("user_id").(uuid.UUID)
	if gist.Visibility == models.VisibilityPrivate && (gist.UserID == nil || *gist.UserID != userID) {
		return echo.NewHTTPError(http.StatusForbidden, "access denied")
	}

	// Increment view count
	h.db.Model(&gist).Update("view_count", gist.ViewCount+1)

	// Return response
	return c.JSON(http.StatusOK, h.buildGistResponse(&gist, gist.User))
}

// Update updates a gist
func (h *GistHandler) Update(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Get user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	// Fetch gist
	var gist models.Gist
	if err := h.db.First(&gist, gistID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "gist not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gist")
	}

	// Check ownership
	if gist.UserID == nil || *gist.UserID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "access denied")
	}

	// Parse request
	var req CreateGistRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Validate visibility
	visibility := gist.Visibility
	if req.Visibility == "public" {
		visibility = models.VisibilityPublic
	} else if req.Visibility == "private" {
		visibility = models.VisibilityPrivate
	} else if req.Visibility == "unlisted" {
		visibility = models.VisibilityUnlisted
	}

	// Update gist
	gist.Title = req.Title
	gist.Description = req.Description
	gist.Visibility = visibility

	// Update files (simplified - in production, you'd handle file updates more carefully)
	h.db.Where("gist_id = ?", gistID).Delete(&models.GistFile{})
	for _, fileReq := range req.Files {
		file := models.GistFile{
			ID:       uuid.New(),
			GistID:   gistID,
			Filename: fileReq.Filename,
			Content:  fileReq.Content,
			Language: fileReq.Language,
			Size:     int64(len(fileReq.Content)),
			Lines:    countLines(fileReq.Content),
		}
		h.db.Create(&file)
	}

	// Save changes
	if err := h.db.Save(&gist).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update gist")
	}

	// Reload with associations
	h.db.Preload("User").Preload("Files").First(&gist, gistID)

	// Return response
	return c.JSON(http.StatusOK, h.buildGistResponse(&gist, gist.User))
}

// Delete deletes a gist
func (h *GistHandler) Delete(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Get user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	// Fetch gist
	var gist models.Gist
	if err := h.db.First(&gist, gistID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "gist not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gist")
	}

	// Check ownership
	if gist.UserID == nil || *gist.UserID != userID {
		return echo.NewHTTPError(http.StatusForbidden, "access denied")
	}

	// Delete gist (soft delete)
	if err := h.db.Delete(&gist).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete gist")
	}

	return c.NoContent(http.StatusNoContent)
}

// Helper methods

func (h *GistHandler) buildGistResponse(gist *models.Gist, user *models.User) GistResponse {
	response := GistResponse{
		ID:          gist.ID,
		Title:       gist.Title,
		Description: gist.Description,
		Visibility:  string(gist.Visibility),
		ViewCount:   gist.ViewCount,
		StarCount:   gist.StarCount,
		ForkCount:   gist.ForkCount,
		CreatedAt:   gist.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   gist.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}

	if user != nil {
		response.User = &UserResponse{
			ID:          user.ID,
			Username:    user.Username,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			AvatarURL:   user.AvatarURL,
			IsAdmin:     user.IsAdmin,
		}
	}

	for _, file := range gist.Files {
		response.Files = append(response.Files, FileResponse{
			ID:        file.ID,
			Filename:  file.Filename,
			Language:  file.Language,
			Content:   file.Content,
			Size:      file.Size,
			LineCount: int64(file.Lines),
		})
	}

	return response
}

func (h *GistHandler) buildGistListResponse(gists []models.Gist) []GistResponse {
	responses := make([]GistResponse, 0, len(gists))
	for _, gist := range gists {
		responses = append(responses, h.buildGistResponse(&gist, gist.User))
	}
	return responses
}

// countLines counts the number of lines in a string
func countLines(s string) int {
	if s == "" {
		return 0
	}
	count := 1
	for _, r := range s {
		if r == '\n' {
			count++
		}
	}
	return count
}

// Star stars a gist
func (h *GistHandler) Star(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Get user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	// Check if gist exists
	var gist models.Gist
	if err := h.db.First(&gist, gistID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "gist not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gist")
	}

	// Check visibility
	if gist.Visibility == models.VisibilityPrivate && (gist.UserID == nil || *gist.UserID != userID) {
		return echo.NewHTTPError(http.StatusForbidden, "access denied")
	}

	// Check if already starred
	var star models.GistStar
	result := h.db.Where("user_id = ? AND gist_id = ?", userID, gistID).First(&star)
	
	if result.Error == nil {
		// Already starred
		return c.JSON(http.StatusOK, map[string]string{"message": "already starred"})
	}

	// Create star
	star = models.GistStar{
		ID:     uuid.New(),
		UserID: userID,
		GistID: gistID,
	}

	if err := h.db.Create(&star).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to star gist")
	}

	// Update star count
	h.db.Model(&gist).Update("star_count", gist.StarCount+1)

	// Send notification to gist owner if different from starring user
	if gist.UserID != nil && *gist.UserID != userID {
		// TODO: Send notification through email service
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"message": "gist starred",
		"star_count": gist.StarCount + 1,
	})
}

// Unstar removes a star from a gist
func (h *GistHandler) Unstar(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Get user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	// Delete star
	result := h.db.Where("user_id = ? AND gist_id = ?", userID, gistID).Delete(&models.GistStar{})
	
	if result.RowsAffected == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "star not found")
	}

	// Update star count
	var gist models.Gist
	if err := h.db.First(&gist, gistID).Error; err == nil {
		var count int64
		h.db.Model(&models.GistStar{}).Where("gist_id = ?", gistID).Count(&count)
		h.db.Model(&gist).Update("star_count", count)
	}

	return c.NoContent(http.StatusNoContent)
}

// Fork creates a fork of a gist
func (h *GistHandler) Fork(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Get user ID from context
	userID, ok := c.Get("user_id").(uuid.UUID)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}

	// Fetch original gist with files
	var originalGist models.Gist
	if err := h.db.Preload("Files").Preload("User").First(&originalGist, gistID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "gist not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gist")
	}

	// Check visibility
	if originalGist.Visibility == models.VisibilityPrivate && (originalGist.UserID == nil || *originalGist.UserID != userID) {
		return echo.NewHTTPError(http.StatusForbidden, "access denied")
	}

	// Check if user is trying to fork their own gist
	if originalGist.UserID != nil && *originalGist.UserID == userID {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot fork your own gist")
	}

	// Check if already forked by this user
	var existingFork models.Gist
	if err := h.db.Where("forked_from_id = ? AND user_id = ?", gistID, userID).First(&existingFork).Error; err == nil {
		// Already forked, return the existing fork
		h.db.Preload("User").Preload("Files").First(&existingFork, existingFork.ID)
		return c.JSON(http.StatusOK, h.buildGistResponse(&existingFork, existingFork.User))
	}

	// Create the fork
	fork := models.Gist{
		ID:           uuid.New(),
		UserID:       &userID,
		Title:        originalGist.Title,
		Description:  originalGist.Description,
		Visibility:   originalGist.Visibility,
		GitRepoPath:  uuid.New().String(), // New git repo for the fork
		ForkedFromID: &gistID,
	}

	// Start transaction
	tx := h.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Create gist
	if err := tx.Create(&fork).Error; err != nil {
		tx.Rollback()
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create fork")
	}

	// Copy files
	for _, originalFile := range originalGist.Files {
		file := models.GistFile{
			ID:       uuid.New(),
			GistID:   fork.ID,
			Filename: originalFile.Filename,
			Content:  originalFile.Content,
			Language: originalFile.Language,
			Size:     originalFile.Size,
			Lines:    originalFile.Lines,
		}
		if err := tx.Create(&file).Error; err != nil {
			tx.Rollback()
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to copy files")
		}
	}

	// Update fork count on original
	if err := tx.Model(&originalGist).Update("fork_count", originalGist.ForkCount+1).Error; err != nil {
		tx.Rollback()
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update fork count")
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit fork")
	}

	// Reload fork with associations
	h.db.Preload("User").Preload("Files").First(&fork, fork.ID)

	// Initialize git repository for the fork
	if h.gitOps != nil {
		// TODO: Initialize git repo
	}

	// Send notification to original gist owner
	if originalGist.UserID != nil {
		// TODO: Send notification through email service
	}

	// Return response
	return c.JSON(http.StatusCreated, h.buildGistResponse(&fork, fork.User))
}

// GetStars returns users who starred a gist
func (h *GistHandler) GetStars(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Check if gist exists
	var gist models.Gist
	if err := h.db.First(&gist, gistID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "gist not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gist")
	}

	// Get pagination params
	page := 1
	perPage := 20
	if p, err := strconv.Atoi(c.QueryParam("page")); err == nil && p > 0 {
		page = p
	}
	if pp, err := strconv.Atoi(c.QueryParam("per_page")); err == nil && pp > 0 && pp <= 100 {
		perPage = pp
	}

	// Fetch stars with users
	var stars []models.GistStar
	offset := (page - 1) * perPage
	
	if err := h.db.Preload("User").Where("gist_id = ?", gistID).
		Offset(offset).Limit(perPage).
		Order("created_at DESC").
		Find(&stars).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch stars")
	}

	// Build response
	type UserStarResponse struct {
		ID        uuid.UUID `json:"id"`
		Username  string    `json:"username"`
		Email     string    `json:"email,omitempty"`
		AvatarURL string    `json:"avatar_url"`
		StarredAt time.Time `json:"starred_at"`
	}

	users := make([]UserStarResponse, 0, len(stars))
	for _, star := range stars {
		if star.UserID != uuid.Nil {
			users = append(users, UserStarResponse{
				ID:        star.User.ID,
				Username:  star.User.Username,
				Email:     "", // Don't expose email
				AvatarURL: star.User.AvatarURL,
				StarredAt: star.CreatedAt,
			})
		}
	}

	// Get total count
	var total int64
	h.db.Model(&models.GistStar{}).Where("gist_id = ?", gistID).Count(&total)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"users": users,
		"pagination": map[string]interface{}{
			"page":       page,
			"per_page":   perPage,
			"total":      total,
			"total_pages": (total + int64(perPage) - 1) / int64(perPage),
		},
	})
}

// GetForks returns forks of a gist
func (h *GistHandler) GetForks(c echo.Context) error {
	// Parse gist ID
	gistID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid gist ID")
	}

	// Check if gist exists
	var gist models.Gist
	if err := h.db.First(&gist, gistID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "gist not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch gist")
	}

	// Get pagination params
	page := 1
	perPage := 20
	if p, err := strconv.Atoi(c.QueryParam("page")); err == nil && p > 0 {
		page = p
	}
	if pp, err := strconv.Atoi(c.QueryParam("per_page")); err == nil && pp > 0 && pp <= 100 {
		perPage = pp
	}

	// Fetch forks
	var forks []models.Gist
	offset := (page - 1) * perPage
	
	if err := h.db.Preload("User").Preload("Files").
		Where("forked_from_id = ?", gistID).
		Offset(offset).Limit(perPage).
		Order("created_at DESC").
		Find(&forks).Error; err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch forks")
	}

	// Build response
	forksResponse := h.buildGistListResponse(forks)

	// Get total count
	var total int64
	h.db.Model(&models.Gist{}).Where("forked_from_id = ?", gistID).Count(&total)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"forks": forksResponse,
		"pagination": map[string]interface{}{
			"page":       page,
			"per_page":   perPage,
			"total":      total,
			"total_pages": (total + int64(perPage) - 1) / int64(perPage),
		},
	})
}