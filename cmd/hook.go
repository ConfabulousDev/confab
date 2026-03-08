package cmd

import (
	"encoding/json"
	"io"

	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

// writeHookResponse writes a standard hook response to the given writer.
// All hooks must output valid JSON, even on error, so Claude Code can continue.
func writeHookResponse(w io.Writer, suppressOutput bool) {
	json.NewEncoder(w).Encode(types.HookResponse{
		Continue:       true,
		SuppressOutput: suppressOutput,
	})
}

// hookCmd is the parent command for hook handlers.
// This is distinct from hooksCmd which manages hook installation.
var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Hook handlers for Claude Code events",
	Long: `Hook handlers that are invoked by Claude Code during various events.

These commands are typically called by Claude Code hooks configured in
~/.claude/settings.json, not directly by users.

Available handlers:
  session-start   Handle SessionStart events (starts sync daemon)
  session-end     Handle SessionEnd events (stops sync daemon)
  pre-tool-use    Handle PreToolUse events (e.g., git commit validation)`,
}

func init() {
	rootCmd.AddCommand(hookCmd)
}
