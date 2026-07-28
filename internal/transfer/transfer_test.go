package transfer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/v0id00/deploi/internal/config"
)

func TestHasAnyTag(t *testing.T) {
	tests := []struct {
		name       string
		serverTags []string
		filterTags []string
		want       bool
	}{
		{"no filter", []string{"prod"}, nil, false},
		{"empty filter", []string{"prod"}, []string{}, false},
		{"exact match", []string{"prod"}, []string{"prod"}, true},
		{"multiple tags match", []string{"prod", "web"}, []string{"web"}, true},
		{"no match", []string{"prod"}, []string{"staging"}, false},
		{"partial in filter", []string{"prod"}, []string{"staging", "prod"}, true},
		{"empty both", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAnyTag(tt.serverTags, tt.filterTags); got != tt.want {
				t.Errorf("HasAnyTag(%v, %v) = %v, want %v", tt.serverTags, tt.filterTags, got, tt.want)
			}
		})
	}
}

func TestReadGitignore(t *testing.T) {
	dir := t.TempDir()

	patterns := ReadGitignore(dir)
	if patterns != nil {
		t.Errorf("expected nil for missing .gitignore, got %v", patterns)
	}

	giPath := filepath.Join(dir, ".gitignore")
	os.WriteFile(giPath, []byte("*.log\nnode_modules\n# comment\nbuild/\n/root_only\n\n"), 0644)

	patterns = ReadGitignore(dir)
	expected := []string{"*.log", "node_modules", "build", "root_only"}
	if !stringSliceEqual(patterns, expected) {
		t.Errorf("ReadGitignore() = %v, want %v", patterns, expected)
	}
}

func TestReadGitignoreEmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(""), 0644)

	patterns := ReadGitignore(dir)
	if len(patterns) != 0 {
		t.Errorf("expected empty, got %v", patterns)
	}
}

