package setup

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// WizardService handles the setup wizard flow
type WizardService struct {
	db *gorm.DB
}

// NewWizardService creates a new setup wizard service
func NewWizardService(db *gorm.DB) *WizardService {
	return &WizardService{
		db: db,
	}
}

// WizardStep represents a step in the setup wizard
type WizardStep struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
	Current     bool   `json:"current"`
}

// WizardStatus represents the overall wizard status
type WizardStatus struct {
	CurrentStep   int           `json:"current_step"`
	TotalSteps    int           `json:"total_steps"`
	IsCompleted   bool          `json:"is_completed"`
	Steps         []WizardStep  `json:"steps"`
	CompletedAt   *time.Time    `json:"completed_at,omitempty"`
}

// GetWizardStatus returns the current setup wizard status
func (s *WizardService) GetWizardStatus() (*WizardStatus, error) {
	// Get system configuration to check what's been completed
	var configs []models.SystemConfig
	if err := s.db.Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("failed to get system config: %w", err)
	}

	configMap := make(map[string]string)
	for _, config := range configs {
		configMap[config.Key] = config.Value
	}

	// Define wizard steps (matching SPEC requirements)
	steps := []WizardStep{
		{ID: 1, Name: "welcome", Title: "Welcome and System Check", Description: "Verify system requirements"},
		{ID: 2, Name: "database", Title: "Database Configuration", Description: "Choose and configure database"},
		{ID: 3, Name: "network", Title: "Network Configuration", Description: "Configure server address and ports"},
		{ID: 4, Name: "email", Title: "Email Configuration", Description: "Setup email notifications (optional)"},
		{ID: 5, Name: "security", Title: "Security and Features", Description: "Configure security settings"},
		{ID: 6, Name: "review", Title: "Review and Install", Description: "Review configuration and install"},
		{ID: 7, Name: "install", Title: "Installation Progress", Description: "Installing CasGists components"},
		{ID: 8, Name: "complete", Title: "Setup Complete", Description: "Installation successful"},
	}

	// Check completion status for each step
	currentStep := 1
	completedSteps := 0
	
	// Step 1: Welcome - Check if system check is done
	if configMap["setup.welcome_configured"] == "true" {
		steps[0].Completed = true
		completedSteps++
		currentStep = 2
	}

	// Step 2: Database - Check if database is configured and migrations run
	var userCount int64
	s.db.Model(&models.User{}).Count(&userCount)
	if userCount >= 0 && configMap["setup.database_configured"] == "true" {
		steps[1].Completed = true
		completedSteps++
		if currentStep == 2 {
			currentStep = 3
		}
	}

	// Step 3-6: Check configuration values for remaining setup steps
	configSteps := []struct {
		stepIndex int
		configKey string
	}{
		{2, "setup.network_configured"},   // Network configuration
		{3, "setup.email_configured"},     // Email configuration
		{4, "setup.security_configured"},  // Security and features
		{5, "setup.review_configured"},    // Review and install
	}

	for _, cs := range configSteps {
		if configMap[cs.configKey] == "true" {
			steps[cs.stepIndex].Completed = true
			completedSteps++
			if currentStep == cs.stepIndex+1 {
				currentStep = cs.stepIndex + 2
			}
		}
	}

	// Step 7: Installation - Check if installation is in progress or completed
	if configMap["setup.install_started"] == "true" {
		steps[6].Completed = true
		completedSteps++
		if currentStep == 7 {
			currentStep = 8
		}
	}

	// Step 8: Complete - Check if setup is marked complete
	isCompleted := configMap["setup.completed"] == "true"
	if isCompleted {
		steps[7].Completed = true
		completedSteps++
		currentStep = 8
	}

	// Mark current step
	if currentStep <= len(steps) {
		steps[currentStep-1].Current = true
	}

	// Parse completion time
	var completedAt *time.Time
	if isCompleted {
		if completedAtStr := configMap["setup.completed_at"]; completedAtStr != "" {
			if t, err := time.Parse(time.RFC3339, completedAtStr); err == nil {
				completedAt = &t
			}
		}
	}

	return &WizardStatus{
		CurrentStep: currentStep,
		TotalSteps:  len(steps),
		IsCompleted: isCompleted,
		Steps:       steps,
		CompletedAt: completedAt,
	}, nil
}

// CompleteStep marks a wizard step as completed
func (s *WizardService) CompleteStep(stepName string) error {
	configKey := fmt.Sprintf("setup.%s_configured", stepName)
	
	// Create or update configuration
	config := &models.SystemConfig{
		ID:       uuid.New(),
		Key:      configKey,
		Value:    "true",
		Type:     "boolean",
		Category: "setup",
	}

	if err := s.db.Where("key = ?", configKey).First(config).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// Create new config
			if err := s.db.Create(config).Error; err != nil {
				return fmt.Errorf("failed to create config: %w", err)
			}
		} else {
			return fmt.Errorf("failed to query config: %w", err)
		}
	} else {
		// Update existing config
		config.Value = "true"
		if err := s.db.Save(config).Error; err != nil {
			return fmt.Errorf("failed to update config: %w", err)
		}
	}

	return nil
}

// CompleteWizard marks the entire setup wizard as completed
func (s *WizardService) CompleteWizard() error {
	configs := []*models.SystemConfig{
		{
			ID:       uuid.New(),
			Key:      "setup.completed",
			Value:    "true",
			Type:     "boolean",
			Category: "setup",
		},
		{
			ID:       uuid.New(),
			Key:      "setup.completed_at",
			Value:    time.Now().Format(time.RFC3339),
			Type:     "datetime",
			Category: "setup",
		},
	}

	for _, config := range configs {
		if err := s.db.Where("key = ?", config.Key).First(config).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				// Create new config
				if err := s.db.Create(config).Error; err != nil {
					return fmt.Errorf("failed to create config: %w", err)
				}
			}
		} else {
			// Update existing config based on key
			if config.Key == "setup.completed" {
				config.Value = "true"
			} else {
				config.Value = time.Now().Format(time.RFC3339)
			}
			if err := s.db.Save(config).Error; err != nil {
				return fmt.Errorf("failed to update config: %w", err)
			}
		}
	}

	return nil
}

// IsSetupCompleted checks if the setup wizard has been completed
func (s *WizardService) IsSetupCompleted() (bool, error) {
	var config models.SystemConfig
	if err := s.db.Where("key = ?", "setup.completed").First(&config).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to check setup status: %w", err)
	}
	return config.Value == "true", nil
}

// ResetWizard resets the setup wizard (for development/testing)
func (s *WizardService) ResetWizard() error {
	// Delete all setup-related configurations
	if err := s.db.Where("category = ?", "setup").Delete(&models.SystemConfig{}).Error; err != nil {
		return fmt.Errorf("failed to reset wizard: %w", err)
	}
	return nil
}

// GetStatus returns the current wizard status (alias for GetWizardStatus)
func (s *WizardService) GetStatus() (*WizardStatus, error) {
	return s.GetWizardStatus()
}

// ProcessStep processes a wizard step completion
func (s *WizardService) ProcessStep(stepName string, data map[string]interface{}) error {
	// Mark step as completed
	if err := s.CompleteStep(stepName); err != nil {
		return fmt.Errorf("failed to complete step: %w", err)
	}

	// Handle step-specific processing
	switch stepName {
	case "complete":
		return s.CompleteWizard()
	default:
		// No additional processing needed for other steps
		return nil
	}
}