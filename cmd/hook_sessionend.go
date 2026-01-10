package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/discovery"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

var hookSessionEndCmd = &cobra.Command{
	Use:   "session-end",
	Short: "Handle SessionEnd hook events",
	Long: `Handle SessionEnd hook events from Claude Code.

This command is called by the SessionEnd hook configured in
~/.claude/settings.json. It signals the sync daemon to perform
a final sync and shut down gracefully.

When called from a hook, it reads session info from stdin and
signals the daemon to stop.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return sessionEndFromHook()
	},
}

func init() {
	hookCmd.AddCommand(hookSessionEndCmd)
}

// sessionEndFromHook handles stopping the daemon from a SessionEnd hook
func sessionEndFromHook() error {
	return sessionEndFromReader(os.Stdin)
}

// sessionEndFromReader handles stopping the daemon with input from the given reader.
// This is the testable core of sessionEndFromHook.
func sessionEndFromReader(r io.Reader) error {
	logger.Info("Stopping sync daemon (hook mode)")

	// Always output valid hook response, even on error
	defer func() {
		response := types.HookResponse{
			Continue:       true,
			StopReason:     "",
			SuppressOutput: false,
		}
		json.NewEncoder(os.Stdout).Encode(response)
	}()

	fmt.Fprintln(os.Stderr, "=== Confab: Stopping Sync Daemon ===")
	fmt.Fprintln(os.Stderr)

	// Read hook input from reader
	hookInput, err := discovery.ReadHookInputFrom(r)
	if err != nil {
		logger.ErrorPrint("Error reading hook input: %v", err)
		return nil
	}

	// Signal daemon to stop (it will do final sync in background)
	// Pass hookInput so daemon can access the full SessionEnd payload
	if err := daemon.StopDaemon(hookInput.SessionID, hookInput); err != nil {
		logger.Warn("Could not stop daemon: %v", err)
		fmt.Fprintf(os.Stderr, "Note: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "Daemon signaled to stop (final sync in background)")
	}

	return nil
}
