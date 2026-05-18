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

	AutoUpdateIfNeeded()

	// Announcements are Claude-only: they install Claude skill files under
	// ~/.claude/skills/ and surface Claude-only slash commands. Showing
	// them on Codex SessionStart would silently write Claude config files
	// for users who never installed Claude Code.
	var systemMessage string
	if p.Name() == provider.NameClaudeCode {
		systemMessage = RunAnnouncements()
	}

	defer func() { _ = p.WriteHookResponse(w, false, systemMessage) }()

	fmt.Fprintf(os.Stderr, "=== Confab: Starting %s Sync Daemon ===\n\n", p.Name())

	in, err := p.ParseSessionHook(r)
	if err != nil {
		logger.ErrorPrint("Error reading %s hook input: %v", p.Name(), err)
		return nil
	}

	launch := &daemonLaunchInput{
		Provider:       p.Name(),
		ExternalID:     in.SessionID(),
		TranscriptPath: in.TranscriptPath(),
		CWD:            in.CWD(),
		// ParentPID is filled by maybeSpawnDaemon via p.FindParentPID().
	}

	// Resolve to the root session. Identity for Claude; thread-edge walk
	// for Codex. WalkUpToRoot degrades gracefully on errors and returns
	// the input unchanged.
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

	fmt.Fprintf(os.Stderr, "Provider: %s\nSession:  %s\nPath:     %s\n\n",
		p.Name(), utils.TruncateSecret(launch.ExternalID, 8, 0), launch.TranscriptPath)

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
