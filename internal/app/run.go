// Package app wires everything together — CLI flags, orchestrator, version.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/v0id00/deploi/internal/config"
	"github.com/v0id00/deploi/internal/history"
	"github.com/v0id00/deploi/internal/picker"
	"github.com/v0id00/deploi/internal/transfer"
	"github.com/v0id00/deploi/internal/watcher"
)

var version = "dev"

func Execute() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

type appConfig struct {
	configPath  string
	server      string
	tags        string
	timeout     int
	concurrency int
	force       bool
	dryRun      bool
	verbose     bool
	json        bool
	quiet       bool
	noConfirm   bool
	noPreview   bool
	noGitignore bool
	noStaged    bool // exclude staged files from git-diff
	version     bool

	// File selection
	selectMode string // --select: manual, fzf, editor, all
	filterMode string // --filter: git-diff, git-commit, git-branch, path
	paths      []string
	commit     string
	branch     string
	pick       bool
	pickSrv    bool // interactive server selection via fzf
	remoteDir  string

	// Profile
	profile string

	// Transfer
	rsyncOpts string
	method    string

	// Profile excludes (merged with defaults during push)
	exclude []string

	// Watch
	watchDelay int // in seconds
}

func newRootCmd() *cobra.Command {
	ac := &appConfig{}

	cmd := &cobra.Command{
		Use:   "deploi",
		Short: "Multi-server file sync and deploy tool",
		Long: `deploi — Multi-server file sync and deploy tool.

Reads a TOML config file with server definitions, then transfers files
across all matching servers concurrently.

Features:
  • Pre/Post hooks: run SSH commands before/after transfer on each server
  • Exclude patterns: auto-respects .gitignore, configurable in config
  • Diff preview: shows what will change and asks confirmation (default)
  • Deploy profiles: predefined file selection sets in config
  • Deploy history: every deploy is logged (deploi history)
  • Watch mode: auto-deploy on file changes (deploi watch)
  • Concurrent: goroutine-based transfers to multiple servers

File selection:
  --select <mode>   Selection mode: manual, fzf, editor, all
  --filter <type>   Filter: git-diff, git-commit, git-branch, path

Use --filter git-diff to pick changed files, or --select fzf to browse files interactively.
Use -S to pick target servers interactively via fzf.

|Examples:
  deploi push -s prod --filter git-diff
  deploi push -S --filter git-diff               # pick servers via fzf
  deploi push -S -t prod --filter git-diff        # filter by tag, then pick
  deploi push -s prod --filter git-diff -P        # pick changed files via fzf
  deploi push -s prod --select fzf --filter git-commit   # pick commit via fzf
  deploi push -s prod --select fzf --filter path         # pick files via fzf
  deploi push -s staging --profile assets
  deploi pull -s staging --select all remote/path/
  deploi watch -s staging ./
  deploi history
  deploi rollback 3`,
		SilenceUsage:  false,
		SilenceErrors: false,
		Args:          cobra.MaximumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(ac, cmd)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&ac.configPath, "config", "c", "", "Path to config file")
	flags.StringVarP(&ac.server, "server", "s", "", "Glob/name filter for server names")
	flags.StringVarP(&ac.tags, "tags", "t", "", "Filter by tags (comma-separated, OR)")
	flags.BoolVarP(&ac.pickSrv, "pick-server", "S", false, "Pick servers interactively via fzf")
	flags.IntVar(&ac.timeout, "timeout", 0, "Transfer timeout in seconds")
	flags.IntVar(&ac.concurrency, "concurrency", 0, "Concurrent server limit")
	flags.BoolVar(&ac.force, "force", false, "Skip confirmation")
	flags.BoolVar(&ac.dryRun, "dry-run", false, "Preview changes without transferring")
	flags.BoolVarP(&ac.verbose, "verbose", "v", false, "Show detailed rsync output")
	flags.BoolVar(&ac.json, "json", false, "Output as JSON")
	flags.BoolVarP(&ac.quiet, "quiet", "q", false, "Suppress progress bar and banners")
	flags.BoolVar(&ac.noConfirm, "no-confirm", false, "Skip confirmation prompts when targeting all servers")
	flags.BoolVar(&ac.noPreview, "no-preview", false, "Skip diff preview confirmation before transfer")
	flags.BoolVar(&ac.noGitignore, "no-gitignore", false, "Disable .gitignore auto-detection")
	flags.BoolVar(&ac.noStaged, "no-staged", false, "Exclude staged files from git-diff")
	flags.BoolVar(&ac.version, "version", false, "Show version")

	// Subcommands
	cmd.AddCommand(newPushCmd())
	cmd.AddCommand(newPullCmd())
	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newServersCmd())
	cmd.AddCommand(newRunCmd())
	cmd.AddCommand(newWatchCmd())
	cmd.AddCommand(newHistoryCmd())
	cmd.AddCommand(newRollbackCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newCheckCmd())
	cmd.AddCommand(newCompletionCmd())
	cmd.AddCommand(newSkillCmd())

	return cmd
}

// ---------------------------------------------------------------------------
// deploi push
// ---------------------------------------------------------------------------

func newPushCmd() *cobra.Command {
	ac := &appConfig{}
	cmd := &cobra.Command{
		Use:   "push [files...]",
		Short: "Upload files to remote servers",
		Long: `Upload files to one or more servers.

Shows a diff preview and asks for confirmation before transferring.
Use --no-preview to skip this, or set no_preview=true in config.

File selection:
  --select <mode>   Selection mode: manual, fzf, editor, all
  --filter <type>   Filter: git-diff, git-commit, git-branch, path
  (path args)       Explicit file paths (manual)

Use --filter git-diff for changed files, or --select fzf to browse.
Use --no-staged to exclude staged files from git-diff selection.

Examples:
  deploi push --select fzf --filter git-diff -S    # pick changed files via fzf
  deploi push --select fzf --filter path -S        # pick all files via fzf
  deploi push --select all --filter path ./src     # transfer all files
  deploi push -S -s prod --filter git-diff         # push changed files to prod`,
		Args: cobra.MaximumNArgs(100),
		RunE: func(cmd *cobra.Command, args []string) error {
			ac.paths = args
			return runPushPull(ac, transfer.OpPush)
		},
	}
	flags := cmd.Flags()
	addFileSelectionFlags(flags, ac)
	addServerFlags(flags, ac)
	addExecFlags(flags, ac)
	flags.StringVar(&ac.profile, "profile", "", "Use a named profile from config")
	return cmd
}

// ---------------------------------------------------------------------------
// deploi pull
// ---------------------------------------------------------------------------

func newPullCmd() *cobra.Command {
	ac := &appConfig{}
	cmd := &cobra.Command{
		Use:   "pull [remote-paths...]",
		Short: "Download files from remote servers",
		Long: `Download files from remote servers to local machine.

Examples:
  deploi pull -s staging --select all --filter path remote/path/
  deploi pull -s prod var/log/app.log`,
		Args: cobra.MaximumNArgs(100),
		RunE: func(cmd *cobra.Command, args []string) error {
			ac.paths = args
			return runPushPull(ac, transfer.OpPull)
		},
	}
	flags := cmd.Flags()
	addFileSelectionFlags(flags, ac)
	addServerFlags(flags, ac)
	addExecFlags(flags, ac)
	flags.StringVar(&ac.profile, "profile", "", "Use a named profile from config")
	return cmd
}

// ---------------------------------------------------------------------------
// deploi sync
// ---------------------------------------------------------------------------

