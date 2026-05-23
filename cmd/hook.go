package cmd

import (
	"encoding/json"
	"io"

	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

var hookProviderName string

// writeClaudeHookResponse writes a standard Claude hook response to the given writer.
// All hooks must output valid JSON, even on error, so Claude Code can continue.
func writeClaudeHookResponse(w io.Writer, suppressOutput bool) {
	json.NewEncoder(w).Encode(types.ClaudeHookResponse{
		Continue:       true,
		SuppressOutput: suppressOutput,
	})
}

// hookCmd is the parent command for hook handlers.
// This is distinct from hooksCmd which manages hook installation.
var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Hook handlers for provider events",
	Long: `Hook handlers invoked by the configured provider during session events.

These commands are typically called by hooks configured in the provider's
settings file (Claude Code: ~/.claude/settings.json; Codex: ~/.codex/config.toml),
not directly by users.

Available handlers:
  session-start       Handle SessionStart events (starts sync daemon)
  session-end         Handle SessionEnd events (Claude Code only)
  pre-tool-use        Handle PreToolUse events
  post-tool-use       Handle PostToolUse events
  user-prompt-submit  Handle UserPromptSubmit events (Claude Code only)`,
}

func init() {
	hookCmd.PersistentFlags().StringVar(&hookProviderName, "provider", provider.NameClaudeCode, "Provider for hook input (claude-code or codex)")
	rootCmd.AddCommand(hookCmd)
}
