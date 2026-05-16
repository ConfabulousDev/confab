# pkg/

Internal packages for the Confab CLI. Each package has its own README with extension guides, invariants, and design decisions.

## Package Index

| Package | Purpose | Change this when... |
|---------|---------|---------------------|
| [codextest](codextest/) | Reusable Codex SQLite + sessions-tree fixture for tests | Adding new fixture builders for cross-package Codex tests |
| [config](config/) | Confab config (API key, redaction, settings.json read/write) | Adding config fields, changing settings.json plumbing |
| [daemon](daemon/) | Background sync daemon lifecycle | Changing sync behavior, shutdown logic |
| [discovery](discovery/) | Session scanning, metadata extraction, agent IDs | Adding metadata fields, new ID formats |
| [git](git/) | Git repo info extraction | Adding new git fields to sync |
| [hookconfig](hookconfig/) | Per-provider hook install/uninstall (Claude settings.json, Codex config.toml) | Adding new hook event types, changing hook command shape |
| [http](http/) | HTTP client with compression + retries | Adding error types, changing retry logic |
| [logger](logger/) | Singleton file logger with rotation | Changing log format, adding levels |
| [provider](provider/) | `Provider` interface + Claude Code / Codex implementations (paths, hooks, parent-PID, root walk, hook payloads) | Adding a new provider or changing tool-specific behavior |
| [redactor](redactor/) | JSON-aware sensitive data redaction | Adding pattern types (patterns themselves live in config) |
| [sync](sync/) | Sync engine, API client, file tracking | Adding API endpoints, changing chunking |
| [types](types/) | Shared type definitions | Adding cross-package types |
| [utils](utils/) | Small shared utilities and constants | Rarely — prefer package-local helpers |

## Dependency Map

```
cmd/  (uses all packages)
 │
 ├── daemon ──── sync ──┬── http ──── config, logger
 │                      ├── redactor ── config
 │                      ├── discovery ── config, logger
 │                      ├── provider ── hookconfig ── config, logger
 │                      │                └── types, logger
 │                      ├── git
 │                      └── config
 │
 ├── config
 ├── discovery
 ├── provider
 ├── hookconfig
 ├── sync
 ├── http
 ├── redactor
 ├── git
 └── logger

Test-only:
  codextest (used by provider, sync, daemon, cmd test files)

Leaf packages (no confab dependencies):
  types, utils, logger, git
```

## Data Flow

```
Claude Code writes transcript
        │
        ▼
  ~/.claude/projects/<path>/<session-id>.jsonl
        │
        ▼
  daemon (pkg/daemon) watches file
        │
        ▼
  tracker (pkg/sync) reads new lines, seeks by byte offset
        │
        ▼
  discovery (pkg/discovery) extracts agent IDs + metadata
        │
        ▼
  redactor (pkg/redactor) redacts sensitive data
        │
        ▼
  client (pkg/sync) uploads chunk via HTTP
        │
        ▼
  http (pkg/http) compresses with zstd, sends to backend
```

## Layering Rules

- **`types`, `utils`, `logger`, `git`** are leaf packages — no confab imports. Any package can depend on them.
- **`logger`** is accessed as a singleton — no need to pass it around.
- **Mid-level packages** (`config`, `http`, `redactor`, `discovery`) depend on leaves and each other but not on `daemon` or `sync`.
- **`sync`** depends on mid-level packages. `daemon` depends on `sync`.
- **`cmd/`** depends on everything. It's the only package that imports `daemon`.
- Dependencies flow **downward only**. If you need to add an upward dependency, you have a design problem — use an interface or move the shared type to `types`.
