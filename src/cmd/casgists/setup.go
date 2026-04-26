package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/casapps/casgist/src/internal/installer"
	"golang.org/x/term"
)

// handleSetupCommand handles the setup command
func handleSetupCommand(args []string) error {
	// Check for help flag
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			printSetupHelp()
			return nil
		}
	}

	fmt.Println("🚀 CasGists Setup Wizard")
	fmt.Println("========================")
	fmt.Println()

	// Check if running as root (for system installation)
	isRoot := os.Geteuid() == 0

	reader := bufio.NewReader(os.Stdin)

	// Get system info
	fmt.Println("System Information:")
	fmt.Printf("OS: %s\n", runtime.GOOS)
	fmt.Printf("Architecture: %s\n", runtime.GOARCH)
	if isRoot {
		fmt.Println("Running as: root (system installation)")
	} else {
		fmt.Println("Running as: user (local installation)")
	}
	fmt.Println()

	// Installation type selection
	if isRoot {
		fmt.Println("Installation Type:")
		fmt.Println("1. System service (recommended)")
		fmt.Println("2. Local installation")
		fmt.Print("Choose installation type [1]: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			input = "1"
		}

		if input == "1" {
			return runSystemInstallation(reader)
		}
	}

	return runLocalSetup(reader)
}

func runSystemInstallation(reader *bufio.Reader) error {
	fmt.Println("\n📦 System Installation")
	fmt.Println("======================")

	// Port configuration
	fmt.Print("Port to bind to [64080]: ")
	portStr, _ := reader.ReadString('\n')
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		portStr = "64080"
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port: %s", portStr)
	}

	// Check port availability
	if err := installer.CheckPort(port); err != nil {
		return err
	}

	// User configuration
	fmt.Print("System user [casgists]: ")
	user, _ := reader.ReadString('\n')
	user = strings.TrimSpace(user)
	if user == "" {
		user = "casgists"
	}

	// Installation path
	fmt.Print("Installation path [/opt/casgists]: ")
	installPath, _ := reader.ReadString('\n')
	installPath = strings.TrimSpace(installPath)
	if installPath == "" {
		installPath = "/opt/casgists"
	}

	// Data directory
	fmt.Print("Data directory [/var/lib/casgists]: ")
	dataDir, _ := reader.ReadString('\n')
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = "/var/lib/casgists"
	}

	// Config path
	fmt.Print("Config file [/etc/casgists/config.yaml]: ")
	configPath, _ := reader.ReadString('\n')
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		configPath = "/etc/casgists/config.yaml"
	}

	// Confirmation
	fmt.Println("\n📋 Installation Summary")
	fmt.Println("========================")
	fmt.Printf("Port: %d\n", port)
	fmt.Printf("User: %s\n", user)
	fmt.Printf("Install Path: %s\n", installPath)
	fmt.Printf("Data Directory: %s\n", dataDir)
	fmt.Printf("Config File: %s\n", configPath)
	fmt.Println()

	fmt.Print("Proceed with installation? [y/N]: ")
	confirm, _ := reader.ReadString('\n')
	confirm = strings.ToLower(strings.TrimSpace(confirm))

	if confirm != "y" && confirm != "yes" {
		fmt.Println("Installation cancelled.")
		return nil
	}

	// Create installer
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	config := installer.InstallerConfig{
		ServiceName: "casgists",
		BinaryPath:  binaryPath,
		DataDir:     dataDir,
		ConfigPath:  configPath,
		User:        user,
		Group:       user,
		Port:        port,
		InstallPath: installPath,
	}

	inst := installer.NewInstaller(config)

	// Run installation
	fmt.Println("\n🔧 Installing CasGists...")
	if err := inst.Install(context.Background()); err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	// Run post-install configuration
	return runPostInstallSetup(reader, configPath)
}

