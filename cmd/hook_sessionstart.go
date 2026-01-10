package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/discovery"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

var bgDaemonData string // Hidden flag for daemon mode

var hookSessionStartCmd = &cobra.Command{
	Use:   "session-start",
	Short: "Handle SessionStart hook events",
	Long: `Handle SessionStart hook events from Claude Code.

This command is called by the SessionStart hook configured in
~/.claude/settings.json. It starts a background sync daemon that
uploads session transcripts incrementally.

When called from a hook, it reads session info from stdin and
spawns a background daemon process.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Daemon mode (called by detached process with --bg-daemon flag)
		if bgDaemonData != "" {
			return runDaemon(bgDaemonData)
		}

		// Otherwise, hook mode (read from stdin and spawn daemon)
		return sessionStartFromHook()
	},
}

func init() {
	hookCmd.AddCommand(hookSessionStartCmd)

	// Hidden flag for daemon mode
	hookSessionStartCmd.Flags().StringVar(&bgDaemonData, "bg-daemon", "", "")
	hookSessionStartCmd.Flags().MarkHidden("bg-daemon")
}

// sessionStartFromHook handles starting the daemon from a SessionStart hook
func sessionStartFromHook() error {
	return sessionStartFromReader(os.Stdin)
}

// sessionStartFromReader handles starting the daemon with input from the given reader.
// This is the testable core of sessionStartFromHook.
func sessionStartFromReader(r io.Reader) error {
	logger.Info("Starting sync daemon (hook mode)")

	// Check for updates before starting daemon
	AutoUpdateIfNeeded()

	// Always output valid hook response, even on error
	defer func() {
		response := types.HookResponse{
			Continue:       true,
			StopReason:     "",
			SuppressOutput: false,
		}
		json.NewEncoder(os.Stdout).Encode(response)
	}()

	fmt.Fprintln(os.Stderr, "=== Confab: Starting Sync Daemon ===")
	fmt.Fprintln(os.Stderr)

	// Read hook input from reader
	hookInput, err := discovery.ReadHookInputFrom(r)
	if err != nil {
		logger.ErrorPrint("Error reading hook input: %v", err)
		return nil
	}

	// Display session info (show first 8 chars of session ID)
	sessionPrefix := hookInput.SessionID
	if len(sessionPrefix) > 8 {
		sessionPrefix = sessionPrefix[:8]
	}
	fmt.Fprintf(os.Stderr, "Session: %s\n", sessionPrefix)
	fmt.Fprintf(os.Stderr, "Path:    %s\n", hookInput.TranscriptPath)
	fmt.Fprintln(os.Stderr)

	// Try to spawn daemon (shared logic handles existence check)
	spawned, err := maybeSpawnDaemon(hookInput)
	if err != nil {
		logger.ErrorPrint("Error spawning daemon: %v", err)
		return nil
	}

	if spawned {
		fmt.Fprintln(os.Stderr, "Sync daemon started in background")
	} else {
		fmt.Fprintln(os.Stderr, "Sync daemon already running")
	}

	return nil
}

// parseSyncEnvConfig reads sync configuration from environment variables.
// Returns the sync interval and jitter to use. Invalid values are ignored
// and fall back to defaults.
//
// Environment variables:
//   - CONFAB_SYNC_INTERVAL_MS: sync interval in milliseconds (e.g., "2000" for 2s)
//   - CONFAB_SYNC_JITTER_MS: jitter in milliseconds (e.g., "0" to disable)
func parseSyncEnvConfig() (interval, jitter time.Duration) {
	interval = daemon.DefaultSyncInterval
	if envInterval := os.Getenv("CONFAB_SYNC_INTERVAL_MS"); envInterval != "" {
		if ms, err := strconv.Atoi(envInterval); err == nil && ms > 0 {
			interval = time.Duration(ms) * time.Millisecond
		}
	}

	// jitter defaults to 0; caller/daemon can apply its own default if needed
	if envJitter := os.Getenv("CONFAB_SYNC_JITTER_MS"); envJitter != "" {
		if ms, err := strconv.Atoi(envJitter); err == nil && ms >= 0 {
			jitter = time.Duration(ms) * time.Millisecond
		}
	}
	return
}

// runDaemon runs the actual daemon process
func runDaemon(hookInputJSON string) error {
	logger.Info("Daemon process starting")

	var hookInput types.HookInput
	if err := json.Unmarshal([]byte(hookInputJSON), &hookInput); err != nil {
		return fmt.Errorf("failed to parse hook input: %w", err)
	}

	// Allow env vars to override sync interval and jitter (for testing)
	syncInterval, syncJitter := parseSyncEnvConfig()

	cfg := daemon.Config{
		ExternalID:         hookInput.SessionID,
		TranscriptPath:     hookInput.TranscriptPath,
		CWD:                hookInput.CWD,
		ParentPID:          hookInput.ParentPID,
		SyncInterval:       syncInterval,
		SyncIntervalJitter: syncJitter,
	}

	d := daemon.New(cfg)
	return d.Run(context.Background())
}
