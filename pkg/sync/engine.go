package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/discovery"
	"github.com/ConfabulousDev/confab/pkg/git"
	"github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/redactor"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// Engine is the core sync engine used by both daemon and manual save.
// It provides a unified interface for syncing session data to the backend.
type Engine struct {
	client         *Client
	redactor       *redactor.Redactor
	tracker        *FileTracker
	sessionID      string // Backend session ID (set after Init)
	externalID     string
	transcriptPath string
	cwd            string
	initialized    bool
}

// EngineConfig holds configuration for creating an Engine
type EngineConfig struct {
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

	tracker := NewFileTracker(engineCfg.TranscriptPath)

	return &Engine{
		client:         client,
		redactor:       r,
		tracker:        tracker,
		externalID:     engineCfg.ExternalID,
		transcriptPath: engineCfg.TranscriptPath,
		cwd:            engineCfg.CWD,
	}, nil
}

// NewWithClient creates an engine with a pre-configured client.
// This is useful for testing with mock clients.
func NewWithClient(client *Client, r *redactor.Redactor, engineCfg EngineConfig) *Engine {
	tracker := NewFileTracker(engineCfg.TranscriptPath)

	return &Engine{
		client:         client,
		redactor:       r,
		tracker:        tracker,
		externalID:     engineCfg.ExternalID,
		transcriptPath: engineCfg.TranscriptPath,
		cwd:            engineCfg.CWD,
	}
}

// Init initializes the sync session with the backend.
// - Creates session if not exists, or resumes existing
// - Gets last_synced_line for all known files
// - Sends initial metadata (git info, hostname, username)
// Must be called before SyncAll.
func (e *Engine) Init() error {
	// Try to extract git info from transcript or detect from cwd
	var gitInfoJSON json.RawMessage
	if gitInfo, _ := git.ExtractGitInfoFromTranscript(e.transcriptPath); gitInfo != nil {
		gitInfoJSON, _ = json.Marshal(gitInfo)
	} else if gitInfo, _ := git.DetectGitInfo(e.cwd); gitInfo != nil {
		gitInfoJSON, _ = json.Marshal(gitInfo)
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

	resp, err := e.client.Init(e.externalID, e.transcriptPath, metadata)
	if err != nil {
		return err
	}

	e.sessionID = resp.SessionID
	e.initialized = true

	// Initialize tracker from backend state
	backendState := make(map[string]FileState)
	for fileName, state := range resp.Files {
		backendState[fileName] = FileState{LastSyncedLine: state.LastSyncedLine}
	}
	e.tracker.InitFromBackendState(backendState)

	logger.Info("Sync session initialized: session_id=%s existing_files=%d", e.sessionID, len(resp.Files))

	return nil
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

				// Extract and add summary/first_user_message for transcript files
				if file.Type == "transcript" {
					result := discovery.ExtractMetadataFromLines(chunk.Lines)
					if chunk.Metadata == nil {
						chunk.Metadata = &ChunkMetadata{}
					}
					chunk.Metadata.Summary = result.Summary
					chunk.Metadata.FirstUserMessage = result.FirstUserMessage

					// Trigger session linking for summaries with leafUuid
					for _, link := range result.SummaryLinks {
						e.linkSummaryToPreviousSession(link.Summary, link.LeafUUID)
					}
				}

				// Upload chunk
				lastLine, err := e.client.UploadChunk(e.sessionID, chunk.FileName, chunk.FileType, chunk.FirstLine, chunk.Lines, chunk.Metadata)
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

// SendSessionEnd sends a session_end event to the backend
func (e *Engine) SendSessionEnd(hookInput *types.HookInput, timestamp time.Time) error {
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

	if err := e.client.SendEvent(e.sessionID, "session_end", timestamp, payload); err != nil {
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
	resp, err := e.client.Init(e.externalID, e.transcriptPath, nil)
	if err != nil {
		return err
	}

	// Update tracker with backend state
	backendState := make(map[string]FileState)
	for fileName, state := range resp.Files {
		backendState[fileName] = FileState{LastSyncedLine: state.LastSyncedLine}
	}
	e.tracker.InitFromBackendState(backendState)

	logger.Info("Refreshed sync state from backend: files=%d", len(resp.Files))
	return nil
}
