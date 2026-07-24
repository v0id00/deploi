// Package app wires everything together — CLI flags, orchestrator, version.
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/v0id00/deploi/internal/config"
	"github.com/v0id00/deploi/internal/picker"
	"github.com/v0id00/deploi/internal/transfer"
)

// version is set at build time via -ldflags.
var version = "dev"

// Execute creates and runs the root command.
func Execute() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// appConfig holds all runtime configuration.
type appConfig struct {
	configPath  string
	server      string
	tags        string
	timeout     int
	concurrency int
	force       bool
	dryRun      bool
	json        bool
	quiet       bool
	noConfirm   bool
	version     bool

	// File selection
	source    string // "manual", "git-diff", "git-commit", "fzf", "editor", "all"
	paths     []string
	commit    string
	branch    string
	remoteDir string

	// Transfer
	rsyncOpts string
	method    string // override method
}

func newRootCmd() *cobra.Command {
	ac := &appConfig{}

	cmd := &cobra.Command{
		Use:   "deploi",
		Short: "Multi-server file sync and deploy tool",
		Long: `deploi — Multi-server file sync and deploy tool.

Reads a TOML config file with server definitions, then transfers files
across all matching servers concurrently.

File selection methods:
  manual       Explicit file/directory paths
  git-diff     Files changed in working tree (staged + unstaged)
  git-commit   Files in a specific commit
  git-branch   Files changed between branches
  fzf          Interactive fzf picker
  editor       Open $EDITOR to select files
  all          All files in given directories

Examples:
  deploi push -s prod -m git-diff
  deploi push path/to/file.php -s prod-web
  deploi pull -s staging -m all remote/path/
  deploi sync -s prod -m git-commit --commit HEAD
  deploi servers`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(ac, cmd)
		},
	}

	flags := cmd.Flags()

	// Config
	flags.StringVarP(&ac.configPath, "config", "c", "", "Path to config file")

	// Filtering
	flags.StringVarP(&ac.server, "server", "s", "", "Glob/name filter for server names")
	flags.StringVarP(&ac.tags, "tags", "t", "", "Filter by tags (comma-separated, OR)")

	// Execution
	flags.IntVar(&ac.timeout, "timeout", 0, "Transfer timeout in seconds (default: 300)")
	flags.IntVar(&ac.concurrency, "concurrency", 0, "Concurrent server limit (default: 5)")
	flags.BoolVar(&ac.force, "force", false, "Skip confirmation")
	flags.BoolVar(&ac.dryRun, "dry-run", false, "Show what would be transferred without executing")

	// Output
	flags.BoolVar(&ac.json, "json", false, "Output as JSON")
	flags.BoolVarP(&ac.quiet, "quiet", "q", false, "Suppress progress bar and banners")
	flags.BoolVar(&ac.noConfirm, "no-confirm", false, "Skip confirmation prompt")

	// Misc
	flags.BoolVar(&ac.version, "version", false, "Show version")

	// Subcommands
	cmd.AddCommand(newPushCmd())
	cmd.AddCommand(newPullCmd())
	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newServersCmd())
	cmd.AddCommand(newRunCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newCompletionCmd())
	cmd.AddCommand(newSkillCmd())

	return cmd
}

// ---------------------------------------------------------------------------
// deploi push  —  upload files to servers
// ---------------------------------------------------------------------------

