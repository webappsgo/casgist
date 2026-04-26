package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// GetDefaultPaths returns OS-specific default paths based on privilege level
func GetDefaultPaths() *Paths {
	isPrivileged := isRunningAsPrivileged()
	
	switch runtime.GOOS {
	case "linux":
		return getLinuxPaths(isPrivileged)
	case "darwin":
		return getMacOSPaths(isPrivileged)
	case "freebsd", "openbsd", "netbsd":
		return getBSDPaths(isPrivileged)
	case "windows":
		return getWindowsPaths(isPrivileged)
	default:
		return getLinuxPaths(isPrivileged) // Fallback to Linux
	}
}

// Paths holds all application paths
type Paths struct {
	Binary   string
	Config   string
	Data     string
	Logs     string
	Backup   string
	PID      string
	SSL      string
	Security string
	DB       string
	Service  string
}

func getLinuxPaths(privileged bool) *Paths {
	if privileged {
		return &Paths{
			Binary:   "/usr/local/bin/casgist",
			Config:   "/etc/casapps/casgist",
			Data:     "/var/lib/casapps/casgist",
			Logs:     "/var/log/casapps/casgist",
			Backup:   "/mnt/Backups/casapps/casgist",
			PID:      "/var/run/casapps/casgist.pid",
			SSL:      "/etc/casapps/casgist/ssl",
			Security: "/etc/casapps/casgist/security",
			DB:       "/var/lib/casapps/casgist/db",
			Service:  "/etc/systemd/system/casgist.service",
		}
	}
	
	home := os.Getenv("HOME")
	return &Paths{
		Binary:   filepath.Join(home, ".local/bin/casgist"),
		Config:   filepath.Join(home, ".config/casapps/casgist"),
		Data:     filepath.Join(home, ".local/share/casapps/casgist"),
		Logs:     filepath.Join(home, ".local/log/casapps/casgist"),
		Backup:   filepath.Join(home, ".local/backup/casapps/casgist"),
		PID:      filepath.Join(home, ".local/share/casapps/casgist/casgist.pid"),
		SSL:      filepath.Join(home, ".config/casapps/casgist/ssl"),
		Security: filepath.Join(home, ".config/casapps/casgist/security"),
		DB:       filepath.Join(home, ".local/share/casapps/casgist/db"),
		Service:  "",
	}
}

func getMacOSPaths(privileged bool) *Paths {
	if privileged {
		return &Paths{
			Binary:   "/usr/local/bin/casgist",
			Config:   "/Library/Application Support/casapps/casgist",
			Data:     "/Library/Application Support/casapps/casgist/data",
			Logs:     "/Library/Logs/casapps/casgist",
			Backup:   "/Library/Backups/casapps/casgist",
			PID:      "/var/run/casapps/casgist.pid",
			SSL:      "/Library/Application Support/casapps/casgist/ssl",
			Security: "/Library/Application Support/casapps/casgist/security",
			DB:       "/Library/Application Support/casapps/casgist/db",
			Service:  "/Library/LaunchDaemons/com.casapps.casgist.plist",
		}
	}
	
	home := os.Getenv("HOME")
	return &Paths{
		Binary:   filepath.Join(home, "bin/casgist"),
		Config:   filepath.Join(home, "Library/Application Support/casapps/casgist"),
		Data:     filepath.Join(home, "Library/Application Support/casapps/casgist"),
		Logs:     filepath.Join(home, "Library/Logs/casapps/casgist"),
		Backup:   filepath.Join(home, "Library/Backups/casapps/casgist"),
		PID:      filepath.Join(home, "Library/Application Support/casapps/casgist/casgist.pid"),
		SSL:      filepath.Join(home, "Library/Application Support/casapps/casgist/ssl"),
		Security: filepath.Join(home, "Library/Application Support/casapps/casgist/security"),
		DB:       filepath.Join(home, "Library/Application Support/casapps/casgist/db"),
		Service:  filepath.Join(home, "Library/LaunchAgents/com.casapps.casgist.plist"),
	}
}

