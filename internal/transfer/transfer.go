// Package transfer handles file transfers via rsync, SFTP, and SSH.
package transfer

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/v0id00/deploi/internal/config"

	"github.com/schollz/progressbar/v3"
	"golang.org/x/crypto/ssh"
)

// Operation type.
type Operation int

const (
	OpPush Operation = iota // local → remote
	OpPull                  // remote → local
	OpSync                  // bidirectional / dry-run comparison
)

func (o Operation) String() string {
	switch o {
	case OpPush:
		return "push"
	case OpPull:
		return "pull"
	case OpSync:
		return "sync"
	default:
		return "unknown"
	}
}

// TransferResult holds the outcome of a single file transfer.
type TransferResult struct {
	Server     string `json:"server"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	Speed      string `json:"speed,omitempty"`
	TotalSize  string `json:"total_size,omitempty"`
	Elapsed    string `json:"elapsed"`
	Status     string `json:"status"` // "ok" or "error"
	Error      string `json:"error,omitempty"`
	HooksPre   int    `json:"hooks_pre,omitempty"`
	HooksPost  int    `json:"hooks_post,omitempty"`
}

// RunConfig holds parameters for a transfer run.
type RunConfig struct {
	Operation   Operation
	LocalFiles  []string // list of files/directories to transfer
	RemoteDir   string   // remote base directory
	ServerRegex string
	Tags        []string
	DryRun      bool
	Force       bool
	Concurrency int
	Timeout     int
	ShowBar     bool
	Quiet       bool
	RsyncOpts   string // additional rsync options
	Exclude     []string
	NoGitignore bool  // disable .gitignore auto-detection
	GitDir      string // git directory for .gitignore detection
	BaseDir     string // base directory for relative path resolution (default: cwd)
	Preview     bool  // show diff preview before transfer
	Verbose     bool  // show detailed rsync output
}

// Run executes the transfer on all matching servers.
func Run(servers []config.Server, cfg RunConfig) []TransferResult {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300
	}

	conns := filterServers(servers, cfg.ServerRegex, cfg.Tags)
	if len(conns) == 0 {
		return []TransferResult{{
			Status: "error",
			Error:  "no servers match the given filter",
		}}
	}

	var bar *progressbar.ProgressBar
	if cfg.ShowBar && !cfg.Quiet {
		bar = progressbar.NewOptions(len(conns),
			progressbar.OptionSetDescription(fmt.Sprintf(" 🚀 %d servers", len(conns))),
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionShowCount(),
			progressbar.OptionThrottle(65*time.Millisecond),
			progressbar.OptionSpinnerType(14),
			progressbar.OptionFullWidth(),
			progressbar.OptionSetRenderBlankState(true),
			progressbar.OptionClearOnFinish(),
			progressbar.OptionOnCompletion(func() {
				fmt.Fprintf(os.Stderr, "\n")
			}),
		)
	}

	// Build combined exclude list: config excludes + .gitignore
	exclude := buildExcludeList(cfg)

	var mu sync.Mutex
	results := make([]TransferResult, 0, len(conns))
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for _, srv := range conns {
		wg.Add(1)
		sem <- struct{}{}

		go func(s config.Server) {
			defer wg.Done()
			defer func() { <-sem }()

			start := time.Now()
			r := executeOnServer(s, cfg, exclude)
			r.Server = s.Name
			r.Elapsed = time.Since(start).Round(time.Millisecond).String()

			mu.Lock()
			results = append(results, r)
			if bar != nil {
				bar.Add(1)
			}
			mu.Unlock()
		}(srv)
	}

	wg.Wait()
	return results
}

// executeOnServer runs the full deploy pipeline: hooks → transfer → hooks.
func executeOnServer(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
	// Pre-hooks
	if srv.Hooks != nil && len(srv.Hooks.Pre) > 0 && !cfg.DryRun {
		for _, cmd := range srv.Hooks.Pre {
			out, err := runSSH(srv, cmd)
			if err != nil {
				return TransferResult{
					Status: "error",
					Error:  fmt.Sprintf("pre-hook %q failed: %v\n%s", cmd, err, out),
				}
			}
		}
	}

	// Transfer
	var r TransferResult
	switch srv.Method {
	case "rsync":
		r = runRsync(srv, cfg, exclude)
	case "sftp", "ssh":
		r = runSCP(srv, cfg)
	default:
		return TransferResult{Status: "error", Error: fmt.Sprintf("unknown method: %s", srv.Method)}
	}

	if r.Status != "ok" {
		return r
	}

	// Count hooks
	if srv.Hooks != nil {
		r.HooksPre = len(srv.Hooks.Pre)
		r.HooksPost = len(srv.Hooks.Post)
	}

	// Post-hooks
	if srv.Hooks != nil && len(srv.Hooks.Post) > 0 && !cfg.DryRun {
		for _, cmd := range srv.Hooks.Post {
			out, err := runSSH(srv, cmd)
			if err != nil {
				return TransferResult{
					Status: "error",
					Error:  fmt.Sprintf("post-hook %q failed: %v\n%s", cmd, err, out),
					HooksPre: r.HooksPre,
				}
			}
		}
	}

	return r
}

// buildExcludeList combines config excludes, .gitignore patterns, and server excludes.
func buildExcludeList(cfg RunConfig) []string {
	excludeMap := make(map[string]bool)
	var list []string

	// Config excludes
	for _, e := range cfg.Exclude {
		if !excludeMap[e] {
			excludeMap[e] = true
			list = append(list, e)
		}
	}

	// .gitignore patterns (if enabled)
	if !cfg.NoGitignore {
		gitDir := cfg.GitDir
		if gitDir == "" {
			gitDir, _ = os.Getwd()
		}
		patterns := readGitignore(gitDir)
		for _, p := range patterns {
			if !excludeMap[p] {
				excludeMap[p] = true
				list = append(list, p)
			}
		}
	}

	return list
}

// readGitignore reads .gitignore and .gitignore patterns, returning rsync --exclude args.
func readGitignore(dir string) []string {
	var patterns []string
	gitignorePath := filepath.Join(dir, ".gitignore")
	f, err := os.Open(gitignorePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines, comments, and negations
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		// Remove trailing slash (directory markers in .gitignore)
		line = strings.TrimSuffix(line, "/")
		// Convert leading / (root-relative) to rsync format
		line = strings.TrimPrefix(line, "/")
		if line != "" {
			patterns = append(patterns, line)
		}
	}
	return patterns
}

// runRsync executes the transfer using rsync over SSH.
func runRsync(srv config.Server, cfg RunConfig, exclude []string) TransferResult {
	if len(cfg.LocalFiles) == 0 {
		return TransferResult{Status: "error", Error: "no files to transfer"}
	}

	// Resolve base directory for relative paths
	baseDir := cfg.BaseDir
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}

	// Convert absolute paths to relative (rsync needs relative for proper -R behavior)
	relFiles := make([]string, 0, len(cfg.LocalFiles))
	for _, f := range cfg.LocalFiles {
		rel, err := filepath.Rel(baseDir, f)
		if err != nil {
			rel = f // fallback to original
		}
		// Ensure Unix-style relative paths for rsync
		rel = filepath.ToSlash(rel)
		relFiles = append(relFiles, rel)
	}

	remoteDir := cfg.RemoteDir
	if srv.RemotePath != "" {
		remoteDir = srv.RemotePath
	}
	// rsync destination: use AddrNoPort (port goes in -e)
	remote := fmt.Sprintf("%s:%s", srv.AddrNoPort(), remoteDir)

	args := []string{"-avzR"}
	if cfg.Verbose {
		args = append(args, "-v") // extra verbosity
	}
	if cfg.DryRun {
		args = append(args, "--dry-run")
	}
	if cfg.RsyncOpts != "" {
		args = append(args, strings.Fields(cfg.RsyncOpts)...)
	}

	// Add exclude patterns
	for _, ex := range exclude {
		args = append(args, "--exclude", ex)
	}

	// Add server-level exclude
	for _, ex := range srv.Exclude {
		args = append(args, "--exclude", ex)
	}

	// SSH options
	sshOpt := buildSSHOpts(srv)
	if sshOpt != "" {
		args = append(args, "-e", sshOpt)
	}

	switch cfg.Operation {
	case OpPush, OpSync:
		args = append(args, relFiles...)
		args = append(args, remote)
	case OpPull:
		for _, f := range relFiles {
			pullSrc := fmt.Sprintf("%s:%s%s", srv.AddrNoPort(), remoteDir, f)
			args = append(args, pullSrc)
		}
		args = append(args, ".")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.Dir = baseDir
	if cfg.Verbose {
		fmt.Fprintf(os.Stderr, "  rsync %s\n", strings.Join(args, " "))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return TransferResult{Status: "error",
				Error: enrichRsyncError(err, output, cfg, "timeout")}
		}
		return TransferResult{Status: "error",
			Error: enrichRsyncError(err, output, cfg, "")}
	}

	// Parse rsync output into structured data
	parsed := parseRsyncOutput(string(output))

	if !cfg.Quiet && !cfg.DryRun {
		if cfg.Verbose {
			for _, f := range parsed.Files {
				fmt.Fprintf(os.Stderr, "  📄 %s\n", f)
			}
		}
		if parsed.Summary != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", parsed.Summary)
		}
	}

	return TransferResult{
		Status:    "ok",
		Files:     parsed.FileCount,
		Bytes:     parsed.BytesSent,
		Speed:     parsed.Speed,
		TotalSize: parsed.TotalSize,
	}
}

// enrichRsyncError enriches rsync errors with human-readable explanations.
func enrichRsyncError(err error, output []byte, cfg RunConfig, kind string) string {
	exitCode := 1
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	outStr := string(output)
	msg := ""
	suggestion := ""

	// Detect known error patterns in output
	switch {
	case kind == "timeout":
		msg = fmt.Sprintf("Connection timed out after %ds", cfg.Timeout)
		suggestion = "Check VPN/network, or increase --timeout"
	case exitCode == 1:
		msg = "Rsync reported an error"
		if strings.Contains(outStr, "No such file or directory") {
			msg = "Remote directory or file not found"
			suggestion = "Check remote_path in config, or create it: mkdir -p <remote_path>"
		} else if strings.Contains(outStr, "Permission denied") {
			msg = "SSH authentication failed"
			suggestion = "Check SSH key: ssh-add ~/.ssh/id_ed25519, or verify password"
		} else if strings.Contains(outStr, "connection refused") {
			msg = "SSH connection refused"
			suggestion = "Verify server is running and port is correct (ssh -p PORT HOST)"
		} else if strings.Contains(outStr, "Name or service not known") {
			msg = "Hostname could not be resolved"
			suggestion = "Check host in config, or add to ~/.ssh/config"
		} else if strings.Contains(outStr, "Host key verification failed") {
			msg = "Remote host key has changed"
			suggestion = "Fix: ssh-keygen -R HOSTNAME, then reconnect"
		}
	case exitCode == 10:
		msg = "Rsync error in socket I/O"
		suggestion = "Network issue — check VPN and SSH connectivity"
	case exitCode == 11:
		msg = "Rsync error in file I/O"
		if strings.Contains(outStr, "mkdir") {
			suggestion = "Remote path missing or wrong permissions: check remote_path or create the directory"
		} else {
			suggestion = "Disk full, permission denied, or file vanished during transfer"
		}
	case exitCode == 12:
		msg = "Rsync error in protocol data stream"
		suggestion = "SSH connection issue — try: ssh HOST -p PORT"
	case exitCode == 23:
		msg = "Rsync partial transfer due to error"
		if strings.Contains(outStr, "Permission denied") {
			suggestion = "Check write permissions on remote directory"
		} else {
			suggestion = "Some files could not be transferred, check rsync output"
		}
	case exitCode == 30:
		msg = "Rsync timeout in data transfer"
		suggestion = "Slow connection, increase --timeout or use smaller batches"
	default:
		msg = fmt.Sprintf("rsync exited with code %d", exitCode)
		suggestion = "Run with -v (verbose) to see full error output"
	}

	// Truncate raw output for readability
	if len(outStr) > 500 {
		outStr = outStr[:497] + "..."
	}

	if suggestion != "" {
		return fmt.Sprintf("%s. %s\n%s", msg, suggestion, outStr)
	}
	return fmt.Sprintf("%s\n%s", msg, outStr)
}

// runSCP executes the transfer using SCP.
func runSCP(srv config.Server, cfg RunConfig) TransferResult {
	if len(cfg.LocalFiles) == 0 {
		return TransferResult{Status: "error", Error: "no files to transfer"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	remoteDir := cfg.RemoteDir
	if srv.RemotePath != "" {
		remoteDir = srv.RemotePath
	}

	args := []string{"-r"}
	if srv.Port > 0 && srv.Port != 22 {
		args = append(args, "-P", fmt.Sprintf("%d", srv.Port))
	}
	if srv.KeyFile != "" {
		keyPath := config.ExpandPath(srv.KeyFile)
		args = append(args, "-i", keyPath)
	}
	if cfg.DryRun {
		args = append(args, "--dry-run")
	}

	switch cfg.Operation {
	case OpPush:
		args = append(args, cfg.LocalFiles...)
		args = append(args, fmt.Sprintf("%s:%s", srv.AddrNoPort(), remoteDir))
	case OpPull:
		for _, f := range cfg.LocalFiles {
			args = append(args, fmt.Sprintf("%s:%s%s", srv.AddrNoPort(), remoteDir, f))
		}
		args = append(args, ".")
	}

	cmd := exec.CommandContext(ctx, "scp", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return TransferResult{Status: "error", Error: fmt.Sprintf("scp timeout (%ds)", cfg.Timeout)}
		}
		return TransferResult{Status: "error", Error: fmt.Sprintf("scp: %v\n%s", err, string(output))}
	}

	if !cfg.Quiet {
		os.Stderr.Write(output)
	}

	return TransferResult{Status: "ok"}
}

// runSSH executes commands via SSH directly.
func runSSH(srv config.Server, cmdStr string) (string, error) {
	keyFile := srv.KeyFile
	if keyFile == "" {
		home, _ := os.UserHomeDir()
		keyFile = filepath.Join(home, ".ssh", "id_ed25519")
		if _, err := os.Stat(keyFile); err != nil {
			keyFile = filepath.Join(home, ".ssh", "id_rsa")
		}
	}
	keyFile = config.ExpandPath(keyFile)

	key, err := os.ReadFile(keyFile)
	if err != nil {
		return "", fmt.Errorf("read SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("parse SSH key: %w", err)
	}

	hostPort := fmt.Sprintf("%s:%d", srv.Host, srv.Port)
	client, err := ssh.Dial("tcp", hostPort, &ssh.ClientConfig{
		User:            srv.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("SSH dial: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput(cmdStr)
	if err != nil {
		return string(out), fmt.Errorf("SSH command: %w", err)
	}

	return string(out), nil
}

// rsyncParsed holds parsed rsync output.
type rsyncParsed struct {
	Files     []string
	FileCount int
	BytesSent int64
	Speed     string
	TotalSize string
	Summary   string
}

// parseRsyncOutput parses rsync output into structured data.
func parseRsyncOutput(output string) rsyncParsed {
	var p rsyncParsed
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	var sentRe = regexp.MustCompile(`sent\s+([\d,\.]+)\s+bytes\s+received\s+[\d,\.]+\s+bytes\s+([\d\.,]+)\s+bytes/sec`)
	var totalRe = regexp.MustCompile(`total size is\s+([\d,\.]+)\s+speedup\s+([\d\.,]+)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "sending incremental file list" {
			continue
		}

		if m := sentRe.FindStringSubmatch(line); m != nil {
			bytesStr := strings.ReplaceAll(m[1], ",", "")
			fmt.Sscanf(bytesStr, "%d", &p.BytesSent)
			p.Speed = m[2] + "/s"
			continue
		}

		if m := totalRe.FindStringSubmatch(line); m != nil {
			p.TotalSize = m[1]
			p.Summary = fmt.Sprintf("📦 %s  ⚡ %s/s  📊 %s total  🚀 %sx",
				formatBytes(p.BytesSent), p.Speed, p.TotalSize, m[2])
			continue
		}

		if strings.HasPrefix(line, "delta-transmission") ||
			strings.HasPrefix(line, "total:") ||
			strings.HasPrefix(line, "sent ") ||
			strings.HasPrefix(line, "receiving") ||
			strings.HasPrefix(line, "created directory") ||
			strings.HasPrefix(line, "removing duplicate") {
			continue
		}

		p.Files = append(p.Files, line)
		p.FileCount++
	}

	if p.Summary == "" {
		p.Summary = fmt.Sprintf("📦 %d files  %s transferred", p.FileCount, formatBytes(p.BytesSent))
	}

	return p
}

