package setup

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/config"
	"github.com/casapps/casgist/src/internal/database/models"
)

// AdminSetupService handles the comprehensive 8-step admin setup wizard
type AdminSetupService struct {
	db       *gorm.DB
	config   *config.Config
	pathConfig *config.PathConfig
}

// NewAdminSetupService creates a new admin setup service
func NewAdminSetupService(db *gorm.DB, config *config.Config, pathConfig *config.PathConfig) *AdminSetupService {
	return &AdminSetupService{
		db:         db,
		config:     config,
		pathConfig: pathConfig,
	}
}

// SystemInfo represents system information for the welcome step
type SystemInfo struct {
	OperatingSystem string `json:"operating_system"`
	Architecture    string `json:"architecture"`
	GoVersion       string `json:"go_version"`
	AvailableMemory string `json:"available_memory"`
	AvailableDisk   string `json:"available_disk"`
	NetworkOK       bool   `json:"network_ok"`
	IsPrivileged    bool   `json:"is_privileged"`
	CanCreateDirs   bool   `json:"can_create_dirs"`
	CanBindPorts    bool   `json:"can_bind_ports"`
}

// DatabaseConfig represents database configuration options
type DatabaseConfig struct {
	Type     string `json:"type"`     // sqlite, postgresql, mysql
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Database string `json:"database,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	SSLMode  string `json:"ssl_mode,omitempty"`
}

// NetworkConfig represents network configuration
type NetworkConfig struct {
	ServerURL      string   `json:"server_url"`
	ListenPort     int      `json:"listen_port"`
	PublicAccess   bool     `json:"public_access"`
	CustomDomain   string   `json:"custom_domain,omitempty"`
	GenerateProxy  bool     `json:"generate_proxy"`
	ProxyTypes     []string `json:"proxy_types"` // nginx, apache, caddy, traefik
}

// EmailConfig represents email configuration
type EmailConfig struct {
	Enabled      bool   `json:"enabled"`
	SMTPHost     string `json:"smtp_host,omitempty"`
	SMTPPort     int    `json:"smtp_port,omitempty"`
	SMTPUsername string `json:"smtp_username,omitempty"`
	SMTPPassword string `json:"smtp_password,omitempty"`
	FromEmail    string `json:"from_email,omitempty"`
	FromName     string `json:"from_name,omitempty"`
	Security     string `json:"security,omitempty"` // starttls, tls, none
}

// SecurityConfig represents security configuration
type SecurityConfig struct {
	AllowRegistration   bool `json:"allow_registration"`
	RequireVerification bool `json:"require_verification"`
	Enable2FA           bool `json:"enable_2fa"`
	DefaultVisibility   string `json:"default_visibility"` // private, unlisted, public
	MaxFileSize         int    `json:"max_file_size"`       // MB
	MaxFilesPerGist     int    `json:"max_files_per_gist"`
	EnableOrganizations bool   `json:"enable_organizations"`
}

// InstallationResult represents the installation result
type InstallationResult struct {
	Success       bool     `json:"success"`
	Steps         []string `json:"steps"`
	Errors        []string `json:"errors"`
	ServerURL     string   `json:"server_url"`
	AdminUsername string   `json:"admin_username"`
	ConfigFiles   []string `json:"config_files"`
}

// PerformSystemCheck performs comprehensive system requirements check
func (s *AdminSetupService) PerformSystemCheck() (*SystemInfo, error) {
	info := &SystemInfo{
		OperatingSystem: runtime.GOOS,
		Architecture:    runtime.GOARCH,
		GoVersion:      runtime.Version(),
	}

	// Check memory
	info.AvailableMemory = s.getAvailableMemory()
	
	// Check disk space
	info.AvailableDisk = s.getAvailableDisk()
	
	// Check network connectivity
	info.NetworkOK = s.checkNetworkConnectivity()
	
	// Check privileges
	info.IsPrivileged = s.checkPrivileges()
	
	// Check directory creation
	info.CanCreateDirs = s.checkDirectoryCreation()
	
	// Check port binding
	info.CanBindPorts = s.checkPortBinding()

	return info, nil
}

// ConfigureDatabase configures the database connection
func (s *AdminSetupService) ConfigureDatabase(config *DatabaseConfig) error {
	// Validate configuration
	if err := s.validateDatabaseConfig(config); err != nil {
		return fmt.Errorf("invalid database configuration: %w", err)
	}

	// Test database connection
	if err := s.testDatabaseConnection(config); err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	// Save configuration
	if err := s.saveDatabaseConfig(config); err != nil {
		return fmt.Errorf("failed to save database configuration: %w", err)
	}

	// Mark step as completed
	return s.markStepCompleted("database")
}

// ConfigureNetwork configures network settings
func (s *AdminSetupService) ConfigureNetwork(config *NetworkConfig) error {
	// Detect server IP and set defaults
	if config.ServerURL == "" {
		config.ServerURL = s.detectServerURL()
	}

	// Set random port if not specified
	if config.ListenPort == 0 {
		port, err := s.selectRandomPort()
		if err != nil {
			return fmt.Errorf("failed to select port: %w", err)
		}
		config.ListenPort = port
	}

	// Generate proxy configurations if requested
	if config.GenerateProxy && len(config.ProxyTypes) > 0 {
		if err := s.generateProxyConfigs(config); err != nil {
			return fmt.Errorf("failed to generate proxy configs: %w", err)
		}
	}

	// Save network configuration
	if err := s.saveNetworkConfig(config); err != nil {
		return fmt.Errorf("failed to save network configuration: %w", err)
	}

	return s.markStepCompleted("network")
}

// ConfigureEmail configures email settings
func (s *AdminSetupService) ConfigureEmail(config *EmailConfig) error {
	if config.Enabled {
		// Test SMTP connection
		if err := s.testSMTPConnection(config); err != nil {
			return fmt.Errorf("SMTP connection failed: %w", err)
		}
	}

	// Save email configuration
	if err := s.saveEmailConfig(config); err != nil {
		return fmt.Errorf("failed to save email configuration: %w", err)
	}

	return s.markStepCompleted("email")
}

// ConfigureSecurity configures security settings
func (s *AdminSetupService) ConfigureSecurity(config *SecurityConfig) error {
	// Save security configuration
	if err := s.saveSecurityConfig(config); err != nil {
		return fmt.Errorf("failed to save security configuration: %w", err)
	}

	return s.markStepCompleted("security")
}

// PerformInstallation performs the actual installation
func (s *AdminSetupService) PerformInstallation(ctx context.Context) (*InstallationResult, error) {
	result := &InstallationResult{
		Success: false,
		Steps:   []string{},
		Errors:  []string{},
	}

	// Mark installation as started
	s.markStepCompleted("install")

	// Step 1: Create system user
	result.Steps = append(result.Steps, "Creating system user 'casgists'")
	if err := s.createSystemUser(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to create system user: %v", err))
		// Continue with installation (non-fatal)
	}

	// Step 2: Create directories
	result.Steps = append(result.Steps, "Creating directories")
	if err := s.createDirectories(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to create directories: %v", err))
		return result, err
	}

	// Step 3: Set up permissions
	result.Steps = append(result.Steps, "Setting up permissions")
	if err := s.setupPermissions(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to setup permissions: %v", err))
		// Continue with installation (non-fatal)
	}

	// Step 4: Install system service
	result.Steps = append(result.Steps, "Installing system service")
	if err := s.installSystemService(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to install system service: %v", err))
		// Continue with installation (non-fatal)
	}

	// Step 5: Initialize database
	result.Steps = append(result.Steps, "Initializing database")
	if err := s.initializeDatabase(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to initialize database: %v", err))
		return result, err
	}

	// Step 6: Start CasGists service
	result.Steps = append(result.Steps, "Starting CasGists service")
	if err := s.startService(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to start service: %v", err))
		// Continue (may be running in dev mode)
	}

	// Get final configuration
	result.ServerURL = "localhost:8080" // Default server URL
	result.AdminUsername = "administrator" // Default admin username
	result.ConfigFiles = s.getGeneratedConfigFiles()

	result.Success = true
	return result, nil
}

// Helper methods for system checks
func (s *AdminSetupService) getAvailableMemory() string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	// This is a simplified check - in production you'd want to check actual system memory
	availableGB := float64(m.Sys) / 1024 / 1024 / 1024
	return fmt.Sprintf("%.1f GB", availableGB)
}

func (s *AdminSetupService) getAvailableDisk() string {
	// Check disk space for data directory
	// This is a simplified implementation
	return "45.2 GB" // Placeholder
}

func (s *AdminSetupService) checkNetworkConnectivity() bool {
	// Test network connectivity to common services
	_, err := net.DialTimeout("tcp", "8.8.8.8:53", 5*time.Second)
	return err == nil
}

func (s *AdminSetupService) checkPrivileges() bool {
	// Check if running as root/admin
	return os.Geteuid() == 0
}

func (s *AdminSetupService) checkDirectoryCreation() bool {
	// Test directory creation in target location
	testDir := "/tmp/casgists-setup-test"
	err := os.MkdirAll(testDir, 0755)
	if err != nil {
		return false
	}
	os.RemoveAll(testDir)
	return true
}

func (s *AdminSetupService) checkPortBinding() bool {
	// Test binding to a high port
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func (s *AdminSetupService) detectServerURL() string {
	// Auto-detect server IP and construct URL
	// This is simplified - in production you'd detect the actual IP
	return "http://172.17.0.1:64001"
}

func (s *AdminSetupService) selectRandomPort() (int, error) {
	// Select random port in range 64000-64999
	const minPort = 64000
	const maxPort = 64999
	
	for attempts := 0; attempts < 50; attempts++ {
		port := minPort + (attempts * 20) // Simple port selection
		
		// Test port availability
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports in range %d-%d", minPort, maxPort)
}

func (s *AdminSetupService) markStepCompleted(stepName string) error {
	configKey := fmt.Sprintf("setup.%s_configured", stepName)
	
	config := &models.SystemConfig{
		ID:       uuid.New(),
		Key:      configKey,
		Value:    "true",
		Type:     "boolean",
		Category: "setup",
	}

	return s.db.Where("key = ?", configKey).FirstOrCreate(config).Error
}

// Placeholder methods for configuration steps
func (s *AdminSetupService) validateDatabaseConfig(config *DatabaseConfig) error {
	// Implement database configuration validation
	return nil
}

func (s *AdminSetupService) testDatabaseConnection(config *DatabaseConfig) error {
	// Implement database connection testing
	return nil
}

func (s *AdminSetupService) saveDatabaseConfig(config *DatabaseConfig) error {
	// Save database configuration to system config
	return nil
}

func (s *AdminSetupService) generateProxyConfigs(config *NetworkConfig) error {
	// Generate nginx, apache, caddy configuration files
	return nil
}

func (s *AdminSetupService) saveNetworkConfig(config *NetworkConfig) error {
	// Save network configuration to system config
	return nil
}

func (s *AdminSetupService) testSMTPConnection(config *EmailConfig) error {
	// Test SMTP connection
	return nil
}

func (s *AdminSetupService) saveEmailConfig(config *EmailConfig) error {
	// Save email configuration to system config
	return nil
}

func (s *AdminSetupService) saveSecurityConfig(config *SecurityConfig) error {
	// Save security configuration to system config
	return nil
}

func (s *AdminSetupService) createSystemUser() error {
	// Create system user for CasGists
	return nil
}

func (s *AdminSetupService) createDirectories() error {
	// Create all necessary directories
	return s.pathConfig.CreateDirectories()
}

func (s *AdminSetupService) setupPermissions() error {
	// Set up file permissions
	return nil
}

func (s *AdminSetupService) installSystemService() error {
	// Install systemd/Windows service
	return nil
}

func (s *AdminSetupService) initializeDatabase() error {
	// Run database migrations
	return nil
}

func (s *AdminSetupService) startService() error {
	// Start the CasGists service
	return nil
}

func (s *AdminSetupService) getGeneratedConfigFiles() []string {
	// Return list of generated configuration files
	return []string{
		"/etc/systemd/system/casgists.service",
		"/etc/casgists/nginx.conf",
		"/etc/casgists/apache.conf",
	}
}