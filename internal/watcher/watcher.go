// Package watcher provides file system watching for auto-deploy.
package watcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Config for the file watcher.
type Config struct {
	Paths    []string // directories to watch
	GitDir   string   // git working directory
	Method   string   // file selection method to use on change
	Delay    time.Duration // debounce delay (default: 500ms)
	OnChange func(files []string) // callback when changes are detected
}

// Watch starts watching files for changes.
// Blocks until ctx is cancelled or an error occurs.
func Watch(ctx context.Context, cfg Config) error {
	if len(cfg.Paths) == 0 {
		cfg.Paths = []string{"."}
	}
	if cfg.Delay <= 0 {
		cfg.Delay = 500 * time.Millisecond
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify: %w", err)
	}
	defer watcher.Close()

	// Add paths to watch recursively
	for _, p := range cfg.Paths {
		abs, _ := filepath.Abs(p)
		if err := addRecursive(watcher, abs); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Cannot watch %s: %v\n", abs, err)
		}
	}

	fmt.Fprintf(os.Stderr, "  👀 Watching %d directories for changes (Ctrl+C to stop)...\n", len(cfg.Paths))

	// Debounce logic
	var timer *time.Timer
	var pendingFiles map[string]bool

	trigger := func() {
		if len(pendingFiles) == 0 {
			return
		}
		files := make([]string, 0, len(pendingFiles))
		for f := range pendingFiles {
			files = append(files, f)
		}
		pendingFiles = nil

		if cfg.OnChange != nil {
			cfg.OnChange(files)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// Skip hidden/temp files and .git changes
			base := filepath.Base(event.Name)
			if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") {
				continue
			}
			if strings.Contains(event.Name, ".git/") || strings.Contains(event.Name, "/.git/") {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}

			if pendingFiles == nil {
				pendingFiles = make(map[string]bool)
			}
			pendingFiles[event.Name] = true

			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(cfg.Delay, trigger)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "  ⚠ Watch error: %v\n", err)
		}
	}
}

// addRecursive adds a directory and all subdirectories to the watcher.
func addRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") && base != "." {
			return filepath.SkipDir
		}
		if base == "node_modules" || base == "vendor" || base == "build" {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

// GitChangedFiles returns files changed in the working tree.
func GitChangedFiles(gitDir string) ([]string, error) {
	// Unstaged
	cmd := exec.Command("git", "-C", gitDir, "diff", "--name-only", "--diff-filter=ACDMRTUXB")
	unstaged, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	// Staged
	cmd = exec.Command("git", "-C", gitDir, "diff", "--cached", "--name-only", "--diff-filter=ACDMRTUXB")
	staged, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}

	all := make(map[string]bool)
	for _, src := range []string{string(unstaged), string(staged)} {
		for _, line := range strings.Split(strings.TrimSpace(src), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				all[line] = true
			}
		}
	}

	result := make([]string, 0, len(all))
	for f := range all {
		result = append(result, f)
	}
	return result, nil
}
