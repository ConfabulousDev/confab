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

	"github.com/ConfabulousDev/confab/pkg/confabpath"
	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
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

// parentCheckInterval is how often the parent-PID monitor goroutine
// (CF-549 R6) probes the parent process for liveness. Independent of
// syncInterval so a hung SyncAll cannot delay shutdown after parent
// death. Var (not const) so tests can shorten it; production paths
// never modify it.
var parentCheckInterval = 5 * time.Second

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
//
// For OpenCode, transcriptPath starts empty and is set lazily to the
// materialized file path once the SQLite-backed collector goroutine starts.
// The collector reads from OpenCode's local SQLite DB; the daemon does not
// hold a per-session server URL.
type Daemon struct {
	providerName   string
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

	// collectorCancel stops the OpenCode collector goroutine (nil for
	// Claude/Codex); collectorDone closes when that goroutine has exited.
	// shutdown() cancels then waits on these so the final sync reads a quiesced
	// materialized file (no concurrent append).
	collectorCancel context.CancelFunc
	collectorDone   chan struct{}

	// parentDeathCh is closed by the monitorParent goroutine (CF-549 R6)
	// when the daemon's parent process is detected dead. The main loop's
	// select drains it and triggers shutdown with reason "parent process
	// exited". Unused when parentPID == 0 (no parent monitoring requested).
	parentDeathCh chan struct{}

	// CF-538 OpenCode subagent sidechain capture --------------------------

	// dbReader is the OpenCode SQLite reader shared by the root collector
	// AND every child collector. Resolved once at daemon Run start. Nil on
	// Claude/Codex daemons.
	dbReader *provider.OpenCodeDBReader

	// childCollectorBase is the parent context for every per-descendant
	// child-collector goroutine. Derived from the daemon's main Run ctx so
	// a parent context cancellation tears down everything together.
	// childCollectorCancel cancels this context (and therefore every child)
	// at shutdown.
	childCollectorBase   context.Context
	childCollectorCancel context.CancelFunc

	// childCollectors maps childSessionID → goroutine handle. Guarded by
	// childCollectorsMu. Nil until startChildCollector first runs.
	childCollectors   map[string]*opencodeChildCollector
	childCollectorsMu sync.Mutex
}

// Config holds daemon configuration
type Config struct {
	Provider           string
	ExternalID         string
	TranscriptPath     string
	CWD                string
	ParentPID          int // Claude Code process ID to monitor (0 to disable)
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

	providerName := cfg.Provider
	if providerName == "" {
		providerName = provider.NameClaudeCode
	}

	return &Daemon{
		providerName:   providerName,
		externalID:     cfg.ExternalID,
		transcriptPath: cfg.TranscriptPath,
		cwd:            cfg.CWD,
		parentPID:      cfg.ParentPID,
		syncInterval:   interval,
		syncJitter:     jitter,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
		parentDeathCh:  make(chan struct{}),
	}
}

