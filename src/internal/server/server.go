package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/api/v1"
	echoMiddleware "github.com/casapps/casgist/src/internal/api/middleware"
	"github.com/casapps/casgist/src/internal/auth"
	"github.com/casapps/casgist/src/internal/cache"
	"github.com/casapps/casgist/src/internal/config"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/email"
	"github.com/casapps/casgist/src/internal/git"
	// "github.com/casapps/casgist/src/internal/handlers/public" // Temporarily disabled
	// "github.com/casapps/casgist/src/internal/handlers/setup" // Temporarily disabled
	"github.com/casapps/casgist/src/internal/performance"
	// "github.com/casapps/casgist/src/internal/repositories" // Temporarily disabled
	"github.com/casapps/casgist/src/internal/search"
	"github.com/casapps/casgist/src/internal/webhook"
	// "github.com/casapps/casgist/src/internal/services" // Temporarily disabled
	// setupPkg "github.com/casapps/casgist/src/internal/setup" // Temporarily disabled
)

// Server represents the main application server
type Server struct {
	echo            *echo.Echo
	config          *viper.Viper
	db              *gorm.DB
	cache           *cache.CacheManager
	emailService    *email.Service
	emailProcessor  *email.Processor
	pathConfig      *config.PathConfig
	portManager     *PortManager
	healthService   *v1.HealthService
	networkDetector *NetworkDetector
	auth            *auth.AuthService
	searchManager   *search.Manager
	webhookManager  *webhook.Manager
	startTime       time.Time
}

// New creates a new server instance (legacy - use NewWithPaths)
func New(e *echo.Echo, cfg *viper.Viper, db *gorm.DB) *Server {
	return NewWithPaths(e, cfg, db, nil)
}

// NewWithPaths creates a new server instance with path configuration
func NewWithPaths(e *echo.Echo, cfg *viper.Viper, db *gorm.DB, pathConfig *config.PathConfig) *Server {
	// Initialize system config
	if err := models.InitializeSystemConfig(db); err != nil {
		e.Logger.Warnf("Failed to initialize system config: %v", err)
	}

	// Initialize port manager
	portManager := NewPortManager(db)

	// Initialize cache manager
	cacheManager := cache.NewCacheManager(cfg)
	
	// Initialize email service
	emailService := email.NewService(db, cfg)
	emailProcessor := email.NewProcessor(emailService, cfg)
	
	// Initialize search manager
	searchManager, err := search.NewManager(db, "sqlite_fts", nil)
	if err != nil {
		log.Fatalf("Failed to initialize search manager: %v", err)
	}
	
	// Initialize git service
	gitService := git.NewService(cfg)
	
	// Initialize cache service
	cacheService := cache.NewMemoryCacheService()
	
	// Initialize health service
	healthService := v1.NewHealthService(db, cacheService, nil, gitService)
	
	// Initialize network detector
	networkDetector := NewNetworkDetector()
	
	// Initialize auth service
	authService := auth.NewAuthService(
		cfg.GetString("security.secret_key"),
		cfg.GetString("app.name"),
	)
	
	// Initialize webhook manager
	webhookWorkers := cfg.GetInt("webhook.workers")
	if webhookWorkers == 0 {
		webhookWorkers = 5
	}
	webhookManager := webhook.NewManager(db, webhookWorkers)
	
	// Initialize performance optimizer
	optimizer := performance.NewOptimizer(db, cfg)
	if err := optimizer.OptimizeDatabase(); err != nil {
		// Log warning but don't fail startup
		e.Logger.Warnf("Failed to optimize database: %v", err)
	}
	
	s := &Server{
		echo:            e,
		config:          cfg,
		db:              db,
		cache:           cacheManager,
		emailService:    emailService,
		emailProcessor:  emailProcessor,
		pathConfig:      pathConfig,
		portManager:     portManager,
		healthService:   healthService,
		networkDetector: networkDetector,
		auth:            authService,
		searchManager:   searchManager,
		webhookManager:  webhookManager,
		startTime:       time.Now(),
	}

	// Setup validator
	e.Validator = NewEchoValidator()
	
	s.setupMiddleware()
	s.setupRoutes()
	
	// Setup templates
	fmt.Println("Setting up templates...")
	if err := s.setupTemplates(); err != nil {
		fmt.Printf("Failed to setup templates: %v\n", err)
	} else {
		fmt.Println("Templates setup successfully")
	}

	return s
}

