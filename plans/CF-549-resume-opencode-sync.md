# CF-549: Resume OpenCode sessions — plugin only listens to session.created

## Problem

The OpenCode sync plugin (`pkg/provider/plugins/confab-sync.ts`) spawns the daemon **only** on `session.created`. OpenCode plugins are **in-process** (loaded at startup in the same Bun process). When a user resumes an existing session (a new `opencode` process), `session.created` does NOT fire because the session already exists. Only `session.status` and `session.updated` fire. Since the `running` set is per-process and starts empty, no daemon is spawned → data loss.

## Solution: Option (b) — resolve directory/parentID on Go side from SQLite

The `session.status`/`session.updated` events carry only `{sessionID}` (and `status`) — no `directory` or `parentID`. Option (b) resolves these from the OpenCode SQLite `session` table on the Go side.

## Detailed implementation

### Step 1: `pkg/provider/opencode_db.go` — Add `ReadSessionInfo`

Add a new exported method:

```go
// ReadSessionInfo fetches a session row's directory and parentID from the
// OpenCode SQLite DB. Returns empty strings if the session is not found
// (caller should proceed with best-effort defaults).
func (r *OpenCodeDBReader) ReadSessionInfo(ctx context.Context, sessionID string) (directory, parentID string, err error)
```

Query:
```sql
SELECT directory, COALESCE(parent_id, '') FROM session WHERE id = ?
```

**Design decisions:**
- Returns empty strings (not error) for not-found — the caller (SessionStart) should proceed with default values rather than fail entirely. A missing session row shouldn't block sync.
- Separate from `ReadSession` (which does a joined message+part query) because this is a single-row cheap lookup; no need for the full message machinery.
- Follows the same DB-open pattern as `ReadSession`: readonly mode, busy_timeout, defer close.

### Step 2: `cmd/hook_sessionstart.go` — Update `buildOpencodeLaunchArgs`

Current code:
```go
func buildOpencodeLaunchArgs(r io.Reader) (*daemonLaunchInput, error) {
    p := provider.Opencode{}
    in, err := p.ReadSessionHookInput(r)
    if err != nil {
        return nil, err
    }
    return &daemonLaunchInput{
        Provider:        p.Name(),
        ExternalID:      in.SessionID,
        CWD:             in.CWD,
        SessionParentID: in.ParentID,
    }, nil
}
```

New code:
```go
func buildOpencodeLaunchArgs(r io.Reader) (*daemonLaunchInput, error) {
    p := provider.Opencode{}
    in, err := p.ReadSessionHookInput(r)
    if err != nil {
        return nil, err
    }

    launch := &daemonLaunchInput{
        Provider:   p.Name(),
        ExternalID: in.SessionID,
    }

    // If the event carried cwd + parentID (session.created), use them directly.
    // Otherwise (session.status / session.updated), resolve from the SQLite DB.
    if in.CWD != "" {
        launch.CWD = in.CWD
        launch.SessionParentID = in.ParentID
    } else {
        cwd, parentID, err := resolveOpencodeSessionInfo(in.SessionID)
        if err != nil {
            // Non-fatal: log and proceed with defaults.
            // An empty cwd is handled by DefaultCWD() in the daemon;
            // an empty parentID means treated as root (correct for resume).
            logger.Warn("Failed to resolve OpenCode session info for %s: %v; using defaults", in.SessionID, err)
        }
        launch.CWD = cwd
        launch.SessionParentID = parentID
    }

    return launch, nil
}

// resolveOpencodeSessionInfo reads the session's directory and parent_id from
// the OpenCode SQLite DB. Empty strings are returned on error so the caller
// can proceed with best-effort defaults.
func resolveOpencodeSessionInfo(sessionID string) (cwd, parentID string, _ error) {
    dbPath, err := provider.OpenCodeDBPath()
    if err != nil {
        return "", "", fmt.Errorf("resolve db path: %w", err)
    }
    reader := provider.NewOpenCodeDBReader(dbPath)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    dir, pid, err := reader.ReadSessionInfo(ctx, sessionID)
    if err != nil {
        return "", "", err
    }
    return dir, pid, nil
}
```

