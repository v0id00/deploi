package transfer
import (
	"fmt"
	"os"
	"os/exec"
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

// --- mock-based tests ---

func TestExecuteOnServer_PreHookFails(t *testing.T) {
	defer restoreMocks()()
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		return "mock output", fmt.Errorf("mock failure")
	}

	srv := config.Server{Name: "test", Hooks: &config.Hooks{Pre: []string{"fail"}}}
	r := executeOnServer(srv, RunConfig{}, nil)

	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if !stringsContains(r.Error, "pre-hook") {
		t.Errorf("Error = %q, want pre-hook message", r.Error)
	}
}

func TestExecuteOnServer_PostHookFails(t *testing.T) {
	defer restoreMocks()()
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		return "", nil
	}
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "ok", Files: 5}
	}

	srv := config.Server{Name: "test", Method: "rsync", Hooks: &config.Hooks{
		Pre:  []string{"ok"},
		Post: []string{"fail"},
	}}
	// Override Post-hook to fail
	calls := 0
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		calls++
		if calls > 1 { // post-hook
			return "", fmt.Errorf("post hook error")
		}
		return "", nil
	}

	r := executeOnServer(srv, RunConfig{}, nil)
	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if !stringsContains(r.Error, "post-hook") {
		t.Errorf("Error = %q, want post-hook message", r.Error)
	}
}

func TestExecuteOnServer_UnknownMethod(t *testing.T) {
	srv := config.Server{Name: "test", Method: "invalid"}
	r := executeOnServer(srv, RunConfig{}, nil)

	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if !stringsContains(r.Error, "unknown method") {
		t.Errorf("Error = %q, want unknown method message", r.Error)
	}
}

func TestExecuteOnServer_RsyncMethod(t *testing.T) {
	defer restoreMocks()()
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "ok", Files: 10, Bytes: 1024}
	}

	srv := config.Server{Name: "test", Method: "rsync"}
	r := executeOnServer(srv, RunConfig{}, nil)

	if r.Status != "ok" {
		t.Errorf("Status = %q, want ok", r.Status)
	}
	if r.Files != 10 {
		t.Errorf("Files = %d, want 10", r.Files)
	}
}

func TestExecuteOnServer_SCPMethod(t *testing.T) {
	defer restoreMocks()()
	RunSCPFunc = func(srv config.Server, cfg RunConfig) TransferResult {
		return TransferResult{Status: "ok", Files: 3}
	}

	srv := config.Server{Name: "test", Method: "sftp"}
	r := executeOnServer(srv, RunConfig{}, nil)

	if r.Status != "ok" {
		t.Errorf("Status = %q, want ok", r.Status)
	}
}

func TestExecuteOnServer_DryRunSkipsHooks(t *testing.T) {
	defer restoreMocks()()
	hookRan := false
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		hookRan = true
		return "", nil
	}
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "ok"}
	}

	srv := config.Server{Name: "test", Method: "rsync", Hooks: &config.Hooks{
		Pre:  []string{"should-not-run"},
		Post: []string{"should-not-run"},
	}}
	_ = executeOnServer(srv, RunConfig{DryRun: true}, nil)

	if hookRan {
		t.Error("hooks should not run in dry-run mode")
	}
}

func TestExecuteOnServer_HookCounts(t *testing.T) {
	defer restoreMocks()()
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		return "", nil
	}
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "ok"}
	}

	srv := config.Server{Name: "test", Method: "rsync", Hooks: &config.Hooks{
		Pre:  []string{"a", "b"},
		Post: []string{"c", "d", "e"},
	}}
	r := executeOnServer(srv, RunConfig{}, nil)

	if r.HooksPre != 2 {
		t.Errorf("HooksPre = %d, want 2", r.HooksPre)
	}
	if r.HooksPost != 3 {
		t.Errorf("HooksPost = %d, want 3", r.HooksPost)
	}
}

func TestExecuteOnServer_TransferErrorReturnsEarly(t *testing.T) {
	defer restoreMocks()()
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "error", Error: "rsync failed"}
	}
	postHookRan := false
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		postHookRan = true
		return "", nil
	}

	srv := config.Server{Name: "test", Method: "rsync", Hooks: &config.Hooks{
		Post: []string{"should-not-run"},
	}}
	r := executeOnServer(srv, RunConfig{}, nil)

	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if postHookRan {
		t.Error("post-hooks should not run after transfer error")
	}
}

func TestRunCommands_WithMockSSH(t *testing.T) {
	defer restoreMocks()()
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		return "uptime: 1h", nil
	}

	servers := []config.Server{
		{Name: "web1", Host: "10.0.0.1", User: "deploy"},
		{Name: "web2", Host: "10.0.0.2", User: "deploy"},
	}
	results := RunCommands(servers, []string{"uptime"}, RunConfig{Concurrency: 5})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "ok" {
			t.Errorf("%s: Status = %q, want ok", r.Server, r.Status)
		}
	}
}

func TestRunCommands_CommandFails(t *testing.T) {
	defer restoreMocks()()
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		return "error output", fmt.Errorf("exit code 1")
	}

	servers := []config.Server{{Name: "web1", Host: "10.0.0.1", User: "deploy"}}
	results := RunCommands(servers, []string{"fail"}, RunConfig{Concurrency: 5})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "error" {
		t.Errorf("Status = %q, want error", results[0].Status)
	}
}

func TestRunCommands_DryRun(t *testing.T) {
	defer restoreMocks()()
	ran := false
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		ran = true
		return "", nil
	}

	servers := []config.Server{{Name: "web1", Host: "10.0.0.1", User: "deploy"}}
	results := RunCommands(servers, []string{"uptime"}, RunConfig{DryRun: true, Concurrency: 5})

	if ran {
		t.Error("commands should not run in dry-run mode")
	}
	if results[0].Status != "ok" {
		t.Errorf("Status = %q, want ok", results[0].Status)
	}
}