// Start starts the server and background services
func (s *Server) Start(ctx context.Context, address string) error {
	// Start email processor in background
	if s.config.GetBool("email.enabled") {
		go s.emailProcessor.Start(ctx)
	}
	
	// Start webhook workers
	if s.config.GetBool("webhook.enabled") {
		go s.webhookManager.Start(ctx)
	}
	
	return s.echo.Start(address)
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	// Stop email processor
	if s.emailProcessor != nil {
		s.emailProcessor.Stop()
	}
	
	return s.echo.Shutdown(ctx)
}

func (s *Server) setupMiddleware() {
	// Pretty console logging + Apache format file logging
	s.echo.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		// Pretty console format
		Format: "  ${time_rfc3339} | ${status} | ${latency_human} | ${method} ${uri}\n",
		Output: s.getConsoleWriter(),
	}))

	// Apache format to access.log file only
	s.echo.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: `${remote_ip} - - [${time_custom}] "${method} ${uri} ${protocol}" ${status} ${bytes_out}` + "\n",
		CustomTimeFormat: "02/Jan/2006:15:04:05 -0700",
		Output: s.getAccessLogWriter(),
		Skipper: func(c echo.Context) bool {
			// Skip if no path config (don't write to file)
			return s.pathConfig == nil
		},
	}))
	s.echo.Use(middleware.Recover())
	s.echo.Use(middleware.RequestID())

	// Performance middleware
	s.echo.Use(performance.CompressionMiddleware(s.config))
	s.echo.Use(performance.CacheControlMiddleware())
	s.echo.Use(performance.ResponseBufferingMiddleware(10 * 1024)) // 10KB buffer

	// CORS middleware
	s.echo.Use(echoMiddleware.CORS(s.config))

	// Security middleware
	s.echo.Use(echoMiddleware.Security(s.config))
	
	// CSRF middleware
	s.echo.Use(echoMiddleware.CSRF(s.config))

	// Rate limiting middleware
	s.echo.Use(echoMiddleware.RateLimit(s.config))

	// Custom middleware
	s.echo.Use(echoMiddleware.DatabaseInjector(s.db))
	s.echo.Use(echoMiddleware.ConfigInjector(s.config))
}

// Note: setupRoutes is now in routes.go

func (s *Server) setupSetupRoutes() {
	// TODO: Re-enable after fixing setup service
	// Create setup service dependencies
	// setupService, err := s.createSetupService()
	// if err != nil {
	// 	s.echo.Logger.Errorf("Failed to create setup service: %v", err)
	// 	return
	// }

	// Create setup handler
	// setupHandler := setup.NewWizardHandler(setupService)

	// Setup route group
	setupGroup := s.echo.Group("/setup")

	// TODO: Re-enable after fixing setup handlers
	// Setup wizard routes
	// setupGroup.GET("", setupHandler.ShowSetupWizard)
	// setupGroup.GET("/status", setupHandler.GetSetupStatus)
	// setupGroup.GET("/admin-account", setupHandler.ShowAdminAccountCreation)
	// setupGroup.POST("/admin-account", setupHandler.CreateAdminAccount)
	// setupGroup.GET("/wizard", setupHandler.ShowSetupWizard)
	// setupGroup.GET("/wizard/steps", setupHandler.GetWizardSteps)
	// setupGroup.GET("/wizard/step/:step", setupHandler.ShowWizardStep)
	// setupGroup.POST("/wizard/step/:step", setupHandler.ProcessWizardStep)
	
	// First user flow routes
	setupGroup.GET("/first-user", s.showFirstUserPage)
}

