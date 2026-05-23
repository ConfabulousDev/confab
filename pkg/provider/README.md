# pkg/provider

Provider-specific local behavior for Confab integrations. Current providers: Claude Code and Codex. Each concrete provider owns paths, hook parsing, session discovery, and transcript metadata extraction.

The package defines a `Provider` interface and a `HookInput` interface (Phase 1 + 2 of the abstraction work — see CF-394). Both concrete provider types satisfy `Provider`; hook-input adapters in `hookinput.go` satisfy `HookInput`. As of CF-397 (Phase 3), `pkg/sync/engine.go` dispatches sync-loop behavior (root metadata, descendant discovery, chunk annotation) through the interface; as of CF-398 (Phase 4), session discovery (`ScanSessions`, `FindSessionByID`, `ExtractMetadata`, `DefaultCWD`) is also routed through the interface. `cmd/` has no discovery-related provider-name branches.

## Files

| File | Role |
|------|------|
| `provider.go` | `Provider` and `HookInput` interfaces, sync-loop interfaces (`TranscriptRegistrar`, `DescendantRegistrar`, `ChunkView`), `SummaryLink` / `AnnotationResult` types, provider name constants (`NameClaudeCode`, `NameCodex`), the registry (`Get(name)`), and `NormalizeName(name)` |
| `detect.go` | `DetectInstalled() []string` returns the canonical names of providers whose CLI binary is on `PATH`, in fixed registry order. Uses the exported package-level `LookPath` var (defaults to `exec.LookPath`) so tests can stub it. Backs `confab setup` auto-detect (CF-422) and `confab status` per-provider CLI presence. |
| `session.go` | `SessionInfo` and `SessionMetadata` — cross-provider shapes returned by the discovery interface methods. Also defines `maxLinesForExtraction` and the shared `readHeadLines` helper. |
| `codex_rollout.go` | `CodexRolloutMetadata` — wire-format metadata transmitted on the first chunk of every Codex rollout. Lives here (not pkg/sync) so the Codex implementation can construct one without a cycle; pkg/sync aliases it. |
| `hookinput.go` | `claudeHookInputAdapter` and `codexHookInputAdapter` — wrap the typed structs in `pkg/types` so they satisfy `HookInput`. Required because the structs' existing exported `SessionID` field collides with a `SessionID()` method |
| `claude.go` | `ClaudeCode` — paths, transcript validation, parent-process detection, and the `Provider` methods. Sync-loop methods are no-ops except `AnnotateChunk`, which delegates to `ExtractMetadata` to extract summary + first user message + summary links from transcript chunks. Hook install/uninstall delegates to `pkg/hookconfig`; skill install/uninstall/status delegates to `pkg/config` |
| `claude_discovery.go` | Claude session scanning (`ScanSessions`, `FindSessionByID`) and metadata extraction (`ExtractMetadata`, `DefaultCWD`). Walks `~/.claude/projects/`, parses Claude transcript JSONL for summaries + first user messages, sanitizes HTML, truncates to `maxMetadataFieldSize/2` bytes. |
| `claude_agentids.go` | `ClaudeCode.ExtractAgentIDsFromMessage` and `IsValidAgentID` — Claude-only transcript-schema parsing for sidechain agent file discovery. Called from `pkg/sync/tracker.go` during chunk reads. |
| `codex.go` | `Codex` — paths, transcript validation, parent-process detection, hook handling, and the `Provider` methods. `InitTranscript` attaches root rollout metadata from session_meta; `DiscoverDescendants` walks the SQLite subtree; `AnnotateChunk` attaches codex_rollout on FirstLine==1 and extracts first_user_message once per session via `ExtractMetadata`. Hook install/uninstall delegates to `pkg/hookconfig`; skill install/uninstall/status delegates to `pkg/config` |
| `codex_discovery.go` | Codex rollout discovery: `ScanSessions` (interface), `ScanCodexSessions` (rich type), `FindSessionByID` (walks subagent UUIDs up to the root), package-private rollout resolution, `ReadSessionInfo`, `ExtractFirstUserMessageFromLines`, `ExtractMetadata`, `DefaultCWD`. Also houses `CodexSessionInfo` and the rollout-filename regex. |
| `codex_state.go` | Codex local SQLite reader: `StateDBPath()`, `WalkUpToRoot(threadUUID)`, `ListSubtree(rootUUID)`. Used by the hook handler, `confab save` (via `FindSessionByID`'s walk-up), and `Codex.DiscoverDescendants` to discover subagent rollouts and route them to the top-most root |

## Provider surfaces

### `ClaudeCode`
- Paths: `StateDir`, `SettingsPath`, `ProjectsDir`, transcript path validation against `CONFAB_CLAUDE_DIR`.
- Discovery: `ScanSessions`, `FindSessionByID`, `ExtractMetadata`, `DefaultCWD` (the four `Provider` interface methods); plus `ExtractAgentIDsFromMessage` for sidechain agent file discovery.
- Hooks: `ReadHookInput`, `ReadSessionHookInput`, `InstallHooks`/`UninstallHooks`/`IsHooksInstalled` (delegate to `pkg/hookconfig`, which edits `~/.claude/settings.json`).
- Skills: `InstallSkills` installs `/til` and `/retro` under `~/.claude/skills/`; `UninstallSkills` removes bundled skills; `IsSkillInstalled` reports per-skill state (delegates to `pkg/config`).
- Hook response: `WriteHookResponse` writes a `types.ClaudeHookResponse`.
- Parent detection: parent PID monitoring helpers, Claude-specific.

### `Codex`
- Paths: `StateDir` (override via `CONFAB_CODEX_DIR`), `SessionsDir`, `ConfigPath`.
- Discovery: `ScanSessions`, `FindSessionByID` (walks subagent UUIDs up to the root), `ExtractMetadata`, `DefaultCWD` (the four `Provider` interface methods).
- Additional rollout helpers: `ScanCodexSessions` (rich `CodexSessionInfo` form), `ReadSessionInfo`, `SessionIDFromRolloutPath`, `ExtractFirstUserMessageFromLines`, internal `walkRollouts` helper.
- Filtering: `CodexSessionInfo.IsUserSession()` excludes subagents/memory rollouts by `thread_source` and `agent_*` metadata.
- Hooks: `ReadHookInput`, `ReadSessionHookInput`, `InstallHooks`/`UninstallHooks`/`IsHooksInstalled` (delegate to `pkg/hookconfig`, which edits `~/.codex/config.toml`). Installs `SessionStart`, `PreToolUse`, and `PostToolUse`; shutdown remains parent-PID driven.
- Skills: `InstallSkills` installs `/til` and `/retro` under `~/.codex/skills/`; `UninstallSkills` removes bundled skills; `IsSkillInstalled` reports per-skill state (delegates to `pkg/config`).
- Hook response: `WriteHookResponse` writes a `types.CodexHookResponse`.
- Parent detection: `FindParentPID`, `IsProcess`, `MatchesProcess` (regex `(?i)\bcodex\b`) for daemon parent-liveness monitoring, mirroring `ClaudeCode`.
- Transcript metadata: `ExtractFirstUserMessageFromLines` reads the first `event_msg.user_message` from rollout lines, trims whitespace, and truncates to `types.MaxFirstUserMessageLength` on a UTF-8 boundary.
- Path validation: `ValidateRolloutPath` requires an absolute path under `SessionsDir` matching `rollout-<timestamp>-<uuid>.jsonl`.

### Codex daemon shutdown

Codex fires `Stop` at every agent/turn boundary, including root rollout stops while the interactive Codex session is still alive. Wiring `confab hook session-end` to `[[hooks.Stop]]` would therefore kill the root sync daemon prematurely. Instead:

- `Codex.InstallHooks` writes `[[hooks.SessionStart]]`, `[[hooks.PreToolUse]]`, and `[[hooks.PostToolUse]]` into the managed block.
- `cmd/spawn.go` stores `Codex.FindParentPID()` on the daemon at spawn time.
- The daemon's main loop (`pkg/daemon/daemon.go`) monitors that PID and shuts down when the interactive Codex process exits — same mechanism Claude Code uses.
- `confab hook session-end --provider codex` is rejected with an explicit error pointing users at their `~/.codex/config.toml`.
- Local state DB (`codex_state.go`): reads Codex's `~/.codex/state_*.sqlite` (read-only, highest numeric suffix wins; `CONFAB_CODEX_STATE_DB` overrides). `WalkUpToRoot(threadUUID)` walks the `thread_spawn_edges` chain to the top-most root with a 5×50ms retry budget for the spawn-vs-edge race (and a `thread_source='user'` fast-path that skips retries for known roots). `ListSubtree(rootUUID)` returns every descendant via a recursive CTE. All paths degrade gracefully when the DB is unavailable — callers see `(threadUUID, "", nil)` for `WalkUpToRoot` and a nil slice for `ListSubtree`.

## `Provider` interface

Methods every provider must implement:

- `Name() string` — canonical name (one of `NameClaudeCode`, `NameCodex`).
- `CLIBinaryName() string` — OS-level binary name used by `DetectInstalled` / `confab status` (`"claude"` for Claude Code, `"codex"` for Codex). Distinct from `Name()` because the canonical name (`claude-code`) is not the binary name.
- `StateDir() (string, error)` — local state directory.
- `FindParentPID() int`, `IsProcess(pid int) bool` — parent-process detection.
- `ParseSessionHook(io.Reader) (HookInput, error)` — read a SessionStart hook payload and return the provider-agnostic view.
- `InstallHooks() (string, error)` / `UninstallHooks() (string, error)` / `IsHooksInstalled() (bool, error)` — install/check the full hook set the provider requires. Claude installs 4 bundles (sync, PreToolUse, PostToolUse, UserPromptSubmit). Codex installs 3 events (SessionStart, PreToolUse, PostToolUse). Both methods delegate to `pkg/hookconfig`.
- `SupportsCommitLinking() bool` — true if the provider installs the PreToolUse + PostToolUse events that drive bidirectional GitHub linking. Used by `cmd/hook_pretooluse.go` and `cmd/hook_posttooluse.go` to silently no-op for any future provider that doesn't yet support the flow. Both Claude Code and Codex return true.
- `InstallSkills() error` / `UninstallSkills() error` / `IsSkillInstalled(name string) bool` — manage bundled Confab skills in the provider's local skill layout.
- `WalkUpToRoot(sessionID string) (rootID, rootPath string, error)` — Codex walks `thread_spawn_edges`; Claude is identity with empty `rootPath`.
- `ShouldSpawnForInput(in HookInput) bool` — Codex returns false for subagent rollouts and for unreadable rollout files; Claude always returns true. `os.IsNotExist` is treated as a race-tolerance "spawn anyway" case.
- `WriteHookResponse(w, suppressOutput, systemMessage) error` — write the provider-specific hook response JSON (`ClaudeHookResponse` vs `CodexHookResponse`).
- `InitTranscript(target TranscriptRegistrar, transcriptPath, externalID string) error` — called from `sync.Engine.Init` after the tracker is initialized. Codex attaches root rollout metadata via `target.SetCodexRollout`; Claude is a no-op. Implementations never surface read failures as errors — they log warn and fall through.
- `DiscoverDescendants(reg DescendantRegistrar, externalID string) error` — called once per `SyncAll` cycle, before the BFS loop. Codex walks the SQLite subtree and calls `reg.RegisterCodexRollout` per verified descendant. Claude is a no-op (its agents are discovered transitively from transcript content inside `tracker.DiscoverNewFiles`). Must be idempotent across calls.
- `AnnotateChunk(c ChunkView, sentFirstUserMessage bool, redact func(string) string) AnnotationResult` — called for every chunk before upload. Providers attach chunk-level metadata via setters on `c`; summary links go in the returned `AnnotationResult.SummaryLinks` so the engine drives the HTTP. The `redact` closure is nil-safe and lets providers stay decoupled from `pkg/redactor`. Both providers' implementations delegate to `ExtractMetadata` for the parsing work.
- `ScanSessions() ([]SessionInfo, error)` — returns user-initiated sessions discoverable on disk, oldest first. Claude walks `~/.claude/projects/`; Codex projects from `ScanCodexSessions` and extracts `FirstUserMessage` per rollout for the list-command title.
- `FindSessionByID(partialID string) (id, transcriptPath string, error)` — resolves a full or partial ID. Claude is identity walk-up; Codex walks subagent UUIDs up to the root via `WalkUpToRoot` so callers transparently upload the whole tree.
- `ExtractMetadata(lines []string) SessionMetadata` — in-memory parsing of the first `maxLinesForExtraction` (50) JSONL lines. Claude returns full Summary + FirstUserMessage + SummaryLinks; Codex returns only FirstUserMessage.
- `DefaultCWD(transcriptPath string) string` — CWD to record on the upload. Claude returns `filepath.Dir(transcriptPath)`; Codex reads `session_meta.cwd` with the dir fallback.

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
- Agent-ID extraction (`ClaudeCode.ExtractAgentIDsFromMessage`) is intentionally Claude-only. Codex tracks subagents via its SQLite thread tree and never grows agent IDs in rollout JSONL — `pkg/sync/tracker.go` calls the Claude method on every chunk regardless of provider; the method's `msgType != "user"` early-return safely no-ops on Codex data.
- `Codex.FindSessionByID` returns the ROOT thread for any partial UUID matching a subagent. The package-private `findRolloutByID` helper resolves concrete rollout files before the walk-up step.
- `DetectInstalled()` returns names in fixed `detectOrder` (`claude-code` first, then `codex`) regardless of `LookPath` lookup order. This determinism is load-bearing for setup output and tests.
- `CLIBinaryName()` is the OS binary name (`"claude"`, `"codex"`) — never the canonical provider name. The two diverge for Claude Code (`claude-code` vs `claude`).
- `Codex.InstallHooks` installs `SessionStart`, `PreToolUse`, and `PostToolUse`. Daemon shutdown is driven by parent-PID liveness, never by Codex `Stop`.
- `Codex.InstallSkills` writes only `SKILL.md` files under `~/.codex/skills/<name>/`; optional Codex UI metadata such as `agents/openai.yaml` is not generated for Confab's bundled skills.
- `CodexRolloutMetadata` JSON tags are wire-format pins. Existing rows in the backend's `codex_rollouts` table were written against these tags; renaming any field is a backwards-incompatible change. Adding new optional fields (with `omitempty`) is safe.
- `CodexRolloutMetadata` string fields (cwd, model, agent_*) ride on the first chunk unredacted. Rollout *content* is redacted in `pkg/sync.FileTracker.ReadChunk`; this struct is not. Before adding a field that could carry free-text user content, plumb the redactor into `Codex.InitTranscript` / `Codex.DiscoverDescendants` — see the struct doc in `codex_rollout.go`.
- Sync-loop providers (`InitTranscript`, `DiscoverDescendants`, `AnnotateChunk`) are called from a single goroutine inside the engine's sync loop. Implementations may mutate the passed `TranscriptRegistrar` / `DescendantRegistrar` / `ChunkView` without locking; the engine does not call them concurrently for the same engine instance.

## Used By

`cmd/`, `pkg/hookconfig/` (provider provides the file paths; hookconfig does the file editing), `pkg/sync/` (the engine dispatches root metadata, descendant discovery, and chunk annotation through the `Provider` interface; the tracker calls `ClaudeCode{}.ExtractAgentIDsFromMessage` directly for Claude sidechain discovery).