func runLocalSetup(reader *bufio.Reader) error {
	fmt.Println("\n🏠 Local Setup")
	fmt.Println("===============")

	// Get current directory
	currentDir, _ := os.Getwd()
	
	fmt.Printf("Current directory: %s\n", currentDir)
	fmt.Print("Use current directory for CasGists data? [Y/n]: ")
	
	useCurrentDir, _ := reader.ReadString('\n')
	useCurrentDir = strings.ToLower(strings.TrimSpace(useCurrentDir))
	
	dataDir := currentDir
	if useCurrentDir == "n" || useCurrentDir == "no" {
		fmt.Print("Enter data directory path: ")
		dataDir, _ = reader.ReadString('\n')
		dataDir = strings.TrimSpace(dataDir)
		if dataDir == "" {
			dataDir = currentDir
		}
	}

	// Port configuration
	fmt.Print("Port to bind to [64080]: ")
	portStr, _ := reader.ReadString('\n')
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		portStr = "64080"
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port: %s", portStr)
	}

	// Check port availability
	if err := installer.CheckPort(port); err != nil {
		return err
	}

	// Create config file in data directory
	configPath := filepath.Join(dataDir, "config.yaml")

	// Create directories
	dirs := []string{
		filepath.Join(dataDir, "gists"),
		filepath.Join(dataDir, "repos"),
		filepath.Join(dataDir, "cache"),
		filepath.Join(dataDir, "uploads"),
		filepath.Join(dataDir, "backups"),
		filepath.Join(dataDir, "logs"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Generate basic config
	configContent := fmt.Sprintf(`# CasGists Configuration
server:
  host: 127.0.0.1
  port: %d
  base_url: http://localhost:%d

database:
  type: sqlite
  path: ./casgists.db

paths:
  data_dir: .
  repo_dir: ./repos
  cache_dir: ./cache
  upload_dir: ./uploads
  backup_dir: ./backups

security:
  secret_key: %s

logging:
  level: info
  file: ./logs/casgists.log

features:
  registration: true
  anonymous_gists: true
`, port, port, generateSecretKey())

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}

	fmt.Printf("\n✅ Local setup complete!\n")
	fmt.Printf("Config file: %s\n", configPath)
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Printf("\nTo start CasGists:\n")
	fmt.Printf("  casgists serve --config %s\n", configPath)
	fmt.Printf("\nThen visit: http://localhost:%d\n", port)

	return nil
}

func runPostInstallSetup(reader *bufio.Reader, configPath string) error {
	fmt.Println("\n⚙️  Post-Installation Configuration")
	fmt.Println("===================================")

	// Domain configuration
	fmt.Print("Domain name (e.g., gists.example.com) [localhost]: ")
	domain, _ := reader.ReadString('\n')
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = "localhost"
	}

	// Email configuration
	fmt.Print("Configure email? [y/N]: ")
	configEmail, _ := reader.ReadString('\n')
	configEmail = strings.ToLower(strings.TrimSpace(configEmail))

	var emailConfig string
	if configEmail == "y" || configEmail == "yes" {
		emailConfig = configureEmail(reader)
		_ = emailConfig // Use emailConfig if needed
	}

	// Create admin user
	fmt.Print("Create admin user? [Y/n]: ")
	createAdmin, _ := reader.ReadString('\n')
	createAdmin = strings.ToLower(strings.TrimSpace(createAdmin))

	if createAdmin != "n" && createAdmin != "no" {
		if err := createAdminUser(reader); err != nil {
			fmt.Printf("Warning: Failed to create admin user: %v\n", err)
		}
	}

	fmt.Println("\n✅ Setup complete!")
	fmt.Printf("Edit configuration: %s\n", configPath)
	fmt.Println("Start the service: sudo systemctl start casgists")
	fmt.Printf("Visit: http://%s\n", domain)

	return nil
}

func configureEmail(reader *bufio.Reader) string {
	fmt.Print("SMTP Host: ")
	host, _ := reader.ReadString('\n')
	host = strings.TrimSpace(host)

	fmt.Print("SMTP Port [587]: ")
	portStr, _ := reader.ReadString('\n')
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		portStr = "587"
	}

	fmt.Print("SMTP Username: ")
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)

	fmt.Print("SMTP Password: ")
	passwordBytes, _ := term.ReadPassword(int(syscall.Stdin))
	password := string(passwordBytes)
	fmt.Println()

	fmt.Print("From Email: ")
	fromEmail, _ := reader.ReadString('\n')
	fromEmail = strings.TrimSpace(fromEmail)

	return fmt.Sprintf(`
email:
  smtp_host: %s
  smtp_port: %s
  smtp_username: %s
  smtp_password: %s
  from_email: %s
  from_name: CasGists
`, host, portStr, username, password, fromEmail)
}

func createAdminUser(reader *bufio.Reader) error {
	fmt.Print("Admin username: ")
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)

	fmt.Print("Admin email: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)

	fmt.Print("Admin password: ")
	passwordBytes, _ := term.ReadPassword(int(syscall.Stdin))
	password := string(passwordBytes)
	fmt.Println()

	// In a real implementation, this would create the user in the database
	// For now, just store the information for manual setup
	adminInfo := fmt.Sprintf(`# Admin User Information
# Use this information to create the first admin user via the web interface
Username: %s
Email: %s
Password: %s
`, username, email, password)

	adminFile := "/tmp/casgists_admin_info.txt"
	if err := os.WriteFile(adminFile, []byte(adminInfo), 0600); err != nil {
		return err
	}

	fmt.Printf("Admin user information saved to: %s\n", adminFile)
	fmt.Println("Please delete this file after creating the admin user!")

	return nil
}

func generateSecretKey() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 64)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

func printSetupHelp() {
	fmt.Println(`Interactive setup wizard for CasGists

Usage:
  casgists setup [options]

Options:
  -h, --help            Show this help message

This wizard will help you:
- Configure basic settings (port, domain)
- Set up database connection
- Configure authentication
- Set up email (optional)
- Create admin user

Run this after installation or to reconfigure an existing instance.

Examples:
  casgists setup                 Run setup wizard
  sudo casgists setup            Run setup wizard with system installation`)
}