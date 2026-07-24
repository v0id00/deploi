// Package picker provides multiple file selection strategies.
// It can pick files from git changes, commits, user input, or fzf.
package picker

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SourceType defines how files are selected.
type SourceType int

const (
	SourceManual    SourceType = iota // Explicit file paths
	SourceGitDiff                     // Files changed in working tree
	SourceGitCommit                   // Files in a specific commit
	SourceGitBranch                   // Files changed between branches
	SourceFZF                         // fzf interactive picker
	SourceEditor                      // Editor-based selection
	SourceAll                         // All files in a directory
)

// String returns the human-readable name.
func (s SourceType) String() string {
	switch s {
	case SourceManual:
		return "manual"
	case SourceGitDiff:
		return "git-diff"
	case SourceGitCommit:
		return "git-commit"
	case SourceGitBranch:
		return "git-branch-diff"
	case SourceFZF:
		return "fzf"
	case SourceEditor:
		return "editor"
	case SourceAll:
		return "all"
	default:
		return "unknown"
	}
}

// PickConfig holds parameters for file selection.
type PickConfig struct {
	Source   SourceType
	Paths    []string // explicit paths (for SourceManual / SourceAll)
	GitDir   string   // git working directory (default: cwd)
	Commit   string   // commit hash or ref (for SourceGitCommit)
	Branch   string   // branch name (for SourceGitBranch)
	Editor   string   // editor binary
	BaseDir  string   // base directory for relative paths
}

// FileSet holds the result of file selection.
type FileSet struct {
	Source  SourceType `json:"source"`
	Files   []string   `json:"files"`   // selected file paths (relative or absolute)
	AbsDir  string     `json:"abs_dir"` // base absolute directory
	Count   int        `json:"count"`
	Label   string     `json:"label"` // human-readable label
}

// Pick selects files based on the configuration.
func Pick(cfg PickConfig) (*FileSet, error) {
	// Determine base directory
	baseDir := cfg.BaseDir
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
	}

	switch cfg.Source {
	case SourceManual:
		return pickManual(cfg.Paths, baseDir)
	case SourceGitDiff:
		return pickGitDiff(cfg.GitDir, baseDir)
	case SourceGitCommit:
		return pickGitCommit(cfg.GitDir, cfg.Commit, baseDir)
	case SourceGitBranch:
		return pickGitBranch(cfg.GitDir, cfg.Branch, baseDir)
	case SourceFZF:
		return pickFZF(cfg.Paths, cfg.GitDir, baseDir)
	case SourceEditor:
		return pickEditor(cfg.Paths, cfg.Editor, baseDir)
	case SourceAll:
		return pickAll(cfg.Paths, baseDir)
	default:
		return nil, fmt.Errorf("unknown source type: %v", cfg.Source)
	}
}

// pickManual returns the explicitly provided file paths.
func pickManual(paths []string, baseDir string) (*FileSet, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no file paths provided for manual selection")
	}

	// Check each path exists and resolve to absolute
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(p) {
			abs = filepath.Join(baseDir, p)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("file not found: %s (%s)", p, abs)
		}
		resolved = append(resolved, abs)
	}

	relPaths := make([]string, len(resolved))
	for i, p := range resolved {
		rel, _ := filepath.Rel(baseDir, p)
		relPaths[i] = rel
	}

	return &FileSet{
		Source: SourceManual,
		Files:  resolved,
		AbsDir: baseDir,
		Count:  len(resolved),
		Label:  fmt.Sprintf("manual (%d files)", len(resolved)),
	}, nil
}

// pickGitDiff returns files that have changed in the working tree.
// It includes both staged and unstaged changes.
func pickGitDiff(gitDir, baseDir string) (*FileSet, error) {
	if gitDir == "" {
		gitDir = baseDir
	}

	// Check if we're in a git repo
	if !isGitRepo(gitDir) {
		return nil, fmt.Errorf("not a git repository: %s", gitDir)
	}

	// Get tracked file changes: unstaged changes
	cmd := exec.Command("git", "-C", gitDir, "diff", "--name-only", "--diff-filter=ACDMRTUXB")
	unstaged, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	// Get staged changes
	cmd = exec.Command("git", "-C", gitDir, "diff", "--cached", "--name-only", "--diff-filter=ACDMRTUXB")
	staged, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}

	// Merge and deduplicate
	all := mergeFileLists(string(unstaged), string(staged))
	if len(all) == 0 {
		return nil, fmt.Errorf("no changed files found in working tree")
	}

	absFiles := make([]string, len(all))
	for i, f := range all {
		absFiles[i] = filepath.Join(gitDir, f)
	}

	return &FileSet{
		Source: SourceGitDiff,
		Files:  absFiles,
		AbsDir: gitDir,
		Count:  len(all),
		Label:  fmt.Sprintf("git-diff (%d changed files)", len(all)),
	}, nil
}

// pickGitCommit returns files modified in a specific commit.
func pickGitCommit(gitDir, commit string, baseDir string) (*FileSet, error) {
	if gitDir == "" {
		gitDir = baseDir
	}

	if !isGitRepo(gitDir) {
		return nil, fmt.Errorf("not a git repository: %s", gitDir)
	}

	if commit == "" {
		// Default to HEAD
		commit = "HEAD"
	}

	// For the root commit (no parent), add --root flag
	args := []string{"-C", gitDir, "diff-tree", "--no-commit-id", "--name-only", "-r"}
	if _, err := exec.Command("git", "-C", gitDir, "rev-parse", commit+"~1").Output(); err != nil {
		args = append(args, "--root")
	}
	args = append(args, commit)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s: %w", commit, err)
	}

	files := parseFileList(string(out))
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found in commit %s", commit)
	}

	absFiles := make([]string, len(files))
	for i, f := range files {
		absFiles[i] = filepath.Join(gitDir, f)
	}

	return &FileSet{
		Source: SourceGitCommit,
		Files:  absFiles,
		AbsDir: gitDir,
		Count:  len(files),
		Label:  fmt.Sprintf("git-commit %s (%d files)", commit, len(files)),
	}, nil
}

// pickGitBranch returns files changed between two branches.
func pickGitBranch(gitDir, branch string, baseDir string) (*FileSet, error) {
	if gitDir == "" {
		gitDir = baseDir
	}

	if !isGitRepo(gitDir) {
		return nil, fmt.Errorf("not a git repository: %s", gitDir)
	}

	if branch == "" {
		return nil, fmt.Errorf("branch name is required for git-branch-diff source")
	}

	// Get current branch
	cmd := exec.Command("git", "-C", gitDir, "rev-parse", "--abbrev-ref", "HEAD")
	current, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git current branch: %w", err)
	}
	currentBranch := strings.TrimSpace(string(current))

	// Files different between current branch and target branch
	rangeSpec := fmt.Sprintf("%s..%s", branch, currentBranch)
	cmd = exec.Command("git", "-C", gitDir, "diff", "--name-only", rangeSpec)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", rangeSpec, err)
	}

	files := parseFileList(string(out))
	if len(files) == 0 {
		return nil, fmt.Errorf("no file differences between %s and %s", currentBranch, branch)
	}

	absFiles := make([]string, len(files))
	for i, f := range files {
		absFiles[i] = filepath.Join(gitDir, f)
	}

	return &FileSet{
		Source: SourceGitBranch,
		Files:  absFiles,
		AbsDir: gitDir,
		Count:  len(files),
		Label:  fmt.Sprintf("git-branch %s vs %s (%d files)", branch, currentBranch, len(files)),
	}, nil
}

// pickFZF uses fzf to let the user interactively select files.
// If paths are provided, they're offered as candidates.
// If gitDir is set, changed files are used as candidates.
func pickFZF(paths []string, gitDir, baseDir string) (*FileSet, error) {
	// Check if fzf is installed
	if _, err := exec.LookPath("fzf"); err != nil {
		// Fall back to editor mode
		return pickEditor(paths, "", baseDir)
	}

	candidates := paths
	if len(candidates) == 0 && gitDir != "" {
		// Use git diff output as candidates
		diff, err := exec.Command("git", "-C", gitDir, "diff", "--name-only", "--diff-filter=ACDMRTUXB").Output()
		if err == nil {
			candidates = parseFileList(string(diff))
		}
		staged, err := exec.Command("git", "-C", gitDir, "diff", "--cached", "--name-only", "--diff-filter=ACDMRTUXB").Output()
		if err == nil {
			candidates = append(candidates, parseFileList(string(staged))...)
		}
		candidates = uniqueStrings(candidates)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no candidates for fzf selection")
	}

	// Run fzf with multi-select
	input := strings.Join(candidates, "\n")
	cmd := exec.Command("fzf", "--multi", "--prompt=Select files (Tab to multi-select)> ")
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			// User cancelled (Ctrl+C / Esc)
			return nil, fmt.Errorf("selection cancelled")
		}
		return nil, fmt.Errorf("fzf: %w", err)
	}

	selected := parseFileList(string(out))

	// Resolve to absolute paths
	absFiles := make([]string, len(selected))
	for i, f := range selected {
		if filepath.IsAbs(f) {
			absFiles[i] = f
		} else {
			absFiles[i] = filepath.Join(baseDir, f)
		}
	}

	return &FileSet{
		Source: SourceFZF,
		Files:  absFiles,
		AbsDir: baseDir,
		Count:  len(selected),
		Label:  fmt.Sprintf("fzf (%d files selected)", len(selected)),
	}, nil
}

