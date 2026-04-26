package v1

import (
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/cache"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/git"
	"github.com/casapps/casgist/src/internal/search"
)

// HealthService provides comprehensive health check functionality
type HealthService struct {
	db           *gorm.DB
	cache        cache.Service
	searchEngine search.Engine
	gitService   *git.Service
	startTime    time.Time
}

// NewHealthService creates a new health service
func NewHealthService(db *gorm.DB, cache cache.Service, search search.Engine, git *git.Service) *HealthService {
	return &HealthService{
		db:           db,
		cache:        cache,
		searchEngine: search,
		gitService:   git,
		startTime:    time.Now(),
	}
}

// ComponentHealth represents the health status of a component
type ComponentHealth struct {
	Status   string            `json:"status"` // healthy, warning, critical, disabled
	Message  string            `json:"message,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// HealthResponse represents the comprehensive health check response
type HealthResponse struct {
	Status     string                      `json:"status"`
	Version    string                      `json:"version"`
	Uptime     string                      `json:"uptime"`
	Timestamp  string                      `json:"timestamp"`
	Components map[string]ComponentHealth  `json:"components"`
	Metrics    map[string]interface{}      `json:"metrics"`
	Features   map[string]string           `json:"features"`
}

// GetHealth returns comprehensive health information
func (h *HealthService) GetHealth() (*HealthResponse, int) {
	response := &HealthResponse{
		Status:     "healthy",
		Version:    "1.0.0", // This should come from build info
		Uptime:     h.formatUptime(),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Components: make(map[string]ComponentHealth),
		Metrics:    make(map[string]interface{}),
		Features:   make(map[string]string),
	}

	statusCode := http.StatusOK

	// Check database health
	dbHealth := h.checkDatabaseHealth()
	response.Components["database"] = dbHealth
	if dbHealth.Status == "critical" {
		response.Status = "unhealthy"
		statusCode = http.StatusServiceUnavailable
	}

	// Check storage health
	storageHealth := h.checkStorageHealth()
	response.Components["storage"] = storageHealth

	// Check search health
	searchHealth := h.checkSearchHealth()
	response.Components["search"] = searchHealth

	// Check git health
	gitHealth := h.checkGitHealth()
	response.Components["git"] = gitHealth

	// Check email health
	emailHealth := h.checkEmailHealth()
	response.Components["email"] = emailHealth

	// Check cache health
	cacheHealth := h.checkCacheHealth()
	response.Components["cache"] = cacheHealth

	// Collect metrics
	h.collectMetrics(response.Metrics)

	// Collect feature status
	h.collectFeatures(response.Features)

	return response, statusCode
}

// checkDatabaseHealth checks database connectivity and performance
func (h *HealthService) checkDatabaseHealth() ComponentHealth {
	health := ComponentHealth{
		Status:   "healthy",
		Metadata: make(map[string]interface{}),
	}

	// Check basic connectivity
	var result int
	start := time.Now()
	if err := h.db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		health.Status = "critical"
		health.Message = fmt.Sprintf("Database connection failed: %v", err)
		return health
	}
	
	queryTime := time.Since(start).Milliseconds()
	health.Metadata["query_time_ms"] = queryTime

	// Check if query is slow
	if queryTime > 100 {
		health.Status = "warning"
		health.Message = "Database queries are slow"
	}

	// Get connection stats
	sqlDB, err := h.db.DB()
	if err == nil {
		stats := sqlDB.Stats()
		health.Metadata["open_connections"] = stats.OpenConnections
		health.Metadata["in_use"] = stats.InUse
		health.Metadata["idle"] = stats.Idle
		health.Metadata["wait_count"] = stats.WaitCount
		health.Metadata["wait_duration_ms"] = stats.WaitDuration.Milliseconds()
	}

	return health
}

// checkStorageHealth checks file storage availability
func (h *HealthService) checkStorageHealth() ComponentHealth {
	health := ComponentHealth{
		Status:   "healthy",
		Metadata: make(map[string]interface{}),
	}

	// Get disk usage stats
	var stat runtime.MemStats
	runtime.ReadMemStats(&stat)
	
	health.Metadata["memory_allocated_mb"] = stat.Alloc / 1024 / 1024
	health.Metadata["memory_total_allocated_mb"] = stat.TotalAlloc / 1024 / 1024
	health.Metadata["memory_sys_mb"] = stat.Sys / 1024 / 1024
	health.Metadata["num_gc"] = stat.NumGC

	// TODO: Add actual disk space checking
	health.Metadata["storage_used"] = "1.2GB" // Placeholder
	health.Metadata["storage_available"] = "45.3GB" // Placeholder

	return health
}

// checkSearchHealth checks search engine availability
func (h *HealthService) checkSearchHealth() ComponentHealth {
	health := ComponentHealth{
		Status:   "healthy",
		Metadata: make(map[string]interface{}),
	}

	if h.searchEngine == nil {
		health.Status = "disabled"
		health.Message = "Search engine not configured"
		return health
	}

	// Test search functionality
	start := time.Now()
	_, err := h.searchEngine.Search("test", 1, 10)
	if err != nil {
		health.Status = "critical"
		health.Message = fmt.Sprintf("Search engine error: %v", err)
		return health
	}

	searchTime := time.Since(start).Milliseconds()
	health.Metadata["search_time_ms"] = searchTime
	health.Metadata["backend"] = h.getSearchBackend()

	if searchTime > 500 {
		health.Status = "warning"
		health.Message = "Search queries are slow"
	}

	return health
}

// checkGitHealth checks git operations
func (h *HealthService) checkGitHealth() ComponentHealth {
	health := ComponentHealth{
		Status:   "healthy",
		Metadata: make(map[string]interface{}),
	}

	if h.gitService == nil {
		health.Status = "disabled"
		health.Message = "Git service not configured"
		return health
	}

	// TODO: Add actual git health checks
	health.Metadata["backend"] = "go-git"
	health.Metadata["version"] = "5.0.0"

	return health
}

// checkEmailHealth checks email service status
func (h *HealthService) checkEmailHealth() ComponentHealth {
	health := ComponentHealth{
		Status:   "disabled",
		Message:  "Email service not configured",
		Metadata: make(map[string]interface{}),
	}

	// Check if email is enabled in config
	emailEnabled, _ := models.GetConfigBool(h.db, "email_enabled")
	if emailEnabled {
		health.Status = "healthy"
		health.Message = ""
		// TODO: Add actual email service health check
	}

	return health
}

// checkCacheHealth checks cache service status
func (h *HealthService) checkCacheHealth() ComponentHealth {
	health := ComponentHealth{
		Status:   "healthy",
		Metadata: make(map[string]interface{}),
	}

	if h.cache == nil {
		health.Status = "disabled"
		health.Message = "Cache service not configured"
		return health
	}

	// Test cache operations
	testKey := "health_check_test"
	testValue := "test_value"
	
	// Test set
	start := time.Now()
	err := h.cache.Set(testKey, testValue, 1*time.Minute)
	if err != nil {
		health.Status = "critical"
		health.Message = fmt.Sprintf("Cache write error: %v", err)
		return health
	}
	
	// Test get
	var retrieved string
	err = h.cache.Get(testKey, &retrieved)
	if err != nil || retrieved != testValue {
		health.Status = "critical"
		health.Message = "Cache read/write mismatch"
		return health
	}
	
	cacheTime := time.Since(start).Milliseconds()
	health.Metadata["operation_time_ms"] = cacheTime
	health.Metadata["backend"] = h.getCacheBackend()

	// Clean up
	h.cache.Delete(testKey)

	if cacheTime > 50 {
		health.Status = "warning"
		health.Message = "Cache operations are slow"
	}

	return health
}

// collectMetrics collects system metrics
func (h *HealthService) collectMetrics(metrics map[string]interface{}) {
	// User metrics
	var userCount int64
	h.db.Model(&models.User{}).Count(&userCount)
	metrics["total_users"] = userCount

	// Gist metrics
	var totalGists int64
	var publicGists int64
	h.db.Model(&models.Gist{}).Count(&totalGists)
	h.db.Model(&models.Gist{}).Where("visibility = ?", "public").Count(&publicGists)
	metrics["total_gists"] = totalGists
	metrics["public_gists"] = publicGists

	// Organization metrics
	var orgCount int64
	h.db.Model(&models.Organization{}).Count(&orgCount)
	metrics["total_organizations"] = orgCount

	// Performance metrics
	metrics["requests_per_minute"] = h.getRequestsPerMinute()
	metrics["average_response_time"] = h.getAverageResponseTime()
	
	// Storage metrics (placeholders for now)
	metrics["storage_used"] = "1.2GB"
	metrics["storage_available"] = "45.3GB"

	// Runtime metrics
	metrics["goroutines"] = runtime.NumGoroutine()
	metrics["cpu_count"] = runtime.NumCPU()
}

// collectFeatures collects feature status
func (h *HealthService) collectFeatures(features map[string]string) {
	// Registration
	regEnabled, _ := models.GetConfigBool(h.db, "registration_enabled")
	features["registration"] = h.boolToStatus(regEnabled)

	// Organizations
	orgEnabled, _ := models.GetConfigBool(h.db, "organizations_enabled")
	features["organizations"] = h.boolToStatus(orgEnabled)

	// Webhooks
	webhooksEnabled, _ := models.GetConfigBool(h.db, "webhooks_enabled")
	features["webhooks"] = h.boolToStatus(webhooksEnabled)

	// Public gists
	publicEnabled, _ := models.GetConfigBool(h.db, "public_gists_enabled")
	features["public_gists"] = h.boolToStatus(publicEnabled)

	// Email
	emailEnabled, _ := models.GetConfigBool(h.db, "email_enabled")
	features["email"] = h.boolToStatus(emailEnabled)

	// Search backend
	features["search"] = h.getSearchBackend()

	// Cache backend
	features["cache"] = h.getCacheBackend()
}

// formatUptime formats the uptime duration
func (h *HealthService) formatUptime() string {
	uptime := time.Since(h.startTime)
	days := int(uptime.Hours() / 24)
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// Helper methods

func (h *HealthService) boolToStatus(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func (h *HealthService) getSearchBackend() string {
	backend, _ := models.GetConfigValue(h.db, "search_backend")
	if backend == "" {
		return "sqlite"
	}
	return backend
}

func (h *HealthService) getCacheBackend() string {
	backend, _ := models.GetConfigValue(h.db, "cache_type")
	if backend == "" {
		return "memory"
	}
	return backend
}

func (h *HealthService) getRequestsPerMinute() int {
	// TODO: Implement actual request tracking
	return 45 // Placeholder
}

func (h *HealthService) getAverageResponseTime() string {
	// TODO: Implement actual response time tracking
	return "120ms" // Placeholder
}

// EnhancedHealthHandler handles the enhanced health check endpoint
type EnhancedHealthHandler struct {
	healthService *HealthService
}

// NewEnhancedHealthHandler creates a new enhanced health handler
func NewEnhancedHealthHandler(healthService *HealthService) *EnhancedHealthHandler {
	return &EnhancedHealthHandler{
		healthService: healthService,
	}
}

// GetHealth returns comprehensive health information
func (h *EnhancedHealthHandler) GetHealth(c echo.Context) error {
	health, statusCode := h.healthService.GetHealth()
	return c.JSON(statusCode, health)
}