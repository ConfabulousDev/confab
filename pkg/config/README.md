# pkg/config

Configuration management for Confab's own config, Claude Code's settings file, and bundled skill file content.

Hook install/uninstall logic lives in `pkg/hookconfig`. This package owns the generic plumbing — atomic settings updates, settings struct, paths — and the bundled skill templates installed by provider clients.

## Files

| File | Role |
|------|------|
| `config.go` | `ClaudeSettings` struct + `AtomicUpdateSettings` (read/modify/write `~/.claude/settings.json` with mtime-based optimistic locking). Generic accessor helpers: `GetHooksMap`, `GetEventHooks`, `SetEventHooks`. Tool-name constants used by `pkg/hookconfig`. |
| `upload.go` | Confab config: read/write `~/.confab/config.json`, validation, default redaction patterns, `ParseLogLevel` |
| `paths.go` | Claude state-dir resolution (`~/.claude`) with `CONFAB_CLAUDE_DIR` override. `~/.confab` paths use `pkg/confabpath`. |
| `bundled_skills.go` | Shared bundled-skill registry plus install/uninstall/check and `ReconcileBundledSkills` (install current + prune retired) helpers for provider-local `skills/<name>/SKILL.md` layouts |
| `skill_retro.go` | `/retro` templates for Claude Code and Codex plus legacy Claude helper wrappers |

## Two Config Systems

### Confab config (`~/.confab/config.json`)
Managed by `upload.go`. Contains backend URL, API key, log level, auto-update flag, and redaction settings. This is Confab's own config — we control the schema entirely.

### Claude Code settings (`~/.claude/settings.json`)
Managed by `config.go`. Contains hooks that Claude Code reads to fire events. We install/uninstall hooks here, but Claude Code owns the file and other tools may write to it concurrently.

### Bundled provider skills
Managed by `bundled_skills.go`, `skill_til.go`, and `skill_retro.go` (and future `skill_*.go` files). Skills are standalone `SKILL.md` files installed by provider clients into their local skill layouts: Claude uses `~/.claude/skills/<name>/SKILL.md`; Codex uses `~/.codex/skills/<name>/SKILL.md`. If an existing `SKILL.md` has been customized by the user, install backs it up to `SKILL.md.bak` before overwriting; if the backup write fails, the install aborts rather than silently overwrite.

## Key Types

- **`UploadConfig`** — Confab's configuration (backend URL, API key, redaction settings)
- **`ParseLogLevel(string)`** — translates a config `log_level` value to `logger.Level`. Called from `pkg/loginit` at process startup.
- **`ClaudeSettings`** — Wrapper around `map[string]any` for Claude Code settings, preserving unknown fields
- **`ErrHooksTypeMismatch`** — Exported sentinel error returned when the `"hooks"` field in `settings.json` exists but is not a JSON object. Callers can check `errors.Is(err, ErrHooksTypeMismatch)` and surface a clear message asking users to fix the file manually.
- **`RedactionConfig`** — Redaction enabled flag, use_default_patterns, custom pattern list
- **`RedactionPattern`** — Individual redaction pattern (name, regex, type, capture group, field pattern)

## How to Extend

### Adding a new Confab config field
1. Add the field to `UploadConfig` in `upload.go`
2. Add validation in `SaveUploadConfig()` if needed
3. Update the setup flow in `cmd/setup.go` to prompt for / set the field

### Adding a new hook type
Hook install/uninstall lives in `pkg/hookconfig` — see that package's README. The wiring into `cmd/` flows through `pkg/provider`'s `Provider` interface: `cmd/hooks.go` and `cmd/setup.go` call `p.InstallHooks()`, which delegates to `hookconfig` per provider.

### Adding a new bundled skill
1. Add the provider-rendered template content in `skill_<name>.go`.
2. Add the skill name to `bundledSkillNames` and route it in `bundledSkillTemplate`.
3. Keep path/layout decisions in `pkg/provider`; `pkg/config` only receives a state directory and provider name.
4. Add/update tests for Claude and Codex installs so both provider paths stay covered.

## Invariants

- **Settings writes must use `AtomicUpdateSettings()`.** This provides read-modify-write with mtime-based optimistic locking and exponential backoff retry (max 10 attempts). Never read + write separately — concurrent Claude Code sessions will clobber each other.
- **Config file permissions:** `0600` for `~/.confab/config.json` (contains API key), `0600` for `~/.claude/settings.json`.
- **Directory permissions:** `0700` for `~/.confab/` and `~/.claude/` directories created by Confab. Restrictive permissions prevent other users on shared systems from reading config or API keys.
- **`GetDefaultRedactionPatterns()` pattern order matters.** More specific patterns (e.g., `sk-ant-api03-...`) must come before general ones (e.g., field-name-based patterns) to avoid partial matches.

## Design Decisions

**`ClaudeSettings` uses `map[string]any` instead of typed structs.** Claude Code's settings schema evolves rapidly and includes fields we don't manage. A typed struct would silently drop unknown fields on round-trip. The raw map preserves everything.

**Mtime-based optimistic locking instead of flock.** `AtomicUpdateSettings()` checks that the file's mtime hasn't changed between read and write. If it has, it retries with backoff. This is simpler than file locking, works cross-platform, and is sufficient for the infrequent writes that hooks installation involves.

**Bundled skills use provider-rendered templates.** The shipped skills share a registry, but content can differ where the harnesses expose different session IDs or local transcript layouts. `ReconcileBundledSkills` installs the current bundle and prunes any retired skills (e.g. the removed `/til`) left by older confab versions.


## Testing

```bash
go test ./pkg/config/...
```

Tests cover atomic settings updates under concurrency, field preservation across round-trips, config validation, and bundled skill install/uninstall behavior. Hook install/uninstall tests live in `pkg/hookconfig`.

## Dependencies

**Uses:** `pkg/confabpath` (`~/.confab` path-builder for `getConfigPath`), `pkg/logger` (logging from `config.go`, `skill_*.go`). `paths.go` deliberately does not import `pkg/provider` even though it owns parallel constants — `pkg/provider` imports `pkg/hookconfig`, which imports `pkg/config`. The duplicated `ClaudeStateDirEnv` constant must stay in sync between the two packages.

**Used by:** `cmd/` (setup, login, hooks, status), `pkg/daemon/` (state dir), `pkg/hookconfig/` (settings struct, atomic update, tool-name constants), `pkg/http/` (upload config), `pkg/loginit/` (`GetUploadConfig`, `ParseLogLevel`), `pkg/provider/` (provider paths, skills install), `pkg/redactor/` (redaction patterns), `pkg/sync/` (upload config)
