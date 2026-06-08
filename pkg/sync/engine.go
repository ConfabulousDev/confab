package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/git"
	"github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/redactor"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// Compile-time assertions that the tracker types satisfy the provider
// interfaces. Catches API drift at build time rather than at test time.
var (
	_ provider.TranscriptRegistrar = (*TrackedFile)(nil)
	_ provider.DescendantRegistrar = (*FileTracker)(nil)
	_ provider.WorkflowRegistrar   = (*FileTracker)(nil)
)

// Engine is the core sync engine used by both daemon and manual save.
// It provides a unified interface for syncing session data to the backend.
type Engine struct {
	backend              Backend
	redactor             *redactor.Redactor
	tracker              *FileTracker
	sessionID            string // Backend session ID (set after Init)
	provider             provider.Provider
	externalID           string
	transcriptPath       string
	cwd                  string
	initialized          bool
	sentFirstUserMessage bool

	// Workflow-file capability gating (CF-533). The backend capability is
	// probed lazily (only when the Claude provider finds a workflow run dir)
	// and cached for the engine's lifetime == one backend + one session.
	// Only a DEFINITIVE answer (200 or 404) is cached; a transient probe
	// failure leaves capsResolved false so the next SyncAll re-probes.
	capsResolved      bool         // a definitive capabilities answer was cached
	caps              Capabilities // cached capabilities (zero value on 404)
	loggedProbeError  bool         // a transient probe failure was already logged
	capsProbedThisRun bool         // a probe was already attempted in this SyncAll cycle

	// descendantReg, when non-nil, overrides the default DescendantRegistrar
	// (e.tracker) that SyncAll passes to provider.DiscoverDescendants. The
	// daemon sets this for OpenCode so the registrar wrapper can drive
	// per-child collector spawn through the same provider seam Codex uses.
	// See SetDescendantRegistrar.
	descendantReg provider.DescendantRegistrar
}

// setProviderForTest substitutes the engine's resolved Provider with a stub.
// Test-only seam — production code resolves via provider.Get inside New().
func (e *Engine) setProviderForTest(p provider.Provider) { e.provider = p }

// Backend is the sync transport used by Engine. The HTTP client implements this
// for provider-aware backend sync.
type Backend interface {
	Init(providerName, externalID, transcriptPath string, metadata *InitMetadata) (*InitResponse, error)
	UploadChunk(sessionID, fileName, fileType string, firstLine int, lines []string, metadata *ChunkMetadata) (int, error)
	SendEvent(sessionID, eventType string, timestamp time.Time, payload json.RawMessage) error
	UpdateSessionSummary(externalID, summary string) error
	// Capabilities probes the backend's optional-feature signal (CF-533).
	// Returns an error (404 / network / parse) when the backend does not
	// advertise capabilities; the engine treats a 404 as a definitive
	// "unsupported" and other errors as transient.
	Capabilities() (Capabilities, error)
}

// EngineConfig holds configuration for creating an Engine
type EngineConfig struct {
	Provider       string
	ExternalID     string
	TranscriptPath string
	CWD            string
}

// New creates a new sync engine with the given configuration.
// The engine is not connected to the backend until Init() is called.
func New(uploadCfg *config.UploadConfig, engineCfg EngineConfig) (*Engine, error) {
	client, err := NewClient(uploadCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create sync client: %w", err)
	}

	// Initialize redactor if enabled in config
	var r *redactor.Redactor
	if uploadCfg.Redaction != nil && uploadCfg.Redaction.Enabled {
		var err error
		r, err = redactor.NewFromConfig(uploadCfg.Redaction)
		if err != nil {
			return nil, fmt.Errorf("failed to create redactor: %w", err)
		}
	}

	p, err := provider.Get(engineCfg.Provider)
	if err != nil {
		return nil, fmt.Errorf("invalid provider %q: %w", engineCfg.Provider, err)
	}

	return &Engine{
		backend:        client,
		redactor:       r,
		tracker:        NewFileTracker(engineCfg.TranscriptPath),
		provider:       p,
		externalID:     engineCfg.ExternalID,
		transcriptPath: engineCfg.TranscriptPath,
		cwd:            engineCfg.CWD,
	}, nil
}

