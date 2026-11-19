# pkg/hookconfig

Owns the install/uninstall/check logic for Confabulous Code hooks in Claude Code's `~/.claude/settings.json`, Codex's `~/.codex/config.toml`, and Cursor's `~/.cursor/hooks.json`. Provider methods (`pkg/provider/{claude,codex,cursor}.go`) delegate here so the provider package stays focused on paths, process detection, and rollout metadata.

OpenCode is **not** handled here. It has no settings/config hook system; `Opencode.InstallHooks` (in `pkg/provider/opencode.go`) writes a TS plugin to `~/.config/opencode/plugins/` directly. This package covers the three settings/config-file providers only.

## Why a separate package

Before CF-396 (Phase 2), hook install logic lived in `pkg/config` (Claude side) and `pkg/provider/codex.go` (Codex side). Three problems pushed it out:

1. **Symmetry.** Claude and Codex install logic does the same job — atomic update of confab's own entries in a settings file. Putting them next to each other keeps the patterns aligned.
2. **Provider methods stayed thin.** With install code out of the provider package, `claude.go` and `codex.go` shrank to paths + interface methods that delegate. No 300-line install routines hiding in a "provider" file.
3. **Circular imports.** `pkg/provider` already imports `pkg/config` for path constants; if `pkg/config` had imported `pkg/provider` for the hook command shape, the cycle would have blocked CF-396. Moving install logic out of `pkg/config` resolves that cycle once and for all.

## Files

| File | Role |
|------|------|
| `claude.go` | Claude Code hook install/uninstall: sync (`SessionStart`/`SessionEnd`), `PreToolUse`, `PostToolUse`, `UserPromptSubmit`. Each `Install*`/`Uninstall*`/`Is*Installed` function takes an explicit `settingsPath` (the provider passes `p.SettingsPath()`) and edits it via `config.AtomicUpdateSettingsAt` / `config.ReadSettingsAt` — so hooks install into a non-default config dir (kata hpec) without env mutation. |
| `codex.go` | Codex public API (`InstallCodexHooks`, `UninstallCodexHooks`, `RefreshCodexHooks`, `CodexHooksOutdated`, `IsCodexHooksInstalled`), the `BinaryPath` seam, and `codexHooksTOML` — the single source of the hook groups and trust hashes that get written to `~/.codex/config.toml`. |
| `codex_edit_parse.go` | Parses `config.toml` twice: semantically (`toml.Unmarshal`) and into byte spans of its top-level expressions (go-toml `unstable.Parser`). Locates the `[[hooks.<Event>]]` groups and `[[hooks.<Event>.hooks]]` handlers and marks which handlers are confab's. |
| `codex_edit.go` | The pure text transform (`transformCodexConfig`) behind install, refresh and uninstall: plans edits to confab's own tables, applies them, and leaves every other byte as it was. |
| `codex_edit_write.go` | The pre-write verification (`verifyCodexEdit`) and the file-level write path (`codexEditor.apply`): read, transform, verify, re-read, backup, atomic write. Also `codexHooksOutdated`. |
| `cursor.go` | Cursor hook install/uninstall: writes `sessionStart` (daemon spawn) + `sessionEnd` (signal shutdown) + `preToolUse` + `postToolUse` (GitHub commit/PR linking; 65aq) command hooks into `~/.cursor/hooks.json` (`{"version":1,"hooks":{"<event>":[{"command","type","matcher"?}]}}`). The tool-use events carry `matcher:"Shell"` (an optional per-entry field) to scope them to Cursor's Shell tool. Plain-JSON merge that preserves user-authored hooks and unknown top-level keys (top level + per-event arrays kept as `json.RawMessage`); atomic write with backup; idempotent. No `stop` (per-turn). |

## Public API

### Claude

