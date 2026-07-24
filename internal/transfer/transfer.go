// Package transfer handles file transfers via rsync, SFTP, and SSH.
package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// String returns the human-readable name.
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
	Server  string `json:"server"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	Elapsed string `json:"elapsed"`
	Status  string `json:"status"` // "ok" or "error"
	Error   string `json:"error,omitempty"`
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
}

// Run executes the transfer on all matching servers.
func Run(servers []config.Server, cfg RunConfig) []TransferResult {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300
	}

	// Filter servers
	conns := filterServers(servers, cfg.ServerRegex, cfg.Tags)
	if len(conns) == 0 {
		return []TransferResult{{
			Status: "error",
			Error:  "no servers match the given filter",
		}}
	}

	// Build progress bar
	var bar *progressbar.ProgressBar
	if cfg.ShowBar && !cfg.Quiet {
		bar = progressbar.NewOptions(len(conns),
			progressbar.OptionSetDescription(" transferring"),
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionShowCount(),
			progressbar.OptionThrottle(65*time.Millisecond),
			progressbar.OptionSpinnerType(14),
			progressbar.OptionFullWidth(),
			progressbar.OptionSetRenderBlankState(true),
		)
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
			r := executeOnServer(s, cfg)
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

// executeOnServer runs the transfer on a single server.
func executeOnServer(srv config.Server, cfg RunConfig) TransferResult {
	switch srv.Method {
	case "rsync":
		return runRsync(srv, cfg)
	case "sftp", "ssh":
		return runSCP(srv, cfg)
	default:
		return TransferResult{Status: "error", Error: fmt.Sprintf("unknown method: %s", srv.Method)}
	}
}

// runRsync executes the transfer using rsync over SSH.
func runRsync(srv config.Server, cfg RunConfig) TransferResult {
	if len(cfg.LocalFiles) == 0 {
		return TransferResult{Status: "error", Error: "no files to transfer"}
	}

	// Build remote destination
	remoteDir := cfg.RemoteDir
	if srv.RemotePath != "" {
		remoteDir = srv.RemotePath
	}
	remote := fmt.Sprintf("%s:%s", srv.Addr(), remoteDir)

	// Build rsync args
	args := []string{"-avzR"}
	if cfg.DryRun {
		args = append(args, "--dry-run")
	}
	if cfg.RsyncOpts != "" {
		args = append(args, strings.Fields(cfg.RsyncOpts)...)
	}

	// Add SSH options
	sshOpt := buildSSHOpts(srv)
	if sshOpt != "" {
		args = append(args, "-e", sshOpt)
	}

	// If we have a remote_path per server, we need to handle the target correctly
	// For push: local files → remote:remoteDir
	// For pull: remote:remoteDir/files → local
	switch cfg.Operation {
	case OpPush:
		// Add source files
		args = append(args, cfg.LocalFiles...)
		args = append(args, remote)
	case OpPull:
		// Remote as source, local as dest
		for _, f := range cfg.LocalFiles {
			// When pulling, the files are on the remote side
			pullSrc := fmt.Sprintf("%s:%s%s", srv.Addr(), remoteDir, f)
			args = append(args, pullSrc)
		}
		args = append(args, ".")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rsync", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return TransferResult{Status: "error", Error: fmt.Sprintf("rsync timeout (%ds)", cfg.Timeout)}
		}
		return TransferResult{Status: "error", Error: fmt.Sprintf("rsync: %v\n%s", err, string(output))}
	}

	if !cfg.Quiet {
		os.Stderr.Write(output)
	}

	// Count transferred files from output
	fileCount := parseRsyncOutput(string(output))
	return TransferResult{Status: "ok", Files: fileCount}
}

// runSCP executes the transfer using SCP (when rsync is not available).
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
		args = append(args, fmt.Sprintf("%s:%s", srv.Addr(), remoteDir))
	case OpPull:
		for _, f := range cfg.LocalFiles {
			args = append(args, fmt.Sprintf("%s:%s%s", srv.Addr(), remoteDir, f))
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

// parseRsyncOutput attempts to count transferred files from rsync output.
func parseRsyncOutput(output string) int {
	lines := strings.Split(output, "\n")
	count := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "sending") || strings.HasPrefix(line, "receiving") ||
			strings.HasPrefix(line, "sent ") || strings.HasPrefix(line, "total ") ||
			strings.HasPrefix(line, ".") || strings.HasPrefix(line, "created") ||
			strings.HasPrefix(line, "deleting") {
			continue
		}
		// Skip directory entries (trailing /)
		if strings.HasSuffix(line, "/") {
			continue
		}
		count++
	}
	return count
}

// filterServers filters servers by regex and tags.
func filterServers(servers []config.Server, regex string, tags []string) []config.Server {
	if regex == "" && len(tags) == 0 {
		return servers
	}

	var filtered []config.Server
	for _, s := range servers {
		// Regex filter
		if regex != "" {
			if matched, _ := filepath.Match(regex, s.Name); !matched {
				continue
			}
		}

		// Tags filter (OR logic)
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

// RunCommands executes arbitrary SSH commands on servers (for remote operations).
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
		return fmt.Errorf("read SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("parse SSH key: %w", err)
	}

	hostPort := fmt.Sprintf("%s:%d", srv.Host, srv.Port)
	client, err := ssh.Dial("tcp", hostPort, &ssh.ClientConfig{
		User:            srv.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("SSH dial: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	// Create all directories with mkdir -p
	cmd := "mkdir -p " + strings.Join(dirs, " ")
	_, err = session.CombinedOutput(cmd)
	if err != nil {
		return fmt.Errorf("mkdir remote: %w", err)
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

// IsTerminal checks if stderr is a terminal (for progress bar).
func IsTerminal() bool {
	stat, _ := os.Stderr.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// CopyFile copies a file locally (used for SFTP-based transfers as fallback).
func CopyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return d.Sync()
}