func TestBuildSSHOpts(t *testing.T) {
	tests := []struct {
		name string
		srv  config.Server
		want string
	}{
		{"default port no key", config.Server{}, "ssh"},
		{"custom port", config.Server{Port: 2222}, "ssh -p 2222"},
		{"with key", config.Server{KeyFile: "~/.ssh/id_ed25519"}, "ssh -i " + config.ExpandPath("~/.ssh/id_ed25519")},
		{"port and key", config.Server{Port: 2222, KeyFile: "~/.ssh/id_rsa"}, "ssh -p 2222 -i " + config.ExpandPath("~/.ssh/id_rsa")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildSSHOpts(tt.srv); got != tt.want {
				t.Errorf("buildSSHOpts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{2147483648, "2.0 GB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatBytes(tt.input); got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseRsyncOutput(t *testing.T) {
	output := `sending incremental file list
file1.txt
file2.txt

sent 123,456 bytes  received 987 bytes  55,555.00 bytes/sec
total size is 999,999  speedup is 7.25
`
	result := parseRsyncOutput(output)

	if result.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", result.FileCount)
	}
	if result.BytesSent != 123456 {
		t.Errorf("BytesSent = %d, want 123456", result.BytesSent)
	}
	if result.Speed != "55,555.00/s" {
		t.Errorf("Speed = %q, want 55,555.00/s", result.Speed)
	}
	if result.TotalSize != "999,999" {
		t.Errorf("TotalSize = %q, want 999,999", result.TotalSize)
	}
	if result.Summary == "" {
		t.Error("Summary should not be empty")
	}
}

func TestParseRsyncOutputDryRun(t *testing.T) {
	output := `sending incremental file list
file1.txt

sent 123 bytes  received 45 bytes  55.00 bytes/sec
total size is 1000  speedup is 5.95
`
	result := parseRsyncOutput(output)
	if result.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1", result.FileCount)
	}
	if result.BytesSent != 123 {
		t.Errorf("BytesSent = %d, want 123", result.BytesSent)
	}
}

func TestParseRsyncOutputNoFiles(t *testing.T) {
	output := `sending incremental file list

sent 100 bytes  received 20 bytes  10.00 bytes/sec
total size is 500  speedup is 4.17
`
	result := parseRsyncOutput(output)
	if result.FileCount != 0 {
		t.Errorf("FileCount = %d, want 0", result.FileCount)
	}
}

func TestParseRsyncOutputNoSummaryLine(t *testing.T) {
	output := `sending incremental file list
file.txt
`
	result := parseRsyncOutput(output)
	if result.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1", result.FileCount)
	}
	// Should have fallback summary
	if result.Summary == "" {
		t.Error("Summary should not be empty even without total line")
	}
}

func TestParseRsyncOutputFileLines(t *testing.T) {
	output := `sending incremental file list
src/main.go
src/utils.go
README.md

sent 1,000 bytes  received 10 bytes  100.00 bytes/sec
total size is 50,000  speedup is 49.50
`
	result := parseRsyncOutput(output)
	if result.FileCount != 3 {
		t.Errorf("FileCount = %d, want 3", result.FileCount)
	}
	expectedFiles := []string{"src/main.go", "src/utils.go", "README.md"}
	for i, f := range result.Files {
		if f != expectedFiles[i] {
			t.Errorf("Files[%d] = %q, want %q", i, f, expectedFiles[i])
		}
	}
}

func TestOperationString(t *testing.T) {
	tests := []struct {
		op   Operation
		want string
	}{
		{OpPush, "push"},
		{OpPull, "pull"},
		{OpSync, "sync"},
		{Operation(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.op.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildExcludeList(t *testing.T) {
	cfg := RunConfig{
		Exclude:     []string{".git", "node_modules"},
		NoGitignore: true,
	}

	result := buildExcludeList(cfg)
	expected := []string{".git", "node_modules"}
	if !stringSliceEqual(result, expected) {
		t.Errorf("buildExcludeList() = %v, want %v", result, expected)
	}
}

func TestBuildExcludeListWithGitignore(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n.env\n"), 0644)

	cfg := RunConfig{
		Exclude:     []string{".git"},
		NoGitignore: false,
		GitDir:      dir,
	}

	result := buildExcludeList(cfg)
	if len(result) < 2 {
		t.Errorf("expected at least 2 patterns, got %v", result)
	}
}

func TestEnrichRsyncError(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		output   string
		kind     string
		contains string
	}{
		{
			name:     "timeout",
			kind:     "timeout",
			contains: "timed out",
		},
		{
			name:     "no such file",
			exitCode: 1,
			output:   "No such file or directory",
			contains: "not found",
		},
		{
			name:     "permission denied",
			exitCode: 1,
			output:   "Permission denied",
			contains: "authentication",
		},
		{
			name:     "connection refused",
			exitCode: 1,
			output:   "connection refused",
			contains: "refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.exitCode > 0 {
				err = &mockExitError{exitCode: tt.exitCode}
			}
			msg := enrichRsyncError(err, []byte(tt.output), RunConfig{}, tt.kind)
			if !stringsContains(msg, tt.contains) {
				t.Errorf("enrichRsyncError() = %q, should contain %q", msg, tt.contains)
			}
		})
	}
}

func TestEnrichRsyncErrorDefaultExitCode(t *testing.T) {
	err := &mockExitError{exitCode: 99}
	msg := enrichRsyncError(err, []byte("random error"), RunConfig{}, "")
	if !stringsContains(msg, "99") {
		t.Errorf("expected exit code 99 in message, got: %s", msg)
	}
}

func TestEnrichRsyncErrorTruncatesLongOutput(t *testing.T) {
	longOutput := make([]byte, 1000)
	for i := range longOutput {
		longOutput[i] = 'x'
	}
	msg := enrichRsyncError(nil, longOutput, RunConfig{}, "")
	if len(msg) > 600 {
		t.Errorf("message too long: %d chars", len(msg))
	}
}

func TestGitDiffSummary(t *testing.T) {
	// Create a temp git repo with a change
	dir := t.TempDir()
	gitDir := filepath.Join(dir, "repo")
	os.MkdirAll(gitDir, 0755)

	// Only test non-git fallback (file list)
	os.WriteFile(filepath.Join(gitDir, "test.txt"), []byte("hello"), 0644)

	summary := GitDiffSummary([]string{filepath.Join(gitDir, "test.txt")}, gitDir)
	if summary == "" {
		t.Error("GitDiffSummary should not be empty")
	}
}

func TestFilterServers(t *testing.T) {
	servers := []config.Server{
		{Name: "prod-web", Host: "web1", Tags: []string{"prod", "web"}},
		{Name: "prod-db", Host: "db1", Tags: []string{"prod", "db"}},
		{Name: "staging", Host: "stg1", Tags: []string{"staging"}},
	}

	tests := []struct {
		name  string
		regex string
		tags  []string
		want  int
	}{
		{"no filter", "", nil, 3},
		{"by regex", "prod*", nil, 2},
		{"by exact", "staging", nil, 1},
		{"by tag", "", []string{"web"}, 1},
		{"by tag multiple", "", []string{"prod"}, 2},
		{"no match", "nonexistent", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterServers(servers, tt.regex, tt.tags)
			if len(got) != tt.want {
				t.Errorf("filterServers() = %d, want %d", len(got), tt.want)
			}
		})
	}
}

// --- edge case tests ---

func TestRunNoServers(t *testing.T) {
	results := Run(nil, RunConfig{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "error" {
		t.Errorf("Status = %q, want error", results[0].Status)
	}
}

func TestRunRollbackNoBackups(t *testing.T) {
	results := RunRollback(nil, RollbackOptions{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "error" {
		t.Errorf("Status = %q, want error", results[0].Status)
	}
}

func TestRunCommandsNoServers(t *testing.T) {
	results := RunCommands(nil, []string{"uptime"}, RunConfig{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "error" {
		t.Errorf("Status = %q, want error", results[0].Status)
	}
}

func TestGitDiffSummaryEmptyFiles(t *testing.T) {
	summary := GitDiffSummary(nil, "")
	if summary != "(no files)" {
		t.Errorf("GitDiffSummary() = %q, want \"(no files)\"", summary)
	}
}

func TestEnrichRsyncErrorExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		output   string
		contains string
	}{
		{"exit 10", 10, "", "socket I/O"},
		{"exit 11", 11, "", "file I/O"},
		{"exit 12", 12, "", "protocol data"},
		{"exit 23", 23, "", "partial transfer"},
		{"exit 30", 30, "", "timeout"},
		{"exit 23 permission", 23, "Permission denied", "write permissions"},
		{"exit 11 mkdir", 11, "mkdir", "Remote path"},
		{"exit 1 host key", 1, "Host key verification failed", "host key"},
		{"exit 1 name unknown", 1, "Name or service not known", "resolved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &mockExitError{exitCode: tt.code}
			msg := enrichRsyncError(err, []byte(tt.output), RunConfig{}, "")
			if !stringsContains(msg, tt.contains) {
				t.Errorf("enrichRsyncError() = %q, want to contain %q", msg, tt.contains)
			}
		})
	}
}

// --- helpers ---

type mockExitError struct {
	exitCode int
}

func (e *mockExitError) Error() string {
	return "exit status " + itoa(e.exitCode)
}

func (e *mockExitError) ExitCode() int {
	return e.exitCode
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

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
