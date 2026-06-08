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

Confab is a CLI tool that captures Claude Code session transcripts and Codex session rollouts and uploads them to a backend. It operates in two modes:

### Sync Mode (Primary)
- **Daemon-based incremental sync**: When a Claude Code or Codex session starts, the `SessionStart` hook spawns a background daemon (`confab sync start`)
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
- **pkg/redactor/**: JSON-aware redaction of sensitive data before upload
- **pkg/config/**: Configuration (Confab + Claude `settings.json` plumbing) and bundled skill templates installed into provider-local skill dirs (`~/.claude/skills/`, `~/.codex/skills/`, `~/.config/opencode/skills/`)
- **pkg/hookconfig/**: Per-provider hook install/uninstall for the settings-file providers — edits Claude `~/.claude/settings.json` and Codex `~/.codex/config.toml`. Claude's and Codex's `InstallHooks` / `UninstallHooks` (in `pkg/provider`) delegate here. OpenCode does **not** use this package: it has no settings/config hooks, so `Opencode.InstallHooks` writes a TS plugin to `~/.config/opencode/plugins/` directly (see `pkg/provider/opencode.go`).
- **pkg/http/**: HTTP client with zstd compression, auth, and retry logic
- **pkg/provider/**: `Provider` interface + Claude Code / Codex / OpenCode implementations. Owns session discovery (`ScanSessions`, `FindSessionByID`, `ExtractMetadata`, `DefaultCWD`), metadata extraction, Claude agent-ID parsing, hooks, paths, and Codex tree-walking. `claude_discovery.go` walks `~/.claude/projects/`; `codex_discovery.go` scans `~/.codex/sessions/`; `codex_state.go` reads Codex's local SQLite DB to walk subagent rollouts up to their root. OpenCode has no on-disk transcript file: `opencode_db.go` reads its local SQLite DB at `~/.local/share/opencode/opencode.db`, `opencode_collector.go` materializes complete `{info, parts}` messages into a local JSONL file, and `opencode_session.go` does the assembly + completeness gating (see OpenCode provider differences below). All `cmd/` discovery dispatch routes through this interface.
- **pkg/opencodetest/**: Test fixture builder for the OpenCode SQLite schema. `NewDB(t)` writes a fresh `<t.TempDir()>/opencode.db` with real production tables + indices; `AddSession` / `AddMessage` / `AddPart` chain to seed rows. Shape helpers (`UserTextMessage`, `AssistantMessageFinished`, `TextPart`, `ToolPartCompleted`, …) keep tests declarative. No vendored DB file — every test seeds at runtime.
- **pkg/codextest/**: Reusable Codex SQLite + sessions-tree fixture builder used by tests in `pkg/provider`, `pkg/sync`, `pkg/daemon`, and `cmd`.
- **pkg/confabpath/**: Stdlib-only leaf with `Dir()` / `Subpath(...)` helpers for `~/.confab`. Used everywhere local state paths get built so the home-dir lookup and join happen identically.
- **pkg/loginit/**: Startup-time orchestration that reads `log_level` from upload config and applies it to the logger. Lives in its own package so `pkg/config` and `pkg/logger` don't have to depend on each other.

## Backend

The backend API lives in the sibling repo `../confab-web`. When implementing CLI commands that call backend endpoints, verify the actual API contract by reading handler code there — don't rely solely on ticket specs or documentation.

## Data Flow

1. Claude Code writes transcripts to `~/.claude/projects/<path>/<session-id>.jsonl`; Codex writes rollouts to `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl` (see Codex provider differences below)
2. Daemon watches transcript file, reads new lines, applies redaction, uploads as chunks
3. Backend tracks `last_synced_line` per file; daemon syncs only new content
4. Agent sidechain files (`agent-*.jsonl`) are synced alongside the main transcript

### Claude workflow subagent files (CF-533)

Claude's `Workflow` tool spawns subagents whose transcripts live nested at `<session>/subagents/workflows/<runId>/agent-<id>.jsonl`, plus a per-run `journal.jsonl`. These carry no `agentId` in the main transcript, so they are **not** found by `ExtractAgentIDsFromMessage`. Instead `provider.ClaudeCode.DiscoverWorkflowFiles` (in `pkg/provider/claude_workflows.go`) scans the `subagents/workflows/` directory each `SyncAll` cycle and registers them via `pkg/sync.FileTracker.RegisterWorkflowFile` under **path-encoded** backend `file_name`s, written verbatim (the backend resolves `<runId>` from the path and `<id>` via `path.Base`):

- `subagents/workflows/<runId>/agent-<id>.jsonl` → `file_type=agent`
- `subagents/workflows/<runId>/journal.jsonl` → `file_type=workflow_journal`

`agent-<id>.meta.json`, `wf_<runId>.json`, and the script file are not uploaded. From there they reuse the ordinary incremental/redacted chunk path. This is Claude-only — `Codex.DiscoverWorkflowFiles` is a no-op.

**Capability gating.** Because the CLI may outrun a self-hosted backend, workflow uploads are gated on the backend advertising support via the public `GET /api/v1/capabilities` endpoint (body **is** `{"workflow_files":bool,"workflow_journal":bool}`, no wrapper; shipped by CF-532). The engine (`pkg/sync/engine.go`) probes lazily — only when a workflow run dir actually exists, so non-workflow sessions never probe — caches only definitive answers (a `404` from an old backend → unsupported; a clean `200` → the parsed flags) and re-probes on transient failures. Gating is **per-flag**: `agent` files require `workflow_files`, the journal requires `workflow_journal`. The `journal.jsonl` line schema is documented in `pkg/sync/README.md` as the read contract for the downstream frontend tickets (CF-534/535).

### Codex provider differences

- Codex rollouts live at `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-...<uuid>.jsonl`. A "session" is a user-initiated thread; subagents spawn their own rollouts and are tracked in Codex's local SQLite (`~/.codex/state_*.sqlite`, `threads` + `thread_spawn_edges`).
- Codex fires `SessionStart` for every spawned subagent. The hook handler in `cmd/hook_sessionstart.go` calls `provider.Codex{}.WalkUpToRoot` to resolve the firing UUID to its top-most root before spawning a daemon — so only one daemon runs per root tree, and subagent SessionStart events for already-tracked trees are no-ops.
- The sync engine calls `provider.DiscoverDescendants(tracker, externalID)` once per `SyncAll` cycle. Codex's implementation queries the local SQLite state DB (recursive walk under the root UUID); Claude's is a no-op. New subagent rollouts become `file_type=agent` sidechain files under the root's backend session — the same primitive Claude uses for its subagent files.
- The first chunk of every Codex rollout (root or descendant) carries `chunk.metadata.codex_rollout` with the rollout's identity (thread UUID, parent UUID, rollout path, cwd, model, agent metadata). The backend upserts this into `codex_rollouts` keyed by thread UUID. Retries are safe: the metadata rides along again because `FirstLine == 1` is preserved across retries.
- Daemon shutdown for Codex uses parent-process liveness, **not** a `Stop` hook. Codex fires `Stop` at every agent/turn boundary, so a Stop-driven shutdown would prematurely kill the root daemon. `cmd/spawn.go` resolves the Codex parent PID via `Codex.FindParentPID()` and stores it on the daemon; the daemon's main loop exits when that PID dies (same mechanism as Claude Code). `Codex.InstallHooks` installs `[[hooks.SessionStart]]` + `[[hooks.PreToolUse]]` + `[[hooks.PostToolUse]]` (no `Stop`, no `UserPromptSubmit`); `confab hook session-end --provider codex` returns an explicit error.
- GitHub commit/PR linking is wired for Codex (CF-492). The same handlers as Claude (`cmd/hook_pretooluse.go`, `cmd/hook_posttooluse.go`) route by `--provider`. For each Bash invocation, `getConfabSessionID` first tries the firing UUID's daemon state; if missing, it calls `provider.Codex{}.WalkUpToRoot` and retries with the root UUID — so subagent-initiated `git commit` / `gh pr create` always link to the user-facing root session.
- Redaction is provider-agnostic. `redactor.RedactJSONLine` walks any JSON line shape, and `FileTracker.ReadChunk` applies it to every tracked file regardless of provider — Codex rollouts get the same pattern set as Claude transcripts, which is what the backend's Codex Redactions analytics card relies on. `CodexRolloutMetadata` fields (cwd, model, agent_*) ride on the first chunk unredacted; see the struct doc in `pkg/provider/codex_rollout.go` before adding free-text fields there.

### OpenCode provider differences

OpenCode has **no on-disk transcript file** — session data lives in a local SQLite database at `~/.local/share/opencode/opencode.db` (or `$XDG_DATA_HOME/opencode/opencode.db`, overridable via `CONFAB_OPENCODE_DB`). Rather than fork the file-based sync core, the daemon **materializes** OpenCode data into a local JSONL file and feeds it through the ordinary pipeline. The HTTP/SSE path that earlier versions used (CF-537) is gone: OpenCode v1.1.10+ ships the local HTTP server off by default, so it couldn't be relied on.

- The TS plugin (`pkg/provider/plugins/confab-sync.ts`) bridges lifecycle (the daemon does the data sync): on `session.created` it fires `confab hook session-start --provider opencode` with `{session_id, cwd, parent_pid}` (plus `parent_id` for subagents). On any allowlisted reconcile event (`session.status`/`session.updated`/`session.compacted`/`session.error` — CF-549) it sends the same payload with `cwd:""`; the Go side then resolves `directory` + `parent_id` from SQLite via `OpenCodeDBReader.ReadSessionInfo` (2s context bound, falls back to defaults on error). This is how resumed sessions get a daemon — `session.created` never fires on resume. The plugin's in-process `running` set + a `MAX_DAEMONS=32` cap absorb the reconcile noise so only the first event per session per opencode process pays the shell-out cost. On `dispose` it fires `confab hook session-end --provider opencode` for each session it started. `cmd/hook_sessionend.go` routes `--provider opencode` to `sessionEndOpencode`, which calls `daemon.StopDaemonForProvider`.
- For OpenCode sessions (`d.providerName == NameOpencode`), the daemon (`pkg/daemon/daemon.go`) sets `transcriptPath` to a **real local materialized file** `~/.confab/opencode/<session-id>/messages.jsonl` and starts an `OpenCodeCollector` goroutine. Because `TrackedFile` separates local `Path` from backend `Name`, the materialized file flows through the existing incremental/redacted/chunked upload unchanged — **no `pkg/sync` changes**. The backend receives the absolute path as `transcript_path` (treated like any Claude/Codex path) and `file_name=messages.jsonl`; provider is `"opencode"`, while the *LLM* provider/model live inside each line's `info.providerID`/`info.modelID`.
- The collector (`opencode_collector.go`) polls the local SQLite DB via `provider.OpenCodeDBReader` every `CONFAB_SYNC_INTERVAL_MS` (default 30s; one knob for both backend sync + collector poll). Each tick opens the DB read-only, runs a single indexed `LEFT JOIN` (verified plan uses `message_session_time_created_id_idx` and `part_message_id_id_idx`), and returns `[]ocRawEnvelope` with `id`/`sessionID`/`messageID` reconstructed from row columns (those keys are *never* stored in `message.data` / `part.data` JSON; the backend types require them). HWM (`m.id > ?`) makes the query incremental — long sessions don't re-fetch the whole history every cycle. Each **complete** message is appended once, in ULID order, stopping at the first incomplete one so the file stays append-only and monotonic. Completeness gating (`opencode_session.go`) is unchanged: user messages are complete on arrival; assistant messages once `info.finish` is non-null or `info.error` is present; only terminal (`completed`/`error`) tool parts are included. Idempotent across restart by re-seeding emitted ids from the existing file. Reconcile-error logging uses a Warn-on-first-then-every-Nth cadence (`N = ceil(60s / interval)`) so a stuck collector is loud without spamming.
- `backendSyncEnabled()` gates the backend `Init` on the materialized file existing, so an OpenCode session never creates an **empty** backend session before its first complete message. Shutdown cancels the collector and waits for it before the final sync.
- Daemon shutdown is **signalled by the plugin's `dispose` → `session-end`** (above), with **parent-PID liveness as a backstop**: the daemon monitors the parent OpenCode process in a dedicated goroutine (`monitorParent`, CF-549 R6 — independent of the sync timer so a hung `SyncAll` cannot delay shutdown) and exits if it dies without a clean `dispose` (e.g. a crash). The parent PID is plugin-authoritative — the plugin sends `parent_pid: process.pid` and the Go side trusts it, but `Opencode.FindParentPID` still walks the process tree and logs a Warn on mismatch as production observability for regex drift (CF-549 M1). The collector retries on the DB indefinitely (file missing, locked, etc. all flow through the Warn cadence), so a long-lived daemon outlives transient DB-availability blips.
- **Multi-process resume** (same opencode session opened in two parallel opencode processes) is detected by the shared state file: the second process's `confab hook session-start` finds the existing daemon alive, calls the new `Provider.OnAlreadyRunning` method, and `Opencode.OnAlreadyRunning` logs a Warn to confab's log file (not opencode stderr). Sync is not fully reliable in this configuration; the limitation is documented and not fixed in CF-549.
- **Stale state-file reaper.** Every `sessionStartFromReader` invocation kicks off a `daemon.ReapStaleStates` goroutine (CF-549 F-up A). It walks every `~/.confab/sync/<provider>/` and removes state + inbox files whose PID is no longer alive, with a 5-second grace window (`reapMinAge`) protecting freshly-spawned daemons from being deleted by their own spawn race. Provider-agnostic — one pass covers Claude / Codex / OpenCode. Both files are deleted together via `(*State).DeleteWithInbox`, so a partial cleanup can't strand the inbox.
- **Subagent suppression:** OpenCode fires `session.created` for subagents too. The plugin forwards the session's `parentID`, and `Opencode.ShouldSpawnForInput` returns false for any session with a parent — so only the user-initiated **root** session spawns a daemon.
- **Subagent sidechain capture (CF-538).** The root daemon discovers every descendant session in the local SQLite DB via `OpenCodeDBReader.ListDescendants` (a recursive CTE over `session.parent_id`, capped at 1000 rows as a cycle defense) and runs a **per-child OpenCodeCollector goroutine** alongside the root's. Each child materializes to a nested local path `~/.confab/opencode/<root>/children/<child>/messages.jsonl` and uploads with backend `file_name = opencode/<child>/messages.jsonl` and `file_type = agent` — same primitive Claude/Codex use. Discovery flows through `Opencode.DiscoverDescendants`, which type-asserts the engine's `DescendantRegistrar` to the daemon-supplied `OpencodeDescendantRegistrar` wrapper (a Warn fires if the assertion misses, surfacing a forgotten setter). The wrapper's `RegisterOpencodeChild` checks the `opencode_subagent_files` capability flag (cached via the CF-533 `resolveCaps` machinery, paired backend ticket CF-539), registers via `FileTracker.RegisterSidechainFile`, then spawns the collector idempotently. Shutdown cancels root + every child collector via a shared `childCollectorBase` context, then waits for all `done` channels under a single 2s ceiling. A vanished child (deleted in OpenCode mid-session) keeps its collector running — the existing 1-Warn-per-minute cadence surfaces the stuck state. Daemon shutdown is parent-PID driven (see below); reset on auth failure rebuilds the registrar with the fresh engine and tracker pointers.
- `AnnotateChunk` sets `first_user_message` on the first transcript chunk (CF-540) — the first user message's first text part, trimmed and redacted — so synced sessions appear in the web session list (the backend hides sessions with neither summary nor first_user_message). No summary is set (OpenCode has none) and no `opencode_rollouts` table exists; the backend reads tokens/cost/model from each line. `ScanSessions`/`FindSessionByID` are unsupported (live-sync only; offline manual mode deferred). Redaction applies automatically via `FileTracker.ReadChunk`, same as other providers.
- **Paths and skills.** OpenCode's config dir is `~/.config/opencode` (override `CONFAB_OPENCODE_CONFIG_DIR`); `Opencode.InstallHooks` writes the plugin under `<config>/plugins/`, and `Opencode.InstallSkills` installs the bundled `/retro` skill under `<config>/skills/` (using the generic skill template — only Codex has a provider-specific one). GitHub commit/PR linking is **not** wired (`SupportsCommitLinking()` returns false), so OpenCode installs no PreToolUse/PostToolUse equivalent.

## Hook System

Confab installs four hook bundles in `~/.claude/settings.json` (see `pkg/hookconfig/claude.go`):
- `SessionStart` + `SessionEnd`: spawn / signal-shutdown the sync daemon
- `PreToolUse` (matchers: `Bash`, `mcp__github__create_pull_request`): injects Confab links into git commits and PR creation
- `PostToolUse` (same matchers): links resulting GitHub artifacts back to the Confab session
- `UserPromptSubmit`: re-spawns the daemon if it died between turns

The daemon also monitors its parent PID and shuts down if Claude Code exits unexpectedly.

For Codex, three hook events are installed in `~/.codex/config.toml` (see `pkg/hookconfig/codex.go`):
- `SessionStart`: spawns the sync daemon, with subagent → root walk-up
- `PreToolUse` (matcher: `Bash`): injects the `Confab-Link:` commit trailer and `📝 [Confab link]` PR body line
- `PostToolUse` (matcher: `Bash`): links the resulting commit / PR URL back to the root Confab session

Daemon shutdown stays parent-PID driven (see Codex provider differences above for why `Stop` / `SessionEnd` and `UserPromptSubmit` are not installed).

OpenCode has no settings/config hook system. Instead, `confab setup` installs a TypeScript plugin into `~/.config/opencode/plugins/` (see `pkg/provider/opencode.go` `InstallHooks`) that drives the daemon lifecycle: `session.created` → `session-start`, `dispose` → `session-end`. See the OpenCode provider differences above for the full lifecycle and shutdown behavior.

## Skills

Confab installs bundled skills for every configured provider: Claude Code uses `~/.claude/skills/`, Codex uses `~/.codex/skills/`, and OpenCode uses `~/.config/opencode/skills/`.
- `/retro`: Review and discuss session transcripts — user types `/retro <session-id> [question]`, the harness fetches the condensed transcript via `confab retro`, optionally reads local raw JSONL for richer data, and engages in discussion about the session.

Skills are managed separately from hooks: `confab skills add/remove` (vs `confab hooks add/remove`).

## Releasing

Tag and push — GoReleaser handles the rest. See [RELEASING.md](RELEASING.md) for details.

```bash
git tag v0.X.Y
git push origin v0.X.Y
```

## Testing Notes

- Integration tests in `pkg/daemon/integration_test.go` test the full sync lifecycle
- Use `CONFAB_CLAUDE_DIR` / `CONFAB_CODEX_DIR` env vars to override Claude / Codex state directories for testing

## Development Practices

Priorities in order: **simplicity**, **correctness**, **efficiency**.

### Quality Standards

This is software that runs on user machines. Users trust us with their local environment. This demands high quality:

- **No "negligible" race conditions**: A 100ms race window is not acceptable. Race conditions erode trust and are difficult to debug when they manifest in the field.
- **No "unlikely" bugs**: If a code path can fail, assume it will. Users encounter edge cases we don't anticipate.
- **Updates are costly**: Patches require users to update. Getting it right the first time is far better than shipping fixes. Think through failure modes carefully before implementing.
- **Correctness over cleverness**: A simple, obviously correct solution beats an elegant but subtle one.

### AI provider wire formats

Before adding or changing a struct that decodes Claude Code / Codex payloads (hook stdin, transcript JSONL, rollout JSONL), verify the shape — don't assume. Sources: official docs, real local samples under `~/.claude` / `~/.codex`, and upstream source (Codex is open source at `openai/codex`). Same field name across providers is not the same shape; if upstream declares `any` / `true` / `oneOf`, use `json.RawMessage` or `any`, not `map[string]any`.

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