// NewWithBackend creates an engine with a preconfigured backend.
// Test-facing; returns an error if the provider name is invalid.
func NewWithBackend(backend Backend, r *redactor.Redactor, engineCfg EngineConfig) (*Engine, error) {
	p, err := provider.Get(engineCfg.Provider)
	if err != nil {
		return nil, fmt.Errorf("invalid provider %q: %w", engineCfg.Provider, err)
	}
	return &Engine{
		backend:        backend,
		redactor:       r,
		tracker:        NewFileTracker(engineCfg.TranscriptPath),
		provider:       p,
		externalID:     engineCfg.ExternalID,
		transcriptPath: engineCfg.TranscriptPath,
		cwd:            engineCfg.CWD,
	}, nil
}

// redactFn returns the engine's redactor as a nil-safe closure so providers
// can apply redaction without importing pkg/redactor. Returns nil when no
// redactor is configured; AnnotateChunk implementations guard accordingly.
func (e *Engine) redactFn() func(string) string {
	if e.redactor == nil {
		return nil
	}
	return e.redactor.Redact
}

// chunkView is the in-package adapter that satisfies provider.ChunkView,
// wrapping the *Chunk + *TrackedFile pair the engine has in hand. Setters
// mutate the underlying chunk's metadata (allocating it lazily); accessors
// expose chunk + file fields the provider needs.
type chunkView struct {
	chunk *Chunk
	file  *TrackedFile
}

var _ provider.ChunkView = (*chunkView)(nil)

func (cv *chunkView) FileType() string { return cv.chunk.FileType }
func (cv *chunkView) FirstLine() int   { return cv.chunk.FirstLine }
func (cv *chunkView) Lines() []string  { return cv.chunk.Lines }

func (cv *chunkView) FileCodexRollout() *provider.CodexRolloutMetadata {
	if cv.file == nil {
		return nil
	}
	return cv.file.CodexRollout
}

func (cv *chunkView) SetCodexRolloutMetadata(m *provider.CodexRolloutMetadata) {
	ensureChunkMetadata(cv.chunk).CodexRollout = m
}

func (cv *chunkView) SetSummary(s string) {
	ensureChunkMetadata(cv.chunk).Summary = s
}

func (cv *chunkView) SetFirstUserMessage(s string) {
	ensureChunkMetadata(cv.chunk).FirstUserMessage = s
}

// Init initializes the sync session with the backend.
// - Creates session if not exists, or resumes existing
// - Gets last_synced_line for all known files
// - Sends initial metadata (git info, hostname, username)
// Must be called before SyncAll.
func (e *Engine) Init() error {
	// Try to extract git info from transcript first, then fall back to cwd.
	gitInfo, _ := git.ExtractGitInfoFromTranscript(e.transcriptPath)
	if gitInfo == nil {
		gitInfo, _ = git.DetectGitInfo(e.cwd)
	}
	var gitInfoJSON json.RawMessage
	if gitInfo != nil {
		gitInfoJSON, _ = json.Marshal(gitInfo)
		logGitRemotes(gitInfo)
	}

	// Collect client info
	hostname, _ := os.Hostname()
	var username string
	if u, err := user.Current(); err == nil {
		username = u.Username
	}

	metadata := &InitMetadata{
		CWD:      e.cwd,
		GitInfo:  gitInfoJSON,
		Hostname: hostname,
		Username: username,
	}

	resp, err := e.backend.Init(e.provider.Name(), e.externalID, e.transcriptPath, metadata)
	if err != nil {
		return err
	}

	e.sessionID = resp.SessionID
	e.initialized = true

	e.applyBackendFiles(resp)

	// Provider-owned root-transcript metadata attachment. Claude is a
	// no-op; Codex reads session_meta and attaches root rollout metadata
	// so the first chunk uploaded carries it. Descendants get their
	// metadata attached during provider.DiscoverDescendants.
	if transcript := e.tracker.GetTranscriptFile(); transcript != nil {
		if err := e.provider.InitTranscript(transcript, e.transcriptPath, e.externalID); err != nil {
			logger.Warn("provider InitTranscript failed: %v", err)
		}
	}

	logger.Info("Sync session initialized: session_id=%s existing_files=%d", e.sessionID, len(resp.Files))

	return nil
}

func (e *Engine) applyBackendFiles(resp *InitResponse) {
	backendState := make(map[string]FileState)
	for fileName, state := range resp.Files {
		backendState[fileName] = FileState{LastSyncedLine: state.LastSyncedLine}
	}
	e.tracker.InitFromBackendState(backendState)
}

// IsInitialized returns true if Init() has been called successfully
func (e *Engine) IsInitialized() bool {
	return e.initialized
}

// SessionID returns the backend session ID (empty if not initialized)
func (e *Engine) SessionID() string {
	return e.sessionID
}

