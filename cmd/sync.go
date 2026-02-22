package cmd

import (
	"fmt"
	"time"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Incremental session sync daemon",
	Long: `Manage the incremental sync daemon that uploads session data
during active Claude Code sessions.

The daemon watches transcript files and uploads new content
to the backend every 30 seconds.

Note: The "sync start" and "sync stop" commands are aliases for
"hook session-start" and "hook session-end" respectively.`,
}

var syncStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start sync daemon for a session (alias for 'hook session-start')",
	Long: `Start the sync daemon for a session. When called from a hook,
reads session info from stdin and starts a background daemon.

This command is an alias for 'confab hook session-start'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Delegate to the session-start hook handler
		return hookSessionStartCmd.RunE(hookSessionStartCmd, args)
	},
}

var syncStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop sync daemon for a session (alias for 'hook session-end')",
	Long: `Stop the sync daemon for a session. When called from a hook,
reads session info from stdin and signals the daemon to stop.

This command is an alias for 'confab hook session-end'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Delegate to the session-end hook handler
		return hookSessionEndCmd.RunE(hookSessionEndCmd, args)
	},
}

var syncStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of running sync daemons",
	Long:  `Display information about all running sync daemons.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return showSyncStatus()
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
	syncCmd.AddCommand(syncStartCmd)
	syncCmd.AddCommand(syncStopCmd)
	syncCmd.AddCommand(syncStatusCmd)

	// Forward the --bg-daemon flag to sync start for backwards compatibility.
	// Old daemon processes may still call "sync start --bg-daemon".
	syncStartCmd.Flags().StringVar(&bgDaemonData, "bg-daemon", "", "")
	syncStartCmd.Flags().MarkHidden("bg-daemon")
}

// showSyncStatus displays all running sync daemons
func showSyncStatus() error {
	states, err := daemon.ListAllStates()
	if err != nil {
		return fmt.Errorf("failed to list daemon states: %w", err)
	}

	if len(states) == 0 {
		fmt.Println("No sync daemons running")
		return nil
	}

	fmt.Printf("Running sync daemons:\n\n")

	for _, state := range states {
		running := state.IsDaemonRunning()
		status := "running"
		if !running {
			status = "not running (stale)"
		}

		fmt.Printf("Session: %s\n", state.ExternalID[:8])
		fmt.Printf("  Status:  %s\n", status)
		fmt.Printf("  PID:     %d\n", state.PID)
		fmt.Printf("  Started: %s\n", state.StartedAt.Format(time.RFC3339))
		fmt.Printf("  Path:    %s\n", state.TranscriptPath)
		fmt.Println()
	}

	return nil
}
