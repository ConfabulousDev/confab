package provider

import (
	"fmt"
	"io"
)

const (
	NameClaudeCode = "claude-code"
	NameCodex      = "codex"
	NameOpencode   = "opencode"
)

// HookInput is the provider-agnostic view of a hook payload, exposing the
// fields used by daemon spawning and bookkeeping. Concrete shapes
// (types.ClaudeHookInput, types.CodexHookInput) satisfy this via adapter
// types defined in hookinput.go.
type HookInput interface {
	SessionID() string
	TranscriptPath() string
	CWD() string
	HookEventName() string
	ParentPID() int
}

// TranscriptRegistrar is the minimal surface InitTranscript sees on the
// root transcript's TrackedFile. *sync.TrackedFile satisfies it structurally
// via SetCodexRollout. Lives here (not pkg/sync) to avoid an import cycle.
type TranscriptRegistrar interface {
	SetCodexRollout(*CodexRolloutMetadata)
}

// DescendantRegistrar is the surface DiscoverDescendants uses to register
// newly-discovered sidechain files. *sync.FileTracker satisfies it via
// IsTracked + RegisterCodexRollout.
type DescendantRegistrar interface {
	IsTracked(fileName string) bool
	RegisterCodexRollout(path, fileName string, isRoot bool, meta CodexRolloutMetadata)
}

// FileTypeWorkflowJournal is the sync file_type for a Claude workflow run's
// journal.jsonl (CF-533). The backend (CF-532) accepts and stores it but
// never Claude-parses it; it is excluded from token/transcript analytics.
// Workflow subagent transcripts use the ordinary "agent" file_type.
const FileTypeWorkflowJournal = "workflow_journal"

// WorkflowRegistrar is the surface DiscoverWorkflowFiles uses to register
// Claude workflow subagent transcripts + run journals as path-encoded
// sidechain files. *sync.FileTracker satisfies it. SubagentsDir exposes the
// session's <session>/subagents directory (workflow runs live beneath
// subagents/workflows/<runId>/); RegisterSidechainFile tracks a file by its
// backend file_name (forward-slash path-encoded) and reports whether it was
// newly added (vs. an in-place path/type correction of an existing entry).
type WorkflowRegistrar interface {
	SubagentsDir() string
	RegisterSidechainFile(path, name, fileType string) bool
}

// OpencodeDescendantRegistrar is the surface Opencode.DiscoverDescendants
// uses to register subagent sessions discovered in OpenCode's local SQLite
// (CF-538). The daemon supplies an implementation that wraps *FileTracker,
// performing capability-gated child registration AND idempotent collector
// goroutine spawn in one call.
//
// *FileTracker does NOT satisfy this interface directly (no collector
// goroutine concept); the daemon's opencodeRegistrar wrapper does.
type OpencodeDescendantRegistrar interface {
	DescendantRegistrar

	// RegisterOpencodeChild registers the child file (path-encoded backend
	// file_name = "opencode/<childID>/messages.jsonl", file_type = "agent")
	// AND ensures a collector goroutine is running for it. Idempotent: a
	// repeat call for an already-tracked + already-collecting child is a
	// no-op. Capability-gated internally on opencode_subagent_files; when
	// the capability is off, both register and spawn no-op silently (the
	// engine logs the capability state once via resolveCaps).
	RegisterOpencodeChild(childSessionID, localPath string)
}

// ChunkView is the structural view of a chunk + its source file that
// AnnotateChunk reads from and writes back into. pkg/sync's chunkView
// adapter satisfies it. Setters mutate the underlying chunk's metadata in
// place; accessors return read-only snapshots.
type ChunkView interface {
	FileType() string
	FirstLine() int
	Lines() []string
	FileCodexRollout() *CodexRolloutMetadata
	SetCodexRolloutMetadata(*CodexRolloutMetadata)
	SetSummary(string)
	SetFirstUserMessage(string)
}

// SummaryLink describes a parent-session summary link extracted from a
// Claude transcript chunk. The engine HTTPs them after AnnotateChunk
// returns; the provider remains side-effect-free.
type SummaryLink struct {
	Summary  string
	LeafUUID string
}

// AnnotationResult is the structured return from AnnotateChunk.
// IncludedFirstUserMessage tells the engine whether to flip its
// sentFirstUserMessage flag. SummaryLinks (Claude only) tells the engine
// which parent summaries to link via the backend.
type AnnotationResult struct {
	IncludedFirstUserMessage bool
	SummaryLinks             []SummaryLink
}

