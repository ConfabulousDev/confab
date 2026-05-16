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
| `IsCodexHooksInstalled(configPath string) (bool, error)` | True iff a confab command is registered under `[[hooks.SessionStart]]`. |

The Codex managed block is delimited by `# >>> confab codex hooks (managed) >>>` / `<<< confab codex hooks (managed) <<<` markers and includes the SHA-256 `trusted_hash` Codex requires for non-interactive hook trust.

## Invariants

- **Atomic writes.** Both providers use `config.AtomicUpdateSettings` (Claude) or a `.bak` + atomic rename (Codex) so a crashed install never leaves a half-edited config.
- **Idempotent.** Calling `Install...` twice produces the same file as calling it once. Tests pin this for both providers.
- **Preserves user config.** Neither provider rewrites unmanaged config. Codex only touches `[features]` + the managed `[[hooks.SessionStart]]` block.
- **No `[[hooks.Stop]]` for Codex.** Codex fires `Stop` at every agent/turn boundary, so a Stop-driven daemon shutdown would kill the root prematurely. Daemon shutdown is parent-PID based.
- **Trusted-hash positional keys.** Codex's `[hooks.state."<configPath>:<event>:<group_idx>:<hook_idx>"]` key uses the hook's actual position in the existing `[[hooks.SessionStart]]` list; pre-CF-396 a stale `:0:0` key would land in the wrong slot if the user had other Codex hooks installed.

## Dependencies

- `pkg/config` — for `ClaudeSettings`, `AtomicUpdateSettings`, `GetBinaryPath`, tool-name constants. Codex side uses `config.GetBinaryPath` only.
- `pkg/logger` — Claude side logs install/uninstall events.
- `github.com/pelletier/go-toml/v2` — Codex TOML parsing.

## Used By

`pkg/provider/claude.go` and `pkg/provider/codex.go`. No other package imports this directly — `cmd/` routes through the `Provider` interface.
