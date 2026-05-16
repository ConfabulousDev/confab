# pkg/provider

Provider-specific local behavior for the tools Confab integrates with (currently Claude Code and Codex). Each provider is a concrete type that owns its paths, hook parsing, session discovery, and transcript metadata extraction.

This package does **not** define a generic provider interface, a normalized hook model, or a normalized transcript shape. Provider-specific concerns stay in provider-specific code.

## Files

| File | Role |
|------|------|
| `provider.go` | `NameClaudeCode = "claude-code"`, `NameCodex = "codex"`, and `Normalize(name)` for canonical provider names |
| `claude.go` | `ClaudeCode` — Claude Code paths, hook parsing, transcript path validation, parent process detection |
| `codex.go` | `Codex` — Codex paths, rollout scanning, hook parsing/installation, transcript path validation, first-user-message extraction |
| `codex_state.go` | Codex local SQLite reader: `StateDBPath()`, `WalkUpToRoot(threadUUID)`, `ListSubtree(rootUUID)`. Used by the hook handler, sync tracker, and `confab save` to discover subagent rollouts and route them to the top-most root |

## Provider surfaces

### `ClaudeCode`
- Paths: `StateDir`, `SettingsPath`, `ProjectsDir`, transcript path validation against `CONFAB_CLAUDE_DIR`.
- Hooks: `ReadHookInput`, `ReadSessionHookInput`, install/uninstall in `~/.claude/settings.json`.
- Parent detection: parent PID monitoring helpers, Claude-specific.

### `Codex`
- Paths: `StateDir` (override via `CONFAB_CODEX_DIR`), `SessionsDir`, `ConfigPath`.
- Rollout discovery: `SessionIDFromRolloutPath`, `ScanSessions`, `FindSessionByID` (user sessions only), `FindRolloutByID` (any rollout — used by `confab save` to accept subagent UUIDs), `ReadSessionInfo`, internal `walkRollouts` helper.
- Filtering: `CodexSessionInfo.IsUserSession()` excludes subagents/memory rollouts by `thread_source` and `agent_*` metadata.
- Hooks: `ReadHookInput`, `ReadSessionHookInput`, `InstallHooks`/`UninstallHooks` for `~/.codex/config.toml` (preserves user config, makes backups, idempotent, enables `features.hooks = true`, removes deprecated `features.codex_hooks`). Only `SessionStart` is installed — see [Codex daemon shutdown](#codex-daemon-shutdown).
- Parent detection: `FindParentPID`, `IsProcess`, `MatchesProcess` (regex `(?i)\bcodex\b`) for daemon parent-liveness monitoring, mirroring `ClaudeCode`.
- Transcript metadata: `ExtractFirstUserMessageFromLines` reads the first `event_msg.user_message` from rollout lines, trims whitespace, and truncates to `types.MaxFirstUserMessageLength` on a UTF-8 boundary.
- Path validation: `ValidateRolloutPath` requires an absolute path under `SessionsDir` matching `rollout-<timestamp>-<uuid>.jsonl`.

### Codex daemon shutdown

Codex fires `Stop` at every agent/turn boundary, including root rollout stops while the interactive Codex session is still alive. Wiring `confab hook session-end` to `[[hooks.Stop]]` would therefore kill the root sync daemon prematurely. Instead:

- `Codex.InstallHooks` writes only `[[hooks.SessionStart]]` into the managed block.
- `cmd/spawn.go` stores `Codex.FindParentPID()` on the daemon at spawn time.
- The daemon's main loop (`pkg/daemon/daemon.go`) monitors that PID and shuts down when the interactive Codex process exits — same mechanism Claude Code uses.
- `confab hook session-end --provider codex` is rejected with an explicit error pointing users at their `~/.codex/config.toml`.
- Local state DB (`codex_state.go`): reads Codex's `~/.codex/state_*.sqlite` (read-only, highest numeric suffix wins; `CONFAB_CODEX_STATE_DB` overrides). `WalkUpToRoot(threadUUID)` walks the `thread_spawn_edges` chain to the top-most root with a 5×50ms retry budget for the spawn-vs-edge race (and a `thread_source='user'` fast-path that skips retries for known roots). `ListSubtree(rootUUID)` returns every descendant via a recursive CTE. All paths degrade gracefully when the DB is unavailable — callers see `(threadUUID, "", nil)` for `WalkUpToRoot` and a nil slice for `ListSubtree`.

## Invariants

- `NameClaudeCode` and `NameCodex` are the canonical wire values. Backend session uniqueness is `(user_id, provider, external_id)`.
- `Normalize(name)` returns `claude-code` for empty input (legacy default) and rejects unknown providers.
- `ClaudeCode` preserves existing Claude Code behavior, including `CONFAB_CLAUDE_DIR`.
- Claude hook parsing returns `types.ClaudeHookInput`; Codex hook parsing returns `types.CodexHookInput`. There is no generic normalized hook payload.
- `Codex.ExtractFirstUserMessageFromLines` only considers `event_msg.user_message` — the first `response_item.message[role=user]` line in a Codex rollout contains an `<environment_context>` wrapper, not the user's prompt, and must be skipped.
- `truncateUTF8Bytes` never returns a string longer than `maxBytes`, even on invalid UTF-8 input.
- `Codex.IsUserSession` filters out subagents and memory rollouts so `ScanSessions` only surfaces top-level user sessions.
- `Codex.InstallHooks` is idempotent and never strips unmanaged Codex config sections.
- `Codex.WalkUpToRoot` is the single point that converts a firing thread UUID to its top-most root. All Codex daemon spawning and `confab save` invocations route through it, so subagent rollouts always upload under the root's session — never as orphan sessions.
- `Codex.WalkUpToRoot` never returns the empty string for the root UUID; on any failure mode (no DB, schema mismatch, edge-race exhausted) it returns the input thread UUID so callers can keep moving.
- Parent PID detection is provider-specific (`ClaudeCode.FindParentPID`, `Codex.FindParentPID`) and not part of a generic provider interface; both providers share the package-level `getProcCmdline` / `getParentPID` helpers in `claude.go`.
- `Codex.InstallHooks` installs only `SessionStart`. Daemon shutdown is driven by parent-PID liveness, never by Codex `Stop`.

## Used By

`cmd/`, `pkg/config/`, `pkg/discovery/`, `pkg/sync/` (Codex first-user-message extraction is called from the sync engine's transcript-metadata path).