func getBSDPaths(privileged bool) *Paths {
	if privileged {
		return &Paths{
			Binary:   "/usr/local/bin/casgist",
			Config:   "/usr/local/etc/casapps/casgist",
			Data:     "/var/db/casapps/casgist",
			Logs:     "/var/log/casapps/casgist",
			Backup:   "/var/backups/casapps/casgist",
			PID:      "/var/run/casapps/casgist.pid",
			SSL:      "/usr/local/etc/casapps/casgist/ssl",
			Security: "/usr/local/etc/casapps/casgist/security",
			DB:       "/var/db/casapps/casgist/db",
			Service:  "/usr/local/etc/rc.d/casgist",
		}
	}
	
	home := os.Getenv("HOME")
	return &Paths{
		Binary:   filepath.Join(home, ".local/bin/casgist"),
		Config:   filepath.Join(home, ".config/casapps/casgist"),
		Data:     filepath.Join(home, ".local/share/casapps/casgist"),
		Logs:     filepath.Join(home, ".local/log/casapps/casgist"),
		Backup:   filepath.Join(home, ".local/backup/casapps/casgist"),
		PID:      filepath.Join(home, ".local/share/casapps/casgist/casgist.pid"),
		SSL:      filepath.Join(home, ".config/casapps/casgist/ssl"),
		Security: filepath.Join(home, ".config/casapps/casgist/security"),
		DB:       filepath.Join(home, ".local/share/casapps/casgist/db"),
		Service:  "",
	}
}

func getWindowsPaths(privileged bool) *Paths {
	if privileged {
		programData := os.Getenv("ProgramData")
		return &Paths{
			Binary:   `C:\Program Files\casapps\casgist\casgist.exe`,
			Config:   filepath.Join(programData, "casapps", "casgist"),
			Data:     filepath.Join(programData, "casapps", "casgist", "data"),
			Logs:     filepath.Join(programData, "casapps", "casgist", "logs"),
			Backup:   filepath.Join(programData, "Backups", "casapps", "casgist"),
			PID:      "",
			SSL:      filepath.Join(programData, "casapps", "casgist", "ssl"),
			Security: filepath.Join(programData, "casapps", "casgist", "security"),
			DB:       filepath.Join(programData, "casapps", "casgist", "db"),
			Service:  "",
		}
	}
	
	localAppData := os.Getenv("LocalAppData")
	appData := os.Getenv("AppData")
	return &Paths{
		Binary:   filepath.Join(localAppData, "casapps", "casgist", "casgist.exe"),
		Config:   filepath.Join(appData, "casapps", "casgist"),
		Data:     filepath.Join(localAppData, "casapps", "casgist"),
		Logs:     filepath.Join(localAppData, "casapps", "casgist", "logs"),
		Backup:   filepath.Join(localAppData, "Backups", "casapps", "casgist"),
		PID:      "",
		SSL:      filepath.Join(appData, "casapps", "casgist", "ssl"),
		Security: filepath.Join(appData, "casapps", "casgist", "security"),
		DB:       filepath.Join(localAppData, "casapps", "casgist", "db"),
		Service:  "",
	}
}

func isRunningAsPrivileged() bool {
	if runtime.GOOS == "windows" {
		// On Windows, check if running as Administrator
		// This is a simplified check
		return os.Getenv("USERNAME") == "Administrator"
	}
	return os.Geteuid() == 0
}

// SubstitutePathVariables replaces path variables in a string
func SubstitutePathVariables(s string, paths *Paths) string {
	replacements := map[string]string{
		"${DATA_DIR}":   paths.Data,
		"${CONFIG_DIR}": paths.Config,
		"${LOG_DIR}":    paths.Logs,
	}
	
	result := s
	for key, value := range replacements {
		result = filepath.ToSlash(filepath.FromSlash(replaceAll(result, key, value)))
	}
	return result
}

func replaceAll(s, old, new string) string {
	for {
		replaced := s
		for i := 0; i < len(s); {
			if i+len(old) <= len(s) && s[i:i+len(old)] == old {
				s = s[:i] + new + s[i+len(old):]
				i += len(new)
			} else {
				i++
			}
		}
		if replaced == s {
			break
		}
	}
	return s
}
