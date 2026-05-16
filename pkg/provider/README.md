# pkg/provider

Provider-specific local behavior for the tools Confab integrates with (currently Claude Code and Codex). Each provider is a concrete type that owns its paths, hook parsing, session discovery, and transcript metadata extraction.

The package defines a `Provider` interface and a `HookInput` interface (Phase 1 + 2 of the abstraction work — see CF-394). Both concrete provider types satisfy `Provider`; hook-input adapters in `hookinput.go` satisfy `HookInput`. As of CF-397 (Phase 3), `pkg/sync/engine.go` also dispatches sync-loop behavior (root metadata, descendant discovery, chunk annotation) through the interface; the engine has no provider-name branches.

## Files

| File | Role |
|------|------|
| `provider.go` | `Provider` and `HookInput` interfaces, sync-loop interfaces (`TranscriptRegistrar`, `DescendantRegistrar`, `ChunkView`), `SummaryLink` / `AnnotationResult` types, provider name constants (`NameClaudeCode`, `NameCodex`), the registry (`Get(name)`), and `NormalizeName(name)` |
| `codex_rollout.go` | `CodexRolloutMetadata` — wire-format metadata transmitted on the first chunk of every Codex rollout. Lives here (not pkg/sync) so the Codex implementation can construct one without a cycle; pkg/sync aliases it. |
| `hookinput.go` | `claudeHookInputAdapter` and `codexHookInputAdapter` — wrap the typed structs in `pkg/types` so they satisfy `HookInput`. Required because the structs' existing exported `SessionID` field collides with a `SessionID()` method |
| `claude.go` | `ClaudeCode` — paths, transcript validation, parent-process detection, and the `Provider` methods. Sync-loop methods are no-ops except `AnnotateChunk`, which extracts summary + first user message + summary links from transcript chunks. Hook install/uninstall delegates to `pkg/hookconfig`; skill install delegates to `pkg/config` |
| `codex.go` | `Codex` — paths, rollout scanning, transcript validation, first-user-message extraction, and the `Provider` methods. `InitTranscript` attaches root rollout metadata from session_meta; `DiscoverDescendants` walks the SQLite subtree; `AnnotateChunk` attaches codex_rollout on FirstLine==1 and extracts first_user_message once per session. Hook install/uninstall delegates to `pkg/hookconfig` |
| `codex_state.go` | Codex local SQLite reader: `StateDBPath()`, `WalkUpToRoot(threadUUID)`, `ListSubtree(rootUUID)`. Used by the hook handler, `confab save`, and `Codex.DiscoverDescendants` to discover subagent rollouts and route them to the top-most root |

## Provider surfaces

### `ClaudeCode`
- Paths: `StateDir`, `SettingsPath`, `ProjectsDir`, transcript path validation against `CONFAB_CLAUDE_DIR`.
- Hooks: `ReadHookInput`, `ReadSessionHookInput`, `InstallHooks`/`UninstallHooks`/`IsHooksInstalled` (delegate to `pkg/hookconfig`, which edits `~/.claude/settings.json`).
- Skills: `InstallSkills` installs `/til` and `/retro` Claude Code skills (delegates to `pkg/config`).
- Hook response: `WriteHookResponse` writes a `types.ClaudeHookResponse`.
- Parent detection: parent PID monitoring helpers, Claude-specific.