| Function | Purpose |
|---|---|
| `InstallSyncHooks(settingsPath string) error` | Install `SessionStart` (spawn daemon) + `SessionEnd` (signal shutdown) in `settings.json`. The command strings carry an explicit `--provider claude-code` (kata m9mb), matching codex/cursor. |
| `UninstallSyncHooks(settingsPath string) error` | Remove the two sync hooks. The matcher uses `Contains "hook session-start"/"session-end"`, so it removes both the `--provider claude-code` shape and old no-flag installs. |
| `IsSyncHooksInstalled(settingsPath string) (bool, error)` | True iff both sync hooks are present. |
| `InstallPreToolUseHooks(settingsPath string) error` | Install bash + GitHub MCP `PreToolUse` interceptors for git commit / PR tracking. |
| `UninstallPreToolUseHooks(settingsPath string) error` / `IsPreToolUseHooksInstalled(settingsPath string) (bool, error)` | symmetric |
| `InstallPostToolUseHooks` / `Uninstall…` / `Is…Installed` | `PostToolUse` interceptors. |
| `InstallUserPromptSubmitHook` / `Uninstall…` / `Is…Installed` | Re-spawn the sync daemon if it died between turns. |

`provider.ClaudeCode.InstallHooks()` calls all four install functions in sequence; `UninstallHooks()` mirrors that.

### Codex

| Function | Purpose |
|---|---|
| `InstallCodexHooks(configPath string) (string, error)` | Idempotent install of the three hook groups and their trust-state tables into `config.toml`; sets `[features] hooks = true` and removes the deprecated `codex_hooks`. Used by `confab hooks add` and by `confab setup` when the hooks are not installed. Returns the file path. |
| `UninstallCodexHooks(configPath string) (string, error)` | Removes confab's hook and trust-state tables. `[features] hooks` is left as it is. A missing file is a no-op. |
| `RefreshCodexHooks(configPath string) (string, error)` | Brings an installed but outdated hook set up to date in place. Unlike install it leaves an explicit `[features] hooks = false` (and prints a notice) and keeps the extra keys Codex stores in confab's trust-state tables (`enabled`, `trusted_content_hash`), updating only `trusted_hash`. Used by `confab setup`. |
| `CodexHooksOutdated(configPath string) (bool, error)` | True when `IsCodexHooksInstalled` is true **and** a refresh would change the file. That is the whole definition: it covers the matcher, status message, trusted hash, trust key, binary path, leftover marker comments and duplicate groups, and it makes refresh idempotent by construction. Never writes. |
| `IsCodexHooksInstalled(configPath string) (bool, error)` | True only when all three Confabulous Code hook events (SessionStart, PreToolUse, PostToolUse) carry a confab command. Stale single-event installs (pre-CF-492) read as "not installed" so `confab setup` installs the missing events and transparently upgrades. |
| `BinaryPath` (variable) | Resolves the binary path written into hook commands (`config.GetBinaryPath`). A variable so tests in this package, `pkg/provider` and `cmd` can pin a path named `confab`; test binaries have another name, which `IsCodexHooksInstalled` does not recognise. |

Three hook events are installed:

- `[[hooks.SessionStart]]` — daemon spawn (`startup|resume|clear|fork` matcher, `codexSessionStartMatcher`). `fork` is the source Codex sends for "branch in new chat" (`thread/fork`): the fork is a new user thread with no spawn edge, so the original daemon's descendant discovery cannot see it and it needs its own daemon. `compact` is deliberately excluded (it fires for a thread that already has a daemon). The synced fork holds only what its rollout holds: `history_mode: "legacy"` rollouts (the VS Code extension as of 0.169) start with a copy of the parent's turns, while `"paginated"` rollouts (`codex exec fork` as of 0.170) reference the parent via `history_base` and contain only the fork's own turns. The matcher is part of the trusted-hash identity, so installs predating `fork` read as outdated and keep their old self-consistent hash until `confab setup` refreshes the hooks.
- `[[hooks.PreToolUse]]` — `Confab-Link:` commit trailer + `📝 [Confab link]` PR body injection (`Bash` matcher)
- `[[hooks.PostToolUse]]` — commit/PR URL linking back to the session (`Bash` matcher)

Each event also gets a `[hooks.state."<configPath>:<event_lower>:<group_idx>:<hook_idx>"]` table with the SHA-256 `trusted_hash` Codex requires for non-interactive hook trust. Event labels follow Codex's snake_case convention (`session_start`, `pre_tool_use`, `post_tool_use`) — see `codex-rs/hooks/src/lib.rs:84-110`.

