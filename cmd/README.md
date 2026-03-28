# cmd/

CLI command layer built on [Cobra](https://github.com/spf13/cobra). Each file defines one or more commands and registers them via `init()`.

## Files

| File | Role |
|------|------|
| `root.go` | Root command, persistent pre/post hooks, logger init |
| `hook.go` | Parent command for hook handlers (`confab hook <type>`) |
| `hook_sessionstart.go` | `session-start` hook: spawns sync daemon |
| `hook_sessionend.go` | `session-end` hook: stops sync daemon |
| `hook_pretooluse.go` | `pre-tool-use` hook: injects Confab links into git commits and PRs |
| `hook_posttooluse.go` | `post-tool-use` hook: links GitHub artifacts to Confab sessions |
| `hook_userpromptsubmit.go` | `user-prompt-submit` hook: ensures daemon is running |
| `hooks.go` | `confab hooks add/remove` — install/uninstall hooks in Claude Code settings |
| `sync.go` | `confab sync start/stop/status` — daemon management |
| `spawn.go` | Daemon spawning utilities, Claude PID detection |
| `login.go` | Device code auth flow and API key login |
| `logout.go` | Clear stored credentials |
| `setup.go` | One-command setup: auth + hooks |
| `status.go` | Show hook and auth status |
| `list.go` | List local sessions |
| `list_utils.go` | Duration parsing, session filtering |
| `save.go` | Manual session upload by ID |
| `install.go` | Copy binary to `~/.local/bin/` |
| `update.go` | Check/install updates from GitHub Releases |
| `til.go` | `confab til` — save a TIL to the backend (invoked by /til skill) |
| `retro.go` | `confab retro` — fetch session transcript for retrospective (invoked by /retro skill) |
| `session.go` | Parent command for session subcommands (`confab session <cmd>`) |
| `session_get_summary.go` | `confab session get-summary` — fetch condensed session transcript from backend |
| `session_download.go` | `confab session download` — download raw JSONL transcript files from backend |
| `session_list_files.go` | `confab session list-files` — list transcript file metadata for a session |
| `skills.go` | `confab skills add/remove` — install/uninstall Claude Code skills |
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
├── til
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

1. **`cmd/hook_<name>.go`** — Create hook handler. Read JSON from stdin, do work, write `HookResponse` JSON to stdout
2. **`pkg/config/config.go`** — Add `Install<Name>Hook()`, `Uninstall<Name>Hook()`, `Is<Name>HookInstalled()`
3. **`cmd/hooks.go`** — Add install/uninstall calls in `hooksAddCmd` and `hooksRemoveCmd`
4. **`cmd/status.go`** — Add status check for the new hook
5. **`cmd/setup.go`** — Add to the setup flow
6. **`cmd/hook.go`** — Register the new hook command under `hookCmd`

### Adding a new skill

1. **`pkg/config/skill_<name>.go`** — Add template constant, `Install<Name>Skill()`, `Uninstall<Name>Skill()`, `Is<Name>SkillInstalled()`, `Ensure<Name>Skill()`
2. **`cmd/skills.go`** — Add install/uninstall calls in `skillsAddCmd` and `skillsRemoveCmd`
3. **`cmd/announce.go`** — Add an `Announcement` entry for auto-rollout on update
4. **`cmd/status.go`** — Add status check for the new skill
5. **`cmd/setup.go`** — Add to the setup flow

## Invariants

- **All `io.ReadAll` calls must be bounded.** `login.go` and other commands that read HTTP responses or stdin use `io.LimitReader` to prevent memory exhaustion. Never use unbounded `io.ReadAll` on external input.
- **Environment variable duration overrides are capped.** `hook_sessionstart.go` caps env var durations (e.g., sync interval) to prevent abuse via unreasonable values.
- **Tar extraction in `update.go` has size and path limits.** Extracted files are bounded to prevent zip-bomb attacks, and paths are validated to prevent directory traversal.
- **Hook commands must read JSON from stdin and complete quickly.** Claude Code blocks waiting for hook responses. Long-running work must be delegated (e.g., daemon spawn).
- **Hook commands must not write to stdout except for `HookResponse` JSON.** Claude Code parses stdout as the hook response. Use stderr for status messages.
- **All hooks use `pkg/types.HookInput`.** Parsed via `types.ReadHookInput(os.Stdin)` (base validation) or `discovery.ReadHookInputFrom(os.Stdin)` (adds `transcript_path` validation for session hooks).
- **Hook handlers must always output valid JSON**, even on error. An error should produce a response with `continue: true` rather than crashing with no output.
- **Commands use `RunE` (not `Run`)** to return errors. Cobra handles error display.

## Design Decisions

**Hooks are thin wrappers.** Hook command files read stdin, call into `pkg/` packages, and write the response. Business logic lives in the packages, not in command handlers. This keeps hooks testable and the command layer simple.

**`hook.go` dispatches vs. separate binaries.** All hooks go through a single `confab hook <type>` command rather than separate binaries. This simplifies installation (one binary) and hook management (consistent command pattern).

**`spawn.go` uses `os.StartProcess` with `Setpgid`.** The daemon must outlive the hook command. `Setpgid: true` creates a new process group so the daemon isn't killed when the hook exits. `exec.Command` with `Start()` would work too, but `os.StartProcess` gives more control over the process attributes.

**`maybeSpawnDaemon` is called from both `session-start` and `user-prompt-submit`.** The `user-prompt-submit` hook handles the "teleport" case where Claude Code resumes a session without firing `SessionStart`. If the daemon isn't running, it spawns one.

**Testable function pattern.** Hook handlers extract core logic into functions that take `io.Reader`/`io.Writer` parameters (e.g., `sessionStartFromReader(r io.Reader, w io.Writer)`). Tests call these directly without needing stdin/stdout. Some functions use overridable function variables (e.g., `spawnDaemonFunc`) for test injection.

## Testing

```bash
go test ./cmd/...
```

Tests use the `io.Reader`/`io.Writer` pattern and function variable overrides to test hook behavior without actual process spawning or stdin/stdout.

## Dependencies

**Uses:** all `pkg/` packages

**Used by:** `main.go` (calls `cmd.Execute()`)