### `Codex`
- Paths: `StateDir` (override via `CONFAB_CODEX_DIR`), `SessionsDir`, `ConfigPath`.
- Rollout discovery: `SessionIDFromRolloutPath`, `ScanSessions`, `FindSessionByID` (user sessions only), `FindRolloutByID` (any rollout — used by `confab save` to accept subagent UUIDs), `ReadSessionInfo`, internal `walkRollouts` helper.
- Filtering: `CodexSessionInfo.IsUserSession()` excludes subagents/memory rollouts by `thread_source` and `agent_*` metadata.
- Hooks: `ReadHookInput`, `ReadSessionHookInput`, `InstallHooks`/`UninstallHooks`/`IsHooksInstalled` (delegate to `pkg/hookconfig`, which edits `~/.codex/config.toml`). Only `SessionStart` is installed — see [Codex daemon shutdown](#codex-daemon-shutdown).
- Skills: `InstallSkills` is a no-op for Codex.
- Hook response: `WriteHookResponse` writes a `types.CodexHookResponse`.
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

## `Provider` interface

Methods every provider must implement:

- `Name() string` — canonical name (one of `NameClaudeCode`, `NameCodex`).
- `StateDir() (string, error)` — local state directory.
- `FindParentPID() int`, `IsProcess(pid int) bool` — parent-process detection.
- `ParseSessionHook(io.Reader) (HookInput, error)` — read a SessionStart hook payload and return the provider-agnostic view.
- `InstallHooks() (string, error)` / `UninstallHooks() (string, error)` / `IsHooksInstalled() (bool, error)` — install/check the full hook set the provider requires (Claude: 4 bundles; Codex: SessionStart only). Both methods delegate to `pkg/hookconfig`.
- `InstallSkills() error` — install provider-specific Claude Code skills. No-op for Codex.
- `WalkUpToRoot(sessionID string) (rootID, rootPath string, error)` — Codex walks `thread_spawn_edges`; Claude is identity with empty `rootPath`.
- `ShouldSpawnForInput(in HookInput) bool` — Codex returns false for subagent rollouts and for unreadable rollout files; Claude always returns true. `os.IsNotExist` is treated as a race-tolerance "spawn anyway" case.
- `WriteHookResponse(w, suppressOutput, systemMessage) error` — write the provider-specific hook response JSON (`ClaudeHookResponse` vs `CodexHookResponse`).
- `InitTranscript(target TranscriptRegistrar, transcriptPath, externalID string) error` — called from `sync.Engine.Init` after the tracker is initialized. Codex attaches root rollout metadata via `target.SetCodexRollout`; Claude is a no-op. Implementations never surface read failures as errors — they log warn and fall through.
- `DiscoverDescendants(reg DescendantRegistrar, externalID string) error` — called once per `SyncAll` cycle, before the BFS loop. Codex walks the SQLite subtree and calls `reg.RegisterCodexRollout` per verified descendant. Claude is a no-op (its agents are discovered transitively from transcript content inside `tracker.DiscoverNewFiles`). Must be idempotent across calls.
- `AnnotateChunk(c ChunkView, sentFirstUserMessage bool, redact func(string) string) AnnotationResult` — called for every chunk before upload. Providers attach chunk-level metadata via setters on `c`; summary links go in the returned `AnnotationResult.SummaryLinks` so the engine drives the HTTP. The `redact` closure is nil-safe and lets providers stay decoupled from `pkg/redactor`.

## `Get(name)` and the registry

`Get(name)` returns the registered `Provider` for a canonical name (empty string defaults to `claude-code`). `NormalizeName(name)` is the same lookup but returns the canonical name string. The registry is a package-level read-only map populated at init time — to add a new provider, add its instance to the map and implement the interface.

## Invariants

- `NameClaudeCode` and `NameCodex` are the canonical wire values. Backend session uniqueness is `(user_id, provider, external_id)`.
- `NormalizeName(name)` returns `claude-code` for empty input (legacy default) and rejects unknown providers.
- `ClaudeStateDirEnv` is duplicated between `pkg/config/paths.go` and `pkg/provider/claude.go` to break a circular import. The two MUST stay in sync; reviewers should catch any drift.
- `ClaudeCode` preserves existing Claude Code behavior, including `CONFAB_CLAUDE_DIR`.
- Claude hook parsing returns `types.ClaudeHookInput`; Codex hook parsing returns `types.CodexHookInput`. There is no generic normalized hook payload.
- `Codex.ExtractFirstUserMessageFromLines` only considers `event_msg.user_message` — the first `response_item.message[role=user]` line in a Codex rollout contains an `<environment_context>` wrapper, not the user's prompt, and must be skipped.
- `truncateUTF8Bytes` never returns a string longer than `maxBytes`, even on invalid UTF-8 input.
- `Codex.IsUserSession` filters out subagents and memory rollouts so `ScanSessions` only surfaces top-level user sessions.
- `Codex.InstallHooks` is idempotent and never strips unmanaged Codex config sections.
- `Codex.WalkUpToRoot` is the single point that converts a firing thread UUID to its top-most root. All Codex daemon spawning and `confab save` invocations route through it, so subagent rollouts always upload under the root's session — never as orphan sessions.
- `Codex.WalkUpToRoot` never returns the empty string for the root UUID; on any failure mode (no DB, schema mismatch, edge-race exhausted) it returns the input thread UUID so callers can keep moving.
- Parent PID detection is part of the `Provider` interface (`FindParentPID`, `IsProcess`); the bodies remain provider-specific (different process-name patterns) and share the package-level `getProcCmdline` / `getParentPID` helpers in `claude.go`.
- `Codex.InstallHooks` installs only `SessionStart`. Daemon shutdown is driven by parent-PID liveness, never by Codex `Stop`.
- `CodexRolloutMetadata` JSON tags are wire-format pins. Existing rows in the backend's `codex_rollouts` table were written against these tags; renaming any field is a backwards-incompatible change. Adding new optional fields (with `omitempty`) is safe.
- Sync-loop providers (`InitTranscript`, `DiscoverDescendants`, `AnnotateChunk`) are called from a single goroutine inside the engine's sync loop. Implementations may mutate the passed `TranscriptRegistrar` / `DescendantRegistrar` / `ChunkView` without locking; the engine does not call them concurrently for the same engine instance.

## Used By

`cmd/`, `pkg/discovery/`, `pkg/hookconfig/` (provider provides the file paths; hookconfig does the file editing), `pkg/sync/` (the engine dispatches root metadata, descendant discovery, and chunk annotation through the `Provider` interface).
