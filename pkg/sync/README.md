# pkg/sync

Sync engine that orchestrates incremental transcript uploads to the backend. Handles file tracking, chunking, agent discovery, and chunk upload. Provider-specific behavior (metadata extraction, descendant discovery, root metadata attachment) lives entirely in `pkg/provider`; the engine dispatches through the `provider.Provider` interface (see CF-397).

The engine is fully **file-based** and provider-agnostic about its source: it reads whatever local file the daemon points `transcriptPath` at. For Claude/Codex that is the tool's own JSONL; for OpenCode the daemon **materializes** the HTTP-API session into `~/.confab/opencode/<id>/messages.jsonl` and points `transcriptPath` there, so the materialized file is tracked, redacted (`ReadChunk`), chunked, and uploaded exactly like any transcript — no OpenCode-specific code in this package. Because `TrackedFile` separates local `Path` from backend `Name`, the backend `file_name` is just the base (`messages.jsonl`).

## Files

| File | Role |
|------|------|
| `engine.go` | `Engine` — orchestrates init, sync loop, agent discovery (BFS); dispatches provider behavior via `InitTranscript`/`DiscoverDescendants`/`DiscoverWorkflowFiles`/`AnnotateChunk`. Owns workflow-file capability gating (`resolveCaps`, `workflowFileTypeAllowed`). Includes the `chunkView` adapter that satisfies `provider.ChunkView` |
| `client.go` | `Client` — HTTP API methods for init, chunk upload, events, summary updates, GitHub linking, and the `Capabilities()` probe (`GET /api/v1/capabilities`). Defines the `Capabilities` struct; aliases `provider.CodexRolloutMetadata` as `sync.CodexRolloutMetadata` |
| `tracker.go` | `FileTracker` — tracks file state, reads chunks with byte-offset seeking, discovers agent files (Claude transitive discovery). Implements `provider.TranscriptRegistrar` (via `*TrackedFile.SetCodexRollout`), `provider.DescendantRegistrar` (via `*FileTracker.RegisterCodexRollout`), and `provider.WorkflowRegistrar` (via `SubagentsDir` + `RegisterWorkflowFile`) so providers can register Codex rollouts and Claude workflow files |
| `summary_link.go` | Links child session summaries to parent sessions via `leafUuid` |

## Three Components