// Provider abstracts per-tool local behavior. Adding a new provider means
// implementing this interface and registering the instance.
type Provider interface {
	Name() string
	// CLIBinaryName is the OS-level binary name to look up via
	// exec.LookPath when detecting whether the provider is installed
	// locally (e.g. "claude" for Claude Code, "codex" for Codex).
	CLIBinaryName() string
	StateDir() (string, error)
	FindParentPID() int
	IsProcess(pid int) bool

	// SupportsCommitLinking reports whether this provider's hook system
	// can drive bidirectional GitHub linking (commit-trailer + PR-body
	// injection via PreToolUse; commit/PR URL linking via PostToolUse).
	// Used by cmd/ handlers to silently no-op for providers that don't
	// install those events.
	SupportsCommitLinking() bool

	// ParseSessionHook reads a SessionStart-style hook payload from r and
	// returns the provider-agnostic view.
	ParseSessionHook(r io.Reader) (HookInput, error)

	InstallHooks() (string, error)
	UninstallHooks() (string, error)
	IsHooksInstalled() (bool, error)

	// InstallSkills installs the bundled skills for this provider's local
	// skill layout.
	InstallSkills() error
	UninstallSkills() error
	IsSkillInstalled(name string) bool

	// WalkUpToRoot returns the root session ID and its rollout path. For
	// providers without a separate root file identifier (Claude Code),
	// rootPath is "".
	WalkUpToRoot(sessionID string) (rootID, rootPath string, err error)

	// ShouldSpawnForInput is the per-provider gate on whether a fresh
	// SessionStart should result in a daemon. Codex returns false for
	// subagent rollouts; Claude is always true.
	ShouldSpawnForInput(in HookInput) bool

	// WriteHookResponse writes a hook response payload to w. The response
	// shape is provider-specific but the (continue, suppressOutput,
	// systemMessage) tuple is shared.
	WriteHookResponse(w io.Writer, suppressOutput bool, systemMessage string) error

	// InitTranscript is called from sync.Engine.Init AFTER the backend has
	// returned the initial sync state and the transcript file has been
	// registered in the tracker. Codex reads session_meta and attaches
	// codex_rollout metadata to the root transcript so the first chunk
	// uploaded carries it. Claude is a no-op. The engine logs+continues on
	// error; implementations may return one for true I/O failures.
	InitTranscript(target TranscriptRegistrar, transcriptPath, externalID string) error

	// DiscoverDescendants is called once per SyncAll cycle, BEFORE the BFS
	// loop. Providers with an external discovery model (Codex: SQLite
	// subtree walk) register newly-discovered sidechain files via reg.
	// Must be idempotent across calls (skip already-tracked filenames).
	// Claude is a no-op (its agents are discovered transitively from
	// transcript content, handled in tracker.DiscoverNewFiles).
	DiscoverDescendants(reg DescendantRegistrar, externalID string) error

	// DiscoverWorkflowFiles is called once per SyncAll cycle, alongside
	// DiscoverDescendants, to register Claude workflow subagent transcripts
	// (subagents/workflows/<runId>/agent-<id>.jsonl) and run journals
	// (.../journal.jsonl) as path-encoded sidechain files (CF-533). The
	// allow predicate is supplied by the engine and gates each file by its
	// file_type against backend capability ("agent" → workflow_files,
	// "workflow_journal" → workflow_journal); the provider calls it only
	// after it has a candidate file, so non-workflow sessions never trigger
	// a backend capability probe. Returns the count of newly-registered
	// files (for logging). Claude scans the filesystem; Codex is a no-op
	// (no Workflow-tool equivalent). Must be idempotent across calls.
	DiscoverWorkflowFiles(reg WorkflowRegistrar, allow func(fileType string) bool) (int, error)

	// AnnotateChunk is called for every chunk read from a tracked file,
	// BEFORE upload. Providers attach provider-specific chunk metadata
	// (codex_rollout, first_user_message, summary). The redact closure is
	// nil-safe; providers must guard `if redact != nil { ... }` before
	// applying. Engine inspects the returned AnnotationResult to flip its
	// sentFirstUserMessage flag and dispatch any extracted summary links.
	AnnotateChunk(c ChunkView, sentFirstUserMessage bool, redact func(string) string) AnnotationResult

	// ScanSessions returns the user-initiated sessions discoverable on
	// disk for this provider, sorted oldest first. Subagent rollouts and
	// other non-user transcripts are filtered out.
	ScanSessions() ([]SessionInfo, error)

	// FindSessionByID resolves a full or partial session ID to its full
	// ID and transcript path. For providers with a thread tree (Codex)
	// this walks up to the root so the returned (id, path) refer to the
	// top-most user session — callers that want the unwalked rollout use
	// provider-specific methods.
	FindSessionByID(partialID string) (id, transcriptPath string, err error)

	// ExtractMetadata parses summary, first user message, and
	// (Claude-only) summary links from in-memory transcript lines.
	// Implementations cap the line count to bound cost.
	ExtractMetadata(lines []string) SessionMetadata

	// DefaultCWD returns the working directory to record alongside an
	// upload for this transcript path. Claude derives from the path;
	// Codex reads session_meta.cwd with a path-dir fallback.
	DefaultCWD(transcriptPath string) string

	// OnAlreadyRunning is invoked by maybeSpawnDaemon when a spawn is
	// denied because a daemon for this externalID is already alive.
	// Most providers no-op (hook deduplication is the normal path).
	// OpenCode logs a warning because, for opencode, this state means a
	// parallel process resumed the same session — an unsupported workflow.
	OnAlreadyRunning(externalID string)
}

var registry = map[string]Provider{
	NameClaudeCode: ClaudeCode{},
	NameCodex:      Codex{},
	NameOpencode:   Opencode{},
}

// Get returns the registered Provider for name. An empty string resolves
// to Claude Code for backwards compatibility with NormalizeName.
func Get(name string) (Provider, error) {
	if name == "" {
		name = NameClaudeCode
	}
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q (expected %q, %q, or %q)",
			name, NameClaudeCode, NameCodex, NameOpencode)
	}
	return p, nil
}

// NormalizeName returns the canonical provider name. Backed by the
// registry so it can't drift from the Provider list.
func NormalizeName(name string) (string, error) {
	p, err := Get(name)
	if err != nil {
		return "", err
	}
	return p.Name(), nil
}
