package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/casapps/casgist/src/internal/installer"
)

// handleInstallCommand handles the install command
func handleInstallCommand(args []string) error {
	// Default values
	installPort := 64080
	installUser := "casgists"
	installPath := "/opt/casgists"
	dataDir := "/var/lib/casgists"
	configPath := "/etc/casgists/config.yaml"
	noSystemService := false

	// Parse flags
	for i, arg := range args {
		switch arg {
		case "--port":
			if i+1 < len(args) {
				if port, err := strconv.Atoi(args[i+1]); err == nil {
					installPort = port
				}
			}
		case "--user":
			if i+1 < len(args) {
				installUser = args[i+1]
			}
		case "--install-path":
			if i+1 < len(args) {
				installPath = args[i+1]
			}
		case "--data-dir":
			if i+1 < len(args) {
				dataDir = args[i+1]
			}
		case "--config":
			if i+1 < len(args) {
				configPath = args[i+1]
			}
		case "--no-service":
			noSystemService = true
		case "--help", "-h":
			printInstallHelp()
			return nil
		}
	}

	return runInstall(installPort, installUser, installPath, dataDir, configPath, noSystemService)
}

// handleUninstallCommand handles the uninstall command
func handleUninstallCommand(args []string) error {
	// Default values
	installUser := "casgists"
	installPath := "/opt/casgists"
	dataDir := "/var/lib/casgists"
	configPath := "/etc/casgists/config.yaml"

	// Parse flags
	for _, arg := range args {
		switch arg {
		case "--user":
			// Would need to parse next arg
		case "--help", "-h":
			printUninstallHelp()
			return nil
		}
	}

	return runUninstall(installUser, installPath, dataDir, configPath)
}

func runInstall(port int, user, installPath, dataDir, configPath string, noService bool) error {
	// Verify system requirements
	if err := installer.VerifySystemRequirements(); err != nil {
		return fmt.Errorf("system requirements not met: %w", err)
	}

	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("this command must be run as root (use sudo)")
	}

	// Validate port
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port number: %d", port)
	}

	// Check if port is available
	if err := installer.CheckPort(port); err != nil {
		return err
	}

	// Get current binary path
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Create installer config
	config := installer.InstallerConfig{
		ServiceName:     "casgists",
		BinaryPath:      binaryPath,
		DataDir:         dataDir,
		ConfigPath:      configPath,
		User:            user,
		Group:           user,
		Port:            port,
		InstallPath:     installPath,
		WorkingDir:      dataDir,
		EnvFile:         filepath.Join(filepath.Dir(configPath), "environment"),
		LogFile:         filepath.Join("/var/log/casgists", "casgists.log"),
		NoSystemService: noService,
	}

	// Create installer
	inst := installer.NewInstaller(config)

	// Run installation
	ctx := context.Background()
	if err := inst.Install(ctx); err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	return nil
}

func runUninstall(user, installPath, dataDir, configPath string) error {
	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("this command must be run as root (use sudo)")
	}

	// Create installer config
	config := installer.InstallerConfig{
		ServiceName: "casgists",
		DataDir:     dataDir,
		ConfigPath:  configPath,
		User:        user,
		InstallPath: installPath,
	}

	// Create installer
	inst := installer.NewInstaller(config)

	// Run uninstallation
	ctx := context.Background()
	if err := inst.Uninstall(ctx); err != nil {
		return fmt.Errorf("uninstallation failed: %w", err)
	}

	return nil
}

// handleVerifyInstallCommand verifies installation
func handleVerifyInstallCommand(args []string) error {
	fmt.Println("Verifying CasGists installation...")

	// Check binary
	if _, err := os.Stat("/opt/casgists/bin/casgists"); os.IsNotExist(err) {
		fmt.Println("❌ Binary not found at /opt/casgists/bin/casgists")
	} else {
		fmt.Println("✅ Binary installed")
	}

	// Check config
	if _, err := os.Stat("/etc/casgists/config.yaml"); os.IsNotExist(err) {
		fmt.Println("❌ Configuration not found at /etc/casgists/config.yaml")
	} else {
		fmt.Println("✅ Configuration file exists")
	}

	// Check data directory
	if _, err := os.Stat("/var/lib/casgists"); os.IsNotExist(err) {
		fmt.Println("❌ Data directory not found at /var/lib/casgists")
	} else {
		fmt.Println("✅ Data directory exists")
	}

	// Check service (Linux with systemd)
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/etc/systemd/system/casgists.service"); err == nil {
			fmt.Println("✅ Systemd service installed")
			
			// Try to get service status
			if output, err := exec.Command("systemctl", "is-active", "casgists").Output(); err == nil {
				status := strings.TrimSpace(string(output))
				if status == "active" {
					fmt.Println("✅ Service is running")
				} else {
					fmt.Println("⚠️  Service is not running")
				}
			}
		}
	}

	// Check port
	portFile := "/etc/casgists/config.yaml"
	if data, err := os.ReadFile(portFile); err == nil {
		// Simple port extraction (not robust, but good enough for verification)
		portStr := extractPort(string(data))
		if port, err := strconv.Atoi(portStr); err == nil {
			if err := installer.CheckPort(port); err != nil {
				fmt.Printf("✅ CasGists appears to be listening on port %d\n", port)
			} else {
				fmt.Printf("⚠️  Port %d is not in use\n", port)
			}
		}
	}

	return nil
}

func extractPort(config string) string {
	// Very simple port extraction - in production would use proper YAML parser
	lines := strings.Split(config, "\n")
	for _, line := range lines {
		if strings.Contains(line, "port:") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "64080"
}

func printInstallHelp() {
	fmt.Println(`Install CasGists as a system service

Usage:
  casgists install [options]

Options:
  --port PORT           Port to bind to (default: 64080)
  --user USER           System user to run as (default: casgists)
  --install-path PATH   Installation directory (default: /opt/casgists)
  --data-dir PATH       Data directory (default: /var/lib/casgists)
  --config PATH         Configuration file path (default: /etc/casgists/config.yaml)
  --no-service          Skip system service installation
  -h, --help            Show this help message

Examples:
  sudo casgists install
  sudo casgists install --port 64080 --user casgists
  sudo casgists install --data-dir /var/lib/casgists --no-service`)
}

func printUninstallHelp() {
	fmt.Println(`Uninstall CasGists from the system

Usage:
  casgists uninstall [options]

Options:
  --user USER           System user (default: casgists)
  --install-path PATH   Installation directory (default: /opt/casgists)
  --data-dir PATH       Data directory (default: /var/lib/casgists)
  --config PATH         Configuration file path (default: /etc/casgists/config.yaml)
  -h, --help            Show this help message

Examples:
  sudo casgists uninstall`)
}