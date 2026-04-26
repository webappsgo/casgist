package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all configuration values
type Config struct {
	v *viper.Viper
}

// Load loads configuration from environment variables and config files
func Load() (*viper.Viper, error) {
	v := viper.New()

	// Set config type
	v.SetConfigType("yaml")

	// Set environment variable prefix
	v.SetEnvPrefix("CASGISTS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set defaults
	setDefaults(v)

	// Resolve paths with variable substitution
	resolvePaths(v)

	// Load config file if exists
	configPaths := []string{
		v.GetString("paths.config"),
		".",
		"/etc/casgists",
	}

	for _, path := range configPaths {
		v.AddConfigPath(path)
	}
	v.SetConfigName("server")

	// Read config file (ignore if not found)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Generate secret key if not set
	if v.GetString("security.secret_key") == "" {
		key, err := generateSecretKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate secret key: %w", err)
		}
		v.Set("security.secret_key", key)
	}

	return v, nil
}

func setDefaults(v *viper.Viper) {
	// Path defaults
	if runtime.GOOS == "windows" {
		v.SetDefault("paths.data", expandPath("%PROGRAMDATA%\\casgists"))
		v.SetDefault("paths.logs", expandPath("%PROGRAMDATA%\\casgists\\logs"))
		v.SetDefault("paths.cache", expandPath("%PROGRAMDATA%\\casgists\\cache"))
		v.SetDefault("paths.temp", expandPath("%TEMP%\\casgists"))
		v.SetDefault("paths.config", expandPath("%PROGRAMDATA%\\casgists\\config"))
	} else {
		v.SetDefault("paths.data", "/var/lib/casgists")
		v.SetDefault("paths.logs", "/var/log/casgists")
		v.SetDefault("paths.cache", "/var/cache/casgists")
		v.SetDefault("paths.temp", "/tmp/casgists")
		v.SetDefault("paths.config", "/etc/casgists")
	}

	// Database defaults
	v.SetDefault("database.type", "sqlite")
	v.SetDefault("database.path", "{paths.data}/data.db")
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.name", "casgists")
	v.SetDefault("database.user", "casgists")
	v.SetDefault("database.password", "")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime", "300s")

	// Server defaults
	v.SetDefault("server.port", 0) // 0 = random port selection
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.url", "") // Auto-detect if empty
	v.SetDefault("server.tls.enabled", false)
	v.SetDefault("server.tls.cert_path", "")
	v.SetDefault("server.tls.key_path", "")
	v.SetDefault("server.tls.auto_cert", false)

	// Security defaults
	v.SetDefault("security.secret_key", "")
	v.SetDefault("security.jwt.access_token_ttl", "2h")
	v.SetDefault("security.jwt.refresh_token_ttl", "72h")
	v.SetDefault("security.session.max_concurrent", 5)
	v.SetDefault("security.session.idle_timeout", "8h")
	v.SetDefault("security.password.min_length", 12)
	v.SetDefault("security.password.require_uppercase", true)
	v.SetDefault("security.password.require_lowercase", true)
	v.SetDefault("security.password.require_numbers", true)
	v.SetDefault("security.password.require_symbols", false)

	// Rate limiting defaults
	v.SetDefault("ratelimit.authenticated_api", 1000)
	v.SetDefault("ratelimit.anonymous_api", 100)
	v.SetDefault("ratelimit.login_attempts", 5)
	v.SetDefault("ratelimit.login_window", 15)
	v.SetDefault("ratelimit.gist_creation", 50)
	v.SetDefault("ratelimit.comment_creation", 100)
	v.SetDefault("ratelimit.search_requests", 200)

	// Feature flags
	v.SetDefault("features.registration", true)
	v.SetDefault("features.organizations", true)
	v.SetDefault("features.social", true)
	v.SetDefault("features.search", true)
	v.SetDefault("features.webhooks", true)
	v.SetDefault("features.api", true)

	// Email defaults
	v.SetDefault("email.enabled", false)
	v.SetDefault("email.smtp.host", "")
	v.SetDefault("email.smtp.port", 587)
	v.SetDefault("email.smtp.username", "")
	v.SetDefault("email.smtp.password", "")
	v.SetDefault("email.smtp.tls", true)
	v.SetDefault("email.from.address", "")
	v.SetDefault("email.from.name", "CasGists")

	// Storage defaults
	v.SetDefault("storage.type", "local")
	v.SetDefault("storage.path", "{paths.data}/files")
	v.SetDefault("storage.max_file_size", 5242880) // 5MB
	v.SetDefault("storage.max_files_per_gist", 100)
	v.SetDefault("storage.max_total_size", 26214400) // 25MB

	// Search defaults
	v.SetDefault("search.backend", "sqlite") // sqlite or redis
	v.SetDefault("search.redis.host", "localhost")
	v.SetDefault("search.redis.port", 6379)
	v.SetDefault("search.redis.password", "")
	v.SetDefault("search.redis.db", 0)

	// Cache defaults
	v.SetDefault("cache.type", "memory") // memory or redis
	v.SetDefault("cache.ttl", "5m")
	v.SetDefault("cache.max_entries", 1000)

	// UI defaults
	v.SetDefault("ui.theme", "dracula")
	v.SetDefault("ui.language", "en")
	v.SetDefault("ui.title", "CasGists")
	v.SetDefault("ui.description", "Self-hosted Git snippet manager")
	v.SetDefault("ui.footer", "Powered by CasGists")

	// Backup defaults
	v.SetDefault("backup.enabled", true)
	v.SetDefault("backup.schedule", "weekly")
	v.SetDefault("backup.time", "02:00")
	v.SetDefault("backup.retention", 4)
	v.SetDefault("backup.path", "{paths.data}/backups")
	v.SetDefault("backup.encrypt", true)

	// Compliance defaults
	v.SetDefault("compliance.audit_logs", true)
	v.SetDefault("compliance.gdpr", false)
	v.SetDefault("compliance.soc2", false)
	v.SetDefault("compliance.hipaa", false)
	v.SetDefault("compliance.retention_days", 90)
}

