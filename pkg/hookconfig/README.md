# pkg/hookconfig

Owns the install/uninstall/check logic for Confab hooks in Claude Code's `~/.claude/settings.json`, Codex's `~/.codex/config.toml`, and Cursor's `~/.cursor/hooks.json`. Provider methods (`pkg/provider/{claude,codex,cursor}.go`) delegate here so the provider package stays focused on paths, process detection, and rollout metadata.

OpenCode is **not** handled here. It has no settings/config hook system; `Opencode.InstallHooks` (in `pkg/provider/opencode.go`) writes a TS plugin to `~/.config/opencode/plugins/` directly. This package covers the three settings/config-file providers only.

## Why a separate package

Before CF-396 (Phase 2), hook install logic lived in `pkg/config` (Claude side) and `pkg/provider/codex.go` (Codex side). Three problems pushed it out:

1. **Symmetry.** Claude and Codex install logic does the same job — atomic update of a managed block in a settings file. Putting them next to each other keeps the patterns aligned.
2. **Provider methods stayed thin.** With install code out of the provider package, `claude.go` and `codex.go` shrank to paths + interface methods that delegate. No 300-line install routines hiding in a "provider" file.
3. **Circular imports.** `pkg/provider` already imports `pkg/config` for path constants; if `pkg/config` had imported `pkg/provider` for the hook command shape, the cycle would have blocked CF-396. Moving install logic out of `pkg/config` resolves that cycle once and for all.

## Files

| File | Role |
|------|------|
| `claude.go` | Claude Code hook install/uninstall: sync (`SessionStart`/`SessionEnd`), `PreToolUse`, `PostToolUse`, `UserPromptSubmit`. Each `Install*`/`Uninstall*`/`Is*Installed` function takes an explicit `settingsPath` (the provider passes `p.SettingsPath()`) and edits it via `config.AtomicUpdateSettingsAt` / `config.ReadSettingsAt` — so hooks install into a non-default config dir (kata hpec) without env mutation. |
| `codex.go` | Codex hook install/uninstall: writes a confab-managed `[features]` block plus `SessionStart`, `PreToolUse`, and `PostToolUse` hooks in `~/.codex/config.toml`. Preserves user config; atomic write with backup. |
| `cursor.go` | Cursor hook install/uninstall: writes `sessionStart` (daemon spawn) + `sessionEnd` (signal shutdown) + `preToolUse` + `postToolUse` (GitHub commit/PR linking; 65aq) command hooks into `~/.cursor/hooks.json` (`{"version":1,"hooks":{"<event>":[{"command","type","matcher"?}]}}`). The tool-use events carry `matcher:"Shell"` (an optional per-entry field) to scope them to Cursor's Shell tool. Plain-JSON merge that preserves user-authored hooks and unknown top-level keys (top level + per-event arrays kept as `json.RawMessage`); atomic write with backup; idempotent. No `stop` (per-turn). |

## Public API

### Claude

| Function | Purpose |
|---|---|
| `InstallSyncHooks() error` | Install `SessionStart` (spawn daemon) + `SessionEnd` (signal shutdown) in `settings.json`. |
| `UninstallSyncHooks() error` | Remove the two sync hooks. |
| `IsSyncHooksInstalled() (bool, error)` | True iff both sync hooks are present. |
| `InstallPreToolUseHooks() error` | Install bash + GitHub MCP `PreToolUse` interceptors for git commit / PR tracking. |
| `UninstallPreToolUseHooks() error` / `IsPreToolUseHooksInstalled() (bool, error)` | symmetric |
| `InstallPostToolUseHooks` / `Uninstall…` / `Is…Installed` | `PostToolUse` interceptors. |
| `InstallUserPromptSubmitHook` / `Uninstall…` / `Is…Installed` | Capture user prompts. |

`provider.ClaudeCode.InstallHooks()` calls all four install functions in sequence; `UninstallHooks()` mirrors that.

### Codex

| Function | Purpose |
|---|---|
| `InstallCodexHooks(configPath string) (string, error)` | Idempotent install of the managed block into `config.toml`. Returns the file path. |
| `UninstallCodexHooks(configPath string) (string, error)` | Strip the managed block; restore `features.hooks` to its prior state. |
| `IsCodexHooksInstalled(configPath string) (bool, error)` | True only when all three Confab hook events (SessionStart, PreToolUse, PostToolUse) carry a confab command. Stale single-event installs (pre-CF-492) read as "not installed" so `confab setup` re-emits the managed block and transparently upgrades. |

The Codex managed block is delimited by `# >>> confab codex hooks >>>` / `# <<< confab codex hooks <<<` markers and installs three hook events:

