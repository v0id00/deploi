// Package app wires everything together — CLI flags, orchestrator, version.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
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
	source    string
	paths     []string
	commit    string
	branch    string
	pick      bool
	pickSrv   bool // interactive server selection via fzf
	remoteDir string

	// Profile
	profile string

	// Transfer
	rsyncOpts string
	method    string

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

File selection methods:
  manual       Explicit file/directory paths
  git-diff     Files changed in working tree (staged + unstaged + untracked)
  git-commit   Files in a specific commit (use --commit or --pick/-P)
  git-branch   Files changed between branches
  fzf          Interactive fzf picker for files
  fzf-commit   Pick commit via fzf, then pick files
  editor       Open $EDITOR to select files
  all          All files in given directories

By default, deploi shows a diff preview and asks for confirmation before transfer.
Use --no-preview or set no_preview=true in config to skip confirmation.
.gitignore is respected automatically; use --no-gitignore to disable.
Use --no-staged to exclude staged files from git-diff selection.
Use -S to pick target servers interactively via fzf.

Examples:
  deploi push -s prod -m git-diff
  deploi push -S -m git-diff                # pick servers via fzf
  deploi push -S -t prod -m git-diff         # filter by tag, then pick
  deploi push -s prod -m git-diff -P         # pick changed files via fzf
  deploi push -s prod -m git-commit -P       # pick commit via fzf
  deploi push -s prod -m fzf-commit           # pick commit + files via fzf
  deploi push -s staging --profile assets
  deploi pull -s staging -m all remote/path/
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

File selection methods:
  (path args)    Explicit file paths (manual)
  -m git-diff    Files changed in working tree (staged + unstaged + untracked)
  -m git-diff -P Pick changed files via fzf
  -m git-commit  Files in a commit (use --commit HASH or -P to pick)
  -m fzf-commit  Pick commit then files via fzf
  -m fzf         Interactive fzf selection for files
  -m editor      Open editor to select files
  -m all         All files in a directory

