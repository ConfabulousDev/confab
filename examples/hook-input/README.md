# Claude Code Hook Input Examples

These JSON files show the format of hook input that Claude Code sends to confab via stdin.

## When hooks are triggered

- **SessionStart**: When a Claude Code session begins
- **SessionEnd**: When a session ends (user exits, timeout, etc.)

## Fields

| Field | Description |
|-------|-------------|
| `session_id` | Unique session identifier (UUID) |
| `transcript_path` | Path to the session's JSONL transcript file |
| `cwd` | Working directory where Claude Code was launched |
| `permission_mode` | Permission mode (e.g., "default") |
| `hook_event_name` | Event type ("SessionStart" or "SessionEnd") |
| `reason` | Why the event occurred (e.g., "exit") |

## Usage

These are reference examples only. In practice, Claude Code invokes confab hooks automatically:

```bash
# Configured in ~/.claude/settings.json by `confab hooks add`
confab sync start   # Called on SessionStart (reads JSON from stdin)
confab sync stop    # Called on SessionEnd (reads JSON from stdin)
```

For manual testing of sync commands, you could use:

```bash
cat examples/hook-input/test_hook_input.json | ./confab sync start
```

**Warning**: This spawns a real background daemon process. Use with caution.