// maxSyncIterations is the maximum number of BFS iterations to prevent runaway loops.
// In practice, agent chains rarely exceed 3-4 levels deep.
const maxSyncIterations = 10

// SyncAll syncs all tracked files to the backend using BFS traversal.
// It discovers agent files referenced in transcripts and syncs them transitively
// within a single call. Each file is processed at most once per call.
//
// Algorithm:
//  1. Start with all currently tracked files in the queue
//  2. Process each file in queue (sync if changed, extract agent IDs)
//  3. Discover new files from collected agent IDs
//  4. Add only NEW files to the queue for next iteration
//  5. Repeat until queue is empty (or max iterations reached)
//
// Returns number of chunks uploaded and the first error encountered (if any).
// Continues syncing other files even if one file fails.
func (e *Engine) SyncAll() (int, error) {
	if !e.initialized {
		return 0, fmt.Errorf("engine not initialized: call Init() first")
	}

	totalChunks := 0
	var firstErr error

	// Provider-owned descendant discovery. Claude is a no-op (its agents
	// are discovered transitively from transcript content inside
	// tracker.DiscoverNewFiles). Codex queries the local SQLite state DB
	// for every descendant of the root thread and registers them as agent
	// files. OpenCode walks its SQLite session.parent_id tree and
	// registers each child as a path-encoded sidechain via the daemon-
	// supplied registrar (CF-538). The BFS loop below uploads everything
	// as sidechain files under the root's backend session.
	reg := provider.DescendantRegistrar(e.tracker)
	if e.descendantReg != nil {
		reg = e.descendantReg
	}
	if err := e.provider.DiscoverDescendants(reg, e.externalID); err != nil {
		logger.Warn("provider DiscoverDescendants failed: %v", err)
	}

	// Provider-owned workflow-file discovery (CF-533), gated per-file-type on
	// backend capability via workflowFileTypeAllowed. Claude scans
	// subagents/workflows/<runId>/; Codex is a no-op. Skipped once the backend
	// has definitively reported no support, so we don't re-scan every cycle.
	// Reset the per-cycle probe guard so a transient capability-probe failure
	// is retried at most once per cycle, not once per workflow file.
	e.capsProbedThisRun = false
	if !e.workflowUploadsRuledOut() {
		if n, err := e.provider.DiscoverWorkflowFiles(e.tracker, e.workflowFileTypeAllowed); err != nil {
			logger.Warn("provider DiscoverWorkflowFiles failed: %v", err)
		} else if n > 0 {
			logger.Info("Discovered %d workflow subagent file(s)", n)
		}
	}

	// Start with all currently tracked files
	filesToProcess := e.tracker.GetTrackedFiles()

	// BFS loop: process files in queue, discover new ones, add to queue
	for iteration := 0; iteration < maxSyncIterations && len(filesToProcess) > 0; iteration++ {
		var newAgentIDs []string

		// Process each file in the current queue
		for _, file := range filesToProcess {
			// Check if file has changed (skip if not)
			if !e.tracker.HasFileChanged(file) {
				continue
			}

			// Read and upload chunks until no more data (handles byte-limited chunks)
			for {
				// Read new lines
				chunk, err := e.tracker.ReadChunk(file, e.redactor, DefaultMaxChunkBytes)
				if err != nil {
					logger.Error("Failed to read chunk: file=%s error=%v", file.Path, err)
					if firstErr == nil {
						firstErr = err
					}
					break
				}

				if chunk == nil {
					break // No more lines
				}

				// Collect agent IDs for discovery (local use only)
				if len(chunk.AgentIDs) > 0 {
					newAgentIDs = append(newAgentIDs, chunk.AgentIDs...)
				}

				// Provider-owned chunk metadata. AnnotateChunk runs on every
				// chunk regardless of file type; each provider internally
				// gates its extraction (Codex first_user_message gated on
				// transcript, codex_rollout gated on FirstLine==1; Claude
				// extracts only from transcript files).
				annotation := e.provider.AnnotateChunk(
					&chunkView{chunk: chunk, file: file},
					e.sentFirstUserMessage,
					e.redactFn(),
				)
				for _, link := range annotation.SummaryLinks {
					e.linkSummaryToPreviousSession(link.Summary, link.LeafUUID)
				}

				// Upload chunk
				lastLine, err := e.backend.UploadChunk(e.sessionID, chunk.FileName, chunk.FileType, chunk.FirstLine, chunk.Lines, chunk.Metadata)
				if err != nil {
					logger.Error("Failed to upload chunk: file=%s first_line=%d lines=%d error=%v",
						chunk.FileName, chunk.FirstLine, len(chunk.Lines), err)
					if firstErr == nil {
						firstErr = err
					}

					// Refresh state from backend to handle partial success (e.g., timeout where
					// server stored data but response didn't reach us). This ensures we resume
					// from the correct position on the next sync attempt.
					// Skip for auth errors (handled at daemon level) or session not found (can't recover).
					if !errors.Is(err, http.ErrUnauthorized) && !errors.Is(err, http.ErrSessionNotFound) {
						if refreshErr := e.refreshStateFromBackend(); refreshErr != nil {
							logger.Error("Failed to refresh state from backend: %v", refreshErr)
							// Auth errors from refresh should be propagated so daemon can handle them
							if errors.Is(refreshErr, http.ErrUnauthorized) {
								firstErr = refreshErr
							}
						}
					}

					break
				}

				// Update tracking state
				if annotation.IncludedFirstUserMessage {
					e.sentFirstUserMessage = true
				}
				e.tracker.UpdateAfterSync(file, lastLine, chunk.NewOffset)

				logger.Debug("Synced file: file=%s first_line=%d last_line=%d lines=%d",
					chunk.FileName, chunk.FirstLine, lastLine, len(chunk.Lines))

				totalChunks++
			}
		}

		// Discover new files based on agent IDs found in this iteration.
		// DiscoverNewFiles only returns files not already tracked (cycle-safe).
		newFiles := e.tracker.DiscoverNewFiles(newAgentIDs)
		for _, f := range newFiles {
			logger.Info("Discovered new file: path=%s type=%s", f.Path, f.Type)
		}

		// Queue only the newly discovered files for next iteration
		filesToProcess = newFiles
	}

	return totalChunks, firstErr
}

// resolveCaps lazily probes and caches the backend's workflow capabilities
// (CF-533). It caches only DEFINITIVE answers: a 404 (old backend → both
// false) or a clean 200 (parsed flags). A transient failure (network /
// timeout / 5xx / malformed body) is NOT cached and returns ok=false, so the
// next SyncAll re-probes. Each distinct outcome logs once at Info.
func (e *Engine) resolveCaps() (Capabilities, bool) {
	if e.capsResolved {
		return e.caps, true
	}
	// Probe at most once per SyncAll cycle: a transient failure must not
	// re-hit the backend for every workflow file in the same cycle.
	if e.capsProbedThisRun {
		return Capabilities{}, false
	}
	e.capsProbedThisRun = true

	caps, err := e.backend.Capabilities()
	if err != nil {
		// A 404 is definitive: the backend predates the capabilities
		// endpoint (CF-532), so it supports nothing. Cache it.
		if errors.Is(err, http.ErrSessionNotFound) {
			e.caps = Capabilities{}
			e.capsResolved = true
			logger.Info("Backend has no capabilities endpoint (404); skipping workflow subagent uploads")
			return e.caps, true
		}
		// Transient (network / timeout / 5xx / malformed body): do NOT
		// cache, so the next sync cycle re-probes rather than disabling
		// workflow uploads for the rest of the session.
		if !e.loggedProbeError {
			e.loggedProbeError = true
			logger.Info("Backend capability probe failed (%v); will retry next sync cycle", err)
		}
		return Capabilities{}, false
	}

	e.caps = caps
	e.capsResolved = true
	if caps.WorkflowFiles || caps.WorkflowJournal {
		logger.Info("Backend workflow capabilities: workflow_files=%v workflow_journal=%v",
			caps.WorkflowFiles, caps.WorkflowJournal)
	} else {
		logger.Info("Backend advertises no workflow file support; skipping workflow subagent uploads")
	}
	return e.caps, true
}

// workflowUploadsRuledOut reports whether the backend has definitively
// answered that it supports neither workflow capability. Once true, SyncAll
// stops scanning the filesystem for workflow files for the engine's lifetime.
func (e *Engine) workflowUploadsRuledOut() bool {
	return e.capsResolved && !e.caps.WorkflowFiles && !e.caps.WorkflowJournal
}

// Tracker returns the engine's internal FileTracker. Exposed so the daemon
// can wrap it in a provider-specific DescendantRegistrar (OpenCode) and
// then hand the wrapper back via SetDescendantRegistrar. Direct callers
// outside that wiring should not mutate the returned tracker.
func (e *Engine) Tracker() *FileTracker { return e.tracker }