- `[[hooks.SessionStart]]` — daemon spawn (`startup|resume|clear` matcher)
- `[[hooks.PreToolUse]]` — `Confab-Link:` commit trailer + `📝 [Confab link]` PR body injection (`Bash` matcher)
- `[[hooks.PostToolUse]]` — commit/PR URL linking back to the session (`Bash` matcher)

Each event also writes a `[hooks.state."<configPath>:<event_lower>:<group_idx>:<hook_idx>"]` table with the SHA-256 `trusted_hash` Codex requires for non-interactive hook trust. Event labels follow Codex's snake_case convention (`session_start`, `pre_tool_use`, `post_tool_use`) — see `codex-rs/hooks/src/lib.rs:84-110`.

The hash blob covers `{event_name, hooks: [{async, command, statusMessage, timeout, type}], matcher}` with fields in alphabetical order. `statusMessage` is `"Starting Confab sync"` for SessionStart and `""` for the tool-use events — empty-string is load-bearing because Codex's TOML `Option<String>` deserializes `statusMessage = ""` to `Some("")`, which canonical-JSON-serializes as `"statusMessage": ""`; omitting the field would round-trip to `None` and yield a hash mismatch.

### Cursor

| Function | Purpose |
|---|---|
| `InstallCursorHooks(hooksPath string) (string, error)` | Idempotent install of `sessionStart` + `sessionEnd` + `preToolUse` + `postToolUse` command hooks into `hooks.json` (tool-use events carry `matcher:"Shell"`). Returns the file path. |
| `UninstallCursorHooks(hooksPath string) (string, error)` | Remove confab's managed entries (all four events); prune an event key only when it becomes empty. A missing file is a no-op. |
| `IsCursorHooksInstalled(hooksPath string) (bool, error)` | True only when **all four** managed events (`sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`) carry a confab command. A legacy two-event install (pre-65aq) reads as not installed, so `confab setup` transparently upgrades it. |

Cursor's `hooks.json` is plain JSON (`{"version":1,"hooks":{"<event>":[{"command","type","matcher"?}]}}`, kata 6kys), not Claude's settings schema or Codex's TOML — so the installer is JSON-native (no reuse of the Claude/Codex helpers). It loads the top level as `map[string]json.RawMessage` and each event array as `[]json.RawMessage`, so unknown top-level keys and user-authored hook entries (which may carry keys beyond `command`/`type`/`matcher`) survive byte-faithfully; confab edits only its own four events. Confab entries are identified by the **command signature** (` hook ` + `--provider cursor`) rather than the binary basename — this keeps detection robust where the binary lives, makes re-install idempotent, and uninstall surgical. Installs `sessionStart` (daemon spawn → `hook session-start --provider cursor`), `sessionEnd` (signal shutdown → `hook session-end --provider cursor`), and `preToolUse`/`postToolUse` (matcher `Shell` → `hook pre-tool-use`/`post-tool-use --provider cursor`, GitHub commit/PR linking; 65aq). No `stop` (fires per turn — same hazard as Codex's `Stop`; shutdown stays parent-PID + `sessionEnd` driven).

## Invariants

- **Atomic writes.** All providers use `config.AtomicUpdateSettings` (Claude) or a `.confab-backup-*` + atomic rename (Codex, Cursor) so a crashed install never leaves a half-edited config.
- **Idempotent.** Calling `Install...` twice produces the same file as calling it once. Tests pin this for all three providers.
- **Preserves user config.** No provider rewrites unmanaged config. Codex only touches `[features]` and the managed Confab hook block; Cursor only touches its two event arrays and leaves every other hook / top-level key untouched.
- **No `[[hooks.Stop]]` / `[[hooks.UserPromptSubmit]]` for Codex.** Codex fires `Stop` at every agent/turn boundary (Stop-driven shutdown would kill the root daemon prematurely), and parent-PID monitoring already covers the Claude `UserPromptSubmit` teleport case.
- **Trusted-hash positional keys.** Codex's `[hooks.state."<configPath>:<event>:<group_idx>:<hook_idx>"]` key uses the hook's actual position in the existing `[[hooks.<Event>]]` list. `countCodexHookMatcherGroups` runs **per event** and on the post-strip config so re-installs interleave correctly with any unmanaged user-authored blocks at any of the three event types.

## Dependencies

- `pkg/config` — for `ClaudeSettings`, `AtomicUpdateSettings`, `GetBinaryPath`, tool-name constants. Codex and Cursor sides use `config.GetBinaryPath` only.
- `pkg/logger` — Claude side logs install/uninstall events.
- `github.com/pelletier/go-toml/v2` — Codex TOML parsing.

## Used By

`pkg/provider/claude.go`, `pkg/provider/codex.go`, and `pkg/provider/cursor.go` (not `opencode.go` — it manages its own plugin file). No other package imports this directly — `cmd/` routes through the `Provider` interface.