func newSyncCmd() *cobra.Command {
	ac := &appConfig{}
	cmd := &cobra.Command{
		Use:   "sync [files...]",
		Short: "Compare files with remote servers (dry-run)",
		Long: `Compare local files with remote servers using dry-run mode.
Shows what would change before transferring.

Examples:
  deploi sync -s prod --filter git-diff
  deploi sync -s prod --select fzf`,
		Args: cobra.MaximumNArgs(100),
		RunE: func(cmd *cobra.Command, args []string) error {
			ac.paths = args
			ac.dryRun = true
			return runPushPull(ac, transfer.OpSync)
		},
	}
	flags := cmd.Flags()
	addFileSelectionFlags(flags, ac)
	addServerFlags(flags, ac)
	addExecFlags(flags, ac)
	return cmd
}

// ---------------------------------------------------------------------------
// deploi run
// ---------------------------------------------------------------------------

func newRunCmd() *cobra.Command {
	ac := &appConfig{}
	cmd := &cobra.Command{
		Use:   "run [commands...]",
		Short: "Execute commands on remote servers via SSH",
		Long: `Run one or more shell commands on remote servers.

Example:
  deploi run -s prod "systemctl reload nginx"
  deploi run -s staging "df -h" "uptime"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemote(ac, args)
		},
	}
	flags := cmd.Flags()
	addServerFlags(flags, ac)
	addExecFlags(flags, ac)
	return cmd
}

// ---------------------------------------------------------------------------
// deploi watch
// ---------------------------------------------------------------------------

func newWatchCmd() *cobra.Command {
	ac := &appConfig{}
	cmd := &cobra.Command{
		Use:   "watch [dirs...]",
		Short: "Watch files and auto-deploy on changes",
		Long: `Watch directories for file changes and automatically deploy.

Uses fsnotify to monitor file changes with debounce.
When changes are detected, deploi runs push with git-diff method.

Examples:
  deploi watch -s staging ./src/
  deploi watch -s staging ./                     # watch entire project
  deploi watch -s prod --delay 2                 # 2 second debounce`,
		Args: cobra.MaximumNArgs(10),
		RunE: func(cmd *cobra.Command, args []string) error {
			ac.paths = args
			return runWatch(ac)
		},
	}
	flags := cmd.Flags()
	addServerFlags(flags, ac)
	addExecFlags(flags, ac)
	flags.IntVar(&ac.watchDelay, "delay", 1, "Watch debounce delay in seconds")
	return cmd
}

// ---------------------------------------------------------------------------
// deploi history
// ---------------------------------------------------------------------------

func newHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history [id]",
		Short: "Show deploy history",
		Long: `Show recent deploy operations. Use with an ID to see details.

Examples:
  deploi history
  deploi history 3`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHistory(args)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// deploi rollback
// ---------------------------------------------------------------------------

func newRollbackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback [id]",
		Short: "Rollback to a previous deploy",
		Long: `Rollback files to a previous deploy state.

Uses rsync --backup-dir snapshots to restore files.
Without an ID, rolls back the most recent deploy.

Examples:
  deploi rollback
  deploi rollback 3`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollback(args)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// deploi servers
// ---------------------------------------------------------------------------

func newServersCmd() *cobra.Command {
	ac := &appConfig{}
	cmd := &cobra.Command{
		Use:   "servers",
		Short: "List configured servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServers(ac)
		},
	}
	flags := cmd.Flags()
	addServerFlags(flags, ac)
	flags.BoolVar(&ac.json, "json", false, "JSON output")
	flags.BoolVarP(&ac.quiet, "quiet", "q", false, "Suppress banner")
	flags.StringVarP(&ac.configPath, "config", "c", "", "Path to config file")
	return cmd
}

// ---------------------------------------------------------------------------
// deploi config / completion / skill
// ---------------------------------------------------------------------------

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage configuration"}
	var cfgPath string

	generateCmd := &cobra.Command{
		Use: "generate", Short: "Generate a default config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			oPath, _ := cmd.Flags().GetString("output")
			return runConfigGenerate(oPath)
		},
	}
	generateCmd.Flags().StringP("output", "o", "", "Output path (default: platform config dir, '-' for stdout)")

	validateCmd := &cobra.Command{
		Use: "validate", Short: "Validate config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigValidate(cfgPath)
		},
	}
	validateCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "Path to config file")

	initCmd := &cobra.Command{
		Use: "init", Short: "Create a project-level deploi.toml interactively",
		Long: `Create a deploi.toml in the current directory with servers picked
from the global config (~/.config/deploi/config.toml).

Interactively:
  1. Select which servers this project uses
  2. Set remote path per server
  3. Add optional pre/post deploy hooks
  4. deploi.toml is written to current directory`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigInit()
		},
	}

	cmd.AddCommand(generateCmd, validateCmd, initCmd)
	return cmd
}

func newCheckCmd() *cobra.Command {
	ac := &appConfig{}
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Test SSH connectivity to all configured servers",
		Long: `Test SSH connectivity to servers defined in the config with latency.
	
Examples:
  deploi check              # test all servers
  deploi check -s www*      # servers matching glob
  deploi check -t prod      # servers with tag
  deploi check -S           # pick servers interactively
  deploi check -v           # show SSH version banner`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(ac)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&ac.server, "server", "s", "", "Glob/name filter")
	flags.StringVarP(&ac.tags, "tags", "t", "", "Tag filter (comma-separated, OR)")
	flags.BoolVarP(&ac.pickSrv, "pick-server", "S", false, "Pick servers via fzf")
	flags.BoolVarP(&ac.verbose, "verbose", "v", false, "Show SSH version banner")
	return cmd
}

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "completion [bash|zsh|fish|powershell]", Short: "Generate shell completion script",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletion(os.Stdout)
			default:
				return fmt.Errorf("unknown shell: %s", args[0])
			}
		},
	}
}

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Manage AI agent skill"}
	cmd.AddCommand(&cobra.Command{
		Use: "install", Short: "Install deploi skill for Hermes AI agent",
		RunE: func(cmd *cobra.Command, args []string) error { return installSkill() },
	})
	cmd.AddCommand(&cobra.Command{
		Use: "show", Short: "Print the embedded skill content",
		RunE: func(cmd *cobra.Command, args []string) error { fmt.Print(skillContent); return nil },
	})
	return cmd
}

// ---------------------------------------------------------------------------
// Flag helpers
// ---------------------------------------------------------------------------

func addFileSelectionFlags(flags *pflag.FlagSet, ac *appConfig) {
	flags.StringVar(&ac.selectMode, "select", "",
		"Selection mode: manual, fzf, editor, all")
	flags.StringVar(&ac.filterMode, "filter", "",
		"Filter: git-diff, git-commit, git-branch, path")
	flags.StringVar(&ac.commit, "commit", "", "Git commit hash (for git-commit filter)")
	flags.StringVar(&ac.branch, "branch", "", "Git branch name (for git-branch filter)")
	flags.BoolVarP(&ac.pick, "pick", "P", false, "Pick commit interactively via fzf")
	flags.StringVar(&ac.remoteDir, "remote-dir", "", "Remote directory override")
	flags.StringVar(&ac.rsyncOpts, "rsync-opts", "", "Additional rsync options")
}

func addServerFlags(flags *pflag.FlagSet, ac *appConfig) {
	flags.StringVarP(&ac.server, "server", "s", "", "Glob/name filter for server names")
	flags.StringVarP(&ac.tags, "tags", "t", "", "Filter by tags (comma-separated, OR)")
	flags.BoolVarP(&ac.pickSrv, "pick-server", "S", false, "Pick servers interactively via fzf")
}