func newPushCmd() *cobra.Command {
	ac := &appConfig{}

	cmd := &cobra.Command{
		Use:   "push [files...]",
		Short: "Upload files to remote servers",
		Long: `Upload files to one or more servers.

File selection methods:
  (path args)    Explicit file paths (manual)
  -m git-diff    Files changed in working tree
  -m git-commit  Files in a commit (use --commit)
  -m fzf         Interactive fzf selection
  -m editor      Open editor to select files
  -m all         All files in a directory

Examples:
  deploi push -s prod -m git-diff
  deploi push -s prod-web-1 config/app.php
  deploi push -s staging -m all ./public/
  deploi push -s prod -m git-commit --commit abc1234`,
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

	return cmd
}

// ---------------------------------------------------------------------------
// deploi pull  —  download files from servers
// ---------------------------------------------------------------------------

func newPullCmd() *cobra.Command {
	ac := &appConfig{}

	cmd := &cobra.Command{
		Use:   "pull [remote-paths...]",
		Short: "Download files from remote servers",
		Long: `Download files from remote servers to local machine.

Examples:
  deploi pull -s staging -m all remote/path/
  deploi pull -s prod-web-1 var/log/app.log
  deploi pull -s prod -m fzf`,
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

	return cmd
}

// ---------------------------------------------------------------------------
// deploi sync  —  compare/check files between servers
// ---------------------------------------------------------------------------

func newSyncCmd() *cobra.Command {
	ac := &appConfig{}

	cmd := &cobra.Command{
		Use:   "sync [files...]",
		Short: "Synchronize files with remote servers (rsync --dry-run)",
		Long: `Compare local files with remote servers using rsync dry-run mode.

Shows what would change before actually transferring.

Examples:
  deploi sync -s prod -m git-diff
  deploi sync -s all`,
		Args: cobra.MaximumNArgs(100),
		RunE: func(cmd *cobra.Command, args []string) error {
			ac.paths = args
			ac.dryRun = true // sync is always dry-run
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
// deploi run  —  execute commands on remote servers
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
// deploi servers  —  list configured servers
// ---------------------------------------------------------------------------

func newServersCmd() *cobra.Command {
	ac := &appConfig{}

	cmd := &cobra.Command{
		Use:   "servers",
		Short: "List configured servers",
		Long: `List all servers from the config file.

Examples:
  deploi servers
  deploi servers -s prod
  deploi servers --json`,
		Args: cobra.ExactArgs(0),
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
// deploi config  —  manage config
// ---------------------------------------------------------------------------

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration (generate, validate)",
	}

	var cfgPath string

	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a default config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			oPath, _ := cmd.Flags().GetString("output")
			return runConfigGenerate(oPath)
		},
	}
	generateCmd.Flags().StringP("output", "o", "", "Output path (default: platform config dir, '-' for stdout)")

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config file syntax and structure",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigValidate(cfgPath)
		},
	}
	validateCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "Path to config file")

	cmd.AddCommand(generateCmd)
	cmd.AddCommand(validateCmd)
	return cmd
}

// ---------------------------------------------------------------------------
// deploi completion  —  shell completions
// ---------------------------------------------------------------------------

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Args:  cobra.ExactArgs(1),
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
				return fmt.Errorf("unknown shell: %s (supported: bash, zsh, fish, powershell)", args[0])
			}
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// deploi skill  —  manage AI skill
// ---------------------------------------------------------------------------

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage AI agent skill",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Install deploi skill for Hermes AI agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return installSkill()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the embedded skill content",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print(skillContent)
			return nil
		},
	})

	return cmd
}

// ---------------------------------------------------------------------------
// Flag helpers
// ---------------------------------------------------------------------------

func addFileSelectionFlags(flags *pflag.FlagSet, ac *appConfig) {
	flags.StringVarP(&ac.source, "method", "m", "manual",
		`File selection method: manual, git-diff, git-commit, git-branch, fzf, editor, all`)
	flags.StringVar(&ac.commit, "commit", "", "Git commit hash (for git-commit method)")
	flags.StringVar(&ac.branch, "branch", "", "Git branch name (for git-branch method)")
	flags.StringVar(&ac.remoteDir, "remote-dir", "", "Remote directory override")
	flags.StringVar(&ac.rsyncOpts, "rsync-opts", "", "Additional rsync options")
}

func addServerFlags(flags *pflag.FlagSet, ac *appConfig) {
	flags.StringVarP(&ac.server, "server", "s", "", "Glob/name filter for server names")
	flags.StringVarP(&ac.tags, "tags", "t", "", "Filter by tags (comma-separated, OR)")
}