// func (s *Server) createSetupService() (*services.SetupService, error) {
// 	// Create repository
// 	userRepo := repositories.NewUserRepository(s.db)
// 	
// 	// Note: Using simplified auth service
// 	authService := auth.NewAuthService("temp-secret", "casgists")
// 	
// 	// Note: Setup service needs to be updated to use new auth system
// 	// For now, return nil to fix compilation
// 	return nil, fmt.Errorf("setup service not implemented")
// }

func (s *Server) setupStaticRoutes() {
	// Serve static files (embedded or from disk for development)
	// In production, files are embedded; in development, they're served from disk
	s.echo.Static("/static", "src/web/static")
	
	// Serve specific files
	s.echo.GET("/favicon.ico", s.favicon)
	s.echo.GET("/robots.txt", s.robots)
	s.echo.GET("/manifest.json", s.manifest)
	s.echo.GET("/service-worker.js", s.serviceWorker)
	s.echo.GET("/sw.js", s.serviceWorker) // Alternative path
	s.echo.GET("/.well-known/security.txt", s.securityTxt)
}

// setupTemplates configures the template renderer
func (s *Server) setupTemplates() error {
	templatesPath := filepath.Join("src", "web", "templates")
	debug := s.config.GetBool("debug")
	
	renderer, err := NewTemplateRenderer(templatesPath, debug)
	if err != nil {
		return fmt.Errorf("failed to create template renderer: %w", err)
	}
	
	s.echo.Renderer = renderer
	return nil
}

// registerDocumentationRoutes registers API documentation routes
func (s *Server) registerDocumentationRoutes(api *echo.Group) {
	// Create documentation handler
	docsHandler := v1.NewDocsHandler()
	
	// Register documentation routes
	docsHandler.RegisterRoutes(api)
}

// registerSetupAPIRoutes registers setup-related API routes
func (s *Server) registerSetupAPIRoutes(api *echo.Group) {
	// TODO: Re-enable after fixing setup services
	// Note: Using simplified auth service for now
	// authService := auth.NewAuthService("temp-secret", "casgists")
	// adminAccountService := setupPkg.NewAdminAccountService(s.db, authService)
	// wizardService := setupPkg.NewWizardService(s.db)
	
	// Create setup API handler
	// setupHandler := v1.NewSetupHandler(adminAccountService, wizardService)
	
	// Register routes
	// setupHandler.RegisterRoutes(api.Group("/setup"))
}

// showFirstUserPage shows the first user admin account creation page
func (s *Server) showFirstUserPage(c echo.Context) error {
	// TODO: Re-enable after implementing setup service
	// Check if this is actually the first user
	// authService := auth.NewAuthService("temp-secret", "casgists")
	// adminAccountService := setupPkg.NewAdminAccountService(s.db, authService)
	// isFirst, err := adminAccountService.IsFirstUser()
	// if err != nil || !isFirst {
	// 	// Redirect to home if not first user
	// 	return c.Redirect(http.StatusFound, "/")
	// }
	
	// For now, just return a simple response
	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "First user setup not yet implemented",
	})
}

func (s *Server) getSystemStatus() *HealthStatus {
	status := &HealthStatus{
		Status:    "healthy",
		Version:   s.config.GetString("version"),
		Timestamp: "2024-01-01T00:00:00Z", // TODO: Use actual timestamp
		Components: map[string]string{
			"database": "healthy",
			"storage":  "healthy",
			"search":   "healthy",
			"git":      "healthy",
			"email":    "disabled",
		},
	}

	// Check database connectivity
	if sqlDB, err := s.db.DB(); err != nil || sqlDB.Ping() != nil {
		status.Components["database"] = "critical"
		status.Status = "unhealthy"
	}

	// TODO: Add other health checks

	return status
}

// Static file handlers
func (s *Server) favicon(c echo.Context) error {
	// Try to serve favicon from static directory
	return c.File("src/web/static/favicon.ico")
}

func (s *Server) robots(c echo.Context) error {
	content := `User-agent: *
Allow: /
Disallow: /api/
Disallow: /admin/
Disallow: /user/settings/

Sitemap: ` + s.config.GetString("server.url") + `/sitemap.xml`
	return c.String(http.StatusOK, content)
}

