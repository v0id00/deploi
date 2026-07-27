# deploi

Multi-server file sync and deploy tool — **push**, **pull**, **sync** files across SSH/SFTP/rsync targets with git-aware file selection.

Like [propq](https://github.com/v0id00/propq) (multi-server SQL executor) but for files.

## Features

- **9 file selection methods**: manual, git-diff, git-commit (with `-P` for interactive pick), git-branch, fzf, fzf-commit, editor, all
- **Interactive server picker**: select target servers via fzf with `-S`/`--pick-server`
- **Pre/Post hooks**: run SSH commands before/after deploy on each server
- **Diff preview**: shows changes and asks for confirmation before transferring (default)
- **Exclude patterns**: auto-respects `.gitignore`, configurable in `config.toml`
- **Deploy profiles**: predefined file selection sets in config
- **Deploy history**: every deploy logged, view with `deploi history`
- **Rollback**: restore previous deploy state with `deploi rollback`
- **Watch mode**: auto-deploy on file changes (`deploi watch`)
- **Concurrent**: send files to many servers at once (goroutine + semaphore)
- **Interactive**: fzf picker or $EDITOR for on-the-fly file selection
- **Config-driven**: TOML config with server definitions, tags for filtering

## Quick Start

```bash
# Generate default config
deploi config generate

# List configured servers
deploi servers

# Push changed files (git-diff) to production servers
deploi push -s prod -m git-diff

# Push specific files
deploi push -s prod config/app.php

# Push files from a commit (interactive commit pick)
deploi push -s prod -m git-commit -P

# Push with fzf interactive file selection
deploi push -s prod -m fzf

# Push using a profile
deploi push -s staging --profile assets

# Pick servers interactively via fzf
deploi push -S -t prod -m git-diff

# Pull files from a server
deploi pull -s staging -m all remote/path/

# Sync (dry-run) to see what would change
deploi sync -s prod -m git-diff

# Run SSH commands
deploi run -s prod "systemctl reload nginx"

# Watch for changes and auto-deploy
deploi watch -s staging ./

# View deploy history
deploi history

# Create a project config interactively
deploi config init

# Test SSH connectivity
deploi check -v
```

## Config

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
remote_path = "/home/deploy/www/"
no_preview = false
exclude = [".git", "node_modules"]
respect_gitignore = true

# Predefined deploy profiles
[profiles.full]
method = "git-diff"
remote_path = "/var/www/project/"

[profiles.assets]
method = "all"
paths = ["public/build/", "public/assets/"]

[servers.prod-web-1]
host = "web1.example.com"
port = 22
user = "deploy"
method = "rsync"
tags = ["prod", "web"]
remote_path = "/var/www/project/"
rsync_options = "-avz --delete"

# Pre/post deploy hooks
[servers.prod-web-1.hooks]
pre = ["php artisan down", "rm -rf var/cache/*"]
post = ["php artisan migrate --force", "php artisan up"]

[servers.staging]
host = "staging.example.com"
user = "deploy"
tags = ["staging"]
```

See `config.toml.example` for a full reference.

## File Selection

Two independent parameters control file selection:

| Parameter | Role | Values |
|-----------|------|--------|
| `--select` | **How** to pick files | `manual`, `fzf`, `editor`, `all` |
| `--filter` | **What** files to show | `git-diff`, `git-commit`, `git-branch`, `path` |

| Select + Filter | Command | Description |
|-----------------|---------|-------------|
| `select fzf + filter git-diff` | `deploi push --select fzf --filter git-diff` | Pick changed files via fzf |
| `select fzf + filter path` | `deploi push --select fzf --filter path .` | Pick all files via fzf (from current dir) |
| `select fzf + filter git-commit` | `deploi push --select fzf --filter git-commit` | Pick commit then files via fzf |
| `select manual + filter git-diff` | `deploi push --filter git-diff` | Auto-select changed files |
| `select manual + filter git-commit` | `deploi push --filter git-commit --commit HASH` | Files in a commit |
| `select manual + filter git-branch` | `deploi push --filter git-branch --branch NAME` | Branch diff |
| `select editor + filter path` | `deploi push --select editor` | Editor-based selection |
| `select all + filter path` | `deploi push --select all --filter path ./dir/` | All files in a directory |

### Legacy `-m` (still works)

| `-m` value | Equivalent `--select` + `--filter` |
|------------|-----------------------------------|
| `-m manual file.php` | `--select manual --filter path file.php` |
| `-m git-diff` | `--select manual --filter git-diff` |
| `-m git-diff -P` | `--select fzf --filter git-diff` |
| `-m git-commit --commit HASH` | `--select manual --filter git-commit --commit HASH` |
| `-m git-commit -P` | `--select fzf --filter git-commit` |
| `-m git-branch --branch NAME` | `--select manual --filter git-branch --branch NAME` |
| `-m fzf` | `--select fzf --filter path` |
| `-m fzf-commit` | `--select fzf --filter git-commit` |
| `-m editor` | `--select editor --filter path` |
| `-m all ./dir/` | `--select all --filter path ./dir/` |

Both styles work and can be mixed. The new `--select`/`--filter` flags are preferred.

## Commands

| Command | Description |
|---------|-------------|
| `push` | Upload files to remote servers (with diff preview) |
| `pull` | Download files from remote servers |
| `sync` | Dry-run comparison (what would change) |
| `run` | Execute SSH commands on remote servers |
| `watch` | Watch files and auto-deploy on changes |
| `history [id]` | Show deploy history |
| `rollback [id]` | Rollback to a previous deploy |
| `servers` | List configured servers |
| `check` | Test SSH connectivity with latency |
| `config generate` | Create a default config file |
| `config validate` | Validate config syntax |
| `config init` | Create a project-level deploi.toml interactively |

## Flags

| Flag | Description |
|------|-------------|
| `-s, --server` | Glob/name filter for servers |
| `-t, --tags` | Filter by tags (comma-separated, OR) |
| `-S, --pick-server` | Pick servers interactively via fzf (also works with -t) |
| `-c, --config` | Config file path |
| `-m, --method` | File selection method (legacy: manual, git-diff, git-commit, git-branch, fzf, fzf-commit, editor, all) |
| `--select` | Selection mode (how): manual, fzf, editor, all |
| `--filter` | Filter mode (what): git-diff, git-commit, git-branch, path |
| `-P, --pick` | Interactive commit pick via fzf |
| `--profile` | Use a named profile from config |
| `--dry-run` | Preview changes without transferring |
| `--no-preview` | Skip diff preview confirmation |
| `--no-gitignore` | Disable .gitignore auto-detection |
| `--no-staged` | Exclude staged files from git-diff |
| `-v, --verbose` | Show detailed rsync output and command |
| `--json` | JSON output |
| `-q, --quiet` | Suppress progress and banners |
| `--force` | Skip confirmations |
| `--rsync-opts` | Extra rsync options |

## Install

```bash
# From source
cd ~/Belgeler/deploi
make install

# Build only
make build    # ./build/deploi

# Cross-platform build (all 7 platforms)
make build-all
```

## GitHub Actions

On tag push (`v*`), CI builds for 7 platforms and creates a release:
`linux-amd64`, `linux-x86`, `linux-arm64`, `macos-amd64`, `macos-arm64`, `windows-amd64.exe`, `windows-x86.exe`

See `.github/workflows/release.yml`.

## AI Agent Integration

```bash
deploi skill install   # Install Hermes skill
deploi skill show      # View embedded skill
```
