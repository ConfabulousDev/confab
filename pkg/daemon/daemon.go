package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	pkgsync "github.com/ConfabulousDev/confab/pkg/sync"
	"github.com/ConfabulousDev/confab/pkg/types"
)

const (
	// DefaultSyncInterval is the base interval for syncing files
	DefaultSyncInterval = 30 * time.Second

	// syncIntervalJitter is random jitter added to sync interval (0 to this value)
	syncIntervalJitter = 5 * time.Second

	// initialWaitTimeout is how long to wait for transcript file to appear
	initialWaitTimeout = 60 * time.Second

	// initialWaitPollInterval is how often to check for transcript file
	initialWaitPollInterval = 2 * time.Second

	// maxConsecutiveNotFound is how many consecutive 404 errors before stopping.
	// This handles the case where a session is deleted from the backend.
	maxConsecutiveNotFound = 3

)

// shutdownTimeout is the maximum time to wait for final sync during shutdown.
// If the backend is slow or unresponsive, we give up and clean up anyway.
// This is a var (not const) to allow tests to override it.
var shutdownTimeout = 30 * time.Second

// Daemon is the background sync process.
//
// The daemon is resilient to backend unavailability - it will keep running
// and retry connecting to the backend on each sync interval. Once connected,
// it will sync any accumulated changes.
//
// If ParentPID is set, the daemon monitors the parent process and shuts down
// gracefully when it exits. This handles cases where Claude Code crashes or
// is killed without firing the SessionEnd hook.
type Daemon struct {
	externalID     string
	transcriptPath string
	cwd            string
	parentPID      int
	syncInterval   time.Duration
	syncJitter     time.Duration

	state               *State
	engine              *pkgsync.Engine
	stopCh              chan struct{}
	stopOnce            sync.Once
	doneCh              chan struct{}
	consecutiveNotFound int // tracks consecutive 404 errors for session deletion detection
}

// Config holds daemon configuration
type Config struct {
	ExternalID         string
	TranscriptPath     string
	CWD                string
	ParentPID          int           // Claude Code process ID to monitor (0 to disable)
	SyncInterval       time.Duration
	SyncIntervalJitter time.Duration // 0 to disable jitter (for testing)
}

// New creates a new daemon instance
func New(cfg Config) *Daemon {
	interval := cfg.SyncInterval
	if interval == 0 {
		interval = DefaultSyncInterval
	}

	jitter := cfg.SyncIntervalJitter
	if jitter == 0 && cfg.SyncInterval == 0 {
		// Only use default jitter if using default interval
		jitter = syncIntervalJitter
	}

	return &Daemon{
		externalID:     cfg.ExternalID,
		transcriptPath: cfg.TranscriptPath,
		cwd:            cfg.CWD,
		parentPID:      cfg.ParentPID,
		syncInterval:   interval,
		syncJitter:     jitter,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
}

// Run starts the daemon and blocks until stopped
func (d *Daemon) Run(ctx context.Context) error {
	// Set session context for all log lines
	logger.SetSession(d.externalID, "")

	logger.Info("Daemon starting: transcript=%s interval=%v", d.transcriptPath, d.syncInterval)

	// Setup signal handling as early as possible to catch signals during
	// initialization (waiting for transcript, backend init).
	// See daemon_test.go for rationale.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Wait for transcript file to exist before doing anything else.
	// Don't save state or set up panic handlers until we have a transcript.
	if err := d.waitForTranscript(ctx, sigCh); err != nil {
		return err
	}

	// Save state for duplicate detection. Done after transcript exists so we
	// don't leave stale state files for sessions that never produced transcripts.
	d.state = NewState(d.externalID, d.transcriptPath, d.cwd, d.parentPID)
	if err := d.state.Save(); err != nil {
		logger.Warn("Failed to save initial state: %v", err)
	}

	// Log panics before crashing. We skip final sync since the program is in an
	// undefined state, but we do delete the state file to avoid blocking future
	// daemon spawns. We log the panic since this CLI runs on many local machines
	// and we need the logs for debugging.
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Daemon panic: %v", r)
			if d.state != nil {
				d.state.Delete()
			}
			panic(r)
		}
	}()

	if d.parentPID > 0 {
		logger.Info("Daemon running: pid=%d parent_pid=%d", os.Getpid(), d.parentPID)
	} else {
		logger.Info("Daemon running: pid=%d (no parent monitoring)", os.Getpid())
	}

	// Main loop with jittered interval to avoid thundering herd.
	// First iteration fires immediately (0 duration), then uses normal interval.
	firstSync := true
	for {
		var delay time.Duration
		if firstSync {
			delay = 0
			firstSync = false
		} else {
			delay = d.syncInterval
			if d.syncJitter > 0 {
				delay += time.Duration(rand.Int63n(int64(d.syncJitter)))
			}
		}
		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return d.shutdown("context cancelled")

		case <-d.stopCh:
			timer.Stop()
			return d.shutdown("stop requested")

		case sig := <-sigCh:
			timer.Stop()
			return d.shutdown(fmt.Sprintf("signal %v", sig))

		case <-timer.C:
			// Check if parent Claude Code process is still running.
			// If it crashed or was killed, shut down gracefully.
			if d.parentPID > 0 && !isProcessRunning(d.parentPID) {
				return d.shutdown("parent process exited")
			}

			// If not initialized yet, try to connect to backend
			if d.engine == nil || !d.engine.IsInitialized() {
				if err := d.tryInit(); err != nil {
					logger.Warn("Backend init failed (will retry): %v", err)
					// If auth failed during init, reset engine to force config re-read.
					// User may have re-authenticated with a new API key.
					if errors.Is(err, http.ErrUnauthorized) {
						logger.Info("Auth failed during init, will re-read config on next cycle")
						if d.engine != nil {
							d.engine.Reset()
						}
						d.engine = nil
					}
					continue
				}
			}

			// Sync
			if chunks, err := d.engine.SyncAll(); err != nil {
				logger.Warn("Sync cycle had errors: %v", err)
				// If auth failed, reset engine to force config re-read on next cycle.
				// User may have re-authenticated with a new API key.
				if errors.Is(err, http.ErrUnauthorized) {
					logger.Info("Auth failed, will re-read config on next cycle")
					d.engine.Reset()
					d.engine = nil
				}
				// Track consecutive 404 errors for session deletion detection.
				// Stop after maxConsecutiveNotFound to avoid infinite retries.
				if errors.Is(err, http.ErrSessionNotFound) {
					d.consecutiveNotFound++
					logger.Warn("Session not found (404): count=%d/%d", d.consecutiveNotFound, maxConsecutiveNotFound)
					if d.consecutiveNotFound >= maxConsecutiveNotFound {
						return d.shutdown("session deleted from backend")
					}
				} else {
					d.consecutiveNotFound = 0
				}
			} else {
				d.consecutiveNotFound = 0
				if chunks > 0 {
					logger.Debug("Sync cycle complete: chunks=%d", chunks)
				}
			}
		}
	}
}