func (s *Server) manifest(c echo.Context) error {
	// Get the best URL for this request
	baseURL := s.networkDetector.GetBestURL(c, s.config.GetInt("server.port"))
	
	manifest := map[string]interface{}{
		"name":             s.config.GetString("ui.title"),
		"short_name":       "CasGists",
		"description":      s.config.GetString("ui.description"),
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#1f2937",
		"theme_color":      "#3b82f6",
		"categories":       []string{"productivity", "developer tools"},
		"icons": []map[string]interface{}{
			{
				"src":     baseURL + "/static/icons/icon-192x192.png",
				"sizes":   "192x192",
				"type":    "image/png",
				"purpose": "any maskable",
			},
			{
				"src":     baseURL + "/static/icons/icon-512x512.png",
				"sizes":   "512x512",
				"type":    "image/png",
				"purpose": "any maskable",
			},
		},
		"shortcuts": []map[string]interface{}{
			{
				"name":        "New Gist",
				"short_name":  "New",
				"description": "Create a new gist",
				"url":         "/new",
				"icons": []map[string]interface{}{
					{
						"src":   baseURL + "/static/icons/icon-192x192.png",
						"sizes": "192x192",
					},
				},
			},
		},
	}
	return c.JSON(http.StatusOK, manifest)
}

func (s *Server) securityTxt(c echo.Context) error {
	baseURL := s.networkDetector.GetBestURL(c, s.config.GetInt("server.port"))
	content := `Contact: ` + s.config.GetString("contact.security_email") + `
Expires: 2025-01-01T00:00:00.000Z
Preferred-Languages: en
Canonical: ` + baseURL + `/.well-known/security.txt`
	return c.String(http.StatusOK, content)
}

func (s *Server) serviceWorker(c echo.Context) error {
	// Set headers for service worker
	c.Response().Header().Set("Content-Type", "application/javascript; charset=utf-8")
	c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Response().Header().Set("Service-Worker-Allowed", "/")

	// Try to serve from file first
	return c.File("src/web/static/service-worker.js")
}

// HealthStatus represents the system health status
type HealthStatus struct {
	Status     string            `json:"status"`
	Version    string            `json:"version"`
	Uptime     string            `json:"uptime,omitempty"`
	Timestamp  string            `json:"timestamp"`
	Components map[string]string `json:"components"`
	Metrics    map[string]interface{} `json:"metrics,omitempty"`
	Features   map[string]interface{} `json:"features,omitempty"`
}

// getConsoleWriter returns stdout for pretty console logging
func (s *Server) getConsoleWriter() io.Writer {
	return os.Stdout
}

// getAccessLogWriter returns file writer for Apache format access logs
func (s *Server) getAccessLogWriter() io.Writer {
	logDir := s.getLogDir()
	accessLogPath := filepath.Join(logDir, "access.log")

	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("Warning: Failed to create log directory %s: %v", logDir, err)
		return io.Discard
	}

	// Open log file in append mode
	logFile, err := os.OpenFile(accessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Warning: Failed to open access log %s: %v", accessLogPath, err)
		return io.Discard
	}

	log.Printf("✓ Access logging: %s", accessLogPath)
	return logFile
}

// getServerLogWriter returns file writer for server event logs
func (s *Server) getServerLogWriter() io.Writer {
	logDir := s.getLogDir()
	serverLogPath := filepath.Join(logDir, "server.log")

	// Create log directory if needed
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return io.Discard
	}

	// Open server log file
	logFile, err := os.OpenFile(serverLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return io.Discard
	}

	log.Printf("✓ Server logging: %s", serverLogPath)
	return logFile
}

// getLogDir returns the log directory path
func (s *Server) getLogDir() string {
	if s.pathConfig != nil {
		logDir := s.pathConfig.GetLogDir()
		if logDir != "" {
			return logDir
		}
	}
	// Default fallback
	return "/var/log/casgists"
}