# pkg/discovery

Session discovery, metadata extraction, agent ID parsing, and hook input reading. This package finds Claude Code sessions on disk and extracts useful information from their transcripts.

## Files

| File | Role |
|------|------|
| `sessions.go` | `ScanAllSessions`, `FindSessionByID`, `GetProjectsDir` — find sessions in `~/.claude/projects/` |
| `extract.go` | `ExtractSessionMetadata`, `ExtractMetadataFromLines`, `SanitizeText` — extract summaries and first user messages from transcripts |
| `files.go` | `ExtractAgentIDsFromMessage`, `IsValidAgentID` — find agent IDs in transcript entries |
| `hook.go` | `ReadHookInputFrom` — parse hook input JSON from stdin |

## Key Types

### `SessionInfo`
Returned by `ScanAllSessions`. Contains session ID, transcript path, project path, mod time, size, summary, and first user message.

### `ExtractionResult`
Returned by `ExtractMetadataFromLines`. Contains `Summary` (last local summary), `FirstUserMessage`, and `SummaryLinks` (summaries linking to previous sessions via `leafUuid`).

## How to Extend

**Supporting new transcript metadata fields:** Modify `ExtractMetadataFromLines()` in `extract.go`. Add a new field to `ExtractionResult`, then extract it from the parsed JSON entries. Follow the existing pattern of checking message type and extracting from the appropriate nested field.

**Supporting new agent ID formats:** Update `IsValidAgentID()` in `files.go`. The current validation accepts alphanumeric chars plus hyphen and underscore, with a minimum length of 6. If new formats use different characters, update `isAgentIDChar()`.

**Adding new hook input fields:** The hook input type lives in `pkg/types` (`HookInput`), not here. This package just reads and validates it.

## Invariants

- **Session IDs are exactly 36 characters** (UUID length). Files with other name lengths are silently skipped by `ScanAllSessions`. Don't change this — it's how sessions are distinguished from other JSONL files.
- **Agent files use `agent-` prefix.** The scanner skips these when listing sessions but they're picked up by `pkg/sync` for syncing alongside the main transcript.
- **Metadata extraction reads at most 50 lines** (`MaxLinesForExtraction`). This is a performance bound — transcripts can be very large. Summary and first user message are expected to appear near the beginning.
- **`FirstUserMessage` is truncated to 4KB** (half of `MaxMetadataFieldSize`). This is a backend-imposed limit.
- **Scanning continues on permission errors.** Users may have mixed-permission directories under `~/.claude/projects/`. Failed directories are reported to stderr but don't fail the operation.

## Design Decisions

**Separate `ExtractMetadataFromLines` vs `ExtractSessionMetadata`.** `ExtractSessionMetadata` reads from a file path (used during session scanning). `ExtractMetadataFromLines` operates on in-memory lines (used by `pkg/sync` during chunk processing). The extraction logic is shared; only the I/O differs.

**HTML sanitization on summaries.** Claude Code summaries contain HTML tags (`<b>`, `<i>`, etc.) and entities. `SanitizeText` strips tags, decodes entities, and normalizes whitespace so summaries are clean plain text.

**Agent ID extraction checks two locations.** Agent IDs appear either at the root level (`message.toolUseResult.agentId`) or nested in content blocks. Both paths must be checked — the format depends on the Claude Code version.

## Testing

```bash
go test ./pkg/discovery/...
```

Tests create temporary directory structures mimicking `~/.claude/projects/` with synthetic JSONL files.

## Dependencies

**Uses:** `pkg/types` (HookInput), `pkg/config` (GetProjectsDir), `pkg/logger`

**Used by:** `cmd/` (list, save, hook handlers), `pkg/sync/` (agent ID extraction, metadata)
