package server

import (
	"fmt"
	"math/rand"
	"net"
	"time"

	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// PortManager handles dynamic port selection and management
type PortManager struct {
	db       *gorm.DB
	minPort  int
	maxPort  int
	fallback int
}

// NewPortManager creates a new port manager
func NewPortManager(db *gorm.DB) *PortManager {
	return &PortManager{
		db:       db,
		minPort:  64000,
		maxPort:  64999,
		fallback: 3000, // Standard fallback port
	}
}

// GetConfiguredPort gets the configured port from database or selects a new one
func (m *PortManager) GetConfiguredPort() (int, error) {
	// Check if port is already configured in database
	var config models.SystemConfig
	if err := m.db.Where("key = ?", "server_port").First(&config).Error; err == nil {
		// Port already configured
		port := 0
		if _, err := fmt.Sscanf(config.Value, "%d", &port); err == nil && port > 0 {
			// Verify port is still available
			if m.isPortAvailable(port) {
				return port, nil
			}
			// Port no longer available, select a new one
		}
	}

	// Select a new port
	port, err := m.SelectRandomPort()
	if err != nil {
		// Fall back to standard port
		if m.isPortAvailable(m.fallback) {
			port = m.fallback
		} else {
			return 0, fmt.Errorf("no available ports found")
		}
	}

	// Save selected port to database
	if err := m.savePortConfig(port); err != nil {
		// Log error but continue with selected port
		fmt.Printf("Warning: failed to save port configuration: %v\n", err)
	}

	return port, nil
}

// SelectRandomPort selects a random available port in the configured range
func (m *PortManager) SelectRandomPort() (int, error) {
	rand.Seed(time.Now().UnixNano())
	
	// Try up to 50 times to find an available port
	for attempts := 0; attempts < 50; attempts++ {
		port := rand.Intn(m.maxPort-m.minPort+1) + m.minPort
		
		if m.isPortAvailable(port) {
			return port, nil
		}
	}
	
	// If random selection fails, try sequential scan
	for port := m.minPort; port <= m.maxPort; port++ {
		if m.isPortAvailable(port) {
			return port, nil
		}
	}
	
	return 0, fmt.Errorf("no available ports in range %d-%d", m.minPort, m.maxPort)
}

// isPortAvailable checks if a port is available for binding
func (m *PortManager) isPortAvailable(port int) bool {
	// Try to listen on the port
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	
	// Also check UDP
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	
	return true
}

// savePortConfig saves the selected port to the database
func (m *PortManager) savePortConfig(port int) error {
	config := models.SystemConfig{
		Key:       "server_port",
		Value:     fmt.Sprintf("%d", port),
		UpdatedAt: time.Now(),
	}
	
	// Upsert the configuration
	return m.db.Where("key = ?", "server_port").
		Assign(config).
		FirstOrCreate(&config).Error
}

// UpdatePort updates the configured port
func (m *PortManager) UpdatePort(port int) error {
	if !m.isPortAvailable(port) {
		return fmt.Errorf("port %d is not available", port)
	}
	
	return m.savePortConfig(port)
}

// GetPortInfo returns information about the current port configuration
func (m *PortManager) GetPortInfo() map[string]interface{} {
	var config models.SystemConfig
	currentPort := 0
	
	if err := m.db.Where("key = ?", "server_port").First(&config).Error; err == nil {
		fmt.Sscanf(config.Value, "%d", &currentPort)
	}
	
	return map[string]interface{}{
		"current_port":  currentPort,
		"min_port":      m.minPort,
		"max_port":      m.maxPort,
		"fallback_port": m.fallback,
		"is_available":  currentPort > 0 && m.isPortAvailable(currentPort),
	}
}