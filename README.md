# confab

Sync and explore Claude Code, Codex, OpenCode, and Cursor sessions. Connect `confab` to your Confab backend to capture transcripts in real time for exploration, sharing, and analysis.

Your `claude`, `codex`, `opencode`, and `cursor` workflows stay unchanged.

![How Confab works](docs/how-it-works.svg)

## Install

Supported on macOS and Linux.

```bash
curl -fsSL https://raw.githubusercontent.com/ConfabulousDev/confab/main/install.sh | bash
# Follow the instructions to add confab to your PATH
confab setup --backend-url https://confab.yourcompany.com
```

`confab setup` detects providers (`claude`, `codex`, `opencode`, `cursor-agent` on `PATH`, or a present state dir such as `~/.cursor`) and wires each one. Claude Code, Codex, OpenCode, and Cursor sessions sync in the same setup pass.

## Connect to Your Backend

```bash
# Initial setup: backend, auth, hooks, bundled skills
confab setup --backend-url https://confab.yourcompany.com

# Login separately (if already set up)
confab login --backend-url https://confab.yourcompany.com

# Check connection and hook status
confab status

# Logout
confab logout
```

## Self-Hosting the Backend

To deploy your own Confab backend, see [confab-web](https://github.com/ConfabulousDev/confab-web).

## Usage

### Sync Mode (Default)

Sessions are synced incrementally while you work:

```bash
# Install sync hooks (done automatically by setup)
confab hooks add

# View running sync daemons
confab sync status

# Remove hooks
confab hooks remove
```

The sync daemon uploads transcript chunks while you work, reducing data loss if the session exits unexpectedly.

### List Sessions

```bash
# List local sessions (--provider is required: claude-code, codex, opencode, or cursor)
confab list --provider claude-code

# Filter by duration
confab list --provider claude-code -d 5d    # Sessions from last 5 days
confab list --provider claude-code -d 12h   # Sessions from last 12 hours
```

Use a listed session ID with `confab save`.

### Manual Upload

```bash
# Upload specific sessions by ID (use IDs from 'confab list')
confab save --provider claude-code abc123de

# Upload multiple sessions
confab save --provider claude-code abc123de f9e8d7c6
```

### Redaction

Sensitive data is automatically redacted before uploading. Redaction is enabled by default during `confab setup`.

Built-in patterns detect common secrets (API keys, private keys, JWT tokens, database passwords, and more) without any configuration.

See [Redaction](REDACTION.md) for configuration details.

## Codex

Confab supports Codex alongside Claude Code. `confab setup` detects `codex` on `PATH` and wires Codex hooks automatically. Use `--provider codex` to configure only Codex.

```bash
# Auto-detect: installs hooks for every provider CLI on PATH
confab setup --backend-url https://confab.yourcompany.com

# Codex-only (explicit override)
confab setup --provider codex --backend-url https://confab.yourcompany.com

# List Codex sessions
confab list --provider codex

# Upload a specific Codex session
confab save --provider codex <id>
```

Codex stores rollouts under `~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl`. Confab uses Codex's local SQLite state to walk subagent trees and sync descendant rollouts as sidechain files under the root session.

### Caveats

- Bundled skills (`/retro`) install for Claude Code, Codex, OpenCode, and Cursor.
- GitHub commit/PR linking is wired for Claude Code, Codex, and Cursor. Claude also supports the GitHub MCP PR matcher; Codex uses Bash hooks.
- Codex sync daemons shut down via parent-process liveness, not a `SessionEnd`/`Stop` hook.

## OpenCode

Confab supports OpenCode alongside Claude Code and Codex. `confab setup` detects `opencode` on `PATH` and wires it automatically. Use `--provider opencode` to configure only OpenCode.

```bash
# Auto-detect: wires every provider CLI on PATH
confab setup --backend-url https://confab.yourcompany.com

# OpenCode-only (explicit override)
confab setup --provider opencode --backend-url https://confab.yourcompany.com
```

OpenCode has no on-disk transcript file — session data lives in a local SQLite database (`~/.local/share/opencode/opencode.db`). Confab does not edit OpenCode's config; instead `setup` installs a small TypeScript plugin into `~/.config/opencode/plugins/` that starts and stops the sync daemon on session lifecycle events. The daemon reads the SQLite database, materializes each session into a local transcript file, and uploads it through the same incremental, redacted pipeline as the other providers.

### Caveats

- **No GitHub commit/PR linking.** The bidirectional GitHub linking wired for Claude Code, Codex, and Cursor is not available for OpenCode.
- **One daemon per root session.** OpenCode subagent sessions don't spawn their own daemon; the root session's daemon syncs them as sidechain files under the root session.
- **Plugin-based install.** Lifecycle is driven by the installed plugin (not an OpenCode-native hook system). The daemon also monitors the parent OpenCode process and exits if it dies.
- Bundled skills (`/retro`) install under `~/.config/opencode/skills/`.

## Cursor

Confab supports Cursor — both the `cursor-agent` CLI and the Cursor desktop IDE. `confab setup` detects Cursor when `cursor-agent` is on `PATH` **or** the `~/.cursor` state dir is present (so IDE-only users are detected too) and wires it automatically. Use `--provider cursor` to configure only Cursor.

```bash
# Auto-detect: wires every detected provider
confab setup --backend-url https://confab.yourcompany.com

# Cursor-only (explicit override)
confab setup --provider cursor --backend-url https://confab.yourcompany.com
```

Cursor writes per-session transcripts to disk at `~/.cursor/projects/<workspace>/agent-transcripts/<id>/<id>.jsonl`. `confab setup` installs `sessionStart`, `sessionEnd`, `preToolUse`, and `postToolUse` hooks into `~/.cursor/hooks.json` (merging into any user-authored hooks). Subagent transcripts live beside the root under `subagents/<id>.jsonl` and sync as `file_type=agent` sidechain files under the root session, through the same incremental, redacted pipeline as the other providers.

### Caveats

- **No tool results.** Cursor's transcript records prompts, assistant text, and tool *calls* but not tool *results*, so synced Cursor sessions show no tool outputs.
- **Hybrid shutdown.** The CLI fires `sessionEnd` reliably, but the IDE only fires it on window/app close (not per chat-tab). The daemon's parent-PID liveness on the shared `Cursor.app` process is the primary IDE shutdown — a long IDE session with several chats keeps per-session daemons alive (still syncing incrementally) until the window closes.
- Bundled skills (`/retro`) install under `~/.cursor/skills/`.

## Configuration

| File | Purpose |
|------|---------|
| `~/.confab/config.json` | Backend URL, API key, per-config-dir backend bindings, redaction, log level, and auto-update settings |
| `~/.confab/logs/confab.log` | Operation logs (auto-rotated, 14 day retention) |

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `CONFAB_CLAUDE_DIR` | `~/.claude` | Override the Claude Code state directory |
| `CONFAB_CODEX_DIR` | `~/.codex` | Override the Codex state directory |
| `CONFAB_CODEX_STATE_DB` | highest-numbered `~/.codex/state_*.sqlite` | Override the Codex SQLite state database location |
| `CONFAB_OPENCODE_CONFIG_DIR` | `~/.config/opencode` | Override the OpenCode config directory (plugin + skills) |
| `CONFAB_OPENCODE_DB` | `~/.local/share/opencode/opencode.db` | Override the OpenCode SQLite database location |
| `CONFAB_CURSOR_DIR` | `~/.cursor` | Override the Cursor state directory (hooks + skills + transcripts) |
| `CONFAB_CONFIG_PATH` | `~/.confab/config.json` | Config file location |
| `CONFAB_LOG_DIR` | `~/.confab/logs` | Log directory |
| `CONFAB_SYNC_INTERVAL_MS` | `30000` | Daemon sync interval (also the OpenCode collector poll interval) |
| `CONFAB_SYNC_JITTER_MS` | `0` | Maximum random jitter added to each sync interval |
| `CONFAB_DISABLE_LINK_FROM_GITHUB` | unset | Any non-empty value disables GitHub commit/PR linking |

## Developer Docs

Each package has a README with extension guides, invariants, and design decisions:

- [`cmd/`](cmd/README.md) — CLI commands and hook handlers
- [`pkg/`](pkg/README.md) — Package index and dependency map
  - [`confabpath`](pkg/confabpath/README.md), [`config`](pkg/config/README.md), [`daemon`](pkg/daemon/README.md), [`git`](pkg/git/README.md), [`hookconfig`](pkg/hookconfig/README.md), [`http`](pkg/http/README.md), [`logger`](pkg/logger/README.md), [`loginit`](pkg/loginit/README.md), [`pathcanon`](pkg/pathcanon/README.md), [`provider`](pkg/provider/README.md), [`redactor`](pkg/redactor/README.md), [`sync`](pkg/sync/README.md), [`types`](pkg/types/README.md), [`utils`](pkg/utils/README.md)

See also [`CLAUDE.md`](CLAUDE.md) for AI-oriented architecture notes and development practices.

## Development

```bash
make build
go test ./...
```

### Building from Source

```bash
git clone https://github.com/ConfabulousDev/confab.git
cd confab
make build
./confab install
# Follow the instructions to add confab to your PATH
confab setup --backend-url https://confab.yourcompany.com
```

## License

MIT
