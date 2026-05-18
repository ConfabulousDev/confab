# pkg/config

Configuration management for two separate config systems: Confab's own config and Claude Code's settings file.

Hook install/uninstall logic lives in `pkg/hookconfig`. This package owns the generic plumbing — atomic settings updates, settings struct, paths — and the Claude Code skill files.

## Files

| File | Role |
|------|------|
| `config.go` | `ClaudeSettings` struct + `AtomicUpdateSettings` (read/modify/write `~/.claude/settings.json` with mtime-based optimistic locking). Generic accessor helpers: `GetHooksMap`, `GetEventHooks`, `SetEventHooks`. Tool-name constants used by `pkg/hookconfig`. |
| `upload.go` | Confab config: read/write `~/.confab/config.json`, validation, default redaction patterns |
| `paths.go` | Path resolution with environment variable overrides |
| `skill.go` | Generic skill installer (`skill` struct + `path`/`Install`/`Uninstall`/`Installed`). Each skill file in this package collapses to a template + a `var` of this type + three thin wrappers. |
| `skill_til.go` | `/til` Claude Code skill: template + thin wrappers around `skill.Install/Uninstall/Installed`. Installs to `~/.claude/skills/til/`. |
| `skill_retro.go` | `/retro` Claude Code skill: template + thin wrappers around `skill.Install/Uninstall/Installed`. Installs to `~/.claude/skills/retro/`. |

## Two Config Systems

### Confab config (`~/.confab/config.json`)
Managed by `upload.go`. Contains backend URL, API key, log level, auto-update flag, and redaction settings. This is Confab's own config — we control the schema entirely.

### Claude Code settings (`~/.claude/settings.json`)
Managed by `config.go`. Contains hooks that Claude Code reads to fire events. We install/uninstall hooks here, but Claude Code owns the file and other tools may write to it concurrently.

### Claude Code skills (`~/.claude/skills/`)
Managed by `skill.go` (generic installer) plus one `skill_<name>.go` per skill (`skill_til.go`, `skill_retro.go`, and future ones). Skills are SKILL.md files that extend Claude Code with custom slash commands. Unlike hooks (which live in settings.json), skills are standalone files in the skills directory. If an existing SKILL.md has been customized by the user, `Install` backs it up to `SKILL.md.bak` before overwriting; if the backup write fails, the install aborts rather than silently overwrite.

## Key Types

- **`UploadConfig`** — Confab's configuration (backend URL, API key, redaction settings)
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

### Adding a new Claude Code skill
1. Create `pkg/config/skill_<name>.go` with the SKILL.md template constant and `var <name>Skill = skill{name: "<name>", template: <name>SkillTemplate}`.
2. Add three public wrappers: `Install<Name>Skill() error { return <name>Skill.Install() }`, `Uninstall<Name>Skill() error { return <name>Skill.Uninstall() }`, `Is<Name>SkillInstalled() bool { return <name>Skill.Installed() }`.
3. Wire those wrappers into `cmd/skills.go` (add/remove), `cmd/announce.go` (setup step), `cmd/status.go` (status check), and `pkg/provider/claude.go` (install during provider setup).

## Invariants

- **Settings writes must use `AtomicUpdateSettings()`.** This provides read-modify-write with mtime-based optimistic locking and exponential backoff retry (max 10 attempts). Never read + write separately — concurrent Claude Code sessions will clobber each other.
- **Config file permissions:** `0600` for `~/.confab/config.json` (contains API key), `0600` for `~/.claude/settings.json`.
- **Directory permissions:** `0700` for `~/.confab/` and `~/.claude/` directories created by Confab. Restrictive permissions prevent other users on shared systems from reading config or API keys.
- **`GetDefaultRedactionPatterns()` pattern order matters.** More specific patterns (e.g., `sk-ant-api03-...`) must come before general ones (e.g., field-name-based patterns) to avoid partial matches.

## Design Decisions

**`ClaudeSettings` uses `map[string]any` instead of typed structs.** Claude Code's settings schema evolves rapidly and includes fields we don't manage. A typed struct would silently drop unknown fields on round-trip. The raw map preserves everything.

**Mtime-based optimistic locking instead of flock.** `AtomicUpdateSettings()` checks that the file's mtime hasn't changed between read and write. If it has, it retries with backoff. This is simpler than file locking, works cross-platform, and is sufficient for the infrequent writes that hooks installation involves.


## Testing

```bash
go test ./pkg/config/...
```

Tests cover atomic settings updates under concurrency, field preservation across round-trips, and config validation. Hook install/uninstall tests live in `pkg/hookconfig`.

## Dependencies

**Uses:** `pkg/logger` (logging). `paths.go` deliberately does not import `pkg/provider` even though it owns parallel constants — `pkg/provider` imports `pkg/hookconfig`, which imports `pkg/config`. The duplicated `ClaudeStateDirEnv` constant must stay in sync between the two packages.

**Used by:** `cmd/` (setup, login, hooks, status), `pkg/daemon/` (state dir), `pkg/hookconfig/` (settings struct, atomic update, tool-name constants), `pkg/http/` (upload config), `pkg/provider/` (Claude paths, skills install), `pkg/redactor/` (redaction patterns), `pkg/sync/` (upload config)
