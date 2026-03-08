# pkg/types

Shared type definitions used across packages to avoid circular imports.

## Files

| File | Role |
|------|------|
| `types.go` | All shared types, constants, and the JSONL scanner factory |

## Key Types

### `HookInput`

Union type for all Claude Code hook events. A single struct carries fields for every hook type — unused fields are zero-valued. This is intentional: the number of hook types is small and their fields are largely orthogonal, so splitting into separate types would add complexity without benefit.

**Always-present fields:** `SessionID`, `TranscriptPath`

**Hook-specific fields:**
- `UserPromptSubmit`: `Prompt`
- `PreToolUse` / `PostToolUse`: `ToolName`, `ToolInput`, `ToolUseID`, `ToolResponse`
- `SessionStart` / `SessionEnd`: `Reason`

### `HookResponse` / `PreToolUseResponse`

Response types written to stdout for Claude Code to consume. `PreToolUseResponse` includes `HookSpecificOutput` with permission decisions (allow/deny with instructions).

### `InboxEvent`

Used for inter-process communication between the `sync stop` command and the running daemon. Serialized as JSONL in the inbox file.

### `NewJSONLScanner(reader)`

Factory that creates a `bufio.Scanner` with a 10MB buffer (`MaxJSONLLineSize`). Transcript lines can be very large (thinking blocks, tool results), so the default 64KB buffer is insufficient.

## How to Extend

**Adding a field to `HookInput`:** Add the field with `json:",omitempty"`. No need to update `ReadHookInput()` — `json.Unmarshal` handles new fields automatically. If the field requires validation, add it to the validation block in `ReadHookInput()`.

**Adding a new shared type:** Add it here only if it's needed by 2+ packages that would otherwise create a circular import. Package-specific types belong in their own package.

## Invariants

- `HookInput.SessionID` is validated as non-empty in `ReadHookInput()` — all downstream code can assume it's set.
- `MaxJSONLLineSize` (10MB) must accommodate the largest possible transcript line. Changing this affects every JSONL reader in the codebase.
- `NewJSONLScanner` must be used everywhere JSONL files are read — never create a bare `bufio.Scanner` for transcript files.

## Dependencies

**Uses:** standard library only

**Used by:** nearly every package (`cmd/`, `pkg/daemon/`, `pkg/discovery/`, `pkg/sync/`)