// waitForTranscript waits for the transcript file to exist before proceeding.
// For fresh sessions, Claude Code may not have written the transcript yet.
func (d *Daemon) waitForTranscript(ctx context.Context, sigCh chan os.Signal) error {
	// Check if file already exists
	if _, err := os.Stat(d.transcriptPath); err == nil {
		return nil
	}

	logger.Info("Waiting for transcript file to appear...")

	ticker := time.NewTicker(initialWaitPollInterval)
	defer ticker.Stop()

	timeout := time.After(initialWaitTimeout)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for transcript")
		case <-d.stopCh:
			return fmt.Errorf("stop requested while waiting for transcript")
		case sig := <-sigCh:
			return fmt.Errorf("received signal %v while waiting for transcript", sig)
		case <-timeout:
			return fmt.Errorf("timeout waiting for transcript file after %v", initialWaitTimeout)
		case <-ticker.C:
			if _, err := os.Stat(d.transcriptPath); err == nil {
				logger.Info("Transcript file appeared")
				return nil
			}
		}
	}
}

// tryInit attempts to initialize the sync engine and session with the backend.
// Auth is checked here lazily, not at daemon startup.
func (d *Daemon) tryInit() error {
	// Get authenticated config (lazy - only when we need to talk to backend)
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return fmt.Errorf("not authenticated: %w", err)
	}

	// Create engine if not already created
	if d.engine == nil {
		engine, err := pkgsync.New(cfg, pkgsync.EngineConfig{
			ExternalID:     d.externalID,
			TranscriptPath: d.transcriptPath,
			CWD:            d.cwd,
		})
		if err != nil {
			return fmt.Errorf("failed to create sync engine: %w", err)
		}
		d.engine = engine
	}

	// Initialize the session with backend
	if err := d.engine.Init(); err != nil {
		return err
	}

	// Update session context now that we have the backend session ID
	logger.SetSession(d.externalID, d.engine.SessionID())

	// Persist the Confab session ID so other hooks (e.g., PreToolUse) can access it
	if d.state != nil {
		d.state.ConfabSessionID = d.engine.SessionID()
		if err := d.state.Save(); err != nil {
			logger.Warn("Failed to save Confab session ID to state: %v", err)
		}
	}

	return nil
}