func addExecFlags(flags *pflag.FlagSet, ac *appConfig) {
	flags.IntVar(&ac.timeout, "timeout", 0, "Transfer timeout in seconds")
	flags.IntVar(&ac.concurrency, "concurrency", 0, "Concurrent server limit")
	flags.BoolVar(&ac.force, "force", false, "Skip confirmation")
	flags.BoolVar(&ac.dryRun, "dry-run", false, "Show what would be transferred without executing")
	flags.BoolVar(&ac.json, "json", false, "Output as JSON")
	flags.BoolVarP(&ac.quiet, "quiet", "q", false, "Suppress progress bar and banners")
	flags.BoolVar(&ac.noConfirm, "no-confirm", false, "Skip confirmation prompts")
}

// ---------------------------------------------------------------------------
// run / runPushPull / runServers / runRemote
// ---------------------------------------------------------------------------

func run(ac *appConfig, cmd *cobra.Command) error {
	if ac.version {
		fmt.Fprintf(os.Stderr, "deploi %s\n", version)
		return nil
	}
	return cmd.Help()
}

func runPushPull(ac *appConfig, op transfer.Operation) error {
	// Load config
	cfgPath, err := config.FindConfigPath(ac.configPath)
	if err != nil {
		return err
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// Merge CLI overrides
	mergeDefaults(ac, &cfg.Defaults)

	// Resolve file selection
	fileSet, err := resolveFiles(ac, cfg.Defaults.Editor)
	if err != nil {
		return fmt.Errorf("file selection: %w", err)
	}

	// Confirm
	if !ac.noConfirm && !ac.dryRun && len(cfg.Servers) > 0 && ac.server == "" && ac.tags == "" {
		if cfg.Defaults.ConfirmWithoutFilter {
			if !confirmPrompt(fmt.Sprintf("Transfer %d files to ALL %d servers?", fileSet.Count, len(cfg.Servers))) {
				fmt.Fprintln(os.Stderr, "  Cancelled.")
				return nil
			}
		}
	}

	// Convert to slice
	servers := make([]config.Server, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, s)
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
	}

	// Use remoteDir from config if not overridden
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
		fmt.Fprintln(os.Stderr)
	}

	// Run
	results := transfer.Run(servers, tc)

	// Output
	if ac.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	printResults(results)
	return nil
}

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
		Name       string `json:"name"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		User       string `json:"user"`
		Method     string `json:"method"`
		Tags       []string `json:"tags"`
		RemotePath string `json:"remote_path"`
		Status     string `json:"status"`
	}

	var infos []serverInfo
	for _, srv := range cfg.Servers {
		// Apply filters
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

		infos = append(infos, serverInfo{
			Name:       srv.Name,
			Host:       srv.Host,
			Port:       srv.Port,
			User:       srv.User,
			Method:     srv.Method,
			Tags:       srv.Tags,
			RemotePath: srv.RemotePath,
			Status:     "✓",
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
		rp := info.RemotePath
		if rp == "" {
			rp = "(default)"
		}
		fmt.Fprintf(os.Stdout, "  %s  %s@%s:%d  %s%s  %s  %s\n",
			color.GreenString("✓"), info.User, info.Host, info.Port,
			info.Method, tagStr, info.Status, rp)
	}

	if len(infos) == 0 {
		fmt.Fprintln(os.Stderr, "  No servers match the given filter.")
	}

	return nil
}

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

	tc := transfer.RunConfig{
		ServerRegex: ac.server,
		Tags:        splitTags(ac.tags),
		DryRun:      ac.dryRun,
		Concurrency: ac.concurrency,
		Timeout:     ac.timeout,
		ShowBar:     !ac.quiet && cfg.Defaults.ShowBar,
		Quiet:       ac.quiet,
	}

	if !ac.quiet {
		fmt.Fprintf(os.Stderr, "  deploi run  ·  %d commands  ·  %s\n",
			len(commands), tc.ServerRegex)
		if tc.DryRun {
			fmt.Fprintf(os.Stderr, "  ⚠ Dry-run mode\n")
		}
		fmt.Fprintln(os.Stderr)
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
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

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
		fmt.Fprintf(os.Stderr, "  ⚠ No servers defined in config\n")
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ %d servers defined\n", len(cfg.Servers))
		for name, srv := range cfg.Servers {
			if srv.Host == "" || srv.User == "" {
				fmt.Fprintf(os.Stderr, "  ⚠ %s: missing host or user\n", name)
			} else {
				fmt.Fprintf(os.Stderr, "  ✓ %s → %s@%s:%d (%s)\n", name, srv.User, srv.Host, srv.Port, srv.Method)
			}
		}
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
		return picker.Pick(picker.PickConfig{
			Source:  sourceType,
			Paths:   ac.paths,
			BaseDir: cwd(),
		})

	case picker.SourceGitDiff:
		return picker.Pick(picker.PickConfig{
			Source: sourceType,
			GitDir: cwd(),
		})

	case picker.SourceGitCommit:
		if ac.commit == "" {
			return nil, fmt.Errorf("--commit is required for git-commit method. Use --commit <hash>")
		}
		return picker.Pick(picker.PickConfig{
			Source: sourceType,
			GitDir: cwd(),
			Commit: ac.commit,
		})

	case picker.SourceGitBranch:
		if ac.branch == "" {
			return nil, fmt.Errorf("--branch is required for git-branch method")
		}
		return picker.Pick(picker.PickConfig{
			Source: sourceType,
			GitDir: cwd(),
			Branch: ac.branch,
		})

	case picker.SourceFZF:
		return picker.Pick(picker.PickConfig{
			Source: sourceType,
			Paths:  ac.paths,
			GitDir: cwd(),
			Editor: editor,
		})

	case picker.SourceEditor:
		return picker.Pick(picker.PickConfig{
			Source: sourceType,
			Paths:  ac.paths,
			Editor: editor,
		})

	case picker.SourceAll:
		targets := ac.paths
		if len(targets) == 0 {
			targets = []string{"."}
		}
		return picker.Pick(picker.PickConfig{
			Source:  sourceType,
			Paths:   targets,
			BaseDir: cwd(),
		})

	default:
		return nil, fmt.Errorf("unknown source: %s", ac.source)
	}
}

func parseSourceType(s string) picker.SourceType {
	switch strings.ToLower(s) {
	case "manual":
		return picker.SourceManual
	case "git-diff", "gitdiff", "git_diff", "changed":
		return picker.SourceGitDiff
	case "git-commit", "gitcommit", "git_commit", "commit":
		return picker.SourceGitCommit
	case "git-branch", "gitbranch", "git_branch", "branch":
		return picker.SourceGitBranch
	case "fzf":
		return picker.SourceFZF
	case "editor", "edit", "select":
		return picker.SourceEditor
	case "all", "dir", "directory", "recursive":
		return picker.SourceAll
	default:
		return picker.SourceManual
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mergeDefaults(ac *appConfig, d *config.Defaults) {
	if ac.timeout <= 0 {
		ac.timeout = d.Timeout
	}
	if ac.concurrency <= 0 {
		ac.concurrency = d.Concurrency
	}
	if ac.dryRun && !d.DryRun {
		// user explicitly set it - keep
	}
	if !ac.dryRun && d.DryRun {
		ac.dryRun = true
	}
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

func cwd() string {
	dir, _ := os.Getwd()
	return dir
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
			if r.Files > 0 {
				fmt.Fprintf(os.Stdout, "  %s %-20s  %d files  %s\n",
					color.GreenString("✓"), r.Server, r.Files, r.Elapsed)
			} else {
				fmt.Fprintf(os.Stdout, "  %s %-20s  %s\n",
					color.GreenString("✓"), r.Server, r.Elapsed)
			}
		} else {
			fail++
			errStr := r.Error
			if len(errStr) > 80 {
				errStr = errStr[:77] + "..."
			}
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

// installSkill writes the embedded SKILL.md to the Hermes skills directory.
func installSkill() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}

	skillDir := filepath.Join(home, ".hermes", "skills", "software-development", "deploi")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ Skill installed to %s\n", skillPath)
	return nil
}