The hash blob covers `{event_name, hooks: [{async, command, statusMessage, timeout, type}], matcher}` with fields in alphabetical order. `statusMessage` is `"Starting Confabulous Code sync"` (`codexSessionStartStatusMessage`, the single source for both the TOML literal and the hash input) for SessionStart and `""` for the tool-use events — empty-string is load-bearing because Codex's TOML `Option<String>` deserializes `statusMessage = ""` to `Some("")`, which canonical-JSON-serializes as `"statusMessage": ""`; omitting the field would round-trip to `None` and yield a hash mismatch.

Installs written before the product rename (kata y7wh) carry `"Starting Confab sync"` with its matching hash, which stays self-consistent, so Codex raises no trust prompt. Such an install reads as outdated, so re-running `confab setup` refreshes it: the message and hash are rewritten together. Detection (`IsCodexHooksInstalled`) keys on commands, not on the status message.

#### Editing `config.toml` by ownership (kata cd4q)

Codex rewrites `config.toml` with a comment-preserving editor (`toml_edit`). It puts brand-new top-level tables after the last table and **before** a trailing comment. Earlier versions of this package wrapped confab's hooks in `# >>> confab codex hooks >>>` / `# <<< confab codex hooks <<<` comments and deleted everything between them, which deleted the tables Codex had since added there (`[desktop]`, `[marketplaces.*]`, `[plugins.*]`). The markers are no longer written; old ones are removed wherever they are found.

What confab owns, and therefore may change:

- A **handler** `[[hooks.<Event>.hooks]]` with `type = "command"` whose command is a confab command (`isConfabCommand`: the binary's basename is `confab`), or is exactly a command this binary writes (so a build with another name still recognises its own hooks). Events considered: SessionStart, PreToolUse, PostToolUse.
- A **group** `[[hooks.<Event>]]` that has at least one handler, all of them confab's. A group with both kinds is *mixed*: only confab's handler tables are removed from it.
- A **state table** `[hooks.state."<key>"]` whose key addresses a confab handler in the layout before **or** after the edit. (After matters when a leftover table already sits at the key confab is about to use: by Codex's positional semantics it now is the state of confab's handler, so it is rewritten in place rather than duplicated.)
- The **marker comment** lines.
- Two flags, matched by full key path so same-named keys in other tables are untouched: `features.hooks` and the deprecated `features.codex_hooks`.

Everything else is left byte-identical, wherever it sits. A table's span runs from its header line through the line of its last key-value (trailing same-line comments included); standalone comments and blank lines after it are not part of it. The only other bytes that change: a run of blank lines left around a removed table is collapsed to one, and a file that was edited ends with exactly one newline.

How the edit is made:

- **In place.** The first confab group of an event is replaced where it stands; further confab groups for the same event are removed (this heals duplicated blocks); a group is appended at the end of the file only when the event has none. Trust keys are positional, so moving confab's group would shift the user's own groups and make Codex ask for trust again. When a user's hook does change position (uninstall, or a mixed group), a notice says so.
- **Refusals.** The file is left untouched, with an error that says so, when: it is not valid TOML; a confab handler is not written as array tables (inline arrays, dotted keys) and so has no span to edit; or the pre-write verification fails.
- **Verification before every write** (`verifyCodexEdit`). The config is parsed before and after the edit. Everything confab owns is removed from both and the rest must be identical. Then the mode's promise is checked: after install or refresh each event has exactly one confab handler, in the canonical group, with the expected `trusted_hash` at its positional key; after uninstall none remains.
- **No-op rule.** When the result equals the file, nothing is written and no backup is made.
- **Concurrent writers.** Codex takes no lock confab could share. The file is re-read right before the write; if it changed, the edit is redone on the new content (three attempts, then an error). A write by another program landing between that re-read and the rename is still lost.
- **Notices** go to stderr and the log (Warn): the backup path when a write happens, a user hook that changed position, an explicit `[features] hooks = false` left in place by a refresh, and a confab hook that Codex has disabled (`enabled = false`).

### Cursor

| Function | Purpose |
|---|---|
| `InstallCursorHooks(hooksPath string) (string, error)` | Idempotent install of `sessionStart` + `sessionEnd` + `preToolUse` + `postToolUse` command hooks into `hooks.json` (tool-use events carry `matcher:"Shell"`). Returns the file path. |
| `UninstallCursorHooks(hooksPath string) (string, error)` | Remove confab's managed entries (all four events); prune an event key only when it becomes empty. A missing file is a no-op. |
| `IsCursorHooksInstalled(hooksPath string) (bool, error)` | True only when **all four** managed events (`sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`) carry a confab command. A legacy two-event install (pre-65aq) reads as not installed, so `confab setup` transparently upgrades it. |

Cursor's `hooks.json` is plain JSON (`{"version":1,"hooks":{"<event>":[{"command","type","matcher"?}]}}`, kata 6kys), not Claude's settings schema or Codex's TOML — so the installer is JSON-native (no reuse of the Claude/Codex helpers). It loads the top level as `map[string]json.RawMessage` and each event array as `[]json.RawMessage`, so unknown top-level keys and user-authored hook entries (which may carry keys beyond `command`/`type`/`matcher`) survive byte-faithfully; confab edits only its own four events. Confabulous Code entries are identified by the **command signature** (` hook ` + `--provider cursor`) rather than the binary basename — this keeps detection robust where the binary lives, makes re-install idempotent, and uninstall surgical. Installs `sessionStart` (daemon spawn → `hook session-start --provider cursor`), `sessionEnd` (signal shutdown → `hook session-end --provider cursor`), and `preToolUse`/`postToolUse` (matcher `Shell` → `hook pre-tool-use`/`post-tool-use --provider cursor`, GitHub commit/PR linking; 65aq). No `stop` (fires per turn — same hazard as Codex's `Stop`; shutdown stays parent-PID + `sessionEnd` driven).