// Stop signals the daemon to stop. Safe to call multiple times.
func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		close(d.stopCh)
	})
}

// shutdown performs final sync and cleanup
func (d *Daemon) shutdown(reason string) error {
	defer close(d.doneCh)

	logger.Info("Daemon shutting down: reason=%s", reason)

	// Read inbox events (e.g., SessionEnd payload from sync stop)
	// We need to extract the session_end event to send to backend after final sync
	events := d.readInboxEvents()
	var sessionEndEvent *types.InboxEvent
	for _, event := range events {
		logger.Info("Processing inbox event: type=%s", event.Type)
		if event.Type == "session_end" && event.HookInput != nil {
			logger.Debug("SessionEnd event: reason=%s", event.HookInput.Reason)
			sessionEndEvent = &event
		}
	}

	// Final sync with timeout - if backend is slow/unresponsive, don't hang forever
	if d.engine != nil && d.engine.IsInitialized() {
		done := make(chan struct{})
		go func() {
			defer close(done)

			logger.Info("Performing final sync...")
			if chunks, err := d.engine.SyncAll(); err != nil {
				logger.Error("Final sync had errors: %v", err)
			} else if chunks > 0 {
				logger.Info("Final sync complete: chunks=%d", chunks)
			} else {
				logger.Info("Final sync complete: already up to date")
			}

			// Log final stats
			stats := d.engine.GetSyncStats()
			for file, lines := range stats {
				logger.Info("Final state: file=%s lines_synced=%d", file, lines)
			}

			// Send session_end event to backend (after final sync completes)
			if sessionEndEvent != nil {
				if err := d.engine.SendSessionEnd(sessionEndEvent.HookInput, sessionEndEvent.Timestamp); err != nil {
					logger.Error("Failed to send session_end event: %v", err)
					// Don't fail shutdown for this - the sync already completed
				}
			}
		}()

		select {
		case <-done:
			// Sync completed normally
		case <-time.After(shutdownTimeout):
			logger.Warn("Shutdown timed out after %v, skipping final sync", shutdownTimeout)
		}
	}

	// Clean up state and inbox files
	if d.state != nil {
		d.cleanupInbox()
		if err := d.state.Delete(); err != nil {
			logger.Warn("Failed to delete state file: %v", err)
		}
	}

	logger.Info("Daemon stopped")
	return nil
}

// readInboxEvents reads all events from the inbox file
func (d *Daemon) readInboxEvents() []types.InboxEvent {
	if d.state == nil || d.state.InboxPath == "" {
		return nil
	}

	f, err := os.Open(d.state.InboxPath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("Failed to open inbox file: %v", err)
		}
		return nil
	}
	defer f.Close()

	var events []types.InboxEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event types.InboxEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			logger.Warn("Failed to parse inbox event: %v", err)
			continue
		}
		events = append(events, event)
	}

	if err := scanner.Err(); err != nil {
		logger.Warn("Error reading inbox file: %v", err)
	}

	return events
}

// cleanupInbox removes the inbox file
func (d *Daemon) cleanupInbox() {
	if d.state == nil || d.state.InboxPath == "" {
		return
	}
	if err := os.Remove(d.state.InboxPath); err != nil && !os.IsNotExist(err) {
		logger.Warn("Failed to delete inbox file: %v", err)
	}
}

// StopDaemon sends SIGTERM to a running daemon by external ID.
// If hookInput is provided, it writes a session_end event to the daemon's inbox
// before signaling, so the daemon can access the full SessionEnd payload.
func StopDaemon(externalID string, hookInput *types.HookInput) error {
	state, err := LoadState(externalID)
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("no daemon found for session %s", externalID)
	}

	if !state.IsDaemonRunning() {
		// Clean up stale state file
		state.Delete()
		return fmt.Errorf("daemon not running (stale state cleaned up)")
	}

	// Write event to inbox before signaling (daemon reads on shutdown)
	if hookInput != nil && state.InboxPath != "" {
		if err := writeInboxEvent(state.InboxPath, "session_end", hookInput); err != nil {
			logger.Warn("Failed to write inbox event: %v", err)
			// Continue anyway - daemon can still do final sync without the event
		}
	}

	// Send SIGTERM
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	logger.Info("Sent SIGTERM to daemon: pid=%d", state.PID)
	return nil
}

// writeInboxEvent appends an event to the inbox JSONL file
func writeInboxEvent(inboxPath string, eventType string, hookInput *types.HookInput) error {
	event := types.InboxEvent{
		Type:      eventType,
		Timestamp: time.Now(),
		HookInput: hookInput,
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Append to file (create if doesn't exist)
	f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open inbox file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}

	return nil
}
