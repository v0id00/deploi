package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsSetDefaults(t *testing.T) {
	d := Defaults{}
	d.SetDefaults()

	if d.Timeout != 30 {
		t.Errorf("Timeout = %d, want 30", d.Timeout)
	}
	if d.Concurrency != 5 {
		t.Errorf("Concurrency = %d, want 5", d.Concurrency)
	}
	if d.RsyncBin != "rsync" {
		t.Errorf("RsyncBin = %q, want rsync", d.RsyncBin)
	}
	if !d.ShowBar {
		t.Error("ShowBar = false, want true")
	}
	if d.RespectGitignore == nil || !*d.RespectGitignore {
		t.Error("RespectGitignore should default to true")
	}
}

func TestDefaultsSetDefaultsPreservesExplicit(t *testing.T) {
	tTrue := true
	d := Defaults{
		Timeout:              60,
		Concurrency:          10,
		RsyncBin:             "/usr/bin/rsync",
		RespectGitignore:     &tTrue,
		ConfirmWithoutFilter: true,
	}
	d.SetDefaults()

	if d.Timeout != 60 {
		t.Errorf("Timeout = %d, want 60", d.Timeout)
	}
	if d.Concurrency != 10 {
		t.Errorf("Concurrency = %d, want 10", d.Concurrency)
	}
	if d.RsyncBin != "/usr/bin/rsync" {
		t.Errorf("RsyncBin = %q, want /usr/bin/rsync", d.RsyncBin)
	}
	if !d.ShowBar {
		t.Error("ShowBar should be true after SetDefaults regardless of initial value")
	}
}

func TestIsRespectGitignore(t *testing.T) {
	tests := []struct {
		name string
		d    Defaults
		want bool
	}{
		{"nil", Defaults{RespectGitignore: nil}, false},
		{"true", Defaults{RespectGitignore: boolPtr(true)}, true},
		{"false", Defaults{RespectGitignore: boolPtr(false)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.IsRespectGitignore(); got != tt.want {
				t.Errorf("IsRespectGitignore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServerAddr(t *testing.T) {
	tests := []struct {
		name       string
		srv        Server
		wantAddr   string
		wantNoPort string
	}{
		{
			name:       "basic",
			srv:        Server{Host: "example.com", User: "deploy", Port: 22},
			wantAddr:   "deploy@example.com",
			wantNoPort: "deploy@example.com",
		},
		{
			name:       "non-standard port",
			srv:        Server{Host: "example.com", User: "deploy", Port: 2222},
			wantAddr:   "deploy@example.com:2222",
			wantNoPort: "deploy@example.com",
		},
		{
			name:       "no user",
			srv:        Server{Host: "example.com", Port: 22},
			wantAddr:   "example.com",
			wantNoPort: "example.com",
		},
		{
			name:       "no user non-standard port",
			srv:        Server{Host: "example.com", Port: 2222},
			wantAddr:   "example.com:2222",
			wantNoPort: "example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.srv.Addr(); got != tt.wantAddr {
				t.Errorf("Addr() = %q, want %q", got, tt.wantAddr)
			}
			if got := tt.srv.AddrNoPort(); got != tt.wantNoPort {
				t.Errorf("AddrNoPort() = %q, want %q", got, tt.wantNoPort)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tomlContent := `
[defaults]
timeout = 60
concurrency = 10

[servers.prod]
host = "prod.example.com"
user = "deploy"

[servers.staging]
host = "staging.example.com"
user = "deploy"
port = 2222
method = "sftp"
tags = ["staging"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploi.toml")
	if err := os.WriteFile(path, []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Defaults.Timeout != 60 {
		t.Errorf("Timeout = %d, want 60", cfg.Defaults.Timeout)
	}
	if cfg.Defaults.Concurrency != 10 {
		t.Errorf("Concurrency = %d, want 10", cfg.Defaults.Concurrency)
	}
	if !cfg.Defaults.ShowBar {
		t.Error("ShowBar should be true after SetDefaults")
	}

	if len(cfg.Servers) != 2 {
		t.Fatalf("len(Servers) = %d, want 2", len(cfg.Servers))
	}

	prod := cfg.Servers["prod"]
	if prod.Name != "prod" {
		t.Errorf("prod.Name = %q, want prod", prod.Name)
	}
	if prod.Port != 22 {
		t.Errorf("prod.Port = %d, want 22", prod.Port)
	}
	if prod.Method != "rsync" {
		t.Errorf("prod.Method = %q, want rsync", prod.Method)
	}

	staging := cfg.Servers["staging"]
	if staging.Port != 2222 {
		t.Errorf("staging.Port = %d, want 2222", staging.Port)
	}
	if staging.Method != "sftp" {
		t.Errorf("staging.Method = %q, want sftp", staging.Method)
	}
	if len(staging.Tags) != 1 || staging.Tags[0] != "staging" {
		t.Errorf("staging.Tags = %v, want [staging]", staging.Tags)
	}
}

func TestLoadInvalidToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("[[[ invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid TOML")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/deploi.toml")
	if err == nil {
		t.Fatal("Load() expected error for missing file")
	}
}

func TestFindConfigPathExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "myconfig.toml")
	os.WriteFile(path, []byte("[servers]\n"), 0644)

	got, err := FindConfigPath(path)
	if err != nil {
		t.Fatalf("FindConfigPath() error: %v", err)
	}
	if got != path {
		t.Errorf("FindConfigPath() = %q, want %q", got, path)
	}
}

func TestFindConfigPathExplicitNotFound(t *testing.T) {
	_, err := FindConfigPath("/nonexistent/deploi.toml")
	if err == nil {
		t.Fatal("FindConfigPath expected error for nonexistent path")
	}
}

func TestFindConfigPathLocalFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("deploi.toml", []byte("[servers]\n"), 0644)

	got, err := FindConfigPath("")
	if err != nil {
		t.Fatalf("FindConfigPath() error: %v", err)
	}
	if got != "deploi.toml" {
		t.Errorf("FindConfigPath() = %q, want deploi.toml", got)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"~/projects/deploi", filepath.Join(home, "projects/deploi")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ExpandPath(tt.input); got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHistoryDir(t *testing.T) {
	dir, err := HistoryDir()
	if err != nil {
		t.Fatalf("HistoryDir() error: %v", err)
	}
	if dir == "" {
		t.Fatal("HistoryDir() returned empty path")
	}
	// Directory should exist
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("HistoryDir() directory not created: %v", err)
	}
}

func TestDefaultExampleNotEmpty(t *testing.T) {
	content := DefaultExample()
	if content == "" {
		t.Fatal("DefaultExample() returned empty string")
	}
	// Should contain key sections
	for _, s := range []string{"[defaults]", "[servers.prod-web-1]", "[servers.staging]"} {
		if !contains(content, s) {
			t.Errorf("DefaultExample() missing section: %s", s)
		}
	}
}

func TestLoadEmptyServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.toml")
	os.WriteFile(path, []byte("[defaults]\n"), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Servers == nil {
		t.Error("Servers map should not be nil")
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("len(Servers) = %d, want 0", len(cfg.Servers))
	}
}

func TestServerRemotePathInherited(t *testing.T) {
	tomlContent := `
[defaults]
remote_path = "/var/www"

[servers.web]
host = "web.example.com"
user = "deploy"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.toml")
	os.WriteFile(path, []byte(tomlContent), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Servers["web"].RemotePath != "/var/www" {
		t.Errorf("RemotePath = %q, want /var/www", cfg.Servers["web"].RemotePath)
	}
}

func boolPtr(b bool) *bool { return &b }
func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}
func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