Examples:
  deploi push -s prod -m git-diff
  deploi push -S -m git-diff                # pick servers via fzf
  deploi push -s prod -m git-diff -P         # pick changed files via fzf
  deploi push -s prod -m git-commit -P       # pick commit via fzf
  deploi push -s prod -m fzf-commit           # pick commit + files via fzf
  deploi push -s staging --profile assets`,
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
  deploi pull -s staging -m all remote/path/
  deploi pull -s prod-web-1 var/log/app.log`,
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
  deploi sync -s prod -m git-diff`,
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

	cmd.AddCommand(generateCmd, validateCmd)
	return cmd
}

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "completion [bash|zsh|fish|powershell]", Short: "Generate shell completion script",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash": return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh": return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish": return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell": return cmd.Root().GenPowerShellCompletion(os.Stdout)
			default: return fmt.Errorf("unknown shell: %s", args[0])
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
	flags.StringVarP(&ac.source, "method", "m", "manual",
		"File selection: manual, git-diff, git-commit, git-branch, fzf, fzf-commit, editor, all")
	flags.StringVar(&ac.commit, "commit", "", "Git commit hash (for git-commit method)")
	flags.StringVar(&ac.branch, "branch", "", "Git branch name (for git-branch method)")
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
		if p, ok := cfg.Profiles[ac.profile]; ok {
			applyProfile(ac, p)
		} else if cfg.Defaults.Profile != "" {
			if p, ok := cfg.Profiles[cfg.Defaults.Profile]; ok {
				applyProfile(ac, p)
			}
		}
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

	// Confirm when targeting ALL servers
	if !ac.noConfirm && !ac.dryRun && len(servers) == len(cfg.Servers) {
		if cfg.Defaults.ConfirmWithoutFilter {
			if !confirmPrompt(fmt.Sprintf("Transfer %d files to ALL %d servers?", fileSet.Count, len(servers))) {
				fmt.Fprintln(os.Stderr, "  Cancelled.")
				return nil
			}
		}
	}

	// Preview: show diff summary and ask confirmation (unless disabled)
	if !ac.noPreview && !ac.dryRun && op == transfer.OpPush {
		gitDir := cwd()
		summary := transfer.GitDiffSummary(fileSet.Files, gitDir)
		fmt.Fprintf(os.Stderr, "  ─── Diff Preview ───\n%s\n", summary)
		if !confirmPrompt(fmt.Sprintf("Transfer %d files to %d server(s)?", fileSet.Count, len(servers))) {
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
		Exclude:     cfg.Defaults.Exclude,
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
		opLabel := op.String()
		fmt.Fprintf(os.Stderr, "  deploi %s  ·  %s  ·  %d files  →  %s\n",
			opLabel, fileSet.Label, fileSet.Count, tc.ServerRegex)
		if tc.DryRun {
			fmt.Fprintf(os.Stderr, "  ⚠ Dry-run mode — no files will be transferred\n")
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

	fmt.Fprintf(os.Stderr, "  Rolling back deploy #%d (%s)...\n", entry.ID, entry.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "  Note: Rollback requires your servers to have rsync --backup-dir snapshots.\n")
	fmt.Fprintf(os.Stderr, "  Full rollback implementation coming soon.\n")
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
			if !hasAnyTag(srv.Tags, tagList) {
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

// ---------------------------------------------------------------------------
// File resolution
// ---------------------------------------------------------------------------

func resolveFiles(ac *appConfig, editor string) (*picker.FileSet, error) {
	sourceType := parseSourceType(ac.source)
	switch sourceType {
	case picker.SourceManual:
		if len(ac.paths) == 0 {
			return nil, fmt.Errorf("manual mode requires file paths as arguments")
		}
		return picker.Pick(picker.PickConfig{Source: sourceType, Paths: ac.paths, BaseDir: cwd()})

	case picker.SourceGitDiff:
		includeStaged := !ac.noStaged
		if ac.pick {
			return picker.Pick(picker.PickConfig{
				Source:         sourceType,
				GitDir:         cwd(),
				PickFiles:      true,
				IncludeStaged:  includeStaged,
				IncludeUntracked: true,
			})
		}
		return picker.Pick(picker.PickConfig{
			Source:          sourceType,
			GitDir:          cwd(),
			IncludeStaged:   includeStaged,
			IncludeUntracked: true,
		})

	case picker.SourceGitCommit:
		if ac.pick {
			return picker.Pick(picker.PickConfig{Source: picker.SourceFZFCommit, GitDir: cwd(), PickCommit: true, Editor: editor})
		}
		if ac.commit == "" {
			return nil, fmt.Errorf("--commit is required. Use --commit <hash> or --pick/-P to pick interactively")
		}
		return picker.Pick(picker.PickConfig{Source: sourceType, GitDir: cwd(), Commit: ac.commit})

	case picker.SourceGitBranch:
		if ac.branch == "" {
			return nil, fmt.Errorf("--branch is required for git-branch method")
		}
		return picker.Pick(picker.PickConfig{Source: sourceType, GitDir: cwd(), Branch: ac.branch})

	case picker.SourceFZF:
		return picker.Pick(picker.PickConfig{Source: sourceType, Paths: ac.paths, GitDir: cwd(), Editor: editor})

	case picker.SourceFZFCommit:
		return picker.Pick(picker.PickConfig{Source: sourceType, GitDir: cwd(), PickCommit: true, Editor: editor})

	case picker.SourceEditor:
		return picker.Pick(picker.PickConfig{Source: sourceType, Paths: ac.paths, Editor: editor})

	case picker.SourceAll:
		targets := ac.paths
		if len(targets) == 0 {
			targets = []string{"."}
		}
		return picker.Pick(picker.PickConfig{Source: sourceType, Paths: targets, BaseDir: cwd()})

	default:
		return nil, fmt.Errorf("unknown source: %s", ac.source)
	}
}

func parseSourceType(s string) picker.SourceType {
	switch strings.ToLower(s) {
	case "manual": return picker.SourceManual
	case "git-diff", "gitdiff", "git_diff", "changed": return picker.SourceGitDiff
	case "git-commit", "gitcommit", "git_commit", "commit": return picker.SourceGitCommit
	case "git-branch", "gitbranch", "git_branch", "branch": return picker.SourceGitBranch
	case "fzf": return picker.SourceFZF
	case "fzf-commit", "fzfcommit", "fzf_commit", "commit-pick", "pick-commit": return picker.SourceFZFCommit
	case "editor", "edit", "select": return picker.SourceEditor
	case "all", "dir", "directory", "recursive": return picker.SourceAll
	default: return picker.SourceManual
	}
}

// ---------------------------------------------------------------------------
// Profile support
// ---------------------------------------------------------------------------

func applyProfile(ac *appConfig, p config.Profile) {
	if p.Method != "" {
		ac.source = p.Method
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
	// TODO: exclude from profile is handled at transfer level via config
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
		for _, p := range readGitignoreLocal(baseDir) {
			add(p)
		}
	}

	return list
}

func readGitignoreLocal(dir string) []string {
	var patterns []string
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		line = strings.TrimSuffix(line, "/")
		line = strings.TrimPrefix(line, "/")
		if line != "" {
			patterns = append(patterns, line)
		}
	}
	return patterns
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
		return // silently skip
	}
	store, err := history.NewStore(histDir)
	if err != nil {
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
		Method:     ac.source,
		Servers:    servers,
		Files:      fileSet.Count,
		FileList:   fileSet.Files,
		Source:     fileSet.Label,
		Commit:     ac.commit,
		Branch:     ac.branch,
		RemotePath: ac.remoteDir,
		Status:     status,
		Profile:    ac.profile,
	}

	_ = store.Record(entry) // best effort
}

// ---------------------------------------------------------------------------
// Generic helpers
// ---------------------------------------------------------------------------

func mergeDefaults(ac *appConfig, d *config.Defaults) {
	if ac.timeout <= 0 { ac.timeout = d.Timeout }
	if ac.concurrency <= 0 { ac.concurrency = d.Concurrency }
	if !ac.dryRun && d.DryRun { ac.dryRun = true }
	if !ac.noPreview && d.NoPreview { ac.noPreview = true }
	if !ac.noGitignore && !d.IsRespectGitignore() { ac.noGitignore = true }
}

func splitTags(tags string) []string {
	if tags == "" { return nil }
	parts := strings.Split(tags, ",")
	for i := range parts { parts[i] = strings.TrimSpace(parts[i]) }
	return parts
}

func hasAnyTag(serverTags, filterTags []string) bool {
	for _, ft := range filterTags {
		for _, st := range serverTags {
			if st == ft { return true }
		}
	}
	return false
}

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
			if !hasAnyTag(s.Tags, tagFilter) {
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
		if rp == "" { rp = "(default)" }
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
				selected := parseFileListFZF(string(out))
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

	editor := findEditorSrv()
	editCmd := exec.Command(editor, tmpPath)
	editCmd.Stdin = os.Stdin
	editCmd.Stdout = os.Stdout
	editCmd.Stderr = os.Stderr
	if err := editCmd.Run(); err != nil {
		return nil, fmt.Errorf("editor: %w", err)
	}
	data, _ := os.ReadFile(tmpPath)
	selected := parseEditorOutputSrv(string(data))
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

// parseFileListFZF splits fzf multi-select output into lines.
func parseFileListFZF(s string) []string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

// parseEditorOutputSrv parses the file back from editor, skipping comment lines.
// (local copy to avoid circular deps with picker package)
func parseEditorOutputSrv(content string) []string {
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

// findEditorSrv finds an available editor (local copy).
func findEditorSrv() string {
	if e := os.Getenv("VISUAL"); e != "" { return e }
	if e := os.Getenv("EDITOR"); e != "" { return e }
	for _, c := range []string{"vim", "nano", "hx", "micro"} {
		if _, err := exec.LookPath(c); err == nil { return c }
	}
	return "vim"
}

func confirmPrompt(msg string) bool {
	fmt.Fprintf(os.Stderr, "  ? %s [y/N] ", msg)
	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

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
			if r.Files > 0 {
				fmt.Fprintf(os.Stdout, "  %s %-20s  %d files%s  %s\n",
					color.GreenString("✓"), r.Server, r.Files, hooks, r.Elapsed)
			} else {
				fmt.Fprintf(os.Stdout, "  %s %-20s%s  %s\n",
					color.GreenString("✓"), r.Server, hooks, r.Elapsed)
			}
		} else {
			fail++
			errStr := r.Error
			if len(errStr) > 80 { errStr = errStr[:77] + "..." }
			fmt.Fprintf(os.Stdout, "  %s %-20s  %s\n",
				color.RedString("✗"), r.Server, errStr)
		}
	}
	if fail > 0 {
		fmt.Fprintf(os.Stderr, "\n  %d OK · %d FAIL\n", ok, fail)
	} else {
		fmt.Fprintf(os.Stderr, "\n  %d OK\n", ok)
	}
}

func installSkill() error {
	home, err := os.UserHomeDir()
	if err != nil { return fmt.Errorf("home dir: %w", err) }
	skillDir := filepath.Join(home, ".hermes", "skills", "software-development", "deploi")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Skill installed to %s\n", skillPath)
	return nil
}
