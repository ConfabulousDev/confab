# pkg/logger

Singleton file logger with automatic rotation, level filtering, and test isolation.

## Files

| File | Role |
|------|------|
| `logger.go` | Logger implementation, singleton management, all log methods |

## Key API

```go
logger.Get()                          // Get singleton instance (auto-initializes)
logger.Get().Info("msg %s", arg)      // Log at INFO level
logger.Get().Error("failed: %v", err) // Log at ERROR level
logger.Get().ErrorPrint(...)          // Log to file AND print to stderr
logger.Get().SetLevel(logger.DEBUG)   // Change minimum log level
logger.Get().SetSession(ext, sess)    // Set "[ext=... sess=...]" prefix
```

## Design Decisions

**Singleton pattern.** All packages share one logger instance so session context (external ID, session ID) is set once and appears in all log lines. The alternative — passing a logger to every function — would be significantly more invasive for minimal benefit.

**Lumberjack for rotation.** Uses `gopkg.in/natefinch/lumberjack.v2` for automatic log rotation (1MB max size, 14 day retention, 20 backups, compressed). This is battle-tested and handles edge cases (rotation during write, permission issues) that a hand-rolled solution would miss.

**`ErrorPrint` exists separately.** Most errors are internal (sync failures, network issues) and only need to go to the log file. Some errors need user visibility (auth failures, setup issues). `ErrorPrint` writes to both the log file and stderr.

**Test isolation.** When `testing.Testing()` returns true, the logger auto-discards output unless `CONFAB_LOG_DIR` is explicitly set. This prevents tests from polluting the user's real log file at `~/.confab/logs/confab.log`.

## How to Extend

**Adding a new log level:** Add to the `Level` enum, add a method (e.g., `Trace()`), and handle it in `log()`. Consider whether existing levels are truly insufficient first.

**Adding structured fields:** The current logger uses printf-style formatting. If you need structured logging, consider whether the added complexity is justified — the log audience is primarily humans debugging issues.

## Invariants

- Thread-safe: all methods are mutex-protected.
- `Get()` must always return a usable logger — if `Init()` fails, `Get()` falls back to a stderr-only logger.
- `ResetForTesting()` is for tests only — it resets the singleton so the next `Get()` re-initializes.

## Dependencies

**Uses:** `gopkg.in/natefinch/lumberjack.v2`

**Used by:** nearly every package (via `logger.Get()`)
