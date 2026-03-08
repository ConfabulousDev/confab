# pkg/daemon

Background sync daemon that monitors a Claude Code session transcript and uploads it incrementally to the backend. One daemon runs per active session.

## Files

| File | Role |
|------|------|
| `daemon.go` | `Daemon` struct, `Run` loop, sync cycles, shutdown, inbox I/O, parent monitoring |
| `state.go` | `State` persistence (`~/.confab/sync/{id}.json`), process liveness checks, listing |

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

- **State file must be deleted on exit.** If a state file exists with a dead PID, it blocks future daemon spawns until cleanup. The panic recovery handler also deletes the state file.
- **Shutdown must have a timeout** (`shutdownTimeout`, default 30s). The backend may be unresponsive, and the daemon must not hang forever.
- **Parent PID monitoring uses `signal(0)`, not `/proc`.** `os.FindProcess` + `Signal(0)` works on both macOS and Linux. `/proc` is Linux-only.
- **Daemon must be resilient to backend unavailability.** Never crash on network errors. Log the error and retry on the next sync interval.
- **Inbox file must be cleaned up on shutdown.** Stale inbox files don't cause bugs but are unnecessary clutter.
- **`Stop()` is idempotent** (uses `sync.Once`). Multiple callers (signal handler, parent monitor, explicit stop) can all call `Stop()` safely.
- **Consecutive 404 detection.** After 3 consecutive 404 errors (`maxConsecutiveNotFound`), the daemon shuts down — the session was deleted from the backend.
- **Auth recovery.** On `ErrUnauthorized`, the engine is reset to force config re-read on the next cycle. This allows users to fix their API key without restarting the daemon.

## Design Decisions

**Lazy authentication.** The daemon starts immediately when Claude Code launches a session, but the user may not have authenticated yet. `tryInit()` defers backend communication until the first sync cycle, and handles auth failures gracefully.

**Jittered sync interval.** The base interval is 30s with ±5s random jitter. This prevents thundering herd when multiple Claude Code sessions start simultaneously. The jitter is applied per-cycle, not just at startup.

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

Override `shutdownTimeout` (package var) in tests for fast execution. Use `CONFAB_CLAUDE_DIR` to isolate test directories.

## Dependencies

**Uses:** `pkg/sync`, `pkg/config`, `pkg/discovery`, `pkg/types`, `pkg/logger`, `pkg/redactor`

**Used by:** `cmd/` (spawn, sync start/stop, status)
