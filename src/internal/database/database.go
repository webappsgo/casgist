package database

import (
	"fmt"
	"time"

	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Initialize initializes the database connection
func Initialize(cfg *viper.Viper) (*gorm.DB, error) {
	var dialector gorm.Dialector
	
	// Configure database based on type
	dbType := cfg.GetString("database.type")
	dbDSN := cfg.GetString("database.dsn")
	switch dbType {
	case "postgres", "postgresql":
		dialector = postgres.Open(dbDSN)
	case "mysql":
		dialector = mysql.Open(dbDSN)
	case "sqlite", "":
		dialector = sqlite.Open(dbDSN)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}
	
	// Configure logger - use Silent for production, Info for debug
	logLevel := logger.Silent
	if cfg.GetBool("debug") {
		logLevel = logger.Info
	}

	// Open database connection
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		DisableForeignKeyConstraintWhenMigrating: true,
		PrepareStmt: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	
	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}
	
	// Set connection pool settings
	maxConns := cfg.GetInt("database.max_connections")
	if maxConns <= 0 {
		maxConns = 25 // default
	}
	sqlDB.SetMaxOpenConns(maxConns)
	sqlDB.SetMaxIdleConns(maxConns / 2)
	sqlDB.SetConnMaxIdleTime(time.Duration(cfg.GetInt("database.max_idle_time")) * time.Second)
	
	// Test connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	
	return db, nil
}

// MigrateDB runs all database migrations
func MigrateDB(db *gorm.DB) error {
	// Run fast migrations
	if err := FastMigrations(db); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	
	// Initialize default data
	if err := InitializeDefaultData(db); err != nil {
		return fmt.Errorf("failed to initialize default data: %w", err)
	}
	
	return nil
}

// MigrateTestDB runs database migrations suitable for testing (skips FTS)
func MigrateTestDB(db *gorm.DB) error {
	// Run fast migrations but skip FTS-related ones
	if err := FastMigrationsSkipFTS(db); err != nil {
		return fmt.Errorf("failed to run test migrations: %w", err)
	}
	
	// Initialize default data
	if err := InitializeDefaultData(db); err != nil {
		return fmt.Errorf("failed to initialize default data: %w", err)
	}
	
	return nil
}

// InitializeDefaultData creates default system configuration and admin user
func InitializeDefaultData(db *gorm.DB) error {
	// Create default system configurations
	configs := []models.SystemConfig{
		{
			Key:      "setup_completed",
			Value:    "false",
			Type:     "boolean",
			Category: "system",
		},
		{
			Key:      "registration_enabled",
			Value:    "true",
			Type:     "boolean",
			Category: "auth",
		},
		{
			Key:      "default_visibility",
			Value:    "private",
			Type:     "string",
			Category: "gist",
		},
		{
			Key:      "max_gist_size",
			Value:    "10485760", // 10MB
			Type:     "integer",
			Category: "gist",
		},
		{
			Key:      "smtp_enabled",
			Value:    "false",
			Type:     "boolean",
			Category: "email",
		},
	}
	
	for _, cfg := range configs {
		// Use FirstOrCreate to avoid duplicates
		var existing models.SystemConfig
		if err := db.Where("key = ?", cfg.Key).FirstOrCreate(&existing, &cfg).Error; err != nil {
			return fmt.Errorf("failed to create system config %s: %w", cfg.Key, err)
		}
	}
	
	return nil
}