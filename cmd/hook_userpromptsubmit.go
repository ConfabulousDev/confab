package cmd

import (
	"io"
	"os"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var hookUserPromptSubmitCmd = &cobra.Command{
	Use:   "user-prompt-submit",
	Short: "Handle UserPromptSubmit hook events",
	Long: `Handler for UserPromptSubmit hook events from Claude Code.

This hook fires when a user submits a prompt, before Claude processes
it. It ensures a sync daemon is running for the session, which handles
the teleport case where SessionStart doesn't fire.

This command is typically invoked by Claude Code, not directly by users.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return handleUserPromptSubmit(os.Stdin, os.Stdout)
	},
}

func init() {
	hookCmd.AddCommand(hookUserPromptSubmitCmd)
}

// handleUserPromptSubmit processes UserPromptSubmit hook events.
// UserPromptSubmit is Claude-only, so we hard-bind to ClaudeCode here.
func handleUserPromptSubmit(r io.Reader, w io.Writer) error {
	logger.Info("UserPromptSubmit hook triggered")

	defer writeUserPromptSubmitResponse(w)

	claude := provider.ClaudeCode{}
	hookInput, err := claude.ReadHookInput(r)
	if err != nil {
		logger.Warn("Failed to read hook input: %v", err)
		return nil
	}

	logger.Debug("UserPromptSubmit session_id=%s prompt_length=%d",
		hookInput.SessionID, len(hookInput.Prompt))

	launch := &daemonLaunchInput{
		Provider:       claude.Name(),
		ExternalID:     hookInput.SessionID,
		TranscriptPath: hookInput.TranscriptPath,
		CWD:            hookInput.CWD,
	}

	spawned, err := maybeSpawnDaemon(claude, launch)
	if err != nil {
		logger.Warn("Failed to spawn daemon: %v", err)
		return nil
	}
	if spawned {
		logger.Info("Spawned daemon from UserPromptSubmit (teleport case)")
	}
	return nil
}

func writeUserPromptSubmitResponse(w io.Writer) {
	writeClaudeHookResponse(w, true)
}