**Design decisions:**
- DB lookup is a **best-effort fallback**, not a hard requirement. If the DB can't be opened or the session isn't found, we log a warning and proceed with empty defaults. This prevents a corrupt/missing DB from breaking the entire sync flow.
- 5-second context timeout prevents a hung DB from blocking the SessionStart hook indefinitely.
- The helper `resolveOpencodeSessionInfo` is a package-level function (not method) so it can be tested independently.
- No change to `maybeSpawnDaemon` or `daemonLaunchInput` — the same fields carry the resolved data.

### Step 3: `pkg/provider/plugins/confab-sync.ts` — Subscribe to resume events

Refactored event handler:

```typescript
event: async ({ event }) => {
  // Resume events (session.status / session.updated) only carry sessionID.
  // cwd/parentID are resolved on the Go side from the SQLite DB.
  if (event.type === "session.created") {
    const session = event.properties.info
    await spawn(session.id, session.directory, session.parentID)
  } else if (event.type === "session.status" || event.type === "session.updated") {
    // session.status fires on every idle↔active transition.
    // session.updated fires on any session state change.
    // Both are resume signals. The running set + maybeSpawnDaemon
    // provide dedup, so it's safe to be liberal with matching.
    await spawn(event.properties.sessionID, undefined, undefined)
  }
  // All other events are ignored.
},
```