func resolvePaths(v *viper.Viper) {
	// First pass: expand environment variables and home directory
	for _, key := range []string{
		"paths.data",
		"paths.logs",
		"paths.cache",
		"paths.temp",
		"paths.config",
		"paths.backup",
		"database.path",
	} {
		if path := v.GetString(key); path != "" {
			v.Set(key, expandPath(path))
		}
	}
	
	// Second pass: substitute path variables like ${DATA_DIR}
	for _, key := range v.AllKeys() {
		value := v.GetString(key)
		
		// Check if value contains ${VAR} pattern
		if strings.Contains(value, "${") && strings.Contains(value, "}") {
			resolved := value
			
			// Replace path variables
			resolved = strings.ReplaceAll(resolved, "${DATA_DIR}", v.GetString("paths.data"))
			resolved = strings.ReplaceAll(resolved, "${CONFIG_DIR}", v.GetString("paths.config"))
			resolved = strings.ReplaceAll(resolved, "${LOG_DIR}", v.GetString("paths.logs"))
			resolved = strings.ReplaceAll(resolved, "${BACKUP_DIR}", v.GetString("paths.backup"))
			resolved = strings.ReplaceAll(resolved, "${CACHE_DIR}", v.GetString("paths.cache"))
			resolved = strings.ReplaceAll(resolved, "${TEMP_DIR}", v.GetString("paths.temp"))
			
			v.Set(key, resolved)
		}
		
		// Also check for {var} pattern (existing functionality)
		if strings.Contains(value, "{") && strings.Contains(value, "}") {
			resolved := value
			
			// Replace all {var} patterns
			for _, varKey := range v.AllKeys() {
				varPattern := fmt.Sprintf("{%s}", varKey)
				if strings.Contains(resolved, varPattern) {
					varValue := v.GetString(varKey)
					resolved = strings.ReplaceAll(resolved, varPattern, varValue)
				}
			}
			
			// Expand environment variables
			resolved = expandPath(resolved)
			
			v.Set(key, resolved)
		}
	}
}

func expandPath(path string) string {
	// Expand environment variables
	path = os.ExpandEnv(path)
	
	// Expand home directory
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = strings.Replace(path, "~", home, 1)
		}
	}
	
	// Clean the path
	return filepath.Clean(path)
}

func generateSecretKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// ValidateConfig validates the configuration
func ValidateConfig(v *viper.Viper) error {
	// Validate database configuration
	dbType := v.GetString("database.type")
	switch dbType {
	case "sqlite":
		if v.GetString("database.path") == "" {
			return fmt.Errorf("database.path is required for SQLite")
		}
	case "postgresql", "mysql":
		if v.GetString("database.host") == "" {
			return fmt.Errorf("database.host is required for %s", dbType)
		}
		if v.GetString("database.user") == "" {
			return fmt.Errorf("database.user is required for %s", dbType)
		}
	default:
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	// Validate server configuration
	port := v.GetInt("server.port")
	if port < 0 || port > 65535 {
		return fmt.Errorf("invalid server port: %d", port)
	}

	// Validate security configuration
	if v.GetString("security.secret_key") == "" {
		return fmt.Errorf("security.secret_key is required")
	}

	// Validate email configuration if enabled
	if v.GetBool("email.enabled") {
		if v.GetString("email.smtp.host") == "" {
			return fmt.Errorf("email.smtp.host is required when email is enabled")
		}
		if v.GetString("email.from.address") == "" {
			return fmt.Errorf("email.from.address is required when email is enabled")
		}
	}

	return nil
}

// LoadWithPaths loads configuration using the provided path configuration
func LoadWithPaths(pathConfig *PathConfig) (*viper.Viper, error) {
	v := viper.New()

	// Set config type
	v.SetConfigType("yaml")

	// Set environment variable prefix
	v.SetEnvPrefix("CASGISTS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set path-based defaults
	setPathDefaults(v, pathConfig)

	// Set other defaults
	setDefaults(v)

	// Try to load config file if it exists, but don't require it
	configPath := filepath.Join(filepath.Dir(pathConfig.GetDatabasePath()), "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		// Config file exists, try to read it
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}
	// If no config file exists, that's fine - use environment variables and defaults

	// Generate secret key if not set
	if v.GetString("security.secret_key") == "" {
		key, err := generateSecretKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate secret key: %w", err)
		}
		v.Set("security.secret_key", key)
	}

	return v, nil
}

// setPathDefaults sets path-specific configuration defaults
func setPathDefaults(v *viper.Viper, pathConfig *PathConfig) {
	v.SetDefault("database.path", pathConfig.GetDatabasePath())
	v.SetDefault("storage.path", pathConfig.GetStoragePath())
	v.SetDefault("backup.path", pathConfig.GetBackupDir())
	v.SetDefault("ssl.cert_path", pathConfig.GetTLSCertPath())
	v.SetDefault("ssl.key_path", pathConfig.GetTLSKeyPath())
}

// SelectRandomPort selects a random port in the high range (64000-64999) as per SPEC
func SelectRandomPort() (int, error) {
	const minPort = 64000
	const maxPort = 64999

	for attempts := 0; attempts < 50; attempts++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(maxPort-minPort+1)))
		if err != nil {
			return 0, fmt.Errorf("failed to generate random number: %w", err)
		}
		
		port := int(n.Int64()) + minPort

		// Test port availability
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports in range %d-%d", minPort, maxPort)
}