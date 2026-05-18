# pkg/loginit

Startup wiring that connects user configuration to the logger. Lives in its own package so neither `pkg/config` nor `pkg/logger` has to import the other for startup orchestration.

## Files

| File | Role |
|------|------|
| `loginit.go` | `ApplyLogLevel()` — reads `log_level` from upload config and applies it |

## Key API

- **`ApplyLogLevel()`** — called from `cmd/root.go`'s `PersistentPreRun`. Silently no-ops if the config can't be read; logs a warning and leaves the default level in place if `log_level` is set to an unrecognized value.

## Why it exists

Historically `ApplyLogLevel` lived in `pkg/config`, but moving the path helper for `~/.confab` (CF-463) needed `pkg/logger` to depend on a `pkg/config` helper — which would have created an import cycle (`pkg/config` already imports `pkg/logger`). Splitting the startup orchestration out keeps the dependency graph one-way: both `pkg/config` and `pkg/logger` depend on `pkg/confabpath`, and `pkg/loginit` sits above both.

## Testing

```bash
go test ./pkg/loginit/...
```

Tests use `CONFAB_LOG_DIR` to redirect the logger to a temp directory and verify behavior by checking what was actually emitted to the log file (debug probe → check whether it appeared).

## Dependencies

**Uses:** `pkg/config` (for `GetUploadConfig` + `ParseLogLevel`), `pkg/logger` (for `Get().SetLevel` and warning emission).

**Used by:** `cmd/` (called once at process startup from `rootCmd.PersistentPreRun`).