### Engine (orchestrator)
`Engine.Init()` registers the session with the backend, receiving the current sync state (last synced line per file), then calls `provider.InitTranscript(transcript, ...)` so the provider can attach root-level metadata (Codex attaches `codex_rollout`; Claude is a no-op). `Engine.SyncAll()` performs a BFS traversal: it first calls `provider.DiscoverDescendants(tracker, externalID)` once per cycle (Codex walks the SQLite subtree; Claude is a no-op) and `provider.DiscoverWorkflowFiles(tracker, allow)` (Claude scans `subagents/workflows/`; Codex is a no-op), then for each tracked file checks for changes, reads a chunk, dispatches `provider.AnnotateChunk(chunkView, sentFirst, redact)`, uploads, and discovers new agent files via `tracker.DiscoverNewFiles` (Claude's transitive content-driven discovery). Codex descendants are registered as `file_type=agent` sidechain files under the root's backend session.

### Workflow subagent files + capability gating (CF-533)

Claude's `Workflow` tool spawns subagents whose transcripts live at
`<session>/subagents/workflows/<runId>/agent-<id>.jsonl`, plus a per-run
`journal.jsonl`. These carry no `agentId` in the main transcript, so the
Claude provider discovers them by **scanning that directory** (not via
`ExtractAgentIDsFromMessage`) in `provider.DiscoverWorkflowFiles`, and registers
them through `FileTracker.RegisterWorkflowFile` with **path-encoded** backend
`file_name`s (forward slashes, written verbatim — the backend resolves
`<runId>` from the path and `<id>` via `path.Base`):

- `subagents/workflows/<runId>/agent-<id>.jsonl` → `file_type=agent`
- `subagents/workflows/<runId>/journal.jsonl` → `file_type=workflow_journal`

`agent-<id>.meta.json`, `wf_<runId>.json`, and the script file are **not**
uploaded. Once registered, workflow files flow through the ordinary
`ReadChunk`→`UploadChunk` path: incremental byte-offset/line tracking,
idempotent re-sync, and **redaction applies to every line** (including the
journal's free-text `result`).

**Capability gate.** Because the CLI may be newer than a self-hosted backend,
uploads are gated on the backend advertising support. The engine probes
`GET /api/v1/capabilities` (body **is** `{"workflow_files":bool,"workflow_journal":bool}`,
no wrapper) **lazily** — only when the provider actually finds a workflow run
dir, so non-workflow sessions never probe. `resolveCaps` caches only
**definitive** answers: a `404` (old backend → both false) or a clean `200`
(parsed flags). A transient failure (network / timeout / 5xx / malformed body)
is **not** cached, so the next cycle re-probes. `workflowFileTypeAllowed`
gates **per flag**: `agent` files require `workflow_files`, the journal
requires `workflow_journal` — a backend reporting only one uploads only that
kind. Once the backend has definitively reported support for neither, the
engine short-circuits the provider call so the directory is no longer scanned.

#### `journal.jsonl` line schema (the CF-534/535 read contract)

The CLI uploads `journal.jsonl` **verbatim and never parses it**; the schema
below is what is observed on disk and is owned by the Claude **Workflow
harness**, so it **may evolve** — consumers (CF-534 Workflows card, CF-535
Agents-card rows) must tolerate unknown and missing fields. Each line is one
JSON object:

```jsonc
// agent lifecycle markers, keyed by a content-addressed dedup key:
{"type": "started", "key": "v2:<hash>", "agentId": "<id>"}
{"type": "result",  "key": "v2:<hash>", "agentId": "<id>", "result": <string|object>}
```

Fields: `type` (`"started"` | `"result"`), `key` (dedup key), `agentId`
(matches the run dir's `agent-<id>.jsonl`), and `result` (present only on
`result` lines; an arbitrary JSON string or object — the subagent's return
value). Observed line types are `started`/`result`; treat the set as open.

### Client (API)
Thin wrapper around `pkg/http.Client` that marshals/unmarshals request types for the sync API endpoints: `/api/v1/sync/init`, `/api/v1/sync/chunk`, `/api/v1/sync/event`, and session-specific endpoints for summaries and GitHub links.

### FileTracker (file I/O + state)
Manages the mapping between files on disk and their sync state. `ReadChunk()` seeks to the last known byte offset, reads new lines up to the chunk size limit, applies redaction, and extracts agent IDs. `DiscoverNewFiles()` finds new agent files both from collected agent IDs and by scanning the subagents directory.

Per-chunk `git_info` extraction (CF-493) is provider-agnostic with two paths in `ReadChunk`, each guarded by the `gitInfo == nil` first-wins check:
- `gitInfoFromClaudeMessage` — Claude transcript messages carry inline `gitBranch` + `cwd`; populates `Branch`, `RepoURL`, `Remotes`, `TrackingRemote`.
- `gitInfoFromCodexSessionMeta` — Codex rollouts (both root transcripts and descendant agent files) begin with a `session_meta` line whose payload carries `cwd`; runs `git.DetectBranch(cwd)` and populates all four CF-494-resolver-required fields.

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

**Adding new metadata extraction:** Modify the appropriate provider's `AnnotateChunk` in `pkg/provider/{claude,codex,opencode}.go`. Metadata is extracted from **raw lines before redaction**, then the extracted values are redacted via the closure passed to `AnnotateChunk` before being attached to the chunk via the `ChunkView` setters.

**Tracking a new file type:** Add discovery logic in `DiscoverNewFiles()` (for content-driven discovery), the provider's `DiscoverDescendants` (for external-state discovery), or `DiscoverWorkflowFiles` (for directory-scanned, capability-gated workflow files). Set the file type in `TrackedFile.Type`. The rest of the pipeline (read, chunk, upload) is file-type agnostic.

**Gating behavior on backend support:** Add a field to the `Capabilities` struct (`client.go`), have the backend advertise it via `GET /api/v1/capabilities`, and gate in the engine. Default any absent field to `false` (older backends omit the endpoint → `404`); cache only definitive answers and re-probe on transient failures.

**Adding a new provider:** Implement `provider.Provider` (including the sync-loop methods `InitTranscript`, `DiscoverDescendants`, `DiscoverWorkflowFiles`, `AnnotateChunk`) and register it in `pkg/provider/provider.go`'s registry. Zero changes required in `pkg/sync/engine.go` — the engine dispatches everything through the interface.

## Invariants

- **Chunks must not exceed 14MB** (`DefaultMaxChunkBytes`). The backend rejects larger payloads. The limit is 14MB not 16MB to leave headroom for JSON encoding overhead.
- **`Init()` must be called before `SyncAll()`.** The engine needs a backend session ID and initial sync state.
- **After upload failure, state must be refreshed from backend** (`refreshStateFromBackend`). This handles the case where the server received and stored data but the client timed out before receiving the response. Without refresh, the client would re-upload duplicate lines. `applyBackendFiles` is the shared path for initial and refreshed backend file state.
- **Agent discovery uses BFS with cycle detection.** The `knownAgentIDs` set prevents infinite loops when agents reference each other. Max 10 BFS iterations as a safety bound.
- **Redaction must happen in `ReadChunk()` before lines leave the tracker.** Never upload unredacted content. The same call site covers Claude transcripts, Claude agent files, and Codex rollouts; `redactor.RedactJSONLine` is JSON-shape-agnostic, so no per-provider branching is needed.
- **Metadata is extracted before redaction, then redacted.** Summaries and first user messages need the original text for meaningful extraction, but must be redacted before upload.
- **Byte offsets must be maintained accurately.** `ReadChunk` returns `NewOffset` which is the byte position after the last line read. `UpdateAfterSync` stores this for the next read. Incorrect offsets cause duplicate or missing lines.
- **Directory scan in `DiscoverNewFiles` catches agents from already-synced lines.** After a daemon restart, agent IDs from previously-synced lines are lost from memory. The directory scan recovers them.
- **`codex_rollout` metadata rides on first chunks only.** `provider.Codex.AnnotateChunk` attaches `ChunkMetadata.CodexRollout` whenever `c.FirstLine() == 1` and the tracked file carries a `CodexRollout`. On retry after a failed upload, `FirstLine` remains 1 so the metadata is automatically resent — the backend upsert is idempotent. `InitFromBackendState` preserves `TrackedFile.CodexRollout` across `refreshStateFromBackend` so retries don't lose the payload.
- **The engine has no provider-name branches.** `TestEngine_NoProviderNameLiterals` in `engine_dispatch_test.go` scans `engine.go` for `NameCodex` / `NameClaudeCode` literals and fails CI if either appears. New provider-specific behavior must live in `pkg/provider`, not the engine.
- **Workflow uploads are capability-gated, and gating is per-flag.** Never send `workflow_journal` files or path-encoded agent names to a backend that didn't advertise the matching flag — an older backend would silently mis-store them. The `allow` predicate is the single gate; the provider classifies, the engine decides.
- **Backend capability is assumed stable for a backend.** CF-532 ships both flags permanently, so the engine caches a definitive answer for its lifetime and does not handle a mid-life downgrade (a daemon restart re-probes a fresh engine). Only transient probe failures re-probe within a session.
- **Workflow `file_name`s are path-encoded with forward slashes and written verbatim.** `subagents/workflows/<runId>/...` is load-bearing: the backend parses `<runId>` from the path and `<id>` via `path.Base`. Never flatten them. Redaction still applies to every workflow line, including the journal.

## Design Decisions

**BFS for agent discovery.** Agents can spawn sub-agents transitively (A references B, B references C). BFS ensures all transitive agents are discovered and synced, not just direct children. The iteration cap (10) prevents runaway discovery.

**Byte-offset seeking instead of re-reading.** For large transcripts (megabytes), seeking to the last read position is far more efficient than re-reading from the start and skipping lines.

**`refreshStateFromBackend` after upload failure.** When a chunk upload times out, the server may have stored the data. Without refreshing, the next `SyncAll()` would re-upload the same lines. The refresh call gets the server's actual `LastSyncedLine` and updates the tracker accordingly. Auth errors during refresh are propagated (can't recover without re-auth).

**Summary link injection.** When a transcript contains a summary with a `leafUuid`, it means this session is a continuation of a previous one. `linkSummaryToPreviousSession` finds the parent transcript by scanning other JSONL files for the matching UUID, then calls the backend to update the parent's summary. This is best-effort — failures are logged but don't block sync.

## Testing

```bash
go test ./pkg/sync/...
```

- **`NewWithBackend()`** allows injecting a mock backend/client for unit tests
- **`engine_test.go` / `tracker_test.go`** — unit tests for incremental sync, agent discovery, byte offsets, chunking
- **`integration_test.go`** — full engine lifecycle with mock HTTP backend: init, multi-cycle sync, agent discovery, error recovery, large files, chunk size limits

## Dependencies

**Uses:** `pkg/config`, `pkg/git`, `pkg/http`, `pkg/logger`, `pkg/provider`, `pkg/redactor`, `pkg/types`, `pkg/utils`

**Used by:** `pkg/daemon/` (sync loop), `cmd/` (save command, post-tool-use linking)