// pickEditor opens an editor with the file list and lets the user select.
func pickEditor(paths []string, editor, baseDir string) (*FileSet, error) {
	if editor == "" {
		editor = findEditor()
	}

	candidates := paths
	if len(candidates) == 0 {
		// Try git diff
		diff, err := exec.Command("git", "diff", "--name-only", "--diff-filter=ACDMRTUXB").Output()
		if err == nil {
			candidates = parseFileList(string(diff))
		}
		staged, err := exec.Command("git", "diff", "--cached", "--name-only", "--diff-filter=ACDMRTUXB").Output()
		if err == nil {
			candidates = append(candidates, parseFileList(string(staged))...)
		}
		candidates = uniqueStrings(candidates)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no files to select")
	}

	// Build content with header comments
	header := "# Lines with '#' at the start are ignored.\n"
	header += "# Delete lines you do NOT want to transfer.\n"
	header += "# Save and close (:wq) to confirm selection.\n\n"
	content := header + strings.Join(candidates, "\n") + "\n"

	tmpFile, err := os.CreateTemp("", "deploi-*.txt")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Run editor
	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor: %w", err)
	}

	// Read back the file
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read temp file: %w", err)
	}

	selected := parseEditorOutput(string(data))
	if len(selected) == 0 {
		return nil, fmt.Errorf("no files selected")
	}

	absFiles := make([]string, len(selected))
	for i, f := range selected {
		if filepath.IsAbs(f) {
			absFiles[i] = f
		} else {
			absFiles[i] = filepath.Join(baseDir, f)
		}
	}

	return &FileSet{
		Source: SourceEditor,
		Files:  absFiles,
		AbsDir: baseDir,
		Count:  len(selected),
		Label:  fmt.Sprintf("editor (%d files selected)", len(selected)),
	}, nil
}

// pickAll returns all files in the given directories recursively.
func pickAll(paths []string, baseDir string) (*FileSet, error) {
	targets := paths
	if len(targets) == 0 {
		targets = []string{"."}
	}
	for i, p := range targets {
		if !filepath.IsAbs(p) {
			targets[i] = filepath.Join(baseDir, p)
		}
	}

	var allFiles []string
	for _, root := range targets {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// Skip hidden directories
				if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
					return filepath.SkipDir
				}
				return nil
			}
			allFiles = append(allFiles, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}

	if len(allFiles) == 0 {
		return nil, fmt.Errorf("no files found in %v", targets)
	}

	return &FileSet{
		Source: SourceAll,
		Files:  allFiles,
		AbsDir: baseDir,
		Count:  len(allFiles),
		Label:  fmt.Sprintf("all files (%d files)", len(allFiles)),
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

// parseFileList splits a git output string into a file list.
func parseFileList(s string) []string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// parseEditorOutput parses the file back from editor, skipping comment lines.
func parseEditorOutput(content string) []string {
	lines := strings.Split(content, "\n")
	var selected []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		selected = append(selected, line)
	}
	return selected
}

// mergeFileLists merges two git output strings, deduplicating.
func mergeFileLists(a, b string) []string {
	all := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader([]byte(a)))
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			all[line] = struct{}{}
		}
	}
	scanner = bufio.NewScanner(bytes.NewReader([]byte(b)))
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			all[line] = struct{}{}
		}
	}
	result := make([]string, 0, len(all))
	for k := range all {
		result = append(result, k)
	}
	return result
}

// uniqueStrings deduplicates a string slice.
func uniqueStrings(s []string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, v := range s {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}

// findEditor finds an available editor.
func findEditor() string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	for _, c := range []string{"vim", "nano", "hx", "micro"} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return "vim"
}

// ScanGitLog provides a commit selector UI.
func ScanGitLog(gitDir string, count int) ([]string, error) {
	if gitDir == "" {
		var err error
		gitDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	if count <= 0 {
		count = 20
	}

	cmd := exec.Command("git", "-C", gitDir, "log", "--oneline", fmt.Sprintf("-%d", count))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines, nil
}