func addExecFlags(flags *pflag.FlagSet, ac *appConfig) {
	flags.IntVar(&ac.timeout, "timeout", 0, "Transfer timeout in seconds")
	flags.IntVar(&ac.concurrency, "concurrency", 0, "Concurrent server limit")
	flags.BoolVar(&ac.force, "force", false, "Skip confirmation")
	flags.BoolVar(&ac.dryRun, "dry-run", false, "Preview changes without transferring")
	flags.BoolVarP(&ac.verbose, "verbose", "v", false, "Show detailed rsync output")
	flags.BoolVar(&ac.json, "json", false, "Output as JSON")
	flags.BoolVarP(&ac.quiet, "quiet", "q", false, "Suppress progress bar and banners")
	flags.BoolVar(&ac.noConfirm, "no-confirm", false, "Skip confirmation prompts when targeting all servers")
	flags.BoolVar(&ac.noPreview, "no-preview", false, "Skip diff preview confirmation before transfer")
	flags.BoolVar(&ac.noGitignore, "no-gitignore", false, "Disable .gitignore auto-detection")
	flags.BoolVar(&ac.noStaged, "no-staged", false, "Exclude staged files from git-diff")
}

// ---------------------------------------------------------------------------
// run — root
// ---------------------------------------------------------------------------

func run(ac *appConfig, cmd *cobra.Command) error {
	if ac.version {
		fmt.Fprintf(os.Stderr, "deploi %s\n", version)
		return nil
	}
	return cmd.Help()
}

// ---------------------------------------------------------------------------
// runPushPull — push/pull/sync
// ---------------------------------------------------------------------------