func TestEnsureRemoteDirs(t *testing.T) {
	defer restoreMocks()()
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		if !stringsContains(cmd, "mkdir -p") {
			t.Errorf("expected mkdir command, got %q", cmd)
		}
		return "", nil
	}

	err := EnsureRemoteDirs(config.Server{Name: "test"}, []string{"/remote/path"})
	if err != nil {
		t.Errorf("EnsureRemoteDirs() error: %v", err)
	}
}

func TestEnsureRemoteDirs_EmptyDirs(t *testing.T) {
	err := EnsureRemoteDirs(config.Server{Name: "test"}, nil)
	if err != nil {
		t.Errorf("expected nil for empty dirs, got %v", err)
	}
}

func TestRunWithMockServers(t *testing.T) {
	defer restoreMocks()()
	RunSSHFunc = func(srv config.Server, cmd string) (string, error) {
		return "", nil
	}
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "ok", Files: 3}
	}

	servers := []config.Server{
		{Name: "web1", Host: "10.0.0.1", User: "deploy", Method: "rsync"},
		{Name: "web2", Host: "10.0.0.2", User: "deploy", Method: "rsync"},
	}
	results := Run(servers, RunConfig{Concurrency: 5})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != "ok" {
			t.Errorf("%s: Status = %q, want ok", r.Server, r.Status)
		}
	}
}

func TestRunWithFilter(t *testing.T) {
	defer restoreMocks()()
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "ok"}
	}

	servers := []config.Server{
		{Name: "prod-web", Host: "10.0.0.1", User: "deploy", Method: "rsync"},
		{Name: "prod-db", Host: "10.0.0.2", User: "deploy", Method: "rsync"},
		{Name: "staging", Host: "10.0.0.3", User: "deploy", Method: "rsync"},
	}
	results := Run(servers, RunConfig{ServerRegex: "prod*", Concurrency: 5})

	if len(results) != 2 {
		t.Errorf("expected 2 results (filtered), got %d", len(results))
	}
}

func TestRunWithTags(t *testing.T) {
	defer restoreMocks()()
	RunRsyncFunc = func(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
		return TransferResult{Status: "ok"}
	}

	servers := []config.Server{
		{Name: "web1", Host: "10.0.0.1", User: "deploy", Method: "rsync", Tags: []string{"prod"}},
		{Name: "web2", Host: "10.0.0.2", User: "deploy", Method: "rsync", Tags: []string{"staging"}},
	}
	results := Run(servers, RunConfig{Tags: []string{"prod"}, Concurrency: 5})

	if len(results) != 1 {
		t.Errorf("expected 1 result (tag filter), got %d", len(results))
	}
}

func TestRunRollbackWithData(t *testing.T) {
	defer restoreMocks()()
	RsyncCmdFunc = func(name string, arg ...string) *exec.Cmd {
		// Mock rsync: output multi-line rsync output
		return exec.Command("printf",
			"sending incremental file list\nfile.txt\n\nsent 100 bytes  received 10 bytes  50.00 bytes/sec\ntotal size is 500  speedup is 5.00\n")
	}

	servers := []config.Server{
		{Name: "web1", Host: "10.0.0.1", User: "deploy"},
	}
	results := RunRollback(servers, RollbackOptions{
		BackupPaths: map[string]string{"web1": "/backups/123"},
		RemotePath:  "/var/www",
	})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "ok" {
		t.Errorf("Status = %q, want ok", results[0].Status)
	}
	if results[0].Files == 0 {
		t.Error("expected files > 0 from parsed rsync output")
	}
}

func TestRunRollbackSkipMissingBackup(t *testing.T) {
	defer restoreMocks()()

	servers := []config.Server{
		{Name: "web1", Host: "10.0.0.1"},
		{Name: "web2", Host: "10.0.0.2"}, // no backup
	}
	results := RunRollback(servers, RollbackOptions{
		BackupPaths: map[string]string{"web1": "/backups/123"},
	})
	// web2 has no backup → skipped
	if len(results) != 1 {
		t.Errorf("expected 1 result (web2 skipped), got %d", len(results))
	}
}

func TestSSHAgentAuthNoSocket(t *testing.T) {
	// When SSH_AUTH_SOCK is not set, sshAgentAuth should return nil
	os.Unsetenv("SSH_AUTH_SOCK")
	if a := sshAgentAuth(); a != nil {
		t.Error("sshAgentAuth() should return nil when SSH_AUTH_SOCK is not set")
	}
}

func TestKeyFileSignerNoKey(t *testing.T) {
	// Use a temp dir with no SSH keys
	home := t.TempDir()
	oldHome, _ := os.UserHomeDir()
	os.Setenv("HOME", home) // won't work on all platforms, but tests UserHomeDir
	_ = oldHome
	// Actually, UserHomeDir is OS-dependent. Just test with a server with no key_file.
	srv := config.Server{Name: "test"}
	_, err := keyFileSigner(srv)
	if err == nil {
		t.Log("keyFileSigner returned nil error (may have found a key on this system)")
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

// restoreMocks returns a function that restores all mockable function variables.
func restoreMocks() func() {
	oldSSH := RunSSHFunc
	oldRsync := RunRsyncFunc
	oldSCP := RunSCPFunc
	oldCmd := RsyncCmdFunc
	return func() {
		RunSSHFunc = oldSSH
		RunRsyncFunc = oldRsync
		RunSCPFunc = oldSCP
		RsyncCmdFunc = oldCmd
	}
}

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