// formatBytes formats byte counts human-readable.
func formatBytes(b int64) string {
	switch {
	case b > 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b > 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b > 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// buildSSHOpts builds the SSH options string for rsync -e.
func buildSSHOpts(srv config.Server) string {
	opts := "ssh"
	if srv.Port > 0 && srv.Port != 22 {
		opts += fmt.Sprintf(" -p %d", srv.Port)
	}
	if srv.KeyFile != "" {
		keyPath := config.ExpandPath(srv.KeyFile)
		opts += fmt.Sprintf(" -i %s", keyPath)
	}
	return opts
}

// filterServers filters servers by regex and tags.
func filterServers(servers []config.Server, regex string, tags []string) []config.Server {
	if regex == "" && len(tags) == 0 {
		return servers
	}

	var filtered []config.Server
	for _, s := range servers {
		if regex != "" {
			if matched, _ := filepath.Match(regex, s.Name); !matched {
				continue
			}
		}
		if len(tags) > 0 {
			if !hasAnyTag(s.Tags, tags) {
				continue
			}
		}
		filtered = append(filtered, s)
	}
	return filtered
}

func hasAnyTag(serverTags, filterTags []string) bool {
	for _, ft := range filterTags {
		for _, st := range serverTags {
			if st == ft {
				return true
			}
		}
	}
	return false
}

// RunCommands executes arbitrary SSH commands on servers.
func RunCommands(servers []config.Server, commands []string, cfg RunConfig) []TransferResult {
	conns := filterServers(servers, cfg.ServerRegex, cfg.Tags)
	if len(conns) == 0 {
		return []TransferResult{{
			Status: "error",
			Error:  "no servers match the given filter",
		}}
	}

	var mu sync.Mutex
	results := make([]TransferResult, 0, len(conns))
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for _, srv := range conns {
		wg.Add(1)
		sem <- struct{}{}
		go func(s config.Server) {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			r := TransferResult{Server: s.Name, Status: "ok"}
			for _, cmd := range commands {
				if cfg.DryRun {
					continue
				}
				out, err := runSSH(s, cmd)
				if err != nil {
					r.Status = "error"
					r.Error = fmt.Sprintf("command %q: %v\n%s", cmd, err, out)
					break
				}
			}
			r.Elapsed = time.Since(start).Round(time.Millisecond).String()
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(srv)
	}
	wg.Wait()
	return results
}

// EnsureRemoteDirs creates remote directories via SSH.
func EnsureRemoteDirs(srv config.Server, dirs []string) error {
	if len(dirs) == 0 {
		return nil
	}
	out, err := runSSH(srv, "mkdir -p "+strings.Join(dirs, " "))
	if err != nil {
		return fmt.Errorf("mkdir remote: %w\n%s", err, out)
	}
	return nil
}

// ListRemoteFiles lists files in a remote directory via SSH.
func ListRemoteFiles(srv config.Server, remotePath string) ([]string, error) {
	out, err := runSSH(srv, fmt.Sprintf("ls -1 %s", remotePath))
	if err != nil {
		return nil, err
	}
	files := strings.Split(strings.TrimSpace(out), "\n")
	var result []string
	for _, f := range files {
		if f != "" {
			result = append(result, f)
		}
	}
	return result, nil
}

// IsTerminal checks if stderr is a terminal.
func IsTerminal() bool {
	stat, _ := os.Stderr.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// GitDiffSummary generates a summary of what will be transferred.
func GitDiffSummary(files []string, gitDir string) string {
	if gitDir == "" {
		gitDir, _ = os.Getwd()
	}
	if len(files) == 0 {
		return "(no files)"
	}

	// Run git diff --stat on the selected files
	args := append([]string{"-C", gitDir, "diff", "--stat"}, files...)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		// Fallback: show file list
		var b strings.Builder
		for _, f := range files {
			rel, _ := filepath.Rel(gitDir, f)
			b.WriteString(rel + "\n")
		}
		return b.String()
	}
	return string(out)
}
