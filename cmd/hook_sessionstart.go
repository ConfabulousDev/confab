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
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

// maxSyncEnvMS is the maximum value for CONFAB_SYNC_INTERVAL_MS and CONFAB_SYNC_JITTER_MS (1 hour).
const maxSyncEnvMS = 3600000

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
		providerName, err := provider.NormalizeName(hookProviderName)
		if err != nil {
			return err
		}
		if providerName == provider.NameCodex {
			return codexSessionStartFromHook()
		}
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

	// Check for pending feature announcements (e.g., install /til skill)
	systemMessage := RunAnnouncements()

	// Always output valid hook response, even on error
	defer func() { writeClaudeHookResponseMsg(os.Stdout, false, systemMessage) }()

	fmt.Fprintln(os.Stderr, "=== Confab: Starting Sync Daemon ===")
	fmt.Fprintln(os.Stderr)

	// Read hook input from reader
	claude := provider.ClaudeCode{}
	hookInput, err := claude.ReadSessionHookInput(r)
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
	spawned, err := maybeSpawnDaemon(claude, hookInput)
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

func codexSessionStartFromHook() error {
	return codexSessionStartFromReader(os.Stdin)
}

func codexSessionStartFromReader(r io.Reader) error {
	logger.Info("Starting Codex sync daemon (hook mode)")

	defer func() { writeCodexHookResponse(os.Stdout, false, "Confab Codex sync daemon started") }()

	fmt.Fprintln(os.Stderr, "=== Confab: Starting Codex Sync Daemon ===")
	fmt.Fprintln(os.Stderr)

	codex := provider.Codex{}
	hookInput, err := codex.ReadSessionHookInput(r)
	if err != nil {
		logger.ErrorPrint("Error reading Codex hook input: %v", err)
		return nil
	}

	// Walk up the Codex thread tree to the top-most root. Subagent rollouts
	// fire their own SessionStart in Codex; left as-is, Confab would spawn an
	// orphaned daemon per subagent. Rewriting to the root here makes
	// maybeSpawnCodexDaemon's existing state-file dedup do the right thing:
	// only one daemon runs per root tree, and it discovers descendants via
	// the per-cycle SQLite walk in DiscoverCodexDescendants.
	//
	// WalkUpToRoot degrades gracefully — if the state DB is unavailable, the
	// edge race exhausts retries, or the firing thread is already a root,
	// it returns (firing UUID, "", nil) and we leave the input untouched.
	if hookInput.SessionID != "" {
		rootUUID, rootRolloutPath, _ := codex.WalkUpToRoot(hookInput.SessionID)
		if rootUUID != "" && rootUUID != hookInput.SessionID {
			logger.Info("Codex SessionStart hook resolved to root: firing=%s root=%s rollout=%s",
				hookInput.SessionID, rootUUID, rootRolloutPath)
			hookInput.SessionID = rootUUID
			if rootRolloutPath != "" {
				hookInput.TranscriptPath = rootRolloutPath
			}
		}
	}

	sessionPrefix := hookInput.SessionID
	if len(sessionPrefix) > 8 {
		sessionPrefix = sessionPrefix[:8]
	}
	fmt.Fprintf(os.Stderr, "Provider: codex\n")
	fmt.Fprintf(os.Stderr, "Session:  %s\n", sessionPrefix)
	fmt.Fprintf(os.Stderr, "Path:     %s\n", hookInput.TranscriptPath)
	fmt.Fprintln(os.Stderr, "Backend:  configured Confab backend")
	fmt.Fprintln(os.Stderr)

	spawned, err := maybeSpawnCodexDaemon(hookInput)
	if err != nil {
		logger.ErrorPrint("Error spawning Codex daemon: %v", err)
		return nil
	}
	if spawned {
		fmt.Fprintln(os.Stderr, "Codex sync daemon started in background")
	} else {
		fmt.Fprintln(os.Stderr, "Codex sync daemon already running")
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
		if ms, err := strconv.Atoi(envInterval); err == nil && ms > 0 && ms <= maxSyncEnvMS {
			interval = time.Duration(ms) * time.Millisecond
		}
	}

	// jitter defaults to 0; caller/daemon can apply its own default if needed
	if envJitter := os.Getenv("CONFAB_SYNC_JITTER_MS"); envJitter != "" {
		if ms, err := strconv.Atoi(envJitter); err == nil && ms >= 0 && ms <= maxSyncEnvMS {
			jitter = time.Duration(ms) * time.Millisecond
		}
	}
	return
}

// runDaemon runs the actual daemon process
func runDaemon(hookInputJSON string) error {
	logger.Info("Daemon process starting")

	var launch daemonLaunchInput
	if err := json.Unmarshal([]byte(hookInputJSON), &launch); err == nil && launch.Provider != "" {
		providerName, err := provider.NormalizeName(launch.Provider)
		if err != nil {
			return err
		}
		syncInterval, syncJitter := parseSyncEnvConfig()
		cfg := daemon.Config{
			Provider:           providerName,
			ExternalID:         launch.ExternalID,
			TranscriptPath:     launch.TranscriptPath,
			CWD:                launch.CWD,
			ParentPID:          launch.ParentPID,
			SyncInterval:       syncInterval,
			SyncIntervalJitter: syncJitter,
		}
		d := daemon.New(cfg)
		return d.Run(context.Background())
	}

	var hookInput types.ClaudeHookInput
	if err := json.Unmarshal([]byte(hookInputJSON), &hookInput); err != nil {
		return fmt.Errorf("failed to parse hook input: %w", err)
	}

	// Allow env vars to override sync interval and jitter (for testing)
	syncInterval, syncJitter := parseSyncEnvConfig()

	cfg := daemon.Config{
		Provider:           provider.NameClaudeCode,
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
