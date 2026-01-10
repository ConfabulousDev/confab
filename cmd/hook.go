package cmd

import (
	"github.com/spf13/cobra"
)

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
