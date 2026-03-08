# pkg/sync

Sync engine that orchestrates incremental transcript uploads to the backend. Handles file tracking, chunking, agent discovery, metadata extraction, and summary linking.

## Files

| File | Role |
|------|------|
| `engine.go` | `Engine` — orchestrates init, sync loop, agent discovery (BFS), metadata extraction |
| `client.go` | `Client` — HTTP API methods for init, chunk upload, events, summary updates, GitHub linking |
| `tracker.go` | `FileTracker` — tracks file state, reads chunks with byte-offset seeking, discovers agent files |
| `summary_link.go` | Links child session summaries to parent sessions via `leafUuid` |

## Three Components

### Engine (orchestrator)
`Engine.Init()` registers the session with the backend, receiving the current sync state (last synced line per file). `Engine.SyncAll()` performs a BFS traversal: for each tracked file, it checks for changes, reads a chunk, extracts metadata, uploads, and discovers new agent files. New agent files are added to the queue for the next iteration.

### Client (API)
Thin wrapper around `pkg/http.Client` that marshals/unmarshals request types for the sync API endpoints: `/api/v1/sync/init`, `/api/v1/sync/chunk`, `/api/v1/sync/event`, and session-specific endpoints for summaries and GitHub links.

### FileTracker (file I/O + state)
Manages the mapping between files on disk and their sync state. `ReadChunk()` seeks to the last known byte offset, reads new lines up to the chunk size limit, applies redaction, and extracts agent IDs. `DiscoverNewFiles()` finds new agent files both from collected agent IDs and by scanning the subagents directory.

## Data Flow

```
SyncAll() loop:
  HasFileChanged? ──no──> skip
       │yes
       ▼
  ReadChunk(maxBytes)
    ├── seek to ByteOffset
    ├── read lines until maxBytes
    ├── extract agent IDs (pre-redaction)
    ├── extract metadata (pre-redaction)
    ├── apply redaction
    └── return Chunk
       │
       ▼
  UploadChunk (redacted lines + redacted metadata)
       │
       ▼
  UpdateAfterSync (update byte offset + line count)
       │
       ▼
  DiscoverNewFiles (from collected agent IDs + directory scan)
       │
       ▼
  Add new files to queue → repeat
```

## How to Extend

**Adding a new API endpoint:** Add request/response types in `client.go`, add a method on `Client`, call it from the engine or command layer.

**Adding new metadata extraction:** Modify the metadata extraction section in `SyncAll()` where `ChunkMetadata` is built. Metadata is extracted from **raw lines before redaction**, then the extracted values are redacted separately before upload.

**Tracking a new file type:** Add discovery logic in `DiscoverNewFiles()`. Set the file type in `TrackedFile.Type`. The rest of the pipeline (read, chunk, upload) is file-type agnostic.

## Invariants

- **Chunks must not exceed 14MB** (`DefaultMaxChunkBytes`). The backend rejects larger payloads. The limit is 14MB not 16MB to leave headroom for JSON encoding overhead.
- **`Init()` must be called before `SyncAll()`.** The engine needs a backend session ID and initial sync state.
- **After upload failure, state must be refreshed from backend** (`refreshStateFromBackend`). This handles the case where the server received and stored data but the client timed out before receiving the response. Without refresh, the client would re-upload duplicate lines.
- **Agent discovery uses BFS with cycle detection.** The `knownAgentIDs` set prevents infinite loops when agents reference each other. Max 10 BFS iterations as a safety bound.
- **Redaction must happen in `ReadChunk()` before lines leave the tracker.** Never upload unredacted content.
- **Metadata is extracted before redaction, then redacted.** Summaries and first user messages need the original text for meaningful extraction, but must be redacted before upload.
- **Byte offsets must be maintained accurately.** `ReadChunk` returns `NewOffset` which is the byte position after the last line read. `UpdateAfterSync` stores this for the next read. Incorrect offsets cause duplicate or missing lines.
- **Directory scan in `DiscoverNewFiles` catches agents from already-synced lines.** After a daemon restart, agent IDs from previously-synced lines are lost from memory. The directory scan recovers them.

## Design Decisions

**BFS for agent discovery.** Agents can spawn sub-agents transitively (A references B, B references C). BFS ensures all transitive agents are discovered and synced, not just direct children. The iteration cap (10) prevents runaway discovery.

**Byte-offset seeking instead of re-reading.** For large transcripts (megabytes), seeking to the last read position is far more efficient than re-reading from the start and skipping lines.

**`refreshStateFromBackend` after upload failure.** When a chunk upload times out, the server may have stored the data. Without refreshing, the next `SyncAll()` would re-upload the same lines. The refresh call gets the server's actual `LastSyncedLine` and updates the tracker accordingly. Auth errors during refresh are propagated (can't recover without re-auth).

**Summary link injection.** When a transcript contains a summary with a `leafUuid`, it means this session is a continuation of a previous one. `linkSummaryToPreviousSession` finds the parent transcript by scanning other JSONL files for the matching UUID, then calls the backend to update the parent's summary. This is best-effort — failures are logged but don't block sync.

## Testing

```bash
go test ./pkg/sync/...
```

- **`NewWithClient()`** allows injecting a mock client for unit tests
- **`engine_test.go` / `tracker_test.go`** — unit tests for incremental sync, agent discovery, byte offsets, chunking
- **`integration_test.go`** — full engine lifecycle with mock HTTP backend: init, multi-cycle sync, agent discovery, error recovery, large files, chunk size limits

## Dependencies

**Uses:** `pkg/http`, `pkg/redactor`, `pkg/discovery`, `pkg/git`, `pkg/config`, `pkg/types`, `pkg/logger`

**Used by:** `pkg/daemon/` (sync loop), `cmd/` (save command, post-tool-use linking)
