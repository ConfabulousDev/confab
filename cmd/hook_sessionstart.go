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
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

// maxSyncEnvMS bounds CONFAB_SYNC_INTERVAL_MS / CONFAB_SYNC_JITTER_MS (1 hour).
const maxSyncEnvMS = 3600000

var bgDaemonData string // Hidden flag for daemon mode

var hookSessionStartCmd = &cobra.Command{
	Use:   "session-start",
	Short: "Handle SessionStart hook events",
	Long: `Handle SessionStart hook events.

This command is called by the SessionStart hook configured in each
provider's settings file. It starts a background sync daemon that
uploads session transcripts incrementally.

When called from a hook, it reads session info from stdin and spawns a
background daemon process. Provider is selected via --provider.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if bgDaemonData != "" {
			return runDaemon(bgDaemonData)
		}
		return sessionStartFromHook()
	},
}

func init() {
	hookCmd.AddCommand(hookSessionStartCmd)
	hookSessionStartCmd.Flags().StringVar(&bgDaemonData, "bg-daemon", "", "")
	hookSessionStartCmd.Flags().MarkHidden("bg-daemon")
}

func sessionStartFromHook() error {
	return sessionStartFromReader(os.Stdin, os.Stdout)
}

// sessionStartFromReader is the unified SessionStart handler.
// Provider selection comes from the --provider flag (hookProviderName).
func sessionStartFromReader(r io.Reader, w io.Writer) error {
	providerName, err := provider.NormalizeName(hookProviderName)
	if err != nil {
		return err
	}
	p, err := provider.Get(providerName)
	if err != nil {
		return err
	}

	logger.Info("Starting %s sync daemon (hook mode)", p.Name())

	// CF-549 F-up A: opportunistic cleanup of stale state files left by
	// crashed/killed daemons. Provider-agnostic; runs in a goroutine so it
	// can't slow the interactive hook. Failures are debug-level (cleanup
	// is best-effort).
	go func() {
		if reaped, rerr := daemon.ReapStaleStates(); rerr != nil {
			logger.Debug("reaper: %v", rerr)
		} else if reaped > 0 {
			logger.Info("reaper: cleaned %d stale state files", reaped)
		}
	}()

	AutoUpdateIfNeeded()

	var systemMessage string
	if p.Name() == provider.NameClaudeCode {
		systemMessage = RunAnnouncements()
	} else if err := p.InstallSkills(); err != nil {
		logger.Warn("Failed to ensure %s skills on SessionStart: %v", p.Name(), err)
	}

	defer func() { _ = p.WriteHookResponse(w, false, systemMessage) }()

	fmt.Fprintf(os.Stderr, "=== Confab: Starting %s Sync Daemon ===\n\n", p.Name())

	// OpenCode is different: the TS plugin pipes JSON via stdin, so
	// ParseSessionHook reads the JSON payload from r. For Claude/Codex,
	// the same stdin-based pattern is used by the native hook system.
	var launch *daemonLaunchInput
	if p.Name() == provider.NameOpencode {
		launch, err = buildOpencodeLaunchArgs(r)
	} else {
		launch, err = buildStandardLaunchArgs(p, r)
	}
	if err != nil {
		logger.ErrorPrint("Error reading %s hook input: %v", p.Name(), err)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Provider: %s\nSession:  %s\n",
		p.Name(), utils.TruncateSecret(launch.ExternalID, 8, 0))
	if launch.TranscriptPath != "" {
		fmt.Fprintf(os.Stderr, "Path:     %s\n", launch.TranscriptPath)
	}
	fmt.Fprintf(os.Stderr, "\n")

	spawned, err := maybeSpawnDaemon(p, launch)
	if err != nil {
		logger.ErrorPrint("Error spawning %s daemon: %v", p.Name(), err)
		return nil
	}
	if spawned {
		fmt.Fprintf(os.Stderr, "%s sync daemon started in background\n", p.Name())
	} else {
		fmt.Fprintf(os.Stderr, "%s sync daemon already running\n", p.Name())
	}

	return nil
}

// buildStandardLaunchArgs reads hook input from stdin for Claude/Codex
// providers, resolving subagent rollouts to roots when applicable.
func buildStandardLaunchArgs(p provider.Provider, r io.Reader) (*daemonLaunchInput, error) {
	in, err := p.ParseSessionHook(r)
	if err != nil {
		return nil, err
	}

	launch := &daemonLaunchInput{
		Provider:       p.Name(),
		ExternalID:     in.SessionID(),
		TranscriptPath: in.TranscriptPath(),
		CWD:            in.CWD(),
	}

	if launch.ExternalID != "" {
		rootID, rootPath, _ := p.WalkUpToRoot(launch.ExternalID)
		if rootID != "" && rootID != launch.ExternalID {
			logger.Info("%s SessionStart resolved to root: firing=%s root=%s rollout=%s",
				p.Name(), launch.ExternalID, rootID, rootPath)
			launch.ExternalID = rootID
			if rootPath != "" {
				launch.TranscriptPath = rootPath
			}
		}
	}
	return launch, nil
}

// buildOpencodeLaunchArgs reads the JSON payload piped from the TS plugin.
// The plugin constructs an OpenCodeHookInput with session_id, cwd, and
// optional parent_id + parent_pid — no transcript path since OpenCode data
// lives in the local SQLite DB, which the daemon resolves itself.
//
// CF-549: session.created carries cwd + parent_id inline (fast path).
// Reconcile events (session.status/updated/compacted/error) carry only
// session_id, so resolve cwd + parent_id from the SQLite DB. A DB
// resolution failure is non-fatal: empty cwd just means we lose the
// git-info-from-cwd fallback; empty parent_id is the correct root default.
func buildOpencodeLaunchArgs(r io.Reader) (*daemonLaunchInput, error) {
	p := provider.Opencode{}
	in, err := p.ReadSessionHookInput(r)
	if err != nil {
		return nil, err
	}

	launch := &daemonLaunchInput{
		Provider:        p.Name(),
		ExternalID:      in.SessionID,
		CWD:             in.CWD,
		SessionParentID: in.ParentID,
		ParentPID:       in.ParentPID,
	}

	if launch.CWD == "" {
		cwd, parentID, lookupErr := resolveOpencodeSessionInfo(in.SessionID)
		if lookupErr != nil {
			logger.Warn("Failed to resolve OpenCode session info for %s: %v; using defaults",
				in.SessionID, lookupErr)
		} else {
			launch.CWD = cwd
			// Preserve inline parent_id when set: the DB read can return ""
			// for sql.ErrNoRows (row not yet committed), and we must not
			// clobber a plugin-supplied parent into root status, which
			// would let ShouldSpawnForInput spawn a daemon for a subagent.
			if launch.SessionParentID == "" {
				launch.SessionParentID = parentID
			}
		}
	}

	return launch, nil
}

// resolveOpencodeSessionInfo reads directory + parent_id from the OpenCode
// SQLite DB with a tight 2s context bound. This blocks the interactive
// SessionStart hook, so we'd rather fall back to empty defaults than
// visibly stall opencode under DB contention.
func resolveOpencodeSessionInfo(sessionID string) (cwd, parentID string, _ error) {
	dbPath, err := provider.OpenCodeDBPath()
	if err != nil {
		return "", "", fmt.Errorf("resolve db path: %w", err)
	}
	reader := provider.NewOpenCodeDBReader(dbPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return reader.ReadSessionInfo(ctx, sessionID)
}

// parseSyncEnvConfig reads sync configuration from environment variables.
//
//   - CONFAB_SYNC_INTERVAL_MS: sync interval in milliseconds (e.g., "2000")
//   - CONFAB_SYNC_JITTER_MS: jitter in milliseconds (e.g., "0" to disable)
func parseSyncEnvConfig() (interval, jitter time.Duration) {
	interval = daemon.DefaultSyncInterval
	if envInterval := os.Getenv("CONFAB_SYNC_INTERVAL_MS"); envInterval != "" {
		if ms, err := strconv.Atoi(envInterval); err == nil && ms > 0 && ms <= maxSyncEnvMS {
			interval = time.Duration(ms) * time.Millisecond
		}
	}
	if envJitter := os.Getenv("CONFAB_SYNC_JITTER_MS"); envJitter != "" {
		if ms, err := strconv.Atoi(envJitter); err == nil && ms >= 0 && ms <= maxSyncEnvMS {
			jitter = time.Duration(ms) * time.Millisecond
		}
	}
	return
}

// runDaemon decodes a daemonLaunchInput from JSON and runs the daemon
// loop. The launch struct is now the only wire format — Phase 1's
// Claude-only fallback parse branch is gone.
func runDaemon(hookInputJSON string) error {
	logger.Info("Daemon process starting")

	var launch daemonLaunchInput
	if err := json.Unmarshal([]byte(hookInputJSON), &launch); err != nil {
		return fmt.Errorf("failed to parse daemon launch input: %w", err)
	}
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