## Invariants

- **Atomic writes.** All providers use `config.AtomicUpdateSettingsAt` (Claude) or a `.confab-backup-*` + atomic rename (Codex, Cursor) so a crashed install never leaves a half-edited config. Codex writes nothing, and makes no backup, when the edit would not change the file.
- **Idempotent.** Calling `Install...` twice produces the same file as calling it once. Tests pin this for all three providers.
- **Preserves user config.** No provider rewrites unmanaged config. Codex only touches the two `[features]` flags and the tables Confabulous Code owns, identified by what they contain and never by where they sit in the file (see "Editing `config.toml` by ownership"); a pre-write verification refuses any edit that would change anything else. Cursor only touches its four event arrays and leaves every other hook / top-level key untouched.
- **No `[[hooks.Stop]]` / `[[hooks.UserPromptSubmit]]` for Codex.** Codex fires `Stop` at every agent/turn boundary (Stop-driven shutdown would kill the root daemon prematurely), and parent-PID monitoring already covers the Claude `UserPromptSubmit` teleport case.
- **Trusted-hash positional keys.** Codex's `[hooks.state."<configPath>:<event>:<group_idx>:<hook_idx>"]` key uses the hook's actual position in the `[[hooks.<Event>]]` list. The index is derived **per event** from the layout the edit leaves behind, and confab's group is replaced in place, so reinstalling never shifts the index of a user-authored group.
- **`codexHooksTOML` is the single source of what is installed.** The editor parses its output to get the canonical group text, commands and hashes; nothing else spells out a matcher, command or status message.

## Dependencies

- `pkg/config` — for `ClaudeSettings`, `AtomicUpdateSettings`, `GetBinaryPath`, tool-name constants. Codex and Cursor sides use `config.GetBinaryPath` only.
- `pkg/logger` — Claude side logs install/uninstall events.
- `github.com/pelletier/go-toml/v2` — Codex TOML parsing; its `unstable` package provides the byte spans the Codex editor works on.

## Used By

`pkg/provider/claude.go`, `pkg/provider/codex.go`, and `pkg/provider/cursor.go` (not `opencode.go` — it manages its own plugin file). No other non-test code imports this directly — `cmd/` routes through the `Provider` interface (`Codex` also exposes refresh through the optional `provider.HookRefresher`). Tests in `cmd` and `pkg/provider` import it to pin `BinaryPath`.