func runPushPull(ac *appConfig, op transfer.Operation) error {
	cfgPath, err := config.FindConfigPath(ac.configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// Apply profile if specified
	if ac.profile != "" {
		p, ok := cfg.Profiles[ac.profile]
		if !ok {
			return fmt.Errorf("profile %q not found in config", ac.profile)
		}
		applyProfile(ac, p)
	} else if cfg.Defaults.Profile != "" {
		if p, ok := cfg.Profiles[cfg.Defaults.Profile]; ok {
			applyProfile(ac, p)
		}
	}

	mergeDefaults(ac, &cfg.Defaults)

	// Resolve file selection
	fileSet, err := resolveFiles(ac, cfg.Defaults.Editor)
	if err != nil {
		return fmt.Errorf("file selection: %w", err)
	}

	// Filter files through gitignore and exclude
	var included []string
	excludePatterns := buildExcludePatterns(cfg.Defaults.Exclude, cfg.Defaults.IsRespectGitignore() && !ac.noGitignore, cwd())
	for _, f := range fileSet.Files {
		if isExcluded(f, excludePatterns) {
			continue
		}
		included = append(included, f)
	}
	fileSet.Files = included
	fileSet.Count = len(included)

	if fileSet.Count == 0 {
		return fmt.Errorf("no files to transfer after applying exclude patterns")
	}

	// Convert servers to slice and apply interactive picker BEFORE any prompts
	servers := make([]config.Server, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	if ac.pickSrv {
		selected, err := pickServersFZF(servers, ac.server, splitTags(ac.tags))
		if err != nil {
			return err
		}
		servers = selected
	}

	// ── Section: File Selection ──
	if !ac.quiet {
		fmt.Fprintf(os.Stderr, "\n%s\n",
			color.New(color.FgCyan, color.Bold).Sprintf("═══ File Selection ═══"))
		fmt.Fprintf(os.Stderr, "  %s: %s  ·  %s\n",
			color.CyanString("Mode"), color.GreenString(fileSet.Label), color.YellowString(fmt.Sprintf("%d files", fileSet.Count)))
	}

	// Confirm when targeting ALL servers
	if !ac.noConfirm && !ac.dryRun && len(servers) == len(cfg.Servers) {
		if cfg.Defaults.ConfirmWithoutFilter {
			if !confirmPrompt(fmt.Sprintf("Transfer to ALL %d servers?", len(servers))) {
				fmt.Fprintln(os.Stderr, "  Cancelled.")
				return nil
			}
		}
	}

	// ── Section: Preview ──
	if !ac.noPreview && !ac.dryRun && op == transfer.OpPush {
		gitDir := cwd()
		summary := transfer.GitDiffSummary(fileSet.Files, gitDir)
		if !ac.quiet {
			fmt.Fprintf(os.Stderr, "\n%s\n", color.New(color.FgCyan, color.Bold).Sprintf("═══ Changes ═══"))
		}
		fmt.Fprintf(os.Stderr, "%s\n", summary)
		if !confirmPrompt(fmt.Sprintf("Push %d files to %d server(s)?", fileSet.Count, len(servers))) {
			fmt.Fprintln(os.Stderr, "  Cancelled.")
			return nil
		}
	}

	// Build transfer config
	tc := transfer.RunConfig{
		Operation:   op,
		LocalFiles:  fileSet.Files,
		RemoteDir:   ac.remoteDir,
		ServerRegex: ac.server,
		Tags:        splitTags(ac.tags),
		DryRun:      ac.dryRun,
		Force:       ac.force,
		Concurrency: ac.concurrency,
		Timeout:     ac.timeout,
		ShowBar:     !ac.quiet && cfg.Defaults.ShowBar,
		Quiet:       ac.quiet,
		RsyncOpts:   ac.rsyncOpts,
		Exclude:     mergeExcludes(cfg.Defaults.Exclude, ac.exclude),
		NoGitignore: ac.noGitignore,
		GitDir:      cwd(),
		BaseDir:     cwd(),
		Preview:     !ac.noPreview,
		Verbose:     ac.verbose,
	}

	if tc.RemoteDir == "" {
		tc.RemoteDir = cfg.Defaults.RemotePath
	}

	if !ac.quiet {
		fmt.Fprintf(os.Stderr, "\n%s\n",
			color.New(color.FgCyan, color.Bold).Sprintf("═══ Deploy ═══"))
		opLabel := op.String()
		serverLabel := tc.ServerRegex
		if serverLabel == "" && len(tc.Tags) > 0 {
			serverLabel = strings.Join(tc.Tags, ",")
		}
		if serverLabel == "" {
			serverLabel = fmt.Sprintf("%d servers", len(servers))
		}
		fmt.Fprintf(os.Stderr, "  %s %d files  →  %s\n",
			color.MagentaString(opLabel), fileSet.Count, color.CyanString(serverLabel))
		if tc.DryRun {
			fmt.Fprintf(os.Stderr, "  %s\n", color.YellowString("⚠ Dry-run mode — no files will be transferred"))
		}
	}

	// Ensure remote directory exists (push only, best-effort)
	if !ac.dryRun && op == transfer.OpPush && tc.RemoteDir != "" {
		for _, srv := range servers {
			if err := transfer.EnsureRemoteDirs(srv, []string{tc.RemoteDir}); err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ %s: remote dir mkdir failed: %v (transfer will still be attempted)\n", srv.Name, err)
			}
		}
	}

	// Run transfer
	results := transfer.Run(servers, tc)

	// Record history
	recordDeployHistory(op.String(), ac, fileSet, results)

	// Output
	if ac.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	printResults(results)
	if transferHasErrors(results) {
		return fmt.Errorf("push/pull completed with errors on %d server(s)", countErrors(results))
	}
	return nil
}

// ---------------------------------------------------------------------------
// runWatch — watch mode
// ---------------------------------------------------------------------------

func runWatch(ac *appConfig) error {
	cfgPath, err := config.FindConfigPath(ac.configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	mergeDefaults(ac, &cfg.Defaults)

	// Apply default profile if configured
	if ac.profile != "" {
		if p, ok := cfg.Profiles[ac.profile]; ok {
			applyProfile(ac, p)
		} else {
			return fmt.Errorf("profile %q not found in config", ac.profile)
		}
	} else if cfg.Defaults.Profile != "" {
		if p, ok := cfg.Profiles[cfg.Defaults.Profile]; ok {
			applyProfile(ac, p)
		}
	}

	watchDirs := ac.paths
	if len(watchDirs) == 0 {
		watchDirs = []string{"."}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n  Stopping watch mode...")
		cancel()
	}()

	// Preload deploy config to reuse
	servers := make([]config.Server, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	remoteDir := ac.remoteDir
	if remoteDir == "" {
		remoteDir = cfg.Defaults.RemotePath
	}

	return watcher.Watch(ctx, watcher.Config{
		Paths: watchDirs,
		Delay: time.Duration(ac.watchDelay) * time.Second,
		OnChange: func(changedFiles []string) {
			fmt.Fprintf(os.Stderr, "\n  📦 Changes detected (%d files). Deploying...\n", len(changedFiles))

			// Get git-diff files
			gitFiles, err := watcher.GitChangedFiles(cwd())
			if err != nil || len(gitFiles) == 0 {
				fmt.Fprintf(os.Stderr, "  ⚠ No git changes to deploy\n")
				return
			}

			absFiles := make([]string, len(gitFiles))
			for i, f := range gitFiles {
				absFiles[i] = filepath.Join(cwd(), f)
			}

			tc := transfer.RunConfig{
				Operation:   transfer.OpPush,
				LocalFiles:  absFiles,
				RemoteDir:   remoteDir,
				ServerRegex: ac.server,
				Tags:        splitTags(ac.tags),
				DryRun:      ac.dryRun,
				Force:       true, // no confirmation in watch mode
				Concurrency: ac.concurrency,
				Timeout:     ac.timeout,
				ShowBar:     !ac.quiet && cfg.Defaults.ShowBar,
				Quiet:       ac.quiet,
				NoGitignore: ac.noGitignore,
				GitDir:      cwd(),
				BaseDir:     cwd(),
				Verbose:     ac.verbose,
			}

			results := transfer.Run(servers, tc)
			printResults(results)
			fmt.Fprintf(os.Stderr, "  👀 Watching for more changes...\n")
		},
	})
}

// ---------------------------------------------------------------------------
// runHistory / runRollback
// ---------------------------------------------------------------------------

func runHistory(args []string) error {
	histDir, err := config.HistoryDir()
	if err != nil {
		return err
	}

	store, err := history.NewStore(histDir)
	if err != nil {
		return err
	}

	if len(args) == 1 {
		// Show specific entry
		id := 0
		fmt.Sscanf(args[0], "%d", &id)
		entry, err := store.Load(id)
		if err != nil {
			return err
		}
		fmt.Print(history.FormatEntry(entry))
		return nil
	}

	// List recent
	entries, err := store.List(10)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "  No deploy history yet.")
		return nil
	}

	for _, e := range entries {
		fmt.Print(history.FormatEntry(e))
	}
	return nil
}

func runRollback(args []string) error {
	histDir, err := config.HistoryDir()
	if err != nil {
		return err
	}
	store, err := history.NewStore(histDir)
	if err != nil {
		return err
	}

	var entry history.Entry
	if len(args) == 1 {
		id := 0
		fmt.Sscanf(args[0], "%d", &id)
		entry, err = store.Load(id)
	} else {
		entry, err = store.Latest()
	}
	if err != nil {
		return fmt.Errorf("no deploy entry to rollback: %w", err)
	}

	if len(entry.BackupPaths) == 0 {
		return fmt.Errorf("deploy #%d has no backups — push with rollback support enabled first", entry.ID)
	}

	// Load config for server details
	cfgPath, err := config.FindConfigPath("")
	if err != nil {
		return fmt.Errorf("config not found: %w", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	servers := make([]config.Server, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		if _, hasBackup := entry.BackupPaths[s.Name]; hasBackup {
			servers = append(servers, s)
		}
	}

	fmt.Fprintf(os.Stderr, "  🔙 Rolling back deploy #%d (%s)\n", entry.ID, entry.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "  Files: %d  Servers: %d\n", entry.Files, len(entry.Servers))
	fmt.Fprintln(os.Stderr)

	results := transfer.RunRollback(servers, transfer.RollbackOptions{
		BackupPaths: entry.BackupPaths,
		RemotePath:  entry.RemotePath,
		Exclude:     cfg.Defaults.Exclude,
	})
	printResults(results)
	if transferHasErrors(results) {
		return fmt.Errorf("rollback completed with errors on %d server(s)", countErrors(results))
	}
	return nil
}

// ---------------------------------------------------------------------------
// runServers
// ---------------------------------------------------------------------------

func runServers(ac *appConfig) error {
	cfgPath, err := config.FindConfigPath(ac.configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	tagList := splitTags(ac.tags)

	if !ac.quiet {
		fmt.Fprintf(os.Stderr, "  deploi  ·  cfg %s  ·  srv %d\n\n", cfgPath, len(cfg.Servers))
	}

	type serverInfo struct {
		Name       string   `json:"name"`
		Host       string   `json:"host"`
		Port       int      `json:"port"`
		User       string   `json:"user"`
		Method     string   `json:"method"`
		Tags       []string `json:"tags"`
		RemotePath string   `json:"remote_path"`
		HasHooks   bool     `json:"has_hooks"`
		Profile    string   `json:"profile,omitempty"`
		Status     string   `json:"status"`
	}

	var infos []serverInfo
	for _, srv := range cfg.Servers {
		if ac.server != "" {
			if matched, _ := filepath.Match(ac.server, srv.Name); !matched {
				continue
			}
		}
		if len(tagList) > 0 {
			if !transfer.HasAnyTag(srv.Tags, tagList) {
				continue
			}
		}

		rp := srv.RemotePath
		if rp == "" {
			rp = "(default)"
		}

		infos = append(infos, serverInfo{
			Name: srv.Name, Host: srv.Host, Port: srv.Port, User: srv.User,
			Method: srv.Method, Tags: srv.Tags, RemotePath: rp,
			HasHooks: srv.Hooks != nil, Status: "✓",
		})
	}

	if ac.json {
		return json.NewEncoder(os.Stdout).Encode(infos)
	}

	for _, info := range infos {
		tagStr := ""
		if len(info.Tags) > 0 {
			tagStr = fmt.Sprintf(" [%s]", strings.Join(info.Tags, ", "))
		}
		hooksStr := ""
		if info.HasHooks {
			hooksStr = " ⚡hooks"
		}
		fmt.Fprintf(os.Stdout, "  %s  %s@%s:%d  %s%s%s  %s\n",
			color.GreenString("✓"), info.User, info.Host, info.Port,
			info.Method, tagStr, hooksStr, info.RemotePath)
	}

	if len(infos) == 0 {
		fmt.Fprintln(os.Stderr, "  No servers match the given filter.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// runRemote
// ---------------------------------------------------------------------------

func runRemote(ac *appConfig, commands []string) error {
	cfgPath, err := config.FindConfigPath(ac.configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	mergeDefaults(ac, &cfg.Defaults)

	servers := make([]config.Server, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	// Interactive server selection via fzf
	if ac.pickSrv {
		selected, err := pickServersFZF(servers, ac.server, splitTags(ac.tags))
		if err != nil {
			return err
		}
		servers = selected
		ac.server = ""
		ac.tags = ""
	}

	tc := transfer.RunConfig{
		ServerRegex: ac.server,
		Tags:        splitTags(ac.tags),
		DryRun:      ac.dryRun,
		Verbose:     ac.verbose,
		Concurrency: ac.concurrency,
		Timeout:     ac.timeout,
		ShowBar:     !ac.quiet && cfg.Defaults.ShowBar,
		Quiet:       ac.quiet,
	}

	if !ac.quiet {
		fmt.Fprintf(os.Stderr, "  deploi run  ·  %d commands  ·  %s\n", len(commands), tc.ServerRegex)
		if tc.DryRun {
			fmt.Fprintf(os.Stderr, "  ⚠ Dry-run mode\n")
		}
	}

	results := transfer.RunCommands(servers, commands, tc)
	if ac.json {
		return json.NewEncoder(os.Stdout).Encode(results)
	}
	printResults(results)
	if transferHasErrors(results) {
		return fmt.Errorf("command execution completed with errors on %d server(s)", countErrors(results))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Config generate / validate
// ---------------------------------------------------------------------------

func runConfigGenerate(outputPath string) error {
	if outputPath == "" {
		outputPath = config.PlatformConfigPath()
		if outputPath == "" {
			return fmt.Errorf("could not determine platform config directory")
		}
		fmt.Fprintf(os.Stderr, "  Writing to %s\n", outputPath)
	}
	content := config.DefaultExample()
	if outputPath == "-" {
		fmt.Print(content)
		return nil
	}
	dir := filepath.Dir(outputPath)
	os.MkdirAll(dir, 0755)
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Config file created at %s\n", outputPath)
	return nil
}

func runConfigValidate(cfgPath string) error {
	path, err := config.FindConfigPath(cfgPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  ✓ Config file found: %s\n", path)
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("config parse error: %w", err)
	}
	if len(cfg.Servers) == 0 {
		fmt.Fprintf(os.Stderr, "  ⚠ No servers defined\n")
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ %d servers defined\n", len(cfg.Servers))
		for name, srv := range cfg.Servers {
			if srv.Host == "" || srv.User == "" {
				fmt.Fprintf(os.Stderr, "  ⚠ %s: missing host or user\n", name)
			} else {
				hooks := ""
				if srv.Hooks != nil {
					hooks = fmt.Sprintf(" (%d hooks)", len(srv.Hooks.Pre)+len(srv.Hooks.Post))
				}
				fmt.Fprintf(os.Stderr, "  ✓ %s → %s@%s:%d (%s)%s\n", name, srv.User, srv.Host, srv.Port, srv.Method, hooks)
			}
		}
	}
	if len(cfg.Profiles) > 0 {
		fmt.Fprintf(os.Stderr, "  ✓ %d profiles defined\n", len(cfg.Profiles))
	}
	fmt.Fprintf(os.Stderr, "  ✓ Config is valid\n")
	return nil
}

// runConfigInit interactively creates a project-level deploi.toml.
func runConfigInit() error {
	// Load global config
	cfgPath, err := config.FindConfigPath("")
	if err != nil {
		return fmt.Errorf("no global config found. Run 'deploi config generate' first")
	}

	globalCfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load global config: %w", err)
	}

	if len(globalCfg.Servers) == 0 {
		return fmt.Errorf("no servers defined in global config")
	}

	// Check if local config already exists
	localPath := "deploi.toml"
	if _, err := os.Stat(localPath); err == nil {
		if !confirmPrompt("deploi.toml already exists. Overwrite?") {
			fmt.Fprintln(os.Stderr, "  Cancelled.")
			return nil
		}
	}

	// Display available servers
	fmt.Fprintf(os.Stderr, "\n  Global config: %s\n", cfgPath)
	fmt.Fprintf(os.Stderr, "  Available servers:\n")
	names := make([]string, 0, len(globalCfg.Servers))
	for name := range globalCfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		srv := globalCfg.Servers[name]
		rp := srv.RemotePath
		if rp == "" {
			rp = "(default)"
		}
		tagStr := ""
		if len(srv.Tags) > 0 {
			tagStr = fmt.Sprintf(" [%s]", strings.Join(srv.Tags, ", "))
		}
		fmt.Fprintf(os.Stderr, "    %s  %s@%s:%d%s  %s\n", name, srv.User, srv.Host, srv.Port, tagStr, rp)
	}
	fmt.Fprintln(os.Stderr)

	// Pick servers via fzf or editor
	allServers := make([]config.Server, 0, len(globalCfg.Servers))
	for _, s := range globalCfg.Servers {
		allServers = append(allServers, s)
	}
	sort.Slice(allServers, func(i, j int) bool { return allServers[i].Name < allServers[j].Name })

	picked, err := pickServersFZF(allServers, "", nil)
	if err != nil {
		return err
	}

	if len(picked) == 0 {
		return fmt.Errorf("no servers selected")
	}

	// Build the project config TOML
	var b strings.Builder
	b.WriteString("# deploi.toml — project-level deploy config\n")
	b.WriteString("# Auto-generated by 'deploi config init'\n\n")
	b.WriteString("[defaults]\n")
	b.WriteString(fmt.Sprintf("remote_path = \"%s\"\n\n", guessDefaultPath(picked)))

	b.WriteString("# Servers used by this project\n")
	for _, srv := range picked {
		b.WriteString(fmt.Sprintf("\n[servers.%s]\n", srv.Name))
		b.WriteString(fmt.Sprintf("remote_path = \"%s\"\n", srv.RemotePath))

		// Ask for hooks
		fmt.Fprintf(os.Stderr, "\n  Hooks for %s?\n", srv.Name)
		if confirmPrompt("  Add pre-deploy commands?") {
			fmt.Fprintf(os.Stderr, "  Enter commands (one per line, empty line to finish):\n")
			var preCmds []string
			for {
				fmt.Fprintf(os.Stderr, "    pre> ")
				var cmd string
				fmt.Scanln(&cmd)
				cmd = strings.TrimSpace(cmd)
				if cmd == "" {
					break
				}
				preCmds = append(preCmds, cmd)
			}
			if len(preCmds) > 0 {
				b.WriteString("[servers." + srv.Name + ".hooks]\n")
				b.WriteString("pre = [\n")
				for _, c := range preCmds {
					b.WriteString(fmt.Sprintf("  %q,\n", c))
				}
			}
		}

		if confirmPrompt("  Add post-deploy commands?") {
			if !strings.Contains(b.String(), ".hooks]\n") {
				b.WriteString("[servers." + srv.Name + ".hooks]\n")
			}
			var postCmds []string
			fmt.Fprintf(os.Stderr, "  Enter commands (one per line, empty line to finish):\n")
			for {
				fmt.Fprintf(os.Stderr, "    post> ")
				var cmd string
				fmt.Scanln(&cmd)
				cmd = strings.TrimSpace(cmd)
				if cmd == "" {
					break
				}
				postCmds = append(postCmds, cmd)
			}
			if len(postCmds) > 0 {
				if !strings.Contains(b.String(), "pre = [") {
					b.WriteString("pre = []\n")
				}
				b.WriteString("post = [\n")
				for _, c := range postCmds {
					b.WriteString(fmt.Sprintf("  %q,\n", c))
				}
				b.WriteString("]\n")
			} else if strings.Contains(b.String(), "pre = [") {
				b.WriteString("]\n")
			}
		}
	}

	// Add profiles section
	fmt.Fprintln(os.Stderr)
	if confirmPrompt("  Add deploy profiles? (full, assets, config)") {
		b.WriteString("\n# Profiles\n")
		b.WriteString("[profiles.full]\n")
		b.WriteString("method = \"git-diff\"\n\n")
		b.WriteString("[profiles.assets]\n")
		b.WriteString("method = \"all\"\n")
		b.WriteString("paths = [\"public/build/\", \"public/assets/\"]\n\n")
		b.WriteString("[profiles.config]\n")
		b.WriteString("method = \"git-commit\"\n")
		b.WriteString("pick_commit = true\n")
	}

	// Write the file
	if err := os.WriteFile(localPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}

	fmt.Fprintf(os.Stderr, "\n  ✓ Created %s (%d servers, %d profiles)\n",
		localPath, len(picked), strings.Count(b.String(), "[profiles."))
	return nil
}

// guessDefaultPath picks the most common remote path from selected servers.
func guessDefaultPath(servers []config.Server) string {
	pathCount := make(map[string]int)
	for _, s := range servers {
		if s.RemotePath != "" {
			pathCount[s.RemotePath]++
		}
	}

	maxCount := 0
	bestPath := "/home/deploy/www/"
	for p, c := range pathCount {
		if c > maxCount {
			maxCount = c
			bestPath = p
		}
	}
	return bestPath
}

// ---------------------------------------------------------------------------
// File resolution
// ---------------------------------------------------------------------------

func resolveFiles(ac *appConfig, editor string) (*picker.FileSet, error) {
	// Determine select mode and filter
	sel := ac.selectMode
	fil := ac.filterMode

	// Default filter for each select mode
	if fil == "" {
		switch sel {
		case "fzf", "editor":
			fil = "path"
		case "all":
			fil = "path"
		case "manual":
			fil = "path"
		default:
			fil = "path"
		}
	}

	// Default select for each filter
	if sel == "" {
		switch fil {
		case "git-diff", "git-commit", "git-branch":
			sel = "manual"
		default:
			sel = "manual"
		}
	}

	// Map to legacy source type + options
	switch sel {
	case "manual":
		return resolveFilter(fil, ac, editor, false)
	case "fzf":
		return resolveFilter(fil, ac, editor, true)
	case "editor":
		if fil == "path" {
			return picker.Pick(picker.PickConfig{Source: picker.SourceEditor, Paths: ac.paths, Editor: editor})
		}
		// For git filters with editor, resolve files first then show in editor
		fs, err := resolveFilter(fil, ac, editor, false)
		if err != nil {
			return nil, err
		}
		// Show the resolved files in the editor for interactive selection
		relFiles := make([]string, len(fs.Files))
		for i, f := range fs.Files {
			rel, _ := filepath.Rel(cwd(), f)
			relFiles[i] = rel
		}
		return picker.Pick(picker.PickConfig{Source: picker.SourceEditor, Paths: relFiles, Editor: editor})
	case "all":
		targets := ac.paths
		if len(targets) == 0 {
			targets = []string{"."}
		}
		return picker.Pick(picker.PickConfig{Source: picker.SourceAll, Paths: targets, BaseDir: cwd()})
	}

	return nil, fmt.Errorf("unknown select mode: %s", sel)
}

// resolveFilter maps a filter + optional fzf to the right picker call.
func resolveFilter(fil string, ac *appConfig, editor string, useFZF bool) (*picker.FileSet, error) {
	baseDir := cwd()
	includeStaged := !ac.noStaged

	switch fil {
	case "git-diff":
		if useFZF {
			// First resolve git-diff files respecting --no-staged, then show in fzf
			fs, err := picker.Pick(picker.PickConfig{
				Source:           picker.SourceGitDiff,
				GitDir:           baseDir,
				IncludeStaged:    includeStaged,
				IncludeUntracked: true,
			})
			if err != nil {
				if !includeStaged {
					return nil, err
				}
				// No git changes and --no-staged not set — show all files via fzf
				return picker.Pick(picker.PickConfig{
					Source: picker.SourceFZF,
					GitDir: baseDir,
					Editor: editor,
				})
			}
			relFiles := make([]string, len(fs.Files))
			for i, f := range fs.Files {
				rel, _ := filepath.Rel(baseDir, f)
				relFiles[i] = rel
			}
			return picker.Pick(picker.PickConfig{
				Source: picker.SourceFZF,
				Paths:  relFiles,
				GitDir: baseDir,
				Editor: editor,
			})
		}
		return picker.Pick(picker.PickConfig{
			Source:           picker.SourceGitDiff,
			GitDir:           baseDir,
			IncludeStaged:    includeStaged,
			IncludeUntracked: true,
		})

	case "git-commit":
		if useFZF || ac.pick {
			if ac.commit != "" {
				// First resolve files from the commit, then show in fzf
				fs, err := picker.Pick(picker.PickConfig{
					Source: picker.SourceGitCommit,
					GitDir: baseDir,
					Commit: ac.commit,
				})
				if err != nil {
					return nil, err
				}
				relFiles := make([]string, len(fs.Files))
				for i, f := range fs.Files {
					rel, _ := filepath.Rel(baseDir, f)
					relFiles[i] = rel
				}
				return picker.Pick(picker.PickConfig{
					Source: picker.SourceFZF,
					Paths:  relFiles,
					GitDir: baseDir,
					Editor: editor,
				})
			}
			return picker.Pick(picker.PickConfig{
				Source:     picker.SourceFZFCommit,
				GitDir:     baseDir,
				PickCommit: true,
				Editor:     editor,
			})
		}
		if ac.commit == "" {
			return nil, fmt.Errorf("--commit is required with --filter git-commit")
		}
		return picker.Pick(picker.PickConfig{
			Source: picker.SourceGitCommit,
			GitDir: baseDir,
			Commit: ac.commit,
		})

	case "git-branch":
		if ac.branch == "" {
			return nil, fmt.Errorf("--branch is required with --filter git-branch")
		}
		if useFZF {
			return picker.Pick(picker.PickConfig{
				Source: picker.SourceFZF,
				GitDir: baseDir,
				Branch: ac.branch,
				Editor: editor,
			})
		}
		return picker.Pick(picker.PickConfig{
			Source: picker.SourceGitBranch,
			GitDir: baseDir,
			Branch: ac.branch,
		})

	case "path":
		if useFZF {
			return picker.Pick(picker.PickConfig{
				Source: picker.SourceFZF,
				Paths:  ac.paths,
				GitDir: baseDir,
				Editor: editor,
			})
		}
		if len(ac.paths) == 0 {
			return nil, fmt.Errorf("file paths required with --filter path (provide as arguments)")
		}
		return picker.Pick(picker.PickConfig{
			Source:  picker.SourceManual,
			Paths:   ac.paths,
			BaseDir: baseDir,
		})

	default:
		return nil, fmt.Errorf("unknown filter: %s (use: git-diff, git-commit, git-branch, path)", fil)
	}
}

// ---------------------------------------------------------------------------
// Profile support
//
// Merge precedence (low → high):
//   1. defaults in code (SetDefaults)
//   2. [defaults] in TOML config
//   3. [profiles.X] — applies via applyProfile
//   4. CLI flags — highest priority
//
// Merge strategy per field:
//   - Scalar fields (timeout, remote_path, etc.): profile overrides config
//   - Paths: profile replaces config paths (not append)
//   - Exclude: profile merged + deduplicated with config defaults
//     (via mergeExcludes — union semantics)
//   - RsyncOpts: profile overrides config rsync_options (not append)
// ---------------------------------------------------------------------------

func applyProfile(ac *appConfig, p config.Profile) {
	if p.Method != "" {
		ac.selectMode = p.Method
	}
	if len(p.Paths) > 0 {
		ac.paths = p.Paths
	}
	if p.RemotePath != "" {
		ac.remoteDir = p.RemotePath
	}
	if p.Commit != "" {
		ac.commit = p.Commit
	}
	if p.Branch != "" {
		ac.branch = p.Branch
	}
	if p.PickCommit {
		ac.pick = true
	}
	if p.RsyncOpts != "" {
		ac.rsyncOpts = p.RsyncOpts
	}
	// Apply profile-level exclude patterns
	if len(p.Exclude) > 0 {
		ac.exclude = p.Exclude
	}
}

// ---------------------------------------------------------------------------
// Exclude / gitignore helpers
// ---------------------------------------------------------------------------

func buildExcludePatterns(cfgExclude []string, respectGitignore bool, baseDir string) []string {
	patterns := make(map[string]bool)
	var list []string

	add := func(p string) {
		if !patterns[p] {
			patterns[p] = true
			list = append(list, p)
		}
	}

	for _, e := range cfgExclude {
		add(e)
	}

	if respectGitignore {
		for _, p := range transfer.ReadGitignore(baseDir) {
			add(p)
		}
	}

	return list
}

func isExcluded(path string, patterns []string) bool {
	base := filepath.Base(path)

	// Get relative path from current directory for meaningful matching
	rel := path
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			rel = abs
		}
	}
	wd, _ := os.Getwd()
	relToWd, _ := filepath.Rel(wd, rel)

	for _, p := range patterns {
		// Match against filename
		if matched, _ := filepath.Match(p, base); matched {
			return true
		}
		// Match against relative path
		if relToWd != "" {
			if matched, _ := filepath.Match(p, relToWd); matched {
				return true
			}
			// Match against any path component (e.g., "deploi" matches "dir/deploi/file.go")
			if strings.Contains(relToWd, "/"+p+"/") || strings.HasSuffix(relToWd, "/"+p) ||
				strings.HasPrefix(relToWd, p+"/") || relToWd == p {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// History recording
// ---------------------------------------------------------------------------

func recordDeployHistory(op string, ac *appConfig, fileSet *picker.FileSet, results []transfer.TransferResult) {
	histDir, err := config.HistoryDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ History recording disabled: %v\n", err)
		return
	}
	store, err := history.NewStore(histDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ History store failed: %v\n", err)
		return
	}

	servers := make([]string, len(results))
	okCount := 0
	for i, r := range results {
		servers[i] = r.Server
		if r.Status == "ok" {
			okCount++
		}
	}

	status := "ok"
	if okCount < len(results) {
		status = "error"
	}

	entry := history.Entry{
		Operation:  op,
		Method:     ac.selectMode + "+" + ac.filterMode,
		Servers:    servers,
		Files:      fileSet.Count,
		FileList:   fileSet.Files,
		Source:     fileSet.Label,
		Commit:     ac.commit,
		Branch:     ac.branch,
		RemotePath: ac.remoteDir,
		Status:     status,
		Profile:    ac.profile,
		BackupPaths: func() map[string]string {
			bp := make(map[string]string)
			for _, r := range results {
				if r.BackupPath != "" {
					bp[r.Server] = r.BackupPath
				}
			}
			return bp
		}(),
	}

	_ = store.Record(entry) // best effort
}

// ---------------------------------------------------------------------------
// Generic helpers
// ---------------------------------------------------------------------------

func mergeDefaults(ac *appConfig, d *config.Defaults) {
	if ac.timeout <= 0 {
		ac.timeout = d.Timeout
	}
	if ac.concurrency <= 0 {
		ac.concurrency = d.Concurrency
	}
	if !ac.dryRun && d.DryRun {
		ac.dryRun = true
	}
	if !ac.noPreview && d.NoPreview {
		ac.noPreview = true
	}
	if !ac.noGitignore && !d.IsRespectGitignore() {
		ac.noGitignore = true
	}
	// --force skips all confirmations and previews
	if ac.force {
		ac.noConfirm = true
		ac.noPreview = true
	}
}

// transferHasErrors returns true if any transfer result has status "error".
func transferHasErrors(results []transfer.TransferResult) bool {
	for _, r := range results {
		if r.Status == "error" {
			return true
		}
	}
	return false
}

// countErrors returns the number of transfer results with status "error".
func countErrors(results []transfer.TransferResult) int {
	n := 0
	for _, r := range results {
		if r.Status == "error" {
			n++
		}
	}
	return n
}

func splitTags(tags string) []string {
	if tags == "" {
		return nil
	}
	parts := strings.Split(tags, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// mergeExcludes merges profile-level excludes with config defaults
func mergeExcludes(defaults, profile []string) []string {
	if len(profile) == 0 {
		return defaults
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(defaults)+len(profile))
	for _, e := range defaults {
		if !seen[e] {
			seen[e] = true
			result = append(result, e)
		}
	}
	for _, e := range profile {
		if !seen[e] {
			seen[e] = true
			result = append(result, e)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// isRealTerminal

func cwd() string { d, _ := os.Getwd(); return d }

// isRealTerminal checks if stdin is a real terminal (not piped).
func isRealTerminal() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// pickServersFZF shows candidates in fzf and returns the selected servers.
// If fzf is not installed, falls back to editor mode.
func pickServersFZF(servers []config.Server, serverFilter string, tagFilter []string) ([]config.Server, error) {
	// Apply -s and -t filters first
	var filtered []config.Server
	for _, s := range servers {
		if serverFilter != "" {
			if matched, _ := filepath.Match(serverFilter, s.Name); !matched {
				continue
			}
		}
		if len(tagFilter) > 0 {
			if !transfer.HasAnyTag(s.Tags, tagFilter) {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no servers match the given filter")
	}

	// Build display lines: "name  host:port  [tags]  path"
	lines := make([]string, len(filtered))
	for i, s := range filtered {
		tagStr := strings.Join(s.Tags, ",")
		rp := s.RemotePath
		if rp == "" {
			rp = "(default)"
		}
		if tagStr != "" {
			lines[i] = fmt.Sprintf("%s  %s@%s:%d  [%s]  %s", s.Name, s.User, s.Host, s.Port, tagStr, rp)
		} else {
			lines[i] = fmt.Sprintf("%s  %s@%s:%d  %s", s.Name, s.User, s.Host, s.Port, rp)
		}
	}

	// Try fzf first (only if we have a real terminal)
	if _, err := exec.LookPath("fzf"); err == nil && isRealTerminal() {
		// Write candidates to a temp file for fzf to read
		tmpFile, err := os.CreateTemp("", "deploi-srv-candidates-*")
		if err == nil {
			tmpPath := tmpFile.Name()
			os.WriteFile(tmpPath, []byte(strings.Join(lines, "\n")), 0644)
			tmpFile.Close()
			defer os.Remove(tmpPath)

			cmd := exec.Command("fzf", "--multi",
				"--prompt=Select servers (Tab to multi-select)> ")
			cmd.Stderr = os.Stderr
			// Read candidates from the temp file via stdin
			f, _ := os.Open(tmpPath)
			cmd.Stdin = f
			out, err := cmd.Output()
			f.Close()

			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
					return nil, fmt.Errorf("selection cancelled")
				}
				// Fall through to editor mode
			} else {
				selected := picker.ParseFileList(string(out))
				if len(selected) == 0 {
					return nil, fmt.Errorf("no servers selected")
				}
				// Map selected display lines back to server names
				nameMap := make(map[string]bool)
				for _, line := range selected {
					parts := strings.Fields(line)
					if len(parts) > 0 {
						nameMap[parts[0]] = true
					}
				}
				var result []config.Server
				for _, s := range filtered {
					if nameMap[s.Name] {
						result = append(result, s)
					}
				}
				if len(result) == 0 {
					return nil, fmt.Errorf("no servers selected")
				}
				return result, nil
			}
		}
	}

	// Fallback: editor mode
	fmt.Fprintf(os.Stderr, "  fzf not found. Open editor to select servers.\n")
	header := "# Lines with '#' are ignored.\n"
	header += "# Delete servers you do NOT want, then save (:wq).\n\n"
	content := header + strings.Join(lines, "\n") + "\n"

	tmpFile, _ := os.CreateTemp("", "deploi-srv-*.txt")
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	os.WriteFile(tmpPath, []byte(content), 0644)
	tmpFile.Close()

	editor := picker.FindEditor()
	editCmd := exec.Command(editor, tmpPath)
	editCmd.Stdin = os.Stdin
	editCmd.Stdout = os.Stdout
	editCmd.Stderr = os.Stderr
	if err := editCmd.Run(); err != nil {
		return nil, fmt.Errorf("editor: %w", err)
	}
	data, _ := os.ReadFile(tmpPath)
	selected := picker.ParseEditorOutput(string(data))
	if len(selected) == 0 {
		return nil, fmt.Errorf("no servers selected")
	}

	nameMap := make(map[string]bool)
	for _, line := range selected {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) > 0 && !strings.HasPrefix(parts[0], "#") {
			nameMap[parts[0]] = true
		}
	}
	var result []config.Server
	for _, s := range filtered {
		if nameMap[s.Name] {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no servers selected")
	}
	return result, nil
}

func confirmPrompt(msg string) bool {
	fmt.Fprintf(os.Stderr, "  ? %s [y/N] ", msg)
	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

// runCheck tests SSH connectivity to all matching servers.
func runCheck(ac *appConfig) error {
	cfgPath, err := config.FindConfigPath(ac.configPath)
	if err != nil {
		return fmt.Errorf("config not found: %w", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	servers := make([]config.Server, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	// Apply filters
	if ac.pickSrv {
		selected, err := pickServersFZF(servers, ac.server, splitTags(ac.tags))
		if err != nil {
			return err
		}
		servers = selected
	} else if ac.server != "" || ac.tags != "" {
		tagList := splitTags(ac.tags)
		var filtered []config.Server
		for _, s := range servers {
			if ac.server != "" {
				if matched, _ := filepath.Match(ac.server, s.Name); !matched {
					continue
				}
			}
			if len(tagList) > 0 {
				if !transfer.HasAnyTag(s.Tags, tagList) {
					continue
				}
			}
			filtered = append(filtered, s)
		}
		servers = filtered
	}

	if len(servers) == 0 {
		return fmt.Errorf("no servers match the given filter")
	}

	fmt.Fprintf(os.Stderr, "  🔍 Checking %d server(s)...\n\n", len(servers))
	ok := 0
	fail := 0

	for _, s := range servers {
		hostPort := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
		start := time.Now()

		conn, err := net.DialTimeout("tcp", hostPort, 10*time.Second)
		latency := time.Since(start)

		if err != nil {
			fail++
			errMsg := enrichConnError(err)
			fmt.Fprintf(os.Stdout, "  %s %-20s  %s  %s\n",
				color.RedString("✗"), s.Name, color.RedString("unreachable"), color.YellowString(errMsg))
			continue
		}
		conn.Close()

		sshInfo := ""
		if ac.verbose {
			sshInfo = testSSHVersion(s)
		}

		ok++
		latencyStr := color.CyanString(latency.Round(time.Millisecond).String())
		fmt.Fprintf(os.Stdout, "  %s %-20s  %s%s\n",
			color.GreenString("✓"), s.Name, latencyStr, sshInfo)
	}

	fmt.Fprintf(os.Stderr, "\n  %d OK · %d FAIL\n", ok, fail)
	return nil
}

func enrichConnError(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "Connection refused — is SSH running?"
	case strings.Contains(s, "i/o timeout"):
		return "Connection timed out — check VPN"
	case strings.Contains(s, "no route to host"):
		return "No route to host — check network"
	case strings.Contains(s, "Name or service not known"):
		return "Hostname not found"
	default:
		return s
	}
}

func testSSHVersion(s config.Server) string {
	hostPort := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	conn, err := net.DialTimeout("tcp", hostPort, 5*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 255)
	n, _ := conn.Read(buf)
	if n > 0 {
		banner := strings.TrimSpace(string(buf[:n]))
		if strings.HasPrefix(banner, "SSH-") {
			return fmt.Sprintf("  %s", color.MagentaString(banner))
		}
	}
	return ""
}

// Results display
func printResults(results []transfer.TransferResult) {
	ok := 0
	fail := 0
	for _, r := range results {
		if r.Status == "ok" {
			ok++
			hooks := ""
			if r.HooksPre > 0 || r.HooksPost > 0 {
				hooks = fmt.Sprintf(" ⚡%d/%d", r.HooksPre, r.HooksPost)
			}
			extra := ""
			if r.Speed != "" {
				extra = fmt.Sprintf("  ⚡%s", r.Speed)
			}
			fileInfo := ""
			if r.Files > 0 {
				fileInfo = fmt.Sprintf("%d files", r.Files)
			}
			fmt.Fprintf(os.Stdout, "  %s %-20s  %s%s%s  %s\n",
				color.GreenString("✓"), r.Server, fileInfo, extra, hooks, r.Elapsed)
		} else {
			fail++
			errStr := r.Error
			parts := strings.SplitN(errStr, "\n", 2)
			mainMsg := parts[0]
			detail := ""
			if len(parts) > 1 {
				detail = parts[1]
			}
			if len(mainMsg) > 100 {
				mainMsg = mainMsg[:97] + "..."
			}
			fmt.Fprintf(os.Stdout, "  %s %-20s  %s\n",
				color.RedString("✗"), r.Server, mainMsg)
			if detail != "" {
				fmt.Fprintf(os.Stdout, "    %s\n", color.YellowString("💡 %s", detail))
			}
		}
	}
	fmt.Fprintf(os.Stderr, "\n")
	if fail > 0 {
		fmt.Fprintf(os.Stderr, "  %s%d OK · %s%d FAIL\n",
			color.GreenString("\u2713 "), ok, color.RedString("\u2717 "), fail)
	} else {
		fmt.Fprintf(os.Stderr, "  %s%d OK\n", color.GreenString("\u2713 "), ok)
	}
}

func installSkill() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	skillDir := filepath.Join(home, ".hermes", "skills", "software-development", "deploi")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Skill installed to %s\n", skillPath)
	return nil
}
