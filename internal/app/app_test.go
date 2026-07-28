package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/v0id00/deploi/internal/config"
	"github.com/v0id00/deploi/internal/transfer"
)

func TestCwd(t *testing.T) {
	wd, _ := os.Getwd()
	if got := cwd(); got != wd {
		t.Errorf("cwd() = %q, want %q", got, wd)
	}
}

func TestSplitTags(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"prod", []string{"prod"}},
		{"prod,web", []string{"prod", "web"}},
		{" prod , web ", []string{"prod", "web"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitTags(tt.input)
			if !stringSliceEqual(got, tt.want) {
				t.Errorf("splitTags(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMergeDefaults(t *testing.T) {
	tests := []struct {
		name string
		ac   *appConfig
		d    *config.Defaults
		want appConfig
	}{
		{
			name: "apply timeout and concurrency",
			ac:   &appConfig{timeout: 0, concurrency: 0},
			d:    &config.Defaults{Timeout: 60, Concurrency: 10},
			want: appConfig{timeout: 60, concurrency: 10},
		},
		{
			name: "preserve explicit values",
			ac:   &appConfig{timeout: 120, concurrency: 20},
			d:    &config.Defaults{Timeout: 60, Concurrency: 10},
			want: appConfig{timeout: 120, concurrency: 20},
		},
		{
			name: "dry-run from defaults",
			ac:   &appConfig{timeout: 30, concurrency: 5},
			d:    &config.Defaults{DryRun: true},
			want: appConfig{timeout: 30, concurrency: 5, dryRun: true},
		},
		{
			name: "force skips confirm and preview",
			ac:   &appConfig{timeout: 30, concurrency: 5, force: true},
			d:    &config.Defaults{},
			want: appConfig{timeout: 30, concurrency: 5, force: true, noConfirm: true, noPreview: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.d.SetDefaults()
			mergeDefaults(tt.ac, tt.d)
			if tt.ac.timeout != tt.want.timeout {
				t.Errorf("timeout = %d, want %d", tt.ac.timeout, tt.want.timeout)
			}
			if tt.ac.concurrency != tt.want.concurrency {
				t.Errorf("concurrency = %d, want %d", tt.ac.concurrency, tt.want.concurrency)
			}
			if tt.ac.dryRun != tt.want.dryRun {
				t.Errorf("dryRun = %v, want %v", tt.ac.dryRun, tt.want.dryRun)
			}
			if tt.ac.noConfirm != tt.want.noConfirm {
				t.Errorf("noConfirm = %v, want %v", tt.ac.noConfirm, tt.want.noConfirm)
			}
			if tt.ac.noPreview != tt.want.noPreview {
				t.Errorf("noPreview = %v, want %v", tt.ac.noPreview, tt.want.noPreview)
			}
		})
	}
}

func TestMergeExcludes(t *testing.T) {
	tests := []struct {
		name    string
		def     []string
		profile []string
		want    []string
	}{
		{"no profile", []string{".git", "node_modules"}, nil, []string{".git", "node_modules"}},
		{"empty profile", []string{".git"}, []string{}, []string{".git"}},
		{"merge", []string{".git"}, []string{"*.log"}, []string{".git", "*.log"}},
		{"dedup", []string{".git"}, []string{".git", "node_modules"}, []string{".git", "node_modules"}},
		{"profile only", nil, []string{"*.log"}, []string{"*.log"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeExcludes(tt.def, tt.profile)
			if !stringSliceEqual(got, tt.want) {
				t.Errorf("mergeExcludes(%v, %v) = %v, want %v", tt.def, tt.profile, got, tt.want)
			}
		})
	}
}

func TestTransferHasErrors(t *testing.T) {
	tests := []struct {
		name    string
		results []transfer.TransferResult
		want    bool
	}{
		{"empty", nil, false},
		{"all ok", []transfer.TransferResult{{Status: "ok"}, {Status: "ok"}}, false},
		{"one error", []transfer.TransferResult{{Status: "ok"}, {Status: "error"}}, true},
		{"all error", []transfer.TransferResult{{Status: "error"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := transferHasErrors(tt.results); got != tt.want {
				t.Errorf("transferHasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountErrors(t *testing.T) {
	tests := []struct {
		name    string
		results []transfer.TransferResult
		want    int
	}{
		{"empty", nil, 0},
		{"all ok", []transfer.TransferResult{{Status: "ok"}, {Status: "ok"}}, 0},
		{"one error", []transfer.TransferResult{{Status: "ok"}, {Status: "error"}}, 1},
		{"two errors", []transfer.TransferResult{{Status: "error"}, {Status: "error"}}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countErrors(tt.results); got != tt.want {
				t.Errorf("countErrors() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildExcludePatterns(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n.env\n"), 0644)

	patterns := buildExcludePatterns([]string{".git"}, true, dir)
	if len(patterns) < 2 {
		t.Errorf("expected at least 2 patterns, got %v", patterns)
	}
}

func TestBuildExcludePatternsNoGitignore(t *testing.T) {
	patterns := buildExcludePatterns([]string{".git"}, false, t.TempDir())
	want := []string{".git"}
	if !stringSliceEqual(patterns, want) {
		t.Errorf("buildExcludePatterns() = %v, want %v", patterns, want)
	}
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		patterns []string
		want     bool
	}{
		{"no match", "main.go", []string{"*.log"}, false},
		{"filename glob", "test.log", []string{"*.log"}, true},
		{"exact match", "node_modules", []string{"node_modules"}, true},
		{"subdir match", "src/node_modules/pkg", []string{"node_modules"}, true},
		{"empty patterns", "main.go", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wd, _ := os.Getwd()
			fullPath := filepath.Join(wd, tt.path)
			if got := isExcluded(fullPath, tt.patterns); got != tt.want {
				t.Errorf("isExcluded(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestEnrichConnError(t *testing.T) {
	tests := []struct {
		err  string
		want string
	}{
		{"connection refused", "refused"},
		{"i/o timeout", "timed out"},
		{"no route to host", "No route"},
		{"Name or service not known", "not found"},
		{"random error", "random error"},
	}
	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			got := enrichConnError(&mockConnErr{msg: tt.err})
			if !stringsContains(got, tt.want) {
				t.Errorf("enrichConnError(%q) = %q, want to contain %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestGuessDefaultPath(t *testing.T) {
	tests := []struct {
		name    string
		servers []config.Server
		want    string
	}{
		{"empty", nil, "/home/deploy/www/"},
		{"single", []config.Server{{RemotePath: "/var/www"}}, "/var/www"},
		{"most common", []config.Server{
			{RemotePath: "/var/www"},
			{RemotePath: "/var/www"},
			{RemotePath: "/opt/app"},
		}, "/var/www"},
		{"no remote path set", []config.Server{{}}, "/home/deploy/www/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := guessDefaultPath(tt.servers); got != tt.want {
				t.Errorf("guessDefaultPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyProfile(t *testing.T) {
	ac := &appConfig{}
	p := config.Profile{
		Method:     "git-diff",
		Paths:      []string{"src/", "config/"},
		RemotePath: "/var/www",
		Commit:     "abc1234",
		Branch:     "main",
		PickCommit: true,
		RsyncOpts:  "-avz --delete",
		Exclude:    []string{"*.log"},
	}
	applyProfile(ac, p)

	if ac.selectMode != "git-diff" {
		t.Errorf("selectMode = %q, want git-diff", ac.selectMode)
	}
	if len(ac.paths) != 2 || ac.paths[0] != "src/" {
		t.Errorf("paths = %v, want [src/ config/]", ac.paths)
	}
	if ac.remoteDir != "/var/www" {
		t.Errorf("remoteDir = %q, want /var/www", ac.remoteDir)
	}
	if ac.commit != "abc1234" {
		t.Errorf("commit = %q, want abc1234", ac.commit)
	}
	if ac.branch != "main" {
		t.Errorf("branch = %q, want main", ac.branch)
	}
	if !ac.pick {
		t.Error("pick should be true")
	}
	if ac.rsyncOpts != "-avz --delete" {
		t.Errorf("rsyncOpts = %q, want -avz --delete", ac.rsyncOpts)
	}
	if len(ac.exclude) != 1 || ac.exclude[0] != "*.log" {
		t.Errorf("exclude = %v, want [*.log]", ac.exclude)
	}
}

func TestApplyProfilePartial(t *testing.T) {
	ac := &appConfig{selectMode: "all", remoteDir: "/original"}
	p := config.Profile{Method: "git-diff"} // only set method
	applyProfile(ac, p)

	if ac.selectMode != "git-diff" {
		t.Errorf("selectMode = %q, want git-diff", ac.selectMode)
	}
	// Fields not in profile should keep original values
	if ac.remoteDir != "/original" {
		t.Errorf("remoteDir = %q, want /original (preserved)", ac.remoteDir)
	}
	if ac.pick {
		t.Error("pick should remain false")
	}
}

func TestIsRealTerminal(t *testing.T) {
	// In test environment, stdin is piped, not a terminal
	if isRealTerminal() {
		t.Log("isRealTerminal() = true (running in interactive terminal)")
	}
}

type mockConnErr struct {
	msg string
}

func (e *mockConnErr) Error() string { return e.msg }

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && stringsIndex(s, substr) >= 0
}

func stringsIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
