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

### Retrieval
- `confab session get-summary [--max-chars N] <id>`: Fetch a condensed session transcript from the backend API. Outputs pretty-printed JSON (metadata + transcript). Useful for piping to local AI tools for retrospection.
- `confab session download [--output-dir <dir>] <id>`: Download raw JSONL transcript files. By default streams main transcript to stdout; with `--output-dir`, downloads all files (transcript + agents) to a directory.
- `confab session list-files <id>`: List raw transcript files for a session with metadata (name, type, lines, last updated).

## Key Packages

- **cmd/**: Cobra command handlers (root.go registers all subcommands)
- **pkg/daemon/**: Background sync daemon with state management and parent process monitoring
- **pkg/sync/**: Sync engine with client, tracker, and file management (handles incremental uploads)
- **pkg/discovery/**: Session discovery - scans `~/.claude/projects/` for transcript files
- **pkg/redactor/**: JSON-aware redaction of sensitive data before upload
- **pkg/config/**: Configuration (Confab + Claude `settings.json` plumbing) and skill management (`~/.claude/skills/`)
- **pkg/hookconfig/**: Per-provider hook install/uninstall — edits Claude `~/.claude/settings.json` and Codex `~/.codex/config.toml`. `pkg/provider`'s `InstallHooks` / `UninstallHooks` delegate here.
- **pkg/http/**: HTTP client with zstd compression, auth, and retry logic
- **pkg/provider/**: `Provider` interface + Claude Code / Codex implementations. `codex_state.go` reads Codex's local SQLite DB to walk subagent rollouts up to their root. All `cmd/` dispatch routes through this interface.
- **pkg/codextest/**: Reusable Codex SQLite + sessions-tree fixture builder used by tests in `pkg/provider`, `pkg/sync`, `pkg/daemon`, and `cmd`.

## Backend

The backend API lives in the sibling repo `../confab-web`. When implementing CLI commands that call backend endpoints, verify the actual API contract by reading handler code there — don't rely solely on ticket specs or documentation.

## Data Flow

1. Claude Code writes transcripts to `~/.claude/projects/<path>/<session-id>.jsonl`
2. Daemon watches transcript file, reads new lines, applies redaction, uploads as chunks
3. Backend tracks `last_synced_line` per file; daemon syncs only new content
4. Agent sidechain files (`agent-*.jsonl`) are synced alongside the main transcript

### Codex provider differences

- Codex rollouts live at `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-...<uuid>.jsonl`. A "session" is a user-initiated thread; subagents spawn their own rollouts and are tracked in Codex's local SQLite (`~/.codex/state_*.sqlite`, `threads` + `thread_spawn_edges`).
- Codex fires `SessionStart` for every spawned subagent. The hook handler in `cmd/hook_sessionstart.go` calls `provider.Codex{}.WalkUpToRoot` to resolve the firing UUID to its top-most root before spawning a daemon — so only one daemon runs per root tree, and subagent SessionStart events for already-tracked trees are no-ops.
- The sync engine queries `tracker.DiscoverCodexDescendants` once per `SyncAll` cycle (a recursive SQLite walk under the root UUID). New subagent rollouts become `file_type=agent` sidechain files under the root's backend session — the same primitive Claude uses for its subagent files.
- The first chunk of every Codex rollout (root or descendant) carries `chunk.metadata.codex_rollout` with the rollout's identity (thread UUID, parent UUID, rollout path, cwd, model, agent metadata). The backend upserts this into `codex_rollouts` keyed by thread UUID. Retries are safe: the metadata rides along again because `FirstLine == 1` is preserved across retries.
- Daemon shutdown for Codex uses parent-process liveness, **not** a `Stop` hook. Codex fires `Stop` at every agent/turn boundary, so a Stop-driven shutdown would prematurely kill the root daemon. `cmd/spawn.go` resolves the Codex parent PID via `Codex.FindParentPID()` and stores it on the daemon; the daemon's main loop exits when that PID dies (same mechanism as Claude Code). `Codex.InstallHooks` only installs `[[hooks.SessionStart]]`, and `confab hook session-end --provider codex` returns an explicit error.

## Hook System

Confab installs hooks in `~/.claude/settings.json`:
- `SessionStart`: Runs `confab sync start` to spawn daemon
- `SessionEnd`: Runs `confab sync stop` to signal graceful shutdown

The daemon also monitors its parent PID and shuts down if Claude Code exits unexpectedly.

## Skills

Confab installs Claude Code skills in `~/.claude/skills/`:
- `/til`: Capture TILs (Today I Learned) during sessions — user types `/til "what I learned"`, Claude generates a summary from conversation context, and `confab til` posts it to the backend with the transcript position (message UUID)
- `/retro`: Review and discuss session transcripts — user types `/retro <session-id> [question]`, Claude fetches the condensed transcript via `confab retro`, optionally reads local raw JSONL for richer data, and engages in discussion about the session

Skills are managed separately from hooks: `confab skills add/remove` (vs `confab hooks add/remove`).

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
  ~/go/bin/staticcheck ./...      # Static analysis
  ~/go/bin/deadcode -test ./...   # Find unused code (including test files)
  ```
- **Clean migrations**: When moving or refactoring code, complete the migration fully. Do not leave duplicate code with deprecation comments "for backwards compatibility." Update all callers (including tests) to use the new location immediately. Stale duplicates cause maintenance burden and inevitably diverge.
- **Keep documentation up to date**: When changing code, update the corresponding package README (`cmd/README.md`, `pkg/<package>/README.md`). Key things to keep current: file lists, exported API descriptions, invariants, dependency lists, and extension checklists. If a change spans multiple packages, also check `pkg/README.md` (dependency map) and `CLAUDE.md` (architecture overview). Documentation that contradicts the code is worse than no documentation.
