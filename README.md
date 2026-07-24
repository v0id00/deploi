# deploi

Multi-server file sync and deploy tool — **push**, **pull**, **sync** files across SSH/SFTP/rsync targets with git-aware file selection.

Like [propq](https://github.com/v0id00/propq) (multi-server SQL executor) but for files.

## Features

- **Multiple file selection methods**: git-diff, git-commit, git-branch, fzf, editor, manual, all
- **Multiple servers**: send files to many servers at once (configurable concurrency)
- **Multiple methods**: rsync (default), SCP, or custom SSH commands
- **Git integration**: pick files from working tree changes, commits, or branch diffs
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

# Push files from a commit
deploi push -s prod -m git-commit --commit abc1234

# Push with fzf interactive selection
deploi push -s prod -m fzf

# Pull files from a server
deploi pull -s staging -m all remote/path/

# Sync (dry-run) to see what would change
deploi sync -s prod -m git-diff

# Run SSH commands
deploi run -s prod "systemctl reload nginx"
```

## Config

```toml
[defaults]
timeout = 300
concurrency = 5
editor = "vim"
remote_path = "/home/deploy/www/"

[servers.prod-web-1]
host = "web1.example.com"
port = 22
user = "deploy"
method = "rsync"         # rsync, sftp, ssh
tags = ["prod", "web"]
remote_path = "/var/www/project/"
rsync_options = "-avz --delete"
```

See `config.toml.example` for a full reference.

## File Selection Methods

| Method | Command | Description |
|--------|---------|-------------|
| manual | `deploi push file.php` | Explicit file paths |
| git-diff | `deploi push -m git-diff` | Changed files (staged+unstaged) |
| git-commit | `deploi push -m git-commit --commit HASH` | Files in a commit |
| git-branch | `deploi push -m git-branch --branch NAME` | Branch diff |
| fzf | `deploi push -m fzf` | Interactive fzf picker |
| editor | `deploi push -m editor` | Editor-based selection |
| all | `deploi push -m all ./dir/` | All files in a directory |

## Commands

| Command | Description |
|---------|-------------|
| `push` | Upload files to remote servers |
| `pull` | Download files from remote servers |
| `sync` | Dry-run comparison (what would change) |
| `run` | Execute SSH commands on remote servers |
| `servers` | List configured servers |
| `config generate` | Create a default config file |
| `config validate` | Validate config syntax |

## Install

```bash
# From source
git clone https://github.com/v0id00/deploi
cd deploi
make install

# Or build from any Go environment
go install github.com/v0id00/deploi/cmd/deploi@latest
```

## AI Agent Integration

```bash
deploi skill install   # Install Hermes skill
deploi skill show      # View embedded skill
```
