# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Test Commands

```bash
make build          # Build binary to ./confab
make test           # Run all tests
go test ./...       # Alternative: run all tests
go test ./pkg/daemon/...  # Run tests for a specific package
go test -run TestName ./pkg/daemon/...  # Run a single test
```

## Architecture Overview

Confab is a CLI tool that captures Claude Code session transcripts and uploads them to a backend. It operates in two modes:

### Sync Mode (Primary)
- **Daemon-based incremental sync**: When a Claude Code session starts, the `SessionStart` hook spawns a background daemon (`confab sync start`)
- The daemon (`pkg/daemon/`) monitors the transcript file and uploads chunks periodically (30s intervals with jitter)
- On session end, the `SessionEnd` hook signals the daemon to do a final sync and shut down
- The daemon is resilient to backend unavailability and will retry on each sync interval

### Manual Mode
- `confab save <session-id>`: Upload a specific session by ID

## Key Packages

- **cmd/**: Cobra command handlers (root.go registers all subcommands)
- **pkg/daemon/**: Background sync daemon with state management and parent process monitoring
- **pkg/sync/**: Sync engine with client, tracker, and file management (handles incremental uploads)
- **pkg/discovery/**: Session discovery - scans `~/.claude/projects/` for transcript files
- **pkg/redactor/**: JSON-aware redaction of sensitive data before upload
- **pkg/config/**: Configuration and Claude Code hook management (reads/writes `~/.claude/settings.json`)
- **pkg/http/**: HTTP client with zstd compression, auth, and retry logic

## Data Flow

1. Claude Code writes transcripts to `~/.claude/projects/<path>/<session-id>.jsonl`
2. Daemon watches transcript file, reads new lines, applies redaction, uploads as chunks
3. Backend tracks `last_synced_line` per file; daemon syncs only new content
4. Agent sidechain files (`agent-*.jsonl`) are synced alongside the main transcript

## Hook System

Confab installs hooks in `~/.claude/settings.json`:
- `SessionStart`: Runs `confab sync start` to spawn daemon
- `SessionEnd`: Runs `confab sync stop` to signal graceful shutdown

The daemon also monitors its parent PID and shuts down if Claude Code exits unexpectedly.

## Releasing

Tag and push — GoReleaser handles the rest. See [RELEASING.md](RELEASING.md) for details.

```bash
git tag v0.X.Y
git push origin v0.X.Y
```

## Testing Notes

- Integration tests in `pkg/daemon/integration_test.go` test the full sync lifecycle
- Use `CONFAB_CLAUDE_DIR` env var to override Claude directory for testing

## Development Practices

Priorities in order: **simplicity**, **correctness**, **efficiency**.

### Quality Standards

This is software that runs on user machines. Users trust us with their local environment. This demands high quality:

- **No "negligible" race conditions**: A 100ms race window is not acceptable. Race conditions erode trust and are difficult to debug when they manifest in the field.
- **No "unlikely" bugs**: If a code path can fail, assume it will. Users encounter edge cases we don't anticipate.
- **Updates are costly**: Patches require users to update. Getting it right the first time is far better than shipping fixes. Think through failure modes carefully before implementing.
- **Correctness over cleverness**: A simple, obviously correct solution beats an elegant but subtle one.

### Code Hygiene

- **DRY**: Extract shared logic into reusable functions. Check for existing utilities before adding new code.
- **Follow established patterns**: When adding a new feature, look for comparable existing features and follow their structure. For example, when adding a new hook type, check how existing hooks handle: installation/uninstallation, status checking, setup integration, and command registration. Consider whether each pattern must be propagated to maintain consistency.
- **TDD**: Write tests first. Run tests frequently during development.
- **High test coverage**: Unit tests for all packages, integration tests for cross-package workflows, end-to-end tests for CLI commands.
- **Post-change review**: After completing a batch of changes, always perform a detailed review. Run static analysis before committing:
  ```bash
  staticcheck ./...      # Static analysis
  deadcode -test ./...   # Find unused code (including test files)
  ```
- **Clean migrations**: When moving or refactoring code, complete the migration fully. Do not leave duplicate code with deprecation comments "for backwards compatibility." Update all callers (including tests) to use the new location immediately. Stale duplicates cause maintenance burden and inevitably diverge.
