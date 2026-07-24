---
name: deploi
description: Multi-server file sync and deploy tool — push/pull/sync files across SSH/SFTP/rsync targets with git-aware file selection.
metadata:
  hermes:
    tags: [deploy, rsync, sftp, ssh, file-transfer, git]
---

# deploi — Multi-Server File Sync & Deploy

Multi-server file sync tool. Configure servers in TOML, select files via git diff/commit/fzf or manually, push/pull/sync to all targets concurrently.

## Quick Start

```bash
# Generate default config
deploi config generate

# Push changed files to production
deploi push -s prod -m git-diff

# Pull files from staging
deploi pull -s staging -m all remote/path/

# List configured servers
deploi servers

# Run commands remotely
deploi run -s prod "systemctl reload nginx"
```

## Config (deploi.toml)

```toml
[defaults]
timeout = 300
concurrency = 5
editor = "vim"
remote_path = "/home/deploy/www/"
server = ""
force = false
confirm_without_filter = true

[servers.production]
host = "web1.example.com"
port = 22
user = "deploy"
method = "rsync"
tags = ["prod", "web"]
remote_path = "/var/www/project/"
rsync_options = "-avz --delete"

[servers.staging]
host = "staging.example.com"
user = "deploy"
tags = ["staging"]
```

## File Selection Methods

| Method | Flag | Description |
|--------|------|-------------|
| **manual** | (path args) | Explicit file/dir paths as arguments |
| **git-diff** | `-m git-diff` | Changed files in working tree (staged+unstaged) |
| **git-commit** | `-m git-commit --commit HASH` | Files in a specific commit |
| **git-branch** | `-m git-branch --branch NAME` | Files changed between branches |
| **fzf** | `-m fzf` | Interactive fzf picker (falls back to editor) |
| **editor** | `-m editor` | Open $EDITOR to select files |
| **all** | `-m all [dir...]` | All files in given directories |

## Commands

### push — upload files to servers

```bash
deploi push -s prod config/app.php       # specific file
deploi push -s prod -m git-diff           # changed files
deploi push -s staging -m all ./public/   # all files in dir
deploi push -s prod -m git-commit --commit abc1234
deploi push -s prod -m fzf               # interactive fzf
deploi push -s prod -m editor            # editor selection
```

### pull — download files from servers

```bash
deploi pull -s staging -m all remote/path/
deploi pull -s prod -m fzf
```

### sync — dry-run comparison

```bash
deploi sync -s prod -m git-diff
```

### run — execute commands on remote servers

```bash
deploi run -s prod "systemctl reload nginx"
deploi run -s staging "df -h" "uptime"
```

## Options

| Flag | Description |
|------|-------------|
| `-s, --server` | Glob/name filter for servers |
| `-t, --tags` | Filter by tags (comma-separated, OR) |
| `-c, --config` | Config file path |
| `--dry-run` | Show what would be done |
| `--json` | JSON output |
| `-q, --quiet` | Suppress progress and banners |
| `--force` | Skip confirmations |
| `--rsync-opts` | Extra rsync options |
