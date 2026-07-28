package watcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestGitChangedFiles_NoGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := GitChangedFiles(dir)
	if err == nil {
		t.Fatal("Expected error for non-git directory")
	}
}

func TestGitChangedFiles_CleanRepo(t *testing.T) {
	dir := initTestRepo(t)

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no changes, got %v", files)
	}
}

func TestGitChangedFiles_UnstagedChange(t *testing.T) {
	dir := initTestRepo(t)

	// Modify a file
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("modified"), 0644)

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	if !contains(files, "test.txt") {
		t.Errorf("expected test.txt in changed files, got %v", files)
	}
}

func TestGitChangedFiles_StagedChange(t *testing.T) {
	dir := initTestRepo(t)

	// Stage a change
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("staged"), 0644)
	runGit(t, dir, "add", "test.txt")

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	if !contains(files, "test.txt") {
		t.Errorf("expected test.txt in changed files (staged), got %v", files)
	}
}

func TestGitChangedFiles_UntrackedFile(t *testing.T) {
	dir := initTestRepo(t)

	// Create a new untracked file — GitChangedFiles uses git diff which
	// only detects tracked file changes, not untracked files.
	os.WriteFile(filepath.Join(dir, "newfile.go"), []byte("package main"), 0644)

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("untracked files should not appear in git diff output, got %v", files)
	}
}

func TestGitChangedFiles_DeletedFile(t *testing.T) {
	dir := initTestRepo(t)

	// Delete a tracked file
	os.Remove(filepath.Join(dir, "test.txt"))

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	if !contains(files, "test.txt") {
		t.Errorf("expected test.txt in changed files (deleted), got %v", files)
	}
}

func TestGitChangedFiles_MultipleChanges(t *testing.T) {
	dir := initTestRepo(t)

	os.WriteFile(filepath.Join(dir, "a.go"), []byte("pkg a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("pkg b"), 0644)

	// Stage one
	runGit(t, dir, "add", "a.go")

	// Unstaged edit on another
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("changed"), 0644)

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	if len(files) < 2 {
		t.Errorf("expected at least 2 changed files, got %d: %v", len(files), files)
	}
}

func TestGitChangedFiles_NewFileInSubdir(t *testing.T) {
	dir := initTestRepo(t)

	subdir := filepath.Join(dir, "src", "util")
	os.MkdirAll(subdir, 0755)
	os.WriteFile(filepath.Join(subdir, "helper.go"), []byte("package util"), 0644)
	// Track it via git add so GitChangedFiles can detect it
	runGit(t, dir, "add", filepath.Join("src", "util", "helper.go"))

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	expected := filepath.Join("src", "util", "helper.go")
	if !contains(files, expected) {
		t.Errorf("expected %q in changed files (staged), got %v", expected, files)
	}
}

func TestGitChangedFiles_NewStagedFiles(t *testing.T) {
	dir := initTestRepo(t)

	os.WriteFile(filepath.Join(dir, "z.txt"), []byte("z"), 0644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "m.txt"), []byte("m"), 0644)
	runGit(t, dir, "add", ".")

	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files after git add, got %d: %v", len(files), files)
	}
}

func TestGitChangedFiles_EmptyStringTrim(t *testing.T) {
	dir := initTestRepo(t)

	// Empty output should return empty slice, not [""]
	files, err := GitChangedFiles(dir)
	if err != nil {
		t.Fatalf("GitChangedFiles() error: %v", err)
	}
	for _, f := range files {
		if f == "" {
			t.Error("found empty string in file list")
		}
	}
}

func TestAddRecursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub1", "nested"), 0755)
	os.MkdirAll(filepath.Join(dir, "sub2"), 0755)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Skipf("fsnotify not available: %v", err)
	}
	defer watcher.Close()

	err = addRecursive(watcher, dir)
	if err != nil {
		t.Fatalf("addRecursive() error: %v", err)
	}
	// No error means watched dirs were added. Should have watched:
	// dir, sub1, sub1/nested, sub2 (not .hidden, not node_modules)
}

func TestWatchCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	err := Watch(ctx, Config{Paths: []string{t.TempDir()}})
	if err != nil {
		t.Errorf("Watch() with cancelled context should return nil, got %v", err)
	}
}

// --- helpers ---

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Create an initial file and commit
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("initial"), 0644)
	runGit(t, dir, "add", "test.txt")
	runGit(t, dir, "commit", "-m", "initial")

	return dir
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

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
