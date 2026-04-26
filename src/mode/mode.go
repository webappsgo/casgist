package mode

import (
	"os"
	"strings"
)

// Mode represents the application mode
type Mode string

const (
	Production  Mode = "production"
	Development Mode = "development"
)

// Get returns the current mode based on priority:
// 1. CLI flag (passed as argument)
// 2. MODE environment variable
// 3. Default: production
func Get(cliMode string) Mode {
	// Priority 1: CLI flag
	if cliMode != "" {
		return parseMode(cliMode)
	}
	
	// Priority 2: Environment variable
	if envMode := os.Getenv("MODE"); envMode != "" {
		return parseMode(envMode)
	}
	
	// Default: production
	return Production
}

// parseMode normalizes mode string
func parseMode(s string) Mode {
	s = strings.ToLower(strings.TrimSpace(s))
	
	switch s {
	case "dev", "development":
		return Development
	case "prod", "production":
		return Production
	default:
		return Production // Safe default
	}
}

// IsProduction returns true if mode is production
func (m Mode) IsProduction() bool {
	return m == Production
}

// IsDevelopment returns true if mode is development
func (m Mode) IsDevelopment() bool {
	return m == Development
}

// String returns the string representation
func (m Mode) String() string {
	return string(m)
}

// IsDebug returns true if debug is enabled based on priority:
// 1. CLI flag (passed as argument)
// 2. DEBUG environment variable
// 3. Default: false
func IsDebug(cliDebug bool) bool {
	// Priority 1: CLI flag
	if cliDebug {
		return true
	}
	
	// Priority 2: Environment variable
	if debug := os.Getenv("DEBUG"); debug != "" {
		// Import config package for ParseBool would create circular dependency
		// So we check common truthy values directly here
		debug = strings.ToLower(strings.TrimSpace(debug))
		switch debug {
		case "1", "yes", "true", "on", "enable", "enabled":
			return true
		}
	}
	
	// Default: false
	return false
}
