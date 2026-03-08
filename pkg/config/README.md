# pkg/config

Configuration management for two separate config systems: Confab's own config and Claude Code's settings (hooks).

## Files

| File | Role |
|------|------|
| `config.go` | Claude Code settings management: read/write `~/.claude/settings.json`, hook install/uninstall |
| `upload.go` | Confab config: read/write `~/.confab/config.json`, validation, default redaction patterns |
| `paths.go` | Path resolution with environment variable overrides |

## Two Config Systems

### Confab config (`~/.confab/config.json`)
Managed by `upload.go`. Contains backend URL, API key, log level, auto-update flag, and redaction settings. This is Confab's own config — we control the schema entirely.

### Claude Code settings (`~/.claude/settings.json`)
Managed by `config.go`. Contains hooks that Claude Code reads to fire events. We install/uninstall hooks here, but Claude Code owns the file and other tools may write to it concurrently.

## Key Types

- **`UploadConfig`** — Confab's configuration (backend URL, API key, redaction settings)
- **`ClaudeSettings`** — Wrapper around `map[string]any` for Claude Code settings, preserving unknown fields
- **`RedactionConfig`** — Redaction enabled flag, use_default_patterns, custom pattern list
- **`RedactionPattern`** — Individual redaction pattern (name, regex, type, capture group, field pattern)

## How to Extend

### Adding a new Confab config field
1. Add the field to `UploadConfig` in `upload.go`
2. Add validation in `SaveUploadConfig()` if needed
3. Update the setup flow in `cmd/setup.go` to prompt for / set the field

### Adding a new hook type
This spans multiple packages. On the config side:

1. Add `Install<Name>Hook()` in `config.go` — follow the pattern of existing hooks:
   - `SessionStart`/`SessionEnd` use `"*"` matchers
   - `PreToolUse`/`PostToolUse` use tool name matchers (e.g., `Bash`, `mcp__github`)
   - `UserPromptSubmit` has no matcher
2. Add `Uninstall<Name>Hook()` — must handle both old and new command patterns
3. Add `Is<Name>HookInstalled()` — for status checking
4. Then update: `cmd/hooks.go` (install/uninstall calls), `cmd/status.go` (status check), `cmd/setup.go` (setup flow), `cmd/hook.go` (dispatch)

## Invariants

- **Settings writes must use `AtomicUpdateSettings()`.** This provides read-modify-write with mtime-based optimistic locking and exponential backoff retry (max 10 attempts). Never read + write separately — concurrent Claude Code sessions will clobber each other.
- **Hook install must be idempotent.** If the hook already exists, update it in place. Never duplicate hooks.
- **Hook uninstall must handle old command patterns.** Users may have hooks installed by older Confab versions with different command strings. Uninstall must find and remove these too.
- **Config file permissions:** `0600` for `~/.confab/config.json` (contains API key), `0644` for `~/.claude/settings.json` (needs to be readable by Claude Code).
- **`GetDefaultRedactionPatterns()` pattern order matters.** More specific patterns (e.g., `sk-ant-api03-...`) must come before general ones (e.g., field-name-based patterns) to avoid partial matches.

## Design Decisions

**`ClaudeSettings` uses `map[string]any` instead of typed structs.** Claude Code's settings schema evolves rapidly and includes fields we don't manage. A typed struct would silently drop unknown fields on round-trip. The raw map preserves everything.

**Mtime-based optimistic locking instead of flock.** `AtomicUpdateSettings()` checks that the file's mtime hasn't changed between read and write. If it has, it retries with backoff. This is simpler than file locking, works cross-platform, and is sufficient for the infrequent writes that hooks installation involves.

**Hook matchers vary by type.** `SessionStart`/`SessionEnd` use `"*"` (fire for all events). `PreToolUse`/`PostToolUse` use tool name arrays to target specific tools (e.g., `["Bash"]`, `["mcp__github"]`). `UserPromptSubmit` has no matcher (fires for all prompts). This matches Claude Code's hook specification.

## Testing

```bash
go test ./pkg/config/...
```

Tests cover hook installation/uninstallation, atomic updates under concurrency, field preservation across round-trips, and config validation.

## Dependencies

**Uses:** standard library only

**Used by:** `cmd/` (setup, login, hooks, status), `pkg/discovery/` (paths), `pkg/sync/` (upload config), `pkg/daemon/` (state dir), `pkg/logger/` (log level)
