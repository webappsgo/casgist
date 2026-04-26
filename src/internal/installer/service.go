package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/spf13/viper"

	"github.com/casapps/casgist/src/internal/config"
	"github.com/casapps/casgist/src/internal/privilege"
)

// ServiceInstaller handles system service installation
type ServiceInstaller struct {
	cfg              *viper.Viper
	pathConfig       *config.PathConfig
	privilegeService *privilege.EscalationService
}

// InstallationOptions contains service installation options
type InstallationOptions struct {
	ServiceName    string `json:"service_name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	BinaryPath     string `json:"binary_path"`
	WorkingDir     string `json:"working_dir"`
	User           string `json:"user"`
	Group          string `json:"group"`
	Port           int    `json:"port"`
	Environment    map[string]string `json:"environment"`
	StartupType    string `json:"startup_type"` // auto, manual, disabled
	Dependencies   []string `json:"dependencies"`
	CreateUser     bool   `json:"create_user"`
	EnableFirewall bool   `json:"enable_firewall"`
}

// InstallationResult contains the result of service installation
type InstallationResult struct {
	Success          bool     `json:"success"`
	ServicesCreated  []string `json:"services_created"`
	FilesCreated     []string `json:"files_created"`
	UsersCreated     []string `json:"users_created"`
	CommandsExecuted []string `json:"commands_executed"`
	Errors           []string `json:"errors"`
	NextSteps        []string `json:"next_steps"`
}

// NewServiceInstaller creates a new service installer
func NewServiceInstaller(cfg *viper.Viper, pathConfig *config.PathConfig) *ServiceInstaller {
	return &ServiceInstaller{
		cfg:              cfg,
		pathConfig:       pathConfig,
		privilegeService: privilege.NewEscalationService(cfg),
	}
}

// InstallService installs CasGists as a system service
func (s *ServiceInstaller) InstallService(options InstallationOptions) (*InstallationResult, error) {
	result := &InstallationResult{
		ServicesCreated:  []string{},
		FilesCreated:     []string{},
		UsersCreated:     []string{},
		CommandsExecuted: []string{},
		Errors:           []string{},
		NextSteps:        []string{},
	}

	// Set defaults
	if options.ServiceName == "" {
		options.ServiceName = "casgists"
	}
	if options.DisplayName == "" {
		options.DisplayName = "CasGists"
	}
	if options.Description == "" {
		options.Description = "CasGists - Self-hosted Git Gist Server"
	}
	if options.BinaryPath == "" {
		executable, err := os.Executable()
		if err != nil {
			return result, fmt.Errorf("failed to get executable path: %w", err)
		}
		options.BinaryPath = executable
	}
	if options.WorkingDir == "" {
		options.WorkingDir = s.pathConfig.DataDir
	}
	if options.User == "" {
		options.User = "casgists"
	}
	if options.Group == "" {
		options.Group = "casgists"
	}
	if options.StartupType == "" {
		options.StartupType = "auto"
	}

	// Create system user if requested
	if options.CreateUser && options.User != "root" && options.User != "administrator" {
		if err := s.createSystemUser(options, result); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to create user: %v", err))
		}
	}

	// Install service based on platform
	switch runtime.GOOS {
	case "linux":
		if err := s.installLinuxService(options, result); err != nil {
			return result, err
		}
	case "darwin":
		if err := s.installDarwinService(options, result); err != nil {
			return result, err
		}
	case "windows":
		if err := s.installWindowsService(options, result); err != nil {
			return result, err
		}
	default:
		return result, fmt.Errorf("service installation not supported on %s", runtime.GOOS)
	}

	// Configure firewall if requested
	if options.EnableFirewall && options.Port > 0 {
		if err := s.configureFirewall(options, result); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Firewall configuration failed: %v", err))
		}
	}

	// Set up log rotation
	if err := s.setupLogRotation(options, result); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Log rotation setup failed: %v", err))
	}

	result.Success = len(result.Errors) == 0
	return result, nil
}

// installLinuxService installs systemd service on Linux
func (s *ServiceInstaller) installLinuxService(options InstallationOptions, result *InstallationResult) error {
	// Create systemd service file
	serviceContent := s.generateSystemdService(options)
	servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", options.ServiceName)
	
	if err := s.writeSystemFile(servicePath, serviceContent, result); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	// Reload systemd
	if err := s.executePrivileged("systemctl", []string{"daemon-reload"}, "Reloading systemd", result); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	// Enable service
	if options.StartupType == "auto" {
		if err := s.executePrivileged("systemctl", []string{"enable", options.ServiceName}, "Enabling service", result); err != nil {
			return fmt.Errorf("failed to enable service: %w", err)
		}
	}

	result.ServicesCreated = append(result.ServicesCreated, options.ServiceName)
	result.NextSteps = append(result.NextSteps, fmt.Sprintf("Start service: sudo systemctl start %s", options.ServiceName))
	result.NextSteps = append(result.NextSteps, fmt.Sprintf("Check status: sudo systemctl status %s", options.ServiceName))
	result.NextSteps = append(result.NextSteps, fmt.Sprintf("View logs: sudo journalctl -u %s", options.ServiceName))

	return nil
}


// installDarwinService installs launchd service on macOS
func (s *ServiceInstaller) installDarwinService(options InstallationOptions, result *InstallationResult) error {
	// Create launchd plist
	plistContent := s.generateLaunchdPlist(options)
	plistPath := fmt.Sprintf("/Library/LaunchDaemons/com.casapps.%s.plist", options.ServiceName)
	
	if err := s.writeSystemFile(plistPath, plistContent, result); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	// Set permissions
	if err := s.executePrivileged("chown", []string{"root:wheel", plistPath}, "Setting plist permissions", result); err != nil {
		return fmt.Errorf("failed to set plist permissions: %w", err)
	}

	// Load service
	if options.StartupType == "auto" {
		if err := s.executePrivileged("launchctl", []string{"load", "-w", plistPath}, "Loading service", result); err != nil {
			return fmt.Errorf("failed to load service: %w", err)
		}
	}

	result.ServicesCreated = append(result.ServicesCreated, options.ServiceName)
	result.NextSteps = append(result.NextSteps, fmt.Sprintf("Start service: sudo launchctl start com.casapps.%s", options.ServiceName))
	result.NextSteps = append(result.NextSteps, fmt.Sprintf("Check status: sudo launchctl list | grep %s", options.ServiceName))

	return nil
}


// installWindowsService installs Windows service
func (s *ServiceInstaller) installWindowsService(options InstallationOptions, result *InstallationResult) error {
	// Create Windows service
	args := []string{
		"create", options.ServiceName,
		"binPath=", fmt.Sprintf("\"%s\" --service", options.BinaryPath),
		"DisplayName=", options.DisplayName,
		"description=", options.Description,
	}

	// Set startup type
	switch options.StartupType {
	case "auto":
		args = append(args, "start=", "auto")
	case "manual":
		args = append(args, "start=", "demand")
	case "disabled":
		args = append(args, "start=", "disabled")
	}

	if err := s.executePrivileged("sc", args, "Creating Windows service", result); err != nil {
		return fmt.Errorf("failed to create Windows service: %w", err)
	}

	result.ServicesCreated = append(result.ServicesCreated, options.ServiceName)
	result.NextSteps = append(result.NextSteps, fmt.Sprintf("Start service: sc start %s", options.ServiceName))
	result.NextSteps = append(result.NextSteps, fmt.Sprintf("Check status: sc query %s", options.ServiceName))

	return nil
}


// createSystemUser creates a system user for the service
func (s *ServiceInstaller) createSystemUser(options InstallationOptions, result *InstallationResult) error {
	switch runtime.GOOS {
	case "linux":
		// Create user with useradd
		args := []string{
			"--system",
			"--no-create-home",
			"--shell", "/bin/false",
			"--comment", "CasGists system user",
		}
		if options.Group != "" {
			args = append(args, "--user-group")
		}
		args = append(args, options.User)

		if err := s.executePrivileged("useradd", args, "Creating system user", result); err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}

		result.UsersCreated = append(result.UsersCreated, options.User)
		
	case "darwin":
		// Create user with dscl
		uid := "501" // Start from 501 for system users on macOS
		args := []string{
			".", "-create", fmt.Sprintf("/Users/%s", options.User),
			"UniqueID", uid,
		}

		if err := s.executePrivileged("dscl", args, "Creating system user", result); err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}

		result.UsersCreated = append(result.UsersCreated, options.User)
		
	case "windows":
		// Create user with net user
		args := []string{
			"user", options.User,
			"*", // Prompt for password
			"/add",
			"/comment:CasGists system user",
			"/fullname:CasGists",
		}

		if err := s.executePrivileged("net", args, "Creating system user", result); err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}

		result.UsersCreated = append(result.UsersCreated, options.User)
	}

	return nil
}


// configureFirewall configures firewall rules
func (s *ServiceInstaller) configureFirewall(options InstallationOptions, result *InstallationResult) error {
	switch runtime.GOOS {
	case "linux":
		// Try ufw first, then iptables
		if s.isCommandAvailable("ufw") {
			args := []string{"allow", fmt.Sprintf("%d/tcp", options.Port)}
			return s.executePrivileged("ufw", args, "Configuring firewall (ufw)", result)
		} else if s.isCommandAvailable("iptables") {
			args := []string{
				"-A", "INPUT",
				"-p", "tcp",
				"--dport", fmt.Sprintf("%d", options.Port),
				"-j", "ACCEPT",
			}
			return s.executePrivileged("iptables", args, "Configuring firewall (iptables)", result)
		}
		
	case "darwin":
		// macOS firewall configuration would go here
		result.NextSteps = append(result.NextSteps, fmt.Sprintf("Configure firewall to allow port %d in System Preferences > Security & Privacy > Firewall", options.Port))
		
	case "windows":
		// Windows firewall
		args := []string{
			"advfirewall", "firewall", "add", "rule",
			"name=CasGists",
			"dir=in",
			"action=allow",
			"protocol=TCP",
			fmt.Sprintf("localport=%d", options.Port),
		}
		return s.executePrivileged("netsh", args, "Configuring Windows firewall", result)
	}

	return nil
}


// setupLogRotation sets up log rotation
func (s *ServiceInstaller) setupLogRotation(options InstallationOptions, result *InstallationResult) error {
	if runtime.GOOS != "linux" {
		return nil // Only implement for Linux initially
	}

	logrotateConfig := fmt.Sprintf(`/var/log/%s/*.log {
    daily
    missingok
    rotate 30
    compress
    delaycompress
    notifempty
    sharedscripts
    postrotate
        systemctl reload %s || true
    endscript
}`, options.ServiceName, options.ServiceName)

	logrotateFile := fmt.Sprintf("/etc/logrotate.d/%s", options.ServiceName)
	return s.writeSystemFile(logrotateFile, logrotateConfig, result)
}

// executePrivileged executes a command with privileges
func (s *ServiceInstaller) executePrivileged(command string, args []string, reason string, result *InstallationResult) error {
	req := privilege.PrivilegeRequest{
		Command:   command,
		Arguments: args,
		Reason:    reason,
	}

	privResult, err := s.privilegeService.ExecutePrivileged(req)
	if err != nil {
		return err
	}

	commandStr := fmt.Sprintf("%s %s", command, fmt.Sprintf("%v", args))
	result.CommandsExecuted = append(result.CommandsExecuted, commandStr)

	if !privResult.Success {
		return fmt.Errorf("command failed: %s", privResult.Error)
	}

	return nil
}


// writeSystemFile writes a system file with privileges
func (s *ServiceInstaller) writeSystemFile(filePath, content string, result *InstallationResult) error {
	// Create temporary file first
	tempFile := filepath.Join(os.TempDir(), filepath.Base(filePath))
	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Move to system location with privileges
	if err := s.executePrivileged("cp", []string{tempFile, filePath}, fmt.Sprintf("Installing %s", filePath), result); err != nil {
		return err
	}

	// Set correct permissions
	if err := s.executePrivileged("chmod", []string{"644", filePath}, "Setting file permissions", result); err != nil {
		return err
	}

	result.FilesCreated = append(result.FilesCreated, filePath)
	
	// Clean up temp file
	os.Remove(tempFile)
	
	return nil
}


// generateSystemdService generates systemd service file content
func (s *ServiceInstaller) generateSystemdService(options InstallationOptions) string {
	tmpl := `[Unit]
Description={{.Description}}
After=network.target
{{range .Dependencies}}
Requires={{.}}
After={{.}}
{{end}}

[Service]
Type=simple
User={{.User}}
Group={{.Group}}
WorkingDirectory={{.WorkingDir}}
ExecStart={{.BinaryPath}} --config={{.WorkingDir}}/config.toml
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier={{.ServiceName}}

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths={{.WorkingDir}}

# Environment variables
{{range $key, $value := .Environment}}
Environment="{{$key}}={{$value}}"
{{end}}

[Install]
WantedBy=multi-user.target`

	t := template.Must(template.New("systemd").Parse(tmpl))
	var result strings.Builder
	t.Execute(&result, options)
	return result.String()
}

// generateLaunchdPlist generates launchd plist content for macOS
func (s *ServiceInstaller) generateLaunchdPlist(options InstallationOptions) string {
	tmpl := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.casapps.{{.ServiceName}}</string>
    
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>--config={{.WorkingDir}}/config.toml</string>
    </array>
    
    <key>WorkingDirectory</key>
    <string>{{.WorkingDir}}</string>
    
    <key>UserName</key>
    <string>{{.User}}</string>
    
    <key>GroupName</key>
    <string>{{.Group}}</string>
    
    <key>RunAtLoad</key>
    <{{if eq .StartupType "auto"}}true{{else}}false{{end}}/>
    
    <key>KeepAlive</key>
    <true/>
    
    <key>StandardOutPath</key>
    <string>/var/log/{{.ServiceName}}.log</string>
    
    <key>StandardErrorPath</key>
    <string>/var/log/{{.ServiceName}}.error.log</string>

    {{if .Environment}}
    <key>EnvironmentVariables</key>
    <dict>
        {{range $key, $value := .Environment}}
        <key>{{$key}}</key>
        <string>{{$value}}</string>
        {{end}}
    </dict>
    {{end}}
</dict>
</plist>`

	t := template.Must(template.New("launchd").Parse(tmpl))
	var result strings.Builder
	t.Execute(&result, options)
	return result.String()
}

// UninstallService removes the service from the system
func (s *ServiceInstaller) UninstallService(serviceName string) error {
	switch runtime.GOOS {
	case "linux":
		// Stop and disable service
		s.executePrivileged("systemctl", []string{"stop", serviceName}, "Stopping service", &InstallationResult{})
		s.executePrivileged("systemctl", []string{"disable", serviceName}, "Disabling service", &InstallationResult{})
		
		// Remove service file
		servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)
		s.executePrivileged("rm", []string{servicePath}, "Removing service file", &InstallationResult{})
		
		// Reload systemd
		s.executePrivileged("systemctl", []string{"daemon-reload"}, "Reloading systemd", &InstallationResult{})
		
	case "darwin":
		// Unload and remove plist
		plistPath := fmt.Sprintf("/Library/LaunchDaemons/com.casapps.%s.plist", serviceName)
		s.executePrivileged("launchctl", []string{"unload", "-w", plistPath}, "Unloading service", &InstallationResult{})
		s.executePrivileged("rm", []string{plistPath}, "Removing plist file", &InstallationResult{})
		
	case "windows":
		// Stop and delete service
		s.executePrivileged("sc", []string{"stop", serviceName}, "Stopping service", &InstallationResult{})
		s.executePrivileged("sc", []string{"delete", serviceName}, "Deleting service", &InstallationResult{})
	}

	return nil
}

// isCommandAvailable checks if a command is available in PATH
func (s *ServiceInstaller) isCommandAvailable(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}