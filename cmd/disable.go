package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

// stopAllTimeout is how long disable waits for SIGTERMed daemons before
// SIGKILLing them. A var so tests can shorten it.
var stopAllTimeout = daemon.DefaultStopAllTimeout()

var disableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Stop syncing: stop every sync daemon and pause automatic sync",
	Long: `Turns Confabulous Code's automatic sync off on this machine.

This command:
1. Sets a disabled flag in ~/.confab/config.json, so every hook becomes a
   silent no-op and no new sync daemon starts
2. Stops every running sync daemon (each does its normal final upload of
   what it already read; one that does not exit in time is killed)

Hooks, skills and credentials stay installed. Manual commands (save,
session, retro) keep working. Run 'confab enable' (or 'confab setup') to
turn sync back on.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running disable command")
		return runDisable(os.Stdout, stopAllTimeout)
	},
}

// runDisable sets the sync_disabled flag FIRST, then stops every daemon.
// That order is what makes it race-free: a daemon that the listing misses
// saved its state after the listing, so it reads the flag after it was set
// and exits on its own. Idempotent.
func runDisable(w io.Writer, timeout time.Duration) error {
	changed, err := config.SetSyncDisabled(true)
	if err != nil {
		return fmt.Errorf("failed to disable sync: %w", err)
	}
	if changed {
		fmt.Fprintln(w, "Sync disabled.")
	} else {
		fmt.Fprintln(w, "Sync was already disabled.")
	}

	results, err := daemon.StopAllDaemons(timeout)
	if err != nil {
		return fmt.Errorf("failed to list sync daemons: %w", err)
	}

	var stopped, failed int
	for _, r := range results {
		switch r.Outcome {
		case daemon.StopStopped, daemon.StopKilled:
			stopped++
			if r.Outcome != daemon.StopKilled {
				continue
			}
			fmt.Fprintf(w, "  %s session %s (pid %d) did not exit in time and was killed\n", r.Provider, r.ExternalID, r.PID)
		case daemon.StopFailed:
			failed++
			fmt.Fprintf(w, "  ✗ could not stop %s session %s (pid %d): %v\n", r.Provider, r.ExternalID, r.PID, r.Err)
		}
	}
	if stopped > 0 {
		fmt.Fprintf(w, "Stopped %d sync daemon(s).\n", stopped)
	} else if failed == 0 {
		fmt.Fprintln(w, "No sync daemons were running.")
	}
	if failed > 0 {
		return fmt.Errorf("failed to stop %d sync daemon(s)", failed)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(disableCmd)
}