// SetDescendantRegistrar overrides the default DescendantRegistrar (the
// engine's own *FileTracker) that Engine.SyncAll passes to
// provider.DiscoverDescendants. Used by the daemon to inject a registrar
// that wraps the FileTracker with provider-specific behavior (OpenCode
// child collector spawn). Must be called before SyncAll.
//
// The honest-but-mutable setter is the cleanest break of the cyclic
// dependency between the engine and an OpenCode registrar: the registrar
// needs the engine (for the capability gate); the engine needs the
// registrar (for SyncAll). Construction order is engine → registrar →
// setter; either constructor injection or hidden closures would just
// reshuffle the same mutation.
func (e *Engine) SetDescendantRegistrar(reg provider.DescendantRegistrar) {
	e.descendantReg = reg
}

// OpencodeChildFilesAllowed reports whether OpenCode subagent sidechain
// files may be uploaded to this backend, per its cached capabilities
// (CF-538/CF-539). Lazy-probes once per SyncAll cycle; cached definitive
// answers persist for the engine's lifetime. Exported because the
// daemon's opencodeRegistrar gates RegisterOpencodeChild on it.
func (e *Engine) OpencodeChildFilesAllowed() bool {
	caps, ok := e.resolveCaps()
	if !ok {
		return false
	}
	return caps.OpencodeSubagentFiles
}

// workflowFileTypeAllowed reports whether a workflow file of the given
// file_type may be uploaded to this backend, per its (cached) capabilities.
// Per-flag: "agent" gates on WorkflowFiles, FileTypeWorkflowJournal on
// WorkflowJournal. Passed to provider.DiscoverWorkflowFiles as the gate
// predicate; the first call triggers the lazy probe.
func (e *Engine) workflowFileTypeAllowed(fileType string) bool {
	caps, ok := e.resolveCaps()
	if !ok {
		return false // transient probe failure — skip this cycle, retry next
	}
	switch fileType {
	case "agent":
		return caps.WorkflowFiles
	case provider.FileTypeWorkflowJournal:
		return caps.WorkflowJournal
	default:
		return false
	}
}

// ensureChunkMetadata returns chunk.Metadata, allocating it if nil.
func ensureChunkMetadata(chunk *Chunk) *ChunkMetadata {
	if chunk.Metadata == nil {
		chunk.Metadata = &ChunkMetadata{}
	}
	return chunk.Metadata
}

// logGitRemotes emits a one-line summary of detected remotes + tracking
// remote at session init. No-op when there are no remotes.
func logGitRemotes(info *git.GitInfo) {
	if len(info.Remotes) == 0 {
		return
	}
	names := make([]string, len(info.Remotes))
	for i, r := range info.Remotes {
		names[i] = r.Name
	}
	tracking := info.TrackingRemote
	if tracking == "" {
		tracking = "<none>"
	}
	logger.Info("Git remotes detected: %s (tracking: %s)",
		strings.Join(names, ", "), tracking)
}

// SendSessionEnd sends a session_end event to the backend
func (e *Engine) SendSessionEnd(hookInput *types.ClaudeHookInput, timestamp time.Time) error {
	if !e.initialized || e.sessionID == "" {
		return nil // Nothing to send if not initialized
	}

	if hookInput == nil {
		return nil
	}

	payload, err := json.Marshal(hookInput)
	if err != nil {
		return fmt.Errorf("failed to marshal hook input: %w", err)
	}

	if err := e.backend.SendEvent(e.sessionID, "session_end", timestamp, payload); err != nil {
		return fmt.Errorf("failed to send session_end event: %w", err)
	}

	logger.Info("Sent session_end event: session_id=%s", e.sessionID)
	return nil
}

// GetSyncStats returns current sync statistics (lines synced per file)
func (e *Engine) GetSyncStats() map[string]int {
	stats := make(map[string]int)
	for _, file := range e.tracker.GetTrackedFiles() {
		stats[file.Name] = file.LastSyncedLine
	}
	return stats
}

// Reset clears the initialized state, allowing Init to be called again.
// This is useful when the backend returns an auth error and we need to
// re-authenticate and re-initialize.
func (e *Engine) Reset() {
	e.initialized = false
	e.sessionID = ""
}

// refreshStateFromBackend calls Init to get current backend state and updates tracker.
// This should be called after upload failures to handle cases where the server
// received data but we didn't get a response (e.g., timeout).
func (e *Engine) refreshStateFromBackend() error {
	// Call Init without metadata - we just want to refresh file states
	resp, err := e.backend.Init(e.provider.Name(), e.externalID, e.transcriptPath, nil)
	if err != nil {
		return err
	}

	e.applyBackendFiles(resp)

	logger.Info("Refreshed sync state from backend: files=%d", len(resp.Files))
	return nil
}
