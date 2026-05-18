# pkg/confabpath

Path-builder helpers for the `~/.confab` directory, where all confab local state lives (config, sync state, inboxes, logs, update timestamps).

This is a stdlib-only leaf package so it can be imported by any package without introducing cycles — notably both `pkg/config` and `pkg/logger`, which historically couldn't share a path helper because `pkg/config` already imports `pkg/logger`.

## Files

| File | Role |
|------|------|
| `confabpath.go` | `Dir()` and `Subpath(first, rest...)` helpers |

## Key API

- **`Dir() (string, error)`** — returns `~/.confab` (absolute). Wraps `os.UserHomeDir` errors with `"failed to get home directory: %w"`.
- **`Subpath(first string, rest ...string) (string, error)`** — joins `~/.confab` with the given segments. The first segment is required by the signature, forcing callers to express intent at the call site (use `Dir()` if you really want just `~/.confab`).

## Invariants

- **Helpers do not cache `os.UserHomeDir`.** Several tests in `pkg/daemon` redirect `HOME` between subtests via `os.Setenv`; caching would silently break them. The lookup is essentially a getenv call, so the cost is negligible.
- **Error wrap text is stable.** `"failed to get home directory: %w"` is preserved verbatim so any log-line consumers continue to match.
- **`Subpath`'s first segment is required by signature.** No runtime panic — the type system enforces it.

## Out of scope

- `~/.claude` and `~/.codex` paths — those have their own helpers in `pkg/config` (`GetClaudeStateDir`) and `pkg/provider` respectively, with `CONFAB_CLAUDE_DIR` / `CONFAB_CODEX_DIR` env-override semantics.
- `~/.local/bin` — install/update destination paths live in `cmd/install.go` and `cmd/update.go`.

## Testing

```bash
go test ./pkg/confabpath/...
```

Tests redirect `HOME` via `t.Setenv` so they never touch the real home directory.

## Dependencies

**Uses:** standard library only.

**Used by:** `pkg/config` (`getConfigPath`), `pkg/daemon` (state and inbox path builders), `pkg/logger` (default log dir), `cmd/update.go` (auto-update check timestamp).
