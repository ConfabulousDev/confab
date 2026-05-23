# pkg/hookconfig

Owns the install/uninstall/check logic for Confab hooks in Claude Code's `~/.claude/settings.json` and Codex's `~/.codex/config.toml`. Provider methods (`pkg/provider/{claude,codex}.go`) delegate here so the provider package stays focused on paths, process detection, and rollout metadata.

## Why a separate package

Before CF-396 (Phase 2), hook install logic lived in `pkg/config` (Claude side) and `pkg/provider/codex.go` (Codex side). Three problems pushed it out:

1. **Symmetry.** Claude and Codex install logic does the same job — atomic update of a managed block in a settings file. Putting them next to each other keeps the patterns aligned.
2. **Provider methods stayed thin.** With install code out of the provider package, `claude.go` and `codex.go` shrank to paths + interface methods that delegate. No 300-line install routines hiding in a "provider" file.
3. **Circular imports.** `pkg/provider` already imports `pkg/config` for path constants; if `pkg/config` had imported `pkg/provider` for the hook command shape, the cycle would have blocked CF-396. Moving install logic out of `pkg/config` resolves that cycle once and for all.

## Files

| File | Role |
|------|------|
| `claude.go` | Claude Code hook install/uninstall: sync (`SessionStart`/`SessionEnd`), `PreToolUse`, `PostToolUse`, `UserPromptSubmit`. Edits `~/.claude/settings.json` via `config.AtomicUpdateSettings`. |
| `codex.go` | Codex hook install/uninstall: writes a confab-managed `[features]` + `[[hooks.SessionStart]]` block in `~/.codex/config.toml`. Preserves user config; atomic write with backup. |

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

The Codex managed block is delimited by `# >>> confab codex hooks (managed) >>>` / `<<< confab codex hooks (managed) <<<` markers and installs three hook events:

- `[[hooks.SessionStart]]` — daemon spawn (`startup|resume|clear` matcher)
- `[[hooks.PreToolUse]]` — `Confab-Link:` commit trailer + `📝 [Confab link]` PR body injection (`Bash` matcher)
- `[[hooks.PostToolUse]]` — commit/PR URL linking back to the session (`Bash` matcher)

Each event also writes a `[hooks.state."<configPath>:<event_lower>:<group_idx>:<hook_idx>"]` table with the SHA-256 `trusted_hash` Codex requires for non-interactive hook trust. Event labels follow Codex's snake_case convention (`session_start`, `pre_tool_use`, `post_tool_use`) — see `codex-rs/hooks/src/lib.rs:84-110`.

The hash blob covers `{event_name, hooks: [{async, command, statusMessage, timeout, type}], matcher}` with fields in alphabetical order. `statusMessage` is `"Starting Confab sync"` for SessionStart and `""` for the tool-use events — empty-string is load-bearing because Codex's TOML `Option<String>` deserializes `statusMessage = ""` to `Some("")`, which canonical-JSON-serializes as `"statusMessage": ""`; omitting the field would round-trip to `None` and yield a hash mismatch.

## Invariants

- **Atomic writes.** Both providers use `config.AtomicUpdateSettings` (Claude) or a `.bak` + atomic rename (Codex) so a crashed install never leaves a half-edited config.
- **Idempotent.** Calling `Install...` twice produces the same file as calling it once. Tests pin this for both providers.
- **Preserves user config.** Neither provider rewrites unmanaged config. Codex only touches `[features]` + the managed `[[hooks.SessionStart]]` block.
- **No `[[hooks.Stop]]` / `[[hooks.UserPromptSubmit]]` for Codex.** Codex fires `Stop` at every agent/turn boundary (Stop-driven shutdown would kill the root daemon prematurely), and parent-PID monitoring already covers the Claude `UserPromptSubmit` teleport case.
- **Trusted-hash positional keys.** Codex's `[hooks.state."<configPath>:<event>:<group_idx>:<hook_idx>"]` key uses the hook's actual position in the existing `[[hooks.<Event>]]` list. `countCodexHookMatcherGroups` runs **per event** and on the post-strip config so re-installs interleave correctly with any unmanaged user-authored blocks at any of the three event types.

## Dependencies

- `pkg/config` — for `ClaudeSettings`, `AtomicUpdateSettings`, `GetBinaryPath`, tool-name constants. Codex side uses `config.GetBinaryPath` only.
- `pkg/logger` — Claude side logs install/uninstall events.
- `github.com/pelletier/go-toml/v2` — Codex TOML parsing.

## Used By

`pkg/provider/claude.go` and `pkg/provider/codex.go`. No other package imports this directly — `cmd/` routes through the `Provider` interface.
