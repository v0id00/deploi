# Changelog

All notable changes to this project are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions follow [Semantic Versioning](https://semver.org/).

## [v0.1.8] - 2026-07-30

### Fixed

- `-s/--server` filter now works standalone, without requiring `-S/--pick-server`.

## [v0.1.7] - 2026-07-30

### Fixed

- Improved error messages across commands.
- Fzf terminal detection (works when no TTY).
- Local rsync support (SSH-free testing).

## [v0.1.6] - 2026-07-28

### Fixed

- rsync output parser no longer counts directory entries as files.

## [v0.1.5] - 2026-07-28

### Added

- Segmented CLI output with section headers (File Selection, Changes, Deploy).
- Mock-based test suite across picker/transfer/watcher (coverage up).

### Fixed

- `--select editor` with git filters now opens the editor (not fzf).
- SSH password prompts during rsync now work (stdin passthrough).
- Spinner disabled for single server to avoid SSH prompt conflicts.
- Param interaction and default-behavior bugs in the selection system.

### Changed

- Simplified param system — `--select/--filter` replaced with `-m/--method`.

## [v0.1.4] - 2026-07-27

### Fixed

- CI build script: single-line build, proper `if` statements for EXT,
  `fail-fast: false` so one failed matrix entry doesn't cancel all.

## [v0.1.3] - 2026-07-27

### Added

- `deploi check` — test SSH connectivity with latency.
- `deploi config init` — interactive project config generator (server
  picker, hooks, profiles).
- Full rollback — rsync `--backup-dir` on push, restore with `deploi rollback`.
- `--verbose`/`-v` flag showing the rsync command and full output.
- Clean rsync output summary (📦 size ⚡ speed 📊 total 🚀 speedup).
- `briandowns/spinner` for cleaner live progress.

### Changed

- File selection split into `--select` (how) + `--filter` (what); legacy
  `-m` kept for compatibility, later removed entirely.

### Fixed

- rsync destination must not include port (port goes in `-e ssh -p`).
- Rename detection in `git status --porcelain` (path after `->`).
- Staged-file detection now uses `git status --porcelain` reliably.
- `-S` fzf server picker now runs before all-servers confirmation.
- Error messages shown on invalid parameters.

## [v0.1.2] - 2026-07-24

### Fixed

- `-S` fzf server picker correctly uses a temp file for candidates and
  checks for a real terminal.
- Removed non-standard `diff-filter` flags that broke on some git versions;
  `diff-filter=A` fallback for staged new files.

## [v0.1.1] - 2026-07-24

### Fixed

- Release checksum step now uses an absolute path for dist.

## [v0.1.0] - 2026-07-24

### Added

- Initial release:
  - `push`/`pull`/`sync` across SSH/SFTP/rsync targets.
  - 7 file selection methods: git-diff, git-commit, git-branch, fzf,
    editor, all, path.
  - Interactive fzf server picker (`-S`), hooks (pre/post), exclude
    patterns, deploy profiles, history, watch mode, rollback.
  - GitHub Actions CI.

[Unreleased]: https://github.com/v0id00/deploi/compare/v0.1.8...HEAD
[v0.1.8]: https://github.com/v0id00/deploi/compare/v0.1.7...v0.1.8
[v0.1.7]: https://github.com/v0id00/deploi/compare/v0.1.6...v0.1.7
[v0.1.6]: https://github.com/v0id00/deploi/compare/v0.1.5...v0.1.6
[v0.1.5]: https://github.com/v0id00/deploi/compare/v0.1.4...v0.1.5
[v0.1.4]: https://github.com/v0id00/deploi/compare/v0.1.3...v0.1.4
[v0.1.3]: https://github.com/v0id00/deploi/compare/v0.1.2...v0.1.3
[v0.1.2]: https://github.com/v0id00/deploi/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/v0id00/deploi/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/v0id00/deploi/releases/tag/v0.1.0