// monitorParent polls the parent PID at parentCheckInterval and closes
// parentDeathCh when it detects the parent has exited. Returns when the
// context is cancelled, when stopCh is closed, or when parent death is
// detected (after closing the channel). CF-549 R6: moved out of the sync
// main loop so a hung SyncAll cannot delay shutdown.
func (d *Daemon) monitorParent(ctx context.Context) {
	ticker := time.NewTicker(parentCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			if !isProcessRunning(d.parentPID) {
				logger.Info("Parent process %d exited; signaling shutdown", d.parentPID)
				close(d.parentDeathCh)
				return
			}
		}
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
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Wait for transcript file to exist before doing anything else.
	// Don't save state or set up panic handlers until we have a transcript.
	if err := d.waitForTranscript(ctx, sigCh); err != nil {
		return err
	}

	// Save state for duplicate detection. Done after transcript exists so we
	// don't leave stale state files for sessions that never produced transcripts.
	d.state = NewStateForProvider(d.providerName, d.externalID, d.transcriptPath, d.cwd, d.parentPID)
	if err := d.state.Save(); err != nil {
		logger.Warn("Failed to save initial state: %v", err)
	}

	// OpenCode has no upstream file to tail: derive a local materialized
	// path and start a goroutine that polls OpenCode's SQLite DB
	// (~/.local/share/opencode/opencode.db or CONFAB_OPENCODE_DB) and
	// appends each complete message to the path. The path is set here —
	// after the no-op waitForTranscript above — so the collector creates
	// the file asynchronously and backendSyncEnabled() gates the backend
	// session on its existence (no empty sessions). The collector runs
	// until the daemon shuts down (parent-PID driven); shutdown() cancels
	// it before the final sync.
	if d.providerName == provider.NameOpencode {
		path, err := openCodeMaterializedPath(d.externalID)
		if err != nil {
			return fmt.Errorf("failed to derive OpenCode materialized path: %w", err)
		}
		d.transcriptPath = path
		dbPath, err := provider.OpenCodeDBPath()
		if err != nil {
			return fmt.Errorf("failed to resolve OpenCode db path: %w", err)
		}
		d.dbReader = provider.NewOpenCodeDBReader(dbPath)

		// Parent context for child collectors. Cancelled via
		// d.childCollectorCancel in shutdown(); also tears down on Run's ctx
		// cancellation through the derived chain.
		d.childCollectorBase, d.childCollectorCancel = context.WithCancel(ctx)

		collectorCtx, cancel := context.WithCancel(ctx)
		d.collectorCancel = cancel
		d.collectorDone = make(chan struct{})
		collector := provider.NewOpenCodeCollector(
			d.dbReader, d.externalID, path, d.syncInterval)
		go func() {
			defer close(d.collectorDone)
			if err := collector.Run(collectorCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("OpenCode collector exited: %v", err)
			}
		}()
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
		// CF-549 R6: parent-PID monitoring runs in its own goroutine so a
		// hung SyncAll cannot delay shutdown after parent death. The
		// monitor closes parentDeathCh on detection; the main loop drains
		// it and triggers shutdown with the "parent process exited" reason.
		// Derive a cancellable subcontext + defer cancel so the goroutine
		// exits on every Run() return path (sigCh, parentDeathCh, etc.),
		// not just when the caller's ctx happens to cancel.
		monitorCtx, monitorCancel := context.WithCancel(ctx)
		defer monitorCancel()
		go d.monitorParent(monitorCtx)
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

		case <-d.parentDeathCh:
			// CF-549 R6: monitorParent goroutine detected parent exit.
			// Inline check inside `case <-timer.C` was removed so a hung
			// SyncAll cannot delay this shutdown.
			timer.Stop()
			return d.shutdown("parent process exited")

		case <-timer.C:
			// For OpenCode, the collector materializes the transcript file
			// asynchronously. Stay lifecycle-only — monitor the parent but
			// never contact the backend — until at least one complete
			// message exists, so we don't create empty backend sessions.
			if !d.backendSyncEnabled() {
				continue
			}

			// If not initialized yet, try to connect to backend
			if d.engine == nil || !d.engine.IsInitialized() {
				if err := d.tryInit(); err != nil {
					logger.Warn("Backend init failed (will retry): %v", err)
					if errors.Is(err, http.ErrUnauthorized) {
						d.resetEngineOnAuthFailure()
					}
					continue
				}
			}

			// Sync
			if chunks, err := d.engine.SyncAll(); err != nil {
				logger.Warn("Sync cycle had errors: %v", err)
				if errors.Is(err, http.ErrUnauthorized) {
					d.resetEngineOnAuthFailure()
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
// For OpenCode (empty transcriptPath), there is no file to watch — returns
// immediately; the SQLite-backed collector will materialize the file on its
// own poll cycle.
func (d *Daemon) waitForTranscript(ctx context.Context, sigCh chan os.Signal) error {
	if d.transcriptPath == "" {
		logger.Info("Skipping transcript wait (OpenCode mode; collector will materialize)")
		return nil
	}
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

// backendSyncEnabled reports whether this daemon has a data source to sync.
// Claude/Codex always have a transcript path. OpenCode's path is the
// materialized JSONL the collector writes; we wait for that file to exist
// (i.e. for at least one complete message to land) before contacting the
// backend, so we never create an empty backend session.
func (d *Daemon) backendSyncEnabled() bool {
	if d.transcriptPath == "" {
		return false
	}
	if d.providerName == provider.NameOpencode {
		_, err := os.Stat(d.transcriptPath)
		return err == nil
	}
	return true
}

// openCodeMaterializedPath is where the OpenCode collector writes a session's
// assembled {info, parts} JSONL. It doubles as the daemon's transcriptPath, so
// the ordinary file-based sync pipeline uploads it; the backend file_name is
// its base ("messages.jsonl"), unique within the session.
func openCodeMaterializedPath(externalID string) (string, error) {
	return confabpath.Subpath("opencode", externalID, "messages.jsonl")
}

// tryInit attempts to initialize the sync engine and session with the backend.
// Auth is checked here lazily, not at daemon startup.
func (d *Daemon) tryInit() error {
	// Create engine if not already created
	if d.engine == nil {
		engineCfg := pkgsync.EngineConfig{
			Provider:       d.providerName,
			ExternalID:     d.externalID,
			TranscriptPath: d.transcriptPath,
			CWD:            d.cwd,
		}

		// Get authenticated config lazily, only when we need to talk to backend.
		cfg, cfgErr := config.EnsureAuthenticated()
		if cfgErr != nil {
			return fmt.Errorf("not authenticated: %w", cfgErr)
		}
		engine, err := pkgsync.New(cfg, engineCfg)
		if err != nil {
			return fmt.Errorf("failed to create sync engine: %w", err)
		}
		d.engine = engine

		// CF-538: wrap the engine's tracker so OpenCode's DiscoverDescendants
		// drives per-child collector spawn (and capability gating) through
		// the same provider seam Codex uses. Set once per engine — a reset
		// (auth failure) creates a fresh engine, which gets a fresh wrapper.
		if d.providerName == provider.NameOpencode {
			reg := newOpencodeRegistrar(engine.Tracker(), engine, d)
			engine.SetDescendantRegistrar(reg)
		}
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

// resetEngineOnAuthFailure clears the sync engine to force a config re-read
// on the next cycle. The user may have re-authenticated with a new API key.
func (d *Daemon) resetEngineOnAuthFailure() {
	logger.Info("Auth failed, will re-read config on next cycle")
	if d.engine != nil {
		d.engine.Reset()
	}
	d.engine = nil
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

	// Stop the OpenCode collectors (root + every CF-538 descendant) and wait
	// for them to exit before the final sync, so no append races the final
	// read (which would drop the last materialized message). Each collector
	// does a final SQLite reconcile on cancel, so this returns promptly; the
	// single 2s ceiling covers them all.
	//
	// Children inherit their context from childCollectorBase, so a single
	// cancel of the base propagates to every child. The root collector is
	// cancelled separately because it was spawned before the children pool.
	if d.collectorCancel != nil {
		d.collectorCancel()
	}
	var childDones []chan struct{}
	if d.childCollectorCancel != nil {
		childDones = d.childCollectorDones()
		d.childCollectorCancel()
	}
	if d.collectorCancel != nil || len(childDones) > 0 {
		waitForCollectors(d.collectorDone, childDones, 2*time.Second)
	}

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
			defer func() {
				if r := recover(); r != nil {
					logger.Error("Panic during final sync: %v", r)
				}
			}()

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
		if err := d.state.DeleteWithInbox(); err != nil {
			logger.Warn("Failed to delete state/inbox files: %v", err)
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

// StopDaemon sends SIGTERM to a running daemon by external ID.
// If hookInput is provided, it writes a session_end event to the daemon's inbox
// before signaling, so the daemon can access the full SessionEnd payload.
func StopDaemon(externalID string, hookInput *types.ClaudeHookInput) error {
	return StopDaemonForProvider(provider.NameClaudeCode, externalID, hookInput)
}

// StopDaemonForProvider sends SIGTERM to a running daemon by provider and external ID.
func StopDaemonForProvider(providerName, externalID string, hookInput *types.ClaudeHookInput) error {
	state, err := LoadStateForProvider(providerName, externalID)
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
func writeInboxEvent(inboxPath string, eventType string, hookInput *types.ClaudeHookInput) error {
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
