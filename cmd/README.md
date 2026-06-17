# cmd/

CLI command layer built on [Cobra](https://github.com/spf13/cobra). Each file defines one or more commands and registers them via `init()`.

## Files

| File | Role |
|------|------|
| `root.go` | Root command, persistent pre/post hooks, logger init |
| `helpers.go` | Shared command helpers for authenticated HTTP clients and session API error translation |
| `hook.go` | Parent command for hook handlers (`confab hook <type>`) |
| `hook_sessionstart.go` | `session-start` hook: spawns sync daemon. Provider-agnostic — selects via `--provider` flag and routes through `provider.Provider`. |
| `hook_sessionend.go` | `session-end` hook: stops sync daemon. Claude, OpenCode, and Cursor handle it (OpenCode's plugin fires it on `dispose`, routed to `sessionEndOpencode`; Cursor routes to `sessionEndCursor`, which reads the `CursorHookInput`, forwards the `reason` as a session_end event, and stops the daemon under the `cursor` provider namespace); Codex shutdown is parent-PID driven and explicitly rejects this command. For Cursor the CLI `sessionEnd` is reliable, but the IDE only fires it on window/app close (not per chat-tab) — so the daemon's parent-PID liveness on `Cursor.app` is the primary IDE shutdown, with `sessionEnd` a clean bonus (kata 6kys). |
| `hook_pretooluse.go` | `pre-tool-use` hook: injects Confab links into git commits and PRs |
| `hook_posttooluse.go` | `post-tool-use` hook: links GitHub artifacts to Confab sessions |
| `hook_userpromptsubmit.go` | `user-prompt-submit` hook: ensures daemon is running |
| `hook_tooluse_input.go` | `readToolUseHookInput()` adapter mapping `ClaudeHookInput` / `CodexHookInput` into a shared `toolUseHookInput` shape for the pre/post-tool-use handlers |
| `hooks.go` | `confab hooks add/remove --provider <name>` — install/uninstall hooks for the selected provider via `p.InstallHooks()` |
| `sync.go` | `confab sync start/stop/status` — daemon management |
| `spawn.go` | Generic `maybeSpawnDaemon(p, *daemonLaunchInput)` — single dispatch for Claude, Codex, OpenCode, and Cursor daemon spawn. `daemonLaunchInput` is the canonical wire format between the hook and the freshly-spawned daemon process. For OpenCode, `TranscriptPath` is empty at spawn time — the daemon's collector materializes the transcript from the local SQLite DB. For Cursor, `Model` carries the session's LLM model from the `sessionStart` payload (read in `buildStandardLaunchArgs` via an optional `Model()` type-assert on the hook input); the daemon forwards it to the engine, which stamps it onto transcript chunk metadata (spm9). |
| `login.go` | Device code auth flow and API key login |
| `logout.go` | Clear stored credentials |
| `setup.go` | One-command setup: auth + hooks + bundled skills. Bare `confab setup --backend-url ...` auto-detects every provider whose CLI is on `PATH` **or** whose state/config dir is present (via `provider.DetectInstalled`, CF-572 — covers desktop-app installs) and installs hooks/skills for each. `--provider X` overrides to single-provider mode (`claude-code`, `codex`, `opencode`, or `cursor`). Cursor is now in `provider.DetectInstalled` (kata r5mg — `cursor-agent` on PATH or a present `~/.cursor` state dir, so IDE-only installs count), so bare `setup` configures it alongside the others; `--provider cursor` still scopes setup to Cursor only. `--config-dir <dir>` (requires `--provider`; claude-code only for now, kata hpec) installs into a non-default provider config dir and writes the backend creds to that `(provider, dir)` binding instead of the global top-level config — `setup --config-dir C1 --backend-url B1` then `--config-dir C2 --backend-url B2` route C1→B1 and C2→B2. Passing the default dir explicitly collapses to the global config. Best-effort across providers: per-provider failure is reported in a summary but doesn't abort the loop. |
| `status.go` | Show backend auth + per-provider hook/skill state for every supported provider (iterates `provider.OrderedNames()`). No `--provider` flag — output always covers all providers. A provider is "present" when its CLI is on `PATH` **or** its state/config dir exists (CF-572); the CLI line notes `(state dir present)` for desktop-only installs. No orphan-hook detection: installed hooks live inside the state dir, so `IsHooksInstalled ⟹ StateDirPresent` and an "orphaned" state is unreachable. |
| `list.go` | List local sessions (dispatches through `provider.Provider.ScanSessions`). Unsupported for OpenCode — `Opencode.ScanSessions` returns an explicit "live-sync only" error. |
| `list_utils.go` | Duration parsing, session filtering — fully provider-agnostic |
| `save.go` | Manual session upload by ID (dispatches through `provider.Provider.FindSessionByID` + `DefaultCWD`). Unsupported for OpenCode — `Opencode.FindSessionByID` returns an explicit "live-sync only" error. |
| `install.go` | Copy binary to `~/.local/bin/` |
| `update.go` | Check/install updates from GitHub Releases |
| `retro.go` | `confab retro` — fetch session transcript for retrospective (invoked by /retro skill) |
| `session.go` | Parent command for session subcommands (`confab session <cmd>`) |
| `session_get_summary.go` | `confab session get-summary` — fetch condensed session transcript from backend |
| `session_download.go` | `confab session download` — download raw JSONL transcript files from backend |
| `session_list_files.go` | `confab session list-files` — list transcript file metadata for a session |
| `skills.go` | `confab skills add/remove` — install/uninstall bundled skills for supported providers. `add` defaults to detected providers; `remove` defaults to all supported provider dirs. |
| `announce.go` | General announcement system for post-update feature notifications |
| `autoupdate.go` | Enable/disable auto-update |
| `version.go` | Print version info |
| `redaction.go` | Test redaction rules against a file |

## Command Tree

```
confab
├── hook
│   ├── session-start          (also: sync start)
│   ├── session-end            (also: sync stop)
│   ├── pre-tool-use
│   ├── post-tool-use
│   └── user-prompt-submit
├── sync
│   ├── start / stop
│   └── status
├── hooks
│   ├── add
│   └── remove
├── skills
│   ├── add
│   └── remove
├── session
│   ├── get-summary
│   ├── download
│   └── list-files
├── retro
├── login / logout
├── setup
├── status
├── list
├── save
├── install
├── update
├── autoupdate [enable|disable]
├── version
└── redaction-test
```

## How to Extend

### Adding a new command

1. Create `cmd/<name>.go`
2. Define a `cobra.Command` with `Use`, `Short`, `RunE`
3. In `init()`, call `rootCmd.AddCommand(<name>Cmd)` (or attach to a parent command)
4. Register flags in `init()` via `<name>Cmd.Flags()`
5. Follow existing patterns — look at `save.go` for a simple example, `login.go` for a complex one

### Adding a new hook type

This is a cross-cutting change spanning multiple packages:

1. **`cmd/hook_<name>.go`** — Create hook handler. Read JSON from stdin via `p.ParseSessionHook(r)`, do work, write the response via `p.WriteHookResponse(w, ...)`.
2. **`pkg/hookconfig/{claude,codex}.go`** — Add `Install<Name>Hook()`, `Uninstall<Name>Hook()`, `Is<Name>HookInstalled()`. Wire them into the provider's `InstallHooks` / `UninstallHooks` / `IsHooksInstalled` in `pkg/provider/{claude,codex}.go`.
3. **`cmd/hooks.go`** — No change needed; `p.InstallHooks()` covers it.
4. **`cmd/status.go`** — No change needed; `p.IsHooksInstalled()` covers it.
5. **`cmd/hook.go`** — Register the new hook command under `hookCmd`.

### Adding a new skill

1. **`pkg/config/skill_<name>.go`** — Add provider-rendered template constants/snippets.
2. **`pkg/config/bundled_skills.go`** — Add the skill name to `bundledSkillNames` and `bundledSkillTemplate`.
3. **`cmd/announce.go`** — Add an `Announcement` entry for Claude auto-rollout on update if the skill should be announced.
4. **Provider methods** — `Provider.InstallSkills()` / `UninstallSkills()` / `IsSkillInstalled()` automatically pick up the bundled registry when they call `pkg/config`.

## Invariants

- **All `io.ReadAll` calls must be bounded.** `login.go` and other commands that read HTTP responses or stdin use `io.LimitReader` to prevent memory exhaustion. Never use unbounded `io.ReadAll` on external input.
- **Environment variable duration overrides are capped.** `hook_sessionstart.go` caps env var durations (e.g., sync interval) to prevent abuse via unreasonable values.
- **Tar extraction in `update.go` has size and path limits.** Extracted files are bounded to prevent zip-bomb attacks, and paths are validated to prevent directory traversal.
- **Hook commands must read JSON from stdin and complete quickly.** Claude Code blocks waiting for hook responses. Long-running work must be delegated (e.g., daemon spawn).
- **Hook commands must not write to stdout except for `ClaudeHookResponse` JSON.** Claude Code parses stdout as the hook response. Use stderr for status messages.
- **Hook commands parse stdin via `p.ParseSessionHook(r)`.** Returns the provider-agnostic `provider.HookInput` view. Session hooks also validate `transcript_path`.
- **Hook handlers must always output valid JSON**, even on error. An error should produce a response with `continue: true` rather than crashing with no output.
- **Commands use `RunE` (not `Run`)** to return errors. Cobra handles error display.

## Design Decisions

**Hooks are thin wrappers.** Hook command files read stdin, call into `pkg/` packages, and write the response. Business logic lives in the packages, not in command handlers. This keeps hooks testable and the command layer simple.

**`hook.go` dispatches vs. separate binaries.** All hooks go through a single `confab hook <type>` command rather than separate binaries. This simplifies installation (one binary) and hook management (consistent command pattern).

**`spawn.go` uses `exec.Command` with `Setpgid`.** The daemon must outlive the hook command. `Setpgid: true` creates a new process group so the daemon isn't killed when the hook exits.

**`maybeSpawnDaemon(p, *daemonLaunchInput)` is generic over the provider.** Both `session-start` and `user-prompt-submit` call it. The function asks the provider's `ShouldSpawnForInput` gate, checks for an already-running daemon via `daemon.LoadStateForProvider` (calling `p.OnAlreadyRunning(externalID)` when the gate fires — OpenCode logs a Warn for multi-process resume, Claude/Codex no-op), prefers the launch input's `ParentPID` if non-zero (plugin-authoritative for OpenCode) and otherwise falls back to `p.FindParentPID()`. The walk runs regardless for observability — a Warn logs when plugin and walk disagree so production drift is visible (CF-549 M1). The `launchAsHookInput` internal adapter bridges the `HookInput` interface signature to the mutable `daemonLaunchInput` so `WalkUpToRoot` rewrites can land on the spawn-side struct.

**OpenCode resume path: `buildOpencodeLaunchArgs` reads `{session_id, cwd, parent_id?, parent_pid}` from stdin.** On `session.created`, `cwd` is inline and the build is a straight copy. On a reconcile event (`session.status`/`updated`/`compacted`/`error`), `cwd` is empty and `resolveOpencodeSessionInfo` reads `directory` + `parent_id` from OpenCode's SQLite via `provider.OpenCodeDBReader.ReadSessionInfo` with a 2-second context bound. If the lookup errors, a Warn is logged and the launch proceeds with empty defaults; if the row is absent (`sql.ErrNoRows`), the launch proceeds with empty defaults and a non-empty inline `parent_id` is preserved so subagent suppression still fires (CF-549).

**Reaper fires on every session-start.** `sessionStartFromReader` launches `daemon.ReapStaleStates()` in a goroutine so cleanup of state files left by crashed/killed daemons is opportunistic and non-blocking. Provider-agnostic; failures are debug-level.

**SessionStart routes every firing through `p.WalkUpToRoot`.** Identity for Claude; thread-edge walk for Codex. For Codex, every subagent SessionStart that lands in an already-running root tree becomes a no-op via state-file dedup. `confab save --provider codex <subagent-uuid>` performs the same walk-up so manual saves of any UUID in a tree always sync the whole tree.

**SessionStart keeps bundled skills aligned with hooks.** Claude runs announcements, which install missing skills and return a visible system message. Codex silently ensures bundled skills under `~/.codex/skills/` so users who installed hooks get the same Confab skills without extra setup.

**`list`, `save` route discovery through the `Provider` interface (CF-398).** Adding a new provider requires only `pkg/provider/<name>.go` + `<name>_discovery.go` — no changes in `cmd/`. The remaining `provider.NameClaudeCode` / `provider.NameCodex` references in `cmd/` are flag defaults (entry-point handling) and a couple of user-facing copy gates in `cmd/list.go` for the Codex-specific "save" hint.

**Pre/PostToolUse hook handlers route by `--provider`.** `cmd/hook_pretooluse.go` and `cmd/hook_posttooluse.go` resolve the provider via `resolveCommitLinkingProvider()` (normalizes the flag and gates on `Provider.SupportsCommitLinking()`), then read hook input through `cmd/hook_tooluse_input.go`'s `readToolUseHookInput()` adapter that maps either `ClaudeHookInput` or `CodexHookInput` into a shared `toolUseHookInput` shape. `getConfabSessionID(p, sessionID)` tries the firing UUID's daemon state first and walks up via `p.WalkUpToRoot` on miss — identity for Claude, SQLite walk for Codex (so subagent-initiated commits/PRs link to the root session). `hook_userpromptsubmit.go` remains hard-bound to `provider.ClaudeCode{}`: Codex's daemon liveness is parent-PID monitored, so the teleport case UserPromptSubmit addresses doesn't apply.

**Per-(provider, config dir) backend resolution is runtime-derived, not embedded (kata hpec).** Installed hook commands are byte-identical regardless of config dir. At runtime `configDirForHook(provider, transcriptPath)` (in `hook_sessionstart.go`) resolves which backend a Claude session belongs to: it **short-circuits to `""`** (the global/default binding) when `config.HasBindings` is false — so pure single-dir users run an unchanged path — otherwise it derives the config dir from `transcriptPath` via `ClaudeCode.ConfigDirFromTranscript` (failure also falls back to default). SessionStart/UserPromptSubmit thread the derived dir into the daemon launch (`daemonLaunchInput.ConfigDir`); Pre/PostToolUse use `uploadConfigForHook(p, transcriptPath)` → `provider.BindingFor` → `config.GetUploadConfigFor` so commit/PR links use the session's own backend. A derived custom dir with no stored binding returns `ErrNoBinding` (link/sync skipped, never the default backend). Non-Claude providers always resolve to the default binding (their `--config-dir` support is a fast-follow).

**OpenCode lifecycle is plugin-driven; data sync is the daemon's job.** OpenCode has no settings/config hook system, so `confab setup` installs a TS plugin into `~/.config/opencode/plugins/`. The plugin only fires `confab hook session-start` / `session-end --provider opencode` for lifecycle; it never streams transcript data. The spawned daemon's collector reads OpenCode's local SQLite DB and materializes a transcript file. Because discovery (`list`/`save`) needs an on-disk transcript, those commands are unsupported for OpenCode (the provider returns explicit errors); OpenCode is live-sync only.

**Cursor reuses the Claude file-first lifecycle with a hybrid shutdown (kata mpys/6kys).** SessionStart needs no `cmd/` cursor branch: Cursor's `transcript_path` is `null` at sessionStart, but `Cursor.ParseSessionHook` derives it from `workspace_roots[0]` + `session_id`, so the standard `buildStandardLaunchArgs` path produces a non-empty path and `maybeSpawnDaemon`'s "transcript_path required" gate passes unchanged. The daemon then runs the same file-watch path as Claude (`waitForTranscript` handles the file lagging sessionStart; no OpenCode-style collector). SessionEnd routes to `sessionEndCursor` (`StopDaemonForProvider(NameCursor, ...)`) — Cursor's daemon state lives under the `cursor` namespace, so the Claude-hardcoded `StopDaemon` would never find it. Shutdown is **hybrid**: CLI `sessionEnd` is reliable, but the IDE only fires it on window/app close (not per chat-tab), so the daemon's generic parent-PID liveness on the shared, long-lived `Cursor.app` is the primary IDE shutdown. Caveat: a long IDE session with multiple chats keeps per-session daemons alive (syncing incrementally) until window close.

**Backend session commands share auth/client setup.** `helpers.go` owns the repeated `EnsureAuthenticated` + `pkg/http.NewClient` path and the common "session not found" translation for session fetch/list/download commands. Keep endpoint-specific behavior in the command files, not in the helper.

**Testable function pattern.** Hook handlers extract core logic into functions that take `io.Reader`/`io.Writer` parameters (e.g., `sessionStartFromReader(r io.Reader, w io.Writer)`). Tests call these directly without needing stdin/stdout. Some functions use overridable function variables (e.g., `spawnDaemonFunc`) for test injection.

## Testing

```bash
go test ./cmd/...
```

Tests use the `io.Reader`/`io.Writer` pattern and function variable overrides to test hook behavior without actual process spawning or stdin/stdout.

## Dependencies

**Uses:** all `pkg/` packages

**Used by:** `main.go` (calls `cmd.Execute()`)
