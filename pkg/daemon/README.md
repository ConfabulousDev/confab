# pkg/daemon

Background sync daemon for provider transcripts: Claude Code transcript JSONL, Codex rollout JSONL, or an OpenCode session materialized from its local SQLite DB. One daemon runs per active Claude session, Codex root tree, or OpenCode root session.

## Files

| File | Role |
|------|------|
| `daemon.go` | `Daemon` struct, `Run` loop, sync cycles, shutdown, inbox I/O, parent monitoring. Parent-PID liveness lives in a dedicated `monitorParent` goroutine that ticks at `parentCheckInterval` (5s; `var` so tests can override) and closes `parentDeathCh` on death; the main loop's `select` drains that and shuts down with reason `"parent process exited"`. The goroutine runs under a `context.WithCancel(ctx)` deferred-cancel so it exits on every `Run()` return path, not just when the caller's ctx cancels. For OpenCode (`d.providerName == provider.NameOpencode`) also starts/stops the root `provider.OpenCodeCollector` goroutine (backed by `provider.OpenCodeDBReader`) and derives the materialized transcript path. Holds the shared `dbReader`, `childCollectorBase` context, `childCollectorCancel`, and `childCollectors` map used by the CF-538 subagent sidechain logic in `opencode_children.go`. |
| `opencode_children.go` | CF-538 OpenCode subagent sidechain capture: `opencodeChildCollector` (per-descendant cancel/done handles), `opencodeRegistrar` (the `provider.OpencodeDescendantRegistrar` implementation injected via `engine.SetDescendantRegistrar`), `startChildCollector` (idempotent goroutine spawn under the daemon's `childCollectorBase` context), `childCollectorDones` (snapshot for shutdown to wait on), and `waitForCollectors` (single shared timeout for root + children). |
| `state.go` | `State` persistence (`~/.confab/sync/{provider}/{id}.json`, with legacy flat-path fallback), process liveness checks, listing. Path builders are thin wrappers over `pkg/confabpath`. `(*State).DeleteWithInbox` removes both the state file and the inbox file together — used by both `shutdown` and the reaper so the two-file cleanup stays consistent. |
| `reaper.go` | `ReapStaleStates()` — provider-agnostic sweep that removes state + inbox files whose PID is no longer alive. Files younger than `reapMinAge` (5s) are skipped to protect freshly-spawned daemons. Called as a goroutine from `cmd/hook_sessionstart.go` on every session-start so cleanup is opportunistic and invisible to the user (CF-549 F-up A). |

## Lifecycle

```
spawn ──> waitForTranscript (poll 2s, timeout 60s)
              │
              ▼
         save state file
              │
              ▼
         sync loop ◄──────────────────┐
           │                          │
           ├── tryInit (lazy auth)    │
           ├── SyncAll (engine)       │
           ├── check parent alive     │
           └── sleep(30s ± 5s jitter)─┘
              │
              ▼ (stop signal / parent dead / context cancel)
         shutdown
           ├── read inbox events (SessionEnd payload)
           ├── final sync (with 30s timeout)
           ├── send session_end event
           ├── delete state file
           └── delete inbox file
```

## Key Types

- **`Config`** — Daemon configuration: external ID, transcript path, CWD, parent PID, sync interval/jitter
- **`Daemon`** — Runtime state: engine, stop/done channels, consecutive error counter
- **`State`** — Persisted to disk: external ID, paths, PIDs, start time, backend session ID

## How to Extend

**Adding daemon behavior during sync:** Hook into the sync loop in `Run()`. New behavior should go after the `tryInit()` / `engine.SyncAll()` calls. Follow the existing error handling pattern — log errors, don't crash.

**Adding a new inbox event type:** Add the type string constant. `writeInboxEvent()` and `readInboxEvents()` are generic — they serialize/deserialize `InboxEvent` structs. Handle the new type in `shutdown()` where inbox events are processed.

**Adding new state fields:** Add to the `State` struct in `state.go`. The state is JSON-serialized, so new fields are backwards-compatible with `omitempty`.

## Invariants

- **State directory permissions are 0700.** `~/.confab/sync/` is created with restrictive permissions since state files may contain session metadata.
- **Signal channel buffer is 2** to avoid dropping signals when both SIGINT and SIGTERM arrive in quick succession.
- **Shutdown goroutine has panic recovery** to ensure state file cleanup even if shutdown logic panics.
- **State file must be deleted on exit.** If a state file exists with a dead PID, it blocks future daemon spawns until cleanup. The panic recovery handler also deletes the state file.
- **Shutdown must have a timeout** (`shutdownTimeout`, default 30s). The backend may be unresponsive, and the daemon must not hang forever.
- **Parent PID monitoring uses `signal(0)`, not `/proc`.** `os.FindProcess` + `Signal(0)` works on both macOS and Linux. `/proc` is Linux-only.
- **Daemon must be resilient to backend unavailability.** Never crash on network errors. Log the error and retry on the next sync interval.
- **Inbox file must be cleaned up on shutdown.** Stale inbox files don't cause bugs but are unnecessary clutter.
- **`Stop()` is idempotent** (uses `sync.Once`). Multiple callers (signal handler, parent monitor, explicit stop) can all call `Stop()` safely.
- **Consecutive 404 detection.** After 3 consecutive 404 errors (`maxConsecutiveNotFound`), the daemon shuts down — the session was deleted from the backend.
- **Auth recovery.** On `ErrUnauthorized`, the engine is reset to force config re-read on the next cycle. This allows users to fix their API key without restarting the daemon.
- **Codex: one daemon per root tree, not per rollout.** The hook handler walks every Codex `SessionStart` event up to its top-most root before spawning, so state files are keyed by root UUID. The running root daemon calls provider descendant discovery each sync cycle and uploads verified subagent rollouts as sidechain files. `SessionStart` events for already-running trees become no-ops.
- **OpenCode: collector materializes the data source.** OpenCode has no transcript file, so when `d.providerName == provider.NameOpencode` the daemon derives `~/.confab/opencode/<id>/messages.jsonl` (via `openCodeMaterializedPath`), points `transcriptPath` at it, and runs a `provider.OpenCodeCollector` goroutine. The collector reads OpenCode's local SQLite DB via `provider.NewOpenCodeDBReader(provider.OpenCodeDBPath())` (path is `CONFAB_OPENCODE_DB` → `$XDG_DATA_HOME/opencode/opencode.db` → `~/.local/share/opencode/opencode.db`) and polls at `d.syncInterval` — so the same `CONFAB_SYNC_INTERVAL_MS` knob tunes both backend sync + the SQLite poll. The collector is started **after** the no-op `waitForTranscript` (the file does not exist yet) and `backendSyncEnabled()` gates `Init`/`SyncAll` on the file existing — so no empty backend session is created before the first complete message. Root-session subagents never reach here: `Opencode.ShouldSpawnForInput` refuses them at spawn time.
- **OpenCode subagent sidechain capture (CF-538, in `opencode_children.go`).** Alongside the root collector, the daemon owns a `childCollectors` pool of per-descendant `OpenCodeCollector` goroutines. `opencodeRegistrar` wraps `*sync.FileTracker`, satisfies `provider.OpencodeDescendantRegistrar`, and is injected via `engine.SetDescendantRegistrar` inside `tryInit` (rebuilt fresh after auth-failure reset). Each `SyncAll` cycle the OpenCode provider's `DiscoverDescendants` calls `RegisterOpencodeChild(childID, localPath)`; the registrar checks `engine.OpencodeChildFilesAllowed()` (the `opencode_subagent_files` capability flag, paired with CF-539), registers the child file (backend `file_name = opencode/<child>/messages.jsonl`, `file_type = agent`) via `FileTracker.RegisterSidechainFile`, and idempotently spawns a collector goroutine through `startChildCollector`. Children share the daemon's `*OpenCodeDBReader` instance and the `childCollectorBase` context (a child of the daemon's main `ctx`). `shutdown()` cancels the root + every child collector and waits for all `done` channels under a single 2s ceiling (`waitForCollectors`) before the final sync; a wedged collector logs Warn but cannot block shutdown indefinitely. Vanished children (deleted in OpenCode mid-session) keep their collectors running — the collector's 1-Warn-per-minute reconcile-error cadence surfaces the stuck state.

## Design Decisions

**Lazy authentication.** The daemon starts immediately when the provider launches a session, but the user may not have authenticated yet. `tryInit()` defers backend communication until the first sync cycle, and handles auth failures gracefully.

**Jittered sync interval.** The base interval is 30s with ±5s random jitter. This prevents thundering herd when multiple sessions start simultaneously. The jitter is applied per-cycle, not just at startup.

**State files with PID-based liveness check.** The state file stores the daemon PID. `IsDaemonRunning()` sends signal 0 to check if the process is still alive. This is more reliable than lock files (which can be orphaned) and simpler than IPC.

**Panic recovery deletes state file.** If the daemon panics, the recovery handler logs the panic and deletes the state file. This prevents a corrupt daemon from permanently blocking future spawns. A clean restart is preferred over trying to recover from unknown state.

**Inbox file for IPC.** The `sync stop` command needs to pass the `SessionEnd` hook payload to the running daemon. Rather than building an IPC mechanism (socket, pipe), the stop command appends the event to an inbox JSONL file, then sends SIGTERM. The daemon reads the inbox during shutdown. This is simple and reliable.

## Testing

```bash
go test ./pkg/daemon/...
```

### Unit tests (`daemon_test.go`, `state_test.go`)
- Stop/cancel behavior, idempotency
- Inbox write/read/cleanup
- Auth recovery on 401
- State CRUD, process checks, listing

### Integration tests (`integration_test.go`)
Full lifecycle tests with mock HTTP backend. Key scenarios:
- Sync cycle (init + upload)
- Retry on backend errors
- Agent discovery (including late-appearing agents)
- Incremental sync (only new lines)
- Multiple sync cycles with appended content
- Late-appearing transcript file
- Shutdown with final sync
- Concurrent startup protection
- File truncation resilience
- Large files and chunk size limits

Override `shutdownTimeout` (package var) in tests for fast execution. Use `CONFAB_CLAUDE_DIR` / `CONFAB_CODEX_DIR` to isolate test directories per provider.

## Dependencies

**Uses:** `pkg/sync`, `pkg/config`, `pkg/confabpath`, `pkg/http`, `pkg/types`, `pkg/logger`

**Used by:** `cmd/` (spawn, sync start/stop, status)
