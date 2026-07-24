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

	// Exclude patterns (like .gitignore). Glob patterns.
	Exclude []string `toml:"exclude,omitempty"`

	// Honour .gitignore when selecting files (default: true)
	RespectGitignore *bool `toml:"respect_gitignore,omitempty"`

	// Skip the diff preview confirmation (default: show preview)
	NoPreview bool `toml:"no_preview,omitempty"`

	// Default profile to use when --profile is not given
	Profile string `toml:"profile,omitempty"`
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
	// RespectGitignore defaults to true
	if d.RespectGitignore == nil {
		t := true
		d.RespectGitignore = &t
	}
}

// IsRespectGitignore returns whether .gitignore should be respected.
func (d *Defaults) IsRespectGitignore() bool {
	return d.RespectGitignore != nil && *d.RespectGitignore
}

// Hooks defines commands to run before/after transfer on a server.
type Hooks struct {
	Pre  []string `toml:"pre,omitempty"`
	Post []string `toml:"post,omitempty"`
}

// Profile defines a named deploy profile.
type Profile struct {
	Method     string   `toml:"method,omitempty"`
	Paths      []string `toml:"paths,omitempty"`
	RemotePath string   `toml:"remote_path,omitempty"`
	Exclude    []string `toml:"exclude,omitempty"`
	Commit     string   `toml:"commit,omitempty"`
	Branch     string   `toml:"branch,omitempty"`
	PickCommit bool     `toml:"pick_commit,omitempty"`
	RsyncOpts  string   `toml:"rsync_options,omitempty"`
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

	// Hooks: pre/post SSH commands
	Hooks *Hooks `toml:"hooks,omitempty"`

	// Per-server exclude overrides
	Exclude []string `toml:"exclude,omitempty"`

	// Notifications
	NotifyOnSuccess bool `toml:"notify_on_success,omitempty"`
	NotifyOnFail    bool `toml:"notify_on_fail,omitempty"`
}

// Addr returns an SSH connection string (user@host:port).
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
	Profiles map[string]Profile  `toml:"profiles,omitempty"`
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
		if srv.Exclude == nil {
			srv.Exclude = []string{}
		}
		cfg.Servers[name] = srv
	}

	// Apply global defaults
	cfg.Defaults.SetDefaults()

	return &cfg, nil
}

// FindConfigPath searches for a config file. Priority:
//  1. -c, --config explicit path
//  2. ./deploi.toml (current directory)
//  3. Platform config directory:
//     Linux/macOS: ~/.config/deploi/config.toml  (XDG)
//     Windows:     %APPDATA%/deploi/config.toml
//  4. ~/.deploi/config.toml (home subdirectory — universal fallback)
//  5. ~/.deploi.toml (home file — legacy fallback)
func FindConfigPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("config file not found: %s", explicit)
		}
		return explicit, nil
	}

	home, _ := os.UserHomeDir()
	candidates := []string{"deploi.toml"}

	// Platform-specific config directory
	if p := PlatformConfigPath(); p != "" {
		candidates = append(candidates, p)
	}

	// Home subdirectory (works on all platforms — Linux, macOS, Windows)
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".deploi", "config.toml"))
		// Home file fallback
		candidates = append(candidates, filepath.Join(home, ".deploi.toml"))
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no config file found. Searched:\n  %s", strings.Join(candidates, "\n  "))
}

// PlatformConfigPath returns the platform-specific config file path.
// Linux/macOS: ~/.config/deploi/config.toml (XDG spec)
// Windows:     %APPDATA%/deploi/config.toml
func PlatformConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	if runtime.GOOS == "windows" {
		// Windows: %APPDATA% first, fallback to USERPROFILE
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "deploi", "config.toml")
	}

	// Linux / macOS: XDG_CONFIG_HOME, fallback to ~/.config
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg != "" {
		return filepath.Join(xdg, "deploi", "config.toml")
	}
	return filepath.Join(home, ".config", "deploi", "config.toml")
}

// DefaultExample returns a default config file content as a string.
func DefaultExample() string {
	return `# deploi.toml — v0id00/deploi configuration
# Multi-server file sync and deploy tool.
#
# Copy this file to one of these locations:
#   ./deploi.toml                    (project-level)
#   ~/.config/deploi/config.toml     (Linux/macOS — XDG)
#   %APPDATA%/deploi/config.toml     (Windows)
#   ~/.deploi/config.toml            (universal home dir)
#   ~/.deploi.toml                   (legacy fallback)

[defaults]
# Connection / execution
timeout = 300
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

# Skip the diff preview before deploy (default: show preview)
no_preview = false

# Default filter (applied when no CLI flag is given)
server = ""
tags = ""

# Exclude patterns (like .gitignore). Auto-excluded if .gitignore exists.
# exclude = [".git", "node_modules", "*.log", ".env"]

# Honour .gitignore when selecting files (default: true)
# respect_gitignore = true

# Default profile (optional)
# profile = "full"

# --- Profiles ---
# Named deploy profiles for quick reuse.
# [profiles.full]
# method = "git-diff"
# remote_path = "/var/www/project/"
# 
# [profiles.assets]
# method = "all"
# paths = ["public/build/", "public/assets/"]

# --- Servers ---

[servers.prod-web-1]
host = "web1.example.com"
port = 22
user = "deploy"
method = "rsync"
tags = ["prod", "web"]
remote_path = "/var/www/project/"
rsync_options = "-avz --delete"

# Pre/post deploy hooks (SSH commands)
# [servers.prod-web-1.hooks]
# pre = [
#   "php artisan down",
#   "rm -rf var/cache/*"
# ]
# post = [
#   "php artisan migrate --force",
#   "php artisan up"
# ]

[servers.staging]
host = "staging.example.com"
port = 22
user = "deploy"
tags = ["staging"]
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

// HistoryDir returns the deploi history directory (~/.deploi/history/).
func HistoryDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, ".deploi", "history")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create history dir: %w", err)
	}
	return dir, nil
}