**Key behavioral changes:**
- `session.status` fires on every idle↔active transition (user becomes active, AI finishes responding, etc.). With `running` set dedup, only the first one per process per session triggers a shell-out.
- `session.updated` is a coarser fallback. It fires on any session state change. Same dedup applies.
- No status-value filtering — we match any `session.status` regardless of value. This avoids depending on specific status enum strings from OpenCode.
- `session.idle`, `session.diff`, `session.error`, and other events continue to be ignored (they're not resume signals).

### Step 4: `pkg/provider/opencode.go` — Update embedded plugin source

The `opencodePluginSourceRaw` constant must be updated to match the refactored event handler. Both files (`confab-sync.ts` and the raw string) must be kept in sync — the test `TestOpencodePluginSourceMatchesFile` asserts this.

**Change process:**
1. Edit `pkg/provider/plugins/confab-sync.ts` — the development source
2. Copy the new source into `opencodePluginSourceRaw` with `§BT§` backtick escape
3. Run `TestOpencodePluginSourceMatchesFile` to confirm

### Step 5: `pkg/provider/README.md` — Document `ReadSessionInfo`

The session info resolver in `buildOpencodeLaunchArgs` is internal to `cmd/`, but `ReadSessionInfo` is exported from `pkg/provider/opencode_db.go`.

### No changes needed:

- `cmd/spawn.go` — `daemonLaunchInput`, `launchAsHookInput`, `maybeSpawnDaemon` — unchanged. The resolved cwd/parentID flow through existing fields.
- `pkg/provider/opencode.go` (`ShouldSpawnForInput`) — unchanged. Already handles the resolved parentID correctly.
- `pkg/provider/plugins/types/opencode-plugin.d.ts` — type stubs already include `session.status` and `session.updated`. The catch-all on line 30 covers any event type.

## Data flow for resume path

```
OpenCode resumes session → fires session.status(sessionID="abc")
  ↓
TS plugin event handler: event.type === "session.status"
  ↓
spawn("abc", undefined, undefined)
  ↓
running.has("abc")? → No → running.add("abc")
  ↓
echo '{"session_id":"abc"}' | confab hook session-start --provider opencode
  ↓
buildOpencodeLaunchArgs reads stdin → OpenCodeHookInput{SessionID: "abc", CWD: ""}
  ↓
CWD is empty → resolveOpencodeSessionInfo("abc")
  ↓
OpenCodeDBReader.ReadSessionInfo(ctx, "abc") → {directory: "/home/user/proj", parentID: ""}
  ↓
daemonLaunchInput{ExternalID: "abc", CWD: "/home/user/proj", SessionParentID: ""}
  ↓
maybeSpawnDaemon → ShouldSpawnForInput (parentID empty → true) → no existing daemon → spawn
```

## Dedup layers (summary for clarity)

| Layer | Scope | Mechanism |
|---|---|---|
| TS `running` set | Per `opencode` process | `if (running.has(id)) return` before any shell-out |
| Go `maybeSpawnDaemon` | Cross-process | `LoadStateForProvider` + `IsDaemonRunning()` check before fork |

Within one OpenCode invocation: first event triggers spawn, subsequent events for same session are instant TS-level no-ops.
Across OpenCode restarts: first event in the new process triggers one shell-out, Go dedup catches it if daemon still alive.

## Test strategy

### Plugin tests (`pkg/provider/plugins/confab-sync.test.ts`)

Following existing patterns (each test creates a fresh plugin instance):
1. **session.status spawns daemon**: Fire `session.status({sessionID: "s1", status: "active"})` → verify `confab hook session-start` called with session_id, no cwd/parent_id.
2. **session.status any value spawns**: Fire `session.status({sessionID: "s1", status: "idle"})` → verify spawn still called (we don't filter by status value).
3. **session.updated spawns daemon**: Fire `session.updated({sessionID: "s1"})` → verify spawn called with session_id only.
4. **session.status + session.updated dedup**: Fire multiple resume events for same session → verify only one spawn call.
5. **session.created still works**: Existing tests pass unchanged.
6. **session.created dedup with resume event**: Fire `session.created`, then `session.status` for same session → no second spawn.

### Go unit tests (`pkg/provider/opencode_db_test.go`)

New test file:
1. **TestOpenCodeDBReadSessionInfo**: Insert session row with known directory + parentID, query it back.
2. **TestOpenCodeDBReadSessionInfoRoot**: Root session (null parent_id) → returns empty parentID string.
3. **TestOpenCodeDBReadSessionInfoNotFound**: Unknown session ID → returns empty strings, no error.

### Go unit tests (`cmd/hook_sessionstart_test.go` or new file)

Following existing patterns in `cmd/` package:
4. **TestBuildOpencodeLaunchArgsUsesInlineCWD**: Input with non-empty CWD → no DB lookup, CWD from input.
5. **TestBuildOpencodeLaunchArgsResolvesFromDB**: Input with empty CWD → resolves directory/parentID from DB.
6. **TestBuildOpencodeLaunchArgsDBErrorGraceful**: DB unavailable → falls through with empty defaults (log warning, no error return).

### Existing tests that must still pass

- `TestOpencodePluginSourceMatchesFile` — plugin source matches embedded raw string
- `TestOpencodeShouldSpawnRootSession` / `TestOpencodeShouldSpawnSuppressesSubagent` — subagent suppression
- `TestOpencodeReadSessionHookInput_*` — input parsing unchanged
- All plugin vitest tests (run via `npm test` in the plugin dir)

## Edge cases and error handling

| Scenario | Behavior | Rationale |
|---|---|---|
| DB file missing during resume | Log warning, proceed with empty cwd/parentID | Non-fatal; daemon sync works with defaults |
| DB locked/busy (timeout) | Log warning, proceed with empty cwd/parentID | Same; don't block SessionStart for DB contention |
| Session not in DB (deleted?) | Log warning, proceed with empty cwd/parentID | Still try to sync; daemon may still discover data |
| Resume subagent session | DB returns parentID → ShouldSpawnForInput returns false → suppressed | Root-only gate preserved |
| Rapid session.status events | `running` set absorbs all but first | No redundant shell-outs |
| Resume after daemon crash | Go checks state file → PID dead → new daemon spawned | Re-spawn safety net |
| cwd resolved as empty string | Daemon uses DefaultCWD() (dirname of transcript path) | Graceful degradation |
| parentID resolved as empty string | ShouldSpawnForInput treats as root | Correct for root session resume |

## Files to modify (summary)

| File | Change type |
|---|---|
| `pkg/provider/opencode_db.go` | Add `ReadSessionInfo` method (~30 lines) |
| `pkg/provider/opencode_db_test.go` | New test file (~80 lines) |
| `cmd/hook_sessionstart.go` | Update `buildOpencodeLaunchArgs`, add `resolveOpencodeSessionInfo` (~50 lines) |
| `cmd/hook_sessionstart_test.go` | New test file or extend existing (~80 lines) |
| `pkg/provider/plugins/confab-sync.ts` | Update event handler (~15 lines changed) |
| `pkg/provider/opencode.go` | Update `opencodePluginSourceRaw` (sync with .ts) |
| `pkg/provider/plugins/confab-sync.test.ts` | Add resume event tests (~80 lines) |
| `CLAUDE.md` | Update OpenCode provider differences section |
| `pkg/provider/README.md` | Add `ReadSessionInfo` to exported API |
| `cmd/README.md` | Update if it lists session-start handler details |
