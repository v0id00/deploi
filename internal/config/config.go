// Package config loads and parses the TOML configuration file for deploi.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Defaults holds all configurable execution defaults.
type Defaults struct {
	// Connection / execution
	Timeout     int  `toml:"timeout,omitempty"`
	Concurrency int  `toml:"concurrency,omitempty"`
	DryRun      bool `toml:"dry_run,omitempty"`

	// Safety
	Force bool `toml:"force,omitempty"`

	// Output
	JSON      bool `toml:"json,omitempty"`
	Quiet     bool `toml:"quiet,omitempty"`
	ShowBar   bool `toml:"show_progress,omitempty"`

	// Default filters
	ServerFilter string `toml:"server,omitempty"`
	Tags         string `toml:"tags,omitempty"`

	// Confirm when targeting ALL servers
	ConfirmWithoutFilter bool `toml:"confirm_without_filter,omitempty"`

	// Editor for interactive modes
	Editor string `toml:"editor,omitempty"`

	// Rsync binary path (default: "rsync")
	RsyncBin string `toml:"rsync_bin,omitempty"`

	// Default remote path
	RemotePath string `toml:"remote_path,omitempty"`
}

// SetDefaults fills zero-valued fields with sensible defaults.
func (d *Defaults) SetDefaults() {
	if d.Timeout <= 0 {
		d.Timeout = 30
	}
	if d.Concurrency <= 0 {
		d.Concurrency = 5
	}
	if d.RsyncBin == "" {
		d.RsyncBin = "rsync"
	}
}

// Server defines a single deploy target.
type Server struct {
	Name     string   `toml:"-"` // set from map key
	Host     string   `toml:"host"`
	Port     int      `toml:"port,omitempty"`
	User     string   `toml:"user"`
	Password string   `toml:"password,omitempty"`
	KeyFile  string   `toml:"key_file,omitempty"` // SSH key path
	Method   string   `toml:"method,omitempty"`   // "ssh", "sftp", "rsync" (default: rsync)
	Tags     []string `toml:"tags,omitempty"`

	// Remote paths per server
	RemotePath string `toml:"remote_path,omitempty"`

	// Rsync-specific overrides
	RsyncOptions string `toml:"rsync_options,omitempty"`
}

// DSN returns an SSH connection string (user@host:port).
func (s Server) Addr() string {
	hostPort := s.Host
	if s.Port > 0 && s.Port != 22 {
		hostPort = fmt.Sprintf("%s:%d", s.Host, s.Port)
	}
	if s.User != "" {
		return fmt.Sprintf("%s@%s", s.User, hostPort)
	}
	return hostPort
}

// Config is the top-level TOML configuration.
type Config struct {
	Defaults Defaults            `toml:"defaults,omitempty"`
	Servers  map[string]Server   `toml:"servers"`
}

// Load reads and parses a TOML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Ensure servers map
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]Server)
	}

	// Apply per-server defaults
	for name, srv := range cfg.Servers {
		srv.Name = name
		if srv.Port == 0 {
			srv.Port = 22
		}
		if srv.Method == "" {
			srv.Method = "rsync"
		}
		if srv.Tags == nil {
			srv.Tags = []string{}
		}
		if srv.RemotePath == "" {
			srv.RemotePath = cfg.Defaults.RemotePath
		}
		cfg.Servers[name] = srv
	}

	// Apply global defaults
	cfg.Defaults.SetDefaults()

	return &cfg, nil
}

// FindConfigPath searches for a config file. Priority:
//  1. explicit path (if non-empty)
//  2. ./deploi.toml
//  3. Platform-specific config directory:
//     Linux/macOS: ~/.config/deploi/config.toml
//     Windows:     %APPDATA%/deploi/config.toml
func FindConfigPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config file not found: %s", explicit)
		}
		return explicit, nil
	}

	candidates := []string{"deploi.toml"}

	// Platform-specific config directory
	path := PlatformConfigPath()
	if path != "" {
		candidates = append(candidates, path)
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no config file found. Searched: %s", strings.Join(candidates, ", "))
}

// PlatformConfigPath returns the platform-specific config file path.
func PlatformConfigPath() string {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "deploi", "config.toml")
	}

	// Linux / macOS: XDG spec
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".config", "deploi", "config.toml")
	}
	return filepath.Join(xdg, "deploi", "config.toml")
}

// DefaultExample returns a default config file content as a string.
func DefaultExample() string {
	return `# deploi.toml — v0id00/deploi configuration
# Multi-server file sync and deploy tool.
#
# Copy this file to ./deploi.toml or ~/.config/deploi/config.toml

[defaults]
# Connection / execution
timeout = 30
concurrency = 5
dry_run = false

# Safety
force = false

# Output
json = false
quiet = false
show_progress = true

# Editor for interactive file selection
editor = "vim"

# Confirm when targeting ALL servers
confirm_without_filter = true

# Default remote path (can be overridden per server)
remote_path = "/home/deploy/www/"

# Rsync binary (default: "rsync")
rsync_bin = "rsync"

# Default filter (applied when no CLI flag is given)
server = ""
tags = ""

# --- Servers ---

[servers.prod-web-1]
host = "web1.example.com"
port = 22
user = "deploy"
# password = ""          # Optional: use SSH key instead
# key_file = "~/.ssh/id_ed25519"
method = "rsync"         # rsync, sftp, ssh
tags = ["prod", "web"]
remote_path = "/var/www/project/"
rsync_options = "-avz --delete"

[servers.prod-web-2]
host = "web2.example.com"
port = 22
user = "deploy"
tags = ["prod", "web"]

[servers.staging]
host = "staging.example.com"
port = 22
user = "deploy"
tags = ["staging"]
# remote_path = "/home/deploy/staging/"  # overrides default
`
}

// ExpandPath expands ~ to the user's home directory.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
