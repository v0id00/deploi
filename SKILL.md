---
name: deploi
description: Multi-server file sync and deploy tool — push/pull/sync files across SSH/SFTP/rsync targets with git-aware file selection, hooks, profiles, history, and watch mode.
metadata:
  hermes:
    tags: [deploy, rsync, sftp, ssh, file-transfer, git, golang, hooks, profiles]
---

# deploi — Multi-Server File Sync & Deploy

Multi-server file sync tool written in Go. Configure servers in TOML, select files via git diff/commit/fzf or manually, push/pull/sync to all targets concurrently. Features pre/post deploy hooks, diff preview, auto-gitignore, profiles, history, rollback, watch mode, and interactive server picker.

Located at: `~/Belgeler/deploi/`

## Project Structure

```
deploi/
├── cmd/deploi/main.go           # Entry point
├── internal/
│   ├── app/run.go               # Cobra commands (all subcommands)
│   ├── app/skill.go             # Embedded SKILL.md
│   ├── config/config.go         # TOML config parsing (6 search paths)
│   ├── picker/picker.go         # File selection engine (9 methods + fzf picker)
│   ├── transfer/transfer.go     # Rsync/SCP/SSH + hooks + excludes + relative paths
│   ├── history/history.go       # Deploy history tracking (~/.deploi/history/)
│   └── watcher/watcher.go       # fsnotify file watcher
├── .github/workflows/release.yml  # 7-platform CI on tag push
├── config.toml.example
├── Makefile                      # build, install, build-all, test
└── README.md
```

## Build & Install

```bash
cd ~/Belgeler/deploi
make build          # build/deploi
make install        # go install → $GOPATH/bin/deploi
make build-all      # 7-platform cross-compile
make test           # go test ./...
```

## Config (deploi.toml)

Config search order:
1. `-c PATH` explicit path
2. `./deploi.toml` (current directory)
3. `~/.config/deploi/config.toml` (Linux/macOS — XDG)
4. `%APPDATA%/deploi/config.toml` (Windows)
5. `~/.deploi/config.toml` (home subdirectory)
6. `~/.deploi.toml` (home file — legacy)

```toml
[defaults]
timeout = 300
concurrency = 5
editor = "vim"
remote_path = "/var/www/webticari.net/web"
no_preview = false
respect_gitignore = true
exclude = [".git", "node_modules"]

[profiles.full]
method = "git-diff"

[profiles.assets]
method = "all"
paths = ["public/build/"]

[servers.prod-web-1]
host = "web1.example.com"
port = 22
user = "deploy"
method = "rsync"
tags = ["prod", "web"]

[servers.prod-web-1.hooks]
pre = ["php artisan down"]
post = ["php artisan up"]
```

Webticari servers (8 adet) `~/.config/deploi/config.toml` içinde tanımlı.

## Quick Commands Reference

```bash
# File operations
deploi push -s prod -m git-diff                  # changed files (relative paths)
deploi push -S -t prod -m git-diff               # pick servers via fzf
deploi push -s prod -m git-commit -P             # pick commit via fzf
deploi push -s prod -m fzf-commit                # pick commit + files
deploi push -s staging --profile assets           # named profile
deploi pull -s staging -m all remote/path/
deploi sync -s prod -m git-diff

# Remote commands
deploi run -s prod "systemctl reload nginx"

# Watch mode (fsnotify + debounce)
deploi watch -s staging ./

# History & Rollback
deploi history                    # last 10
deploi history 3                  # deploy #3
deploi rollback                   # undo latest

# Config & Info
deploi servers
deploi servers --json
deploi config validate
deploi config generate -o -
```

## File Selection Methods

| Method | CLI | Description |
|--------|-----|-------------|
| manual | `push file.php` | Explicit paths |
| git-diff | `-m git-diff` | Changed files (staged+unstaged+untracked) |
| git-diff + pick | `-m git-diff -P` | Pick changed files via fzf |
| git-commit | `-m git-commit --commit HASH` | Files in commit |
| git-commit + pick | `-m git-commit -P` | Interactive commit pick via fzf |
| fzf-commit | `-m fzf-commit` | Pick commit then files via fzf |
| git-branch | `-m git-branch --branch NAME` | Branch diff |
| fzf | `-m fzf` | Interactive fzf picker |
| editor | `-m editor` | Editor selection |
| all | `-m all [dir]` | All files recursive |

## Key Flags

| Flag | Description |
|------|-------------|
| `-s, --server` | Glob filter for servers |
| `-t, --tags` | Tag filter (OR) |
| `-S, --pick-server` | Pick servers interactively via fzf |
| `-m, --method` | File selection method |
| `-P, --pick` | Interactive commit pick |
| `--profile` | Named deploy profile |
| `--dry-run` | Preview only |
| `--no-preview` | Skip diff confirmation |
| `--no-gitignore` | Disable .gitignore |
| `--no-staged` | Exclude staged files from git-diff |
| `--json` | JSON output |
| `-q, --quiet` | Suppress output |

## Architecture Notes

- **Relative paths**: files converted to relative from BaseDir before rsync
- **Config search**: 6 locations, platform-aware (Linux/macOS/Windows)
- **Concurrent transfers**: goroutines + semaphores (per-server)
- **Exclude patterns**: config excludes + `.gitignore` auto-detection
- **Pre/Post hooks**: SSH commands via native Go SSH client
- **History**: JSON files in `~/.deploi/history/`
- **Watch**: fsnotify-based with configurable debounce
- **File selection**: calls `git CLI` internally (diff, diff-tree, log)
- **CI/CD**: GitHub Actions — 7 platforms, auto-release on `v*` tag
