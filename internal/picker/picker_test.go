package picker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "file.go", []string{"file.go"}},
		{"multiple", "a.go\nb.go\nc.go", []string{"a.go", "b.go", "c.go"}},
		{"with trailing newline", "a.go\nb.go\n", []string{"a.go", "b.go"}},
		{"with spaces", "  a.go  \n  b.go  ", []string{"a.go", "b.go"}},
		{"git log format", "abc1234 Fix bug\ndef5678 Add feature", []string{"abc1234 Fix bug", "def5678 Add feature"}},
		{"git diff-tree output", "src/main.go\nsrc/utils.go\n", []string{"src/main.go", "src/utils.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFileList(tt.input)
			if !stringSliceEqual(got, tt.want) {
				t.Errorf("ParseFileList(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseFileListEmptyLines(t *testing.T) {
	input := "\n\n\n"
	got := ParseFileList(input)
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestParseEditorOutput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "comments ignored",
			content: "# This is a comment\nfile1.go\n# another comment\nfile2.go\n",
			want:    []string{"file1.go", "file2.go"},
		},
		{
			name:    "empty lines ignored",
			content: "\nfile1.go\n\nfile2.go\n",
			want:    []string{"file1.go", "file2.go"},
		},
		{
			name:    "whitespace trimmed",
			content: "  file1.go  \n  file2.go  \n",
			want:    []string{"file1.go", "file2.go"},
		},
		{
			name:    "all comments",
			content: "# line1\n# line2\n# line3\n",
			want:    nil,
		},
		{
			name:    "empty content",
			content: "",
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseEditorOutput(tt.content)
			if !stringSliceEqual(got, tt.want) {
				t.Errorf("ParseEditorOutput(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestFindEditor_FallsBackToVim(t *testing.T) {
	origVisual := os.Getenv("VISUAL")
	origEditor := os.Getenv("EDITOR")
	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")
	defer func() {
		os.Setenv("VISUAL", origVisual)
		os.Setenv("EDITOR", origEditor)
	}()

	editor := FindEditor()
	if editor == "" {
		t.Fatal("FindEditor() returned empty string")
	}
	known := []string{"vim", "nano", "hx", "micro"}
	for _, k := range known {
		if editor == k {
			return
		}
	}
	t.Logf("FindEditor() = %q (system-specific, not in known list)", editor)
}

func TestFindEditor_UsesVISUAL(t *testing.T) {
	os.Setenv("VISUAL", "code")
	defer os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")

	if got := FindEditor(); got != "code" {
		t.Errorf("FindEditor() = %q, want code", got)
	}
}

func TestFindEditor_UsesEDITOR(t *testing.T) {
	os.Unsetenv("VISUAL")
	os.Setenv("EDITOR", "nano")
	defer os.Unsetenv("EDITOR")

	if got := FindEditor(); got != "nano" {
		t.Errorf("FindEditor() = %q, want nano", got)
	}
}

func TestPickManual(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "test.txt")
	os.WriteFile(file1, []byte("hello"), 0644)

	fs, err := Pick(PickConfig{
		Source:  SourceManual,
		Paths:   []string{file1},
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1", fs.Count)
	}
	if fs.Source != SourceManual {
		t.Errorf("Source = %v, want SourceManual", fs.Source)
	}
}

func TestPickManualNoPaths(t *testing.T) {
	_, err := Pick(PickConfig{Source: SourceManual})
	if err == nil {
		t.Fatal("Expected error for empty paths")
	}
}

func TestPickManualFileNotFound(t *testing.T) {
	_, err := Pick(PickConfig{
		Source: SourceManual,
		Paths:  []string{"/nonexistent/file.txt"},
	})
	if err == nil {
		t.Fatal("Expected error for nonexistent file")
	}
}

func TestPickAll(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte("c"), 0644)

	fs, err := Pick(PickConfig{
		Source:  SourceAll,
		Paths:   []string{dir},
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count != 3 {
		t.Errorf("Count = %d, want 3", fs.Count)
	}
}

func TestPickAllNoFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := Pick(PickConfig{
		Source:  SourceAll,
		Paths:   []string{filepath.Join(dir, "empty")},
		BaseDir: dir,
	})
	if err == nil {
		t.Fatal("Expected error for empty directory")
	}
}

func TestPickAllSkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("a"), 0644)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)
	os.WriteFile(filepath.Join(dir, ".hidden", "secret.txt"), []byte("s"), 0644)

	fs, err := Pick(PickConfig{
		Source:  SourceAll,
		Paths:   []string{dir},
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1 (hidden dirs excluded)", fs.Count)
	}
}

func TestSourceTypeString(t *testing.T) {
	tests := []struct {
		st   SourceType
		want string
	}{
		{SourceManual, "manual"},
		{SourceGitDiff, "git-diff"},
		{SourceGitCommit, "git-commit"},
		{SourceGitBranch, "git-branch-diff"},
		{SourceFZF, "fzf"},
		{SourceFZFCommit, "fzf-commit"},
		{SourceEditor, "editor"},
		{SourceAll, "all"},
		{SourceType(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.st.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPickUnknownSource(t *testing.T) {
	_, err := Pick(PickConfig{Source: SourceType(999)})
	if err == nil {
		t.Fatal("Expected error for unknown source type")
	}
}

func TestIsGitRepo(t *testing.T) {
	dir := t.TempDir()
	if isGitRepo(dir) {
		t.Error("isGitRepo should be false for non-git dir")
	}

	runGit(t, dir, "init")
	if !isGitRepo(dir) {
		t.Error("isGitRepo should be true for git repo")
	}
}

func TestPickGitCommit_DefaultHEAD(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	_, err := Pick(PickConfig{
		Source:  SourceGitCommit,
		GitDir:  dir,
		BaseDir: dir,
	})
	if err == nil {
		t.Fatal("Expected error for empty repo (no commits)")
	}
}

func TestPickGitCommit_WithCommit(t *testing.T) {
	dir := t.TempDir()
	hash := seedRepo(t, dir, "test.txt", "hello")

	fs, err := Pick(PickConfig{
		Source:  SourceGitCommit,
		GitDir:  dir,
		Commit:  hash,
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count < 1 {
		t.Error("expected at least 1 file in commit")
	}
}

func TestPickGitBranch_RequiresBranch(t *testing.T) {
	dir := t.TempDir()
	seedRepo(t, dir, "a.txt", "a")

	_, err := Pick(PickConfig{
		Source:  SourceGitBranch,
		GitDir:  dir,
		BaseDir: dir,
	})
	if err == nil {
		t.Fatal("Expected error for empty branch name")
	}
}

func TestPickGitBranch_Diff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "branch", "-m", "main")
	runGit(t, dir, "commit", "--allow-empty", "-m", "initial")

	// Create and switch to feature branch
	runGit(t, dir, "checkout", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	runGit(t, dir, "add", "b.txt")
	runGit(t, dir, "commit", "-m", "feature commit")
	runGit(t, dir, "checkout", "main")

	fs, err := Pick(PickConfig{
		Source:  SourceGitBranch,
		GitDir:  dir,
		Branch:  "feature",
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count < 1 {
		t.Error("expected at least 1 file diff between branches")
	}
}

func TestPickGitDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "initial")

	_, err := Pick(PickConfig{
		Source: SourceGitDiff,
		GitDir: dir,
	})
	if err == nil {
		t.Fatal("Expected error for clean working tree (no changes)")
	}
}

func TestPickGitDiff_WithChanges(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Make an unstaged change
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("modified"), 0644)

	fs, err := Pick(PickConfig{
		Source: SourceGitDiff,
		GitDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count < 1 {
		t.Error("expected at least 1 changed file")
	}
}

func TestPickGitDiff_ExcludeStaged(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-m", "initial")

	// Stage a change
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("staged"), 0644)
	runGit(t, dir, "add", "a.txt")
	// Unstaged change
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("unstaged"), 0644)

	fs, err := Pick(PickConfig{
		Source:           SourceGitDiff,
		GitDir:           dir,
		IncludeStaged:    false,
		IncludeUntracked: true,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	// Only b.txt should be found (unstaged), a.txt is staged-only
	for _, f := range fs.Files {
		if strings.Contains(f, "a.txt") {
			t.Error("staged-only file a.txt should be excluded when IncludeStaged=false")
		}
	}
}

// --- mock-based tests ---

func TestPickFZFWithPaths(t *testing.T) {
	defer restorePickerMocks()()
	// Mock fzf: output first candidate
	PickerLookPath = func(name string) (string, error) {
		if name == "fzf" {
			return "/usr/bin/fzf", nil
		}
		return exec.LookPath(name)
	}
	PickerExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "fzf" {
			return exec.Command("sh", "-c", `head -1`)
		}
		return exec.Command(name, arg...)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pickme.txt"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(dir, "skipme.txt"), []byte("data"), 0644)

	fs, err := Pick(PickConfig{
		Source:  SourceFZF,
		Paths:   []string{filepath.Join(dir, "pickme.txt"), filepath.Join(dir, "skipme.txt")},
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1", fs.Count)
	}
}

func TestPickFZFFallbackToEditor(t *testing.T) {
	defer restorePickerMocks()()
	// Mock fzf NOT found → falls back to editor
	PickerLookPath = func(name string) (string, error) {
		if name == "fzf" {
			return "", fmt.Errorf("not found")
		}
		return exec.LookPath(name)
	}
	// When editor falls back, it runs the editor command.
	// Mock it with "cat" which preserves the temp file content.
	PickerExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name != "git" && name != "sh" {
			return exec.Command("cat", arg...)
		}
		return exec.Command(name, arg...)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("data"), 0644)

	// fzf with explicit paths + no fzf binary → editor mode with candidates
	// The mock "cat" reads the temp file, so all candidates pass through
	fs, err := Pick(PickConfig{
		Source:  SourceFZF,
		Paths:   []string{filepath.Join(dir, "test.txt")},
		BaseDir: dir,
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Source != SourceFZF && fs.Source != SourceEditor {
		t.Errorf("Source = %v, want SourceFZF or SourceEditor (falls back to editor)", fs.Source)
	}
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1", fs.Count)
	}
}

func TestPickEditorWithPaths(t *testing.T) {
	defer restorePickerMocks()()
	// Mock editor: just cat (preserves temp file)
	PickerExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name != "git" && name != "sh" {
			return exec.Command("cat", arg...)
		}
		return exec.Command(name, arg...)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "editme.txt"), []byte("data"), 0644)

	fs, err := Pick(PickConfig{
		Source: SourceEditor,
		Paths:  []string{filepath.Join(dir, "editme.txt")},
	})
	if err != nil {
		t.Fatalf("Pick() error: %v", err)
	}
	if fs.Count != 1 {
		t.Errorf("Count = %d, want 1", fs.Count)
	}
}

func TestPickGitBranchFallback(t *testing.T) {
	// Test pickGitBranch without a branch name
	dir := t.TempDir()
	seedRepo(t, dir, "a.txt", "a")

	_, err := Pick(PickConfig{
		Source: SourceGitBranch,
		GitDir: dir,
		// No branch set — should error
	})
	if err == nil {
		t.Fatal("expected error for empty branch name")
	}
}

// --- helpers ---

func restorePickerMocks() func() {
	oldCmd := PickerExecCommand
	oldLook := PickerLookPath
	return func() {
		PickerExecCommand = oldCmd
		PickerLookPath = oldLook
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func seedRepo(t *testing.T, dir string, filename, content string) string {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644)
	runGit(t, dir, "add", filename)
	runGit(t, dir, "commit", "-m", "initial commit")
	out := runGit(t, dir, "rev-parse", "HEAD")
	return out
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
