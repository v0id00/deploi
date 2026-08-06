# Contributing to deploi

Thanks for taking the time to contribute! 🚀

## Table of Contents

- [Development Setup](#development-setup)
- [Project Layout](#project-layout)
- [Making Changes](#making-changes)
- [Commit Convention](#commit-convention)
- [Opening a Pull Request](#opening-a-pull-request)
- [Code Style](#code-style)

## Development Setup

Requirements:

- **Go 1.25+** (see `go.mod`)
- **golangci-lint** (for `make lint`)
- **fzf** (optional, used by interactive pickers)

```bash
# Clone and build
git clone git@github.com:v0id00/deploi.git
cd deploi

# Build the binary
make build          # → ./build/deploi

# Run the test suite
make test

# Run the linter
make lint

# Tidy dependencies
make tidy
```

A sample config lives in `config.toml.example`. Copy it to `deploi.toml`
and adjust server definitions to try the tool locally:

```bash
cp config.toml.example deploi.toml
./build/deploi servers
```

## Project Layout

```
cmd/deploi/          Entry point (CLI wiring)
internal/app/        Command implementations, version injection
internal/config/     TOML config loading & validation
internal/transfer/   Push/pull/sync + rsync & SFTP transfer logic
internal/picker/     Interactive selection (fzf, editor)
internal/history/    Deploy history & rollback
internal/watcher/    Watch mode
```

Keep related logic in the package that owns it — don't grow `internal/app`
into a dumping ground.

## Making Changes

1. **Fork** the repository and create a branch from `main`:

   ```bash
   git checkout -b fix/my-descriptive-branch-name
   ```

2. Make your change — small, focused commits (see convention below).

3. Run the checks before pushing:

   ```bash
   make test
   make lint
   go build ./...
   ```

4. Push and open a pull request against `main`.

## Commit Convention

This repo follows **Conventional Commits**. Commit messages must be
`type(scope): description` where `type` is one of:

| Type     | When to use                          |
|----------|--------------------------------------|
| `feat`   | New feature or user-visible behavior |
| `fix`    | Bug fix                              |
| `docs`   | Documentation only                   |
| `refactor` | Code change with no behavior change |
| `test`   | Adding/fixing tests                  |
| `chore`  | Tooling, deps, meta files            |
| `perf`   | Performance improvement              |

Examples:

```text
fix: apply -s/--server filter standalone without needing -S/--pick-server
feat(history): add --json output to history command
chore: add MIT license
```

The `scope` (e.g. `(history)`) is optional — use it when the change is
specific to one command or package. The `fix:` prefix shown above also
matters for changelog generation, so keep the first line under 72 chars
and describe **what** changed, not why — that goes in the body.

## Opening a Pull Request

- Target the **`main`** branch.
- Use the PR template — fill in every section.
- Link any related issue with `Closes #123`.
- Keep PRs small and focused. A PR that mixes refactors with feature work
  is much harder to review; split it if needed.
- If your change alters CLI behavior, update the README and `--help` text
  in the same PR.

## Code Style

- `gofmt`/`goimports` formatting (enforced by `golangci-lint`).
- Errors are returned, never swallowed — log with context where useful.
- No external dependencies without a strong reason; this is a small, lean
  CLI. Discuss new deps in the issue first.
- CLI quality matters here: dual human/`--json` output, `--dry-run` where
  destructive, progress feedback, and actionable error messages are
  expected, not optional.

## Questions?

Open a [discussion](https://github.com/v0id00/deploi/discussions) or an
issue — we're happy to help.
