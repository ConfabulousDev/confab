package cmd

import (
	"encoding/json"
	"io"
	"os"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

var hookUserPromptSubmitCmd = &cobra.Command{
	Use:   "user-prompt-submit",
	Short: "Handle UserPromptSubmit hook events",
	Long: `Handler for UserPromptSubmit hook events from Claude Code.

This hook fires when a user submits a prompt, before Claude processes it.
It ensures a sync daemon is running for the session, which handles the
teleport case where SessionStart doesn't fire.

This command is typically invoked by Claude Code, not directly by users.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return handleUserPromptSubmit(os.Stdin, os.Stdout)
	},
}

func init() {
	hookCmd.AddCommand(hookUserPromptSubmitCmd)
}

// handleUserPromptSubmit processes UserPromptSubmit hook events.
// Ensures a daemon is running for the session (handles teleport case).
func handleUserPromptSubmit(r io.Reader, w io.Writer) error {
	logger.Info("UserPromptSubmit hook triggered")

	// Always output valid hook response, even on error
	defer writeUserPromptSubmitResponse(w)

	// Read and validate hook input
	hookInput, err := types.ReadHookInput(r)
	if err != nil {
		logger.Warn("Failed to read hook input: %v", err)
		return nil
	}

	logger.Debug("UserPromptSubmit session_id=%s prompt_length=%d",
		hookInput.SessionID, len(hookInput.Prompt))

	// Ensure daemon is running (handles teleport case where SessionStart doesn't fire)
	spawned, err := maybeSpawnDaemon(hookInput)
	if err != nil {
		logger.Warn("Failed to spawn daemon: %v", err)
		return nil
	}

	if spawned {
		logger.Info("Spawned daemon from UserPromptSubmit (teleport case)")
	}

	return nil
}

// writeUserPromptSubmitResponse writes a success response allowing the prompt to proceed
func writeUserPromptSubmitResponse(w io.Writer) error {
	response := types.HookResponse{
		Continue:       true,
		StopReason:     "",
		SuppressOutput: true, // Don't add anything to context
	}
	return json.NewEncoder(w).Encode(response)
}
