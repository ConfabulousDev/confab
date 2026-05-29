# pkg/types

Shared type definitions used across packages to avoid circular imports.

## Files

| File | Role |
|------|------|
| `types.go` | All shared types, constants, and the JSONL scanner factory |

## Key Types

### `ClaudeHookInput`

Union type for all Claude Code hook events. A single struct carries fields for every hook type — unused fields are zero-valued. This is intentional: the number of hook types is small and their fields are largely orthogonal, so splitting into separate types would add complexity without benefit.

**Always-present fields:** `SessionID` (validated by `ReadClaudeHookInput`). `TranscriptPath` is present for session hooks but not validated at this level — the Claude provider adds that validation.

**Hook-specific fields:**
- `UserPromptSubmit`: `Prompt`
- `PreToolUse` / `PostToolUse`: `ToolName`, `ToolInput`, `ToolUseID`, `ToolResponse`
- `SessionStart` / `SessionEnd`: `Reason`

### `ClaudeHookResponse` / `PreToolUseResponse`

Response types written to stdout for the harness to consume. `PreToolUseResponse` includes `HookSpecificOutput` (a `PreToolUseOutput`) with permission decisions (allow/deny with instructions). `PreToolUseResponse` is provider-agnostic — Claude Code and Codex both accept the same shape.

### `CodexHookInput` / `CodexHookResponse`

Codex counterparts to the Claude types. `CodexHookInput` is a union type carrying fields from all Codex hook events; `ToolResponseMap()` normalizes Codex's `tool_response` value (the shell tool sends a plain string, other tools send a map) into a `map[string]any`. `CodexHookResponse` is the JSON response sent back to Codex hooks.

### `InboxEvent`

Used for inter-process communication between the `sync stop` command and the running daemon. Serialized as JSONL in the inbox file.

### `ValidateSessionID(id)`

Validates that a session ID contains only safe characters (alphanumeric, hyphens, underscores) using the `sessionIDPattern` regex. Called by `ReadClaudeHookInput` to reject malformed session IDs before they reach downstream code.

### `NewJSONLScanner(reader)`

Factory that creates a `bufio.Scanner` with a 10MB buffer (`MaxJSONLLineSize`). Transcript lines can be very large (thinking blocks, tool results), so the default 64KB buffer is insufficient.

## How to Extend

**Adding a field to `ClaudeHookInput`:** Add the field with `json:",omitempty"`. No need to update `ReadClaudeHookInput()` — `json.Unmarshal` handles new fields automatically. If the field requires validation, add it to the validation block in `ReadClaudeHookInput()`.

**Adding a new shared type:** Add it here only if it's needed by 2+ packages that would otherwise create a circular import. Package-specific types belong in their own package.

## Invariants

- `ClaudeHookInput.SessionID` is validated as non-empty and safe (alphanumeric, hyphens, underscores only) in `ReadClaudeHookInput()` — all downstream code can assume it's set and safe for use in file paths.
- `ReadClaudeHookInput()` uses bounded `io.ReadAll` (limited to `MaxJSONLLineSize`) to prevent memory exhaustion from oversized input.
- `MaxJSONLLineSize` (10MB) must accommodate the largest possible transcript line. Changing this affects every JSONL reader in the codebase.
- `NewJSONLScanner` must be used everywhere JSONL files are read — never create a bare `bufio.Scanner` for transcript files.

## Dependencies

**Uses:** standard library only

**Used by:** nearly every package (`cmd/`, `pkg/daemon/`, `pkg/git/`, `pkg/provider/`, `pkg/redactor/`, `pkg/sync/`)
