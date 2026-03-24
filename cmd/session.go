// ABOUTME: Parent command for session-related subcommands (get, etc.).
// ABOUTME: Groups commands for querying and retrieving session data from the backend.
package cmd

import "github.com/spf13/cobra"

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Query and retrieve sessions",
	Long:  `Commands for querying and retrieving Claude Code session data from the backend.`,
}

func init() {
	rootCmd.AddCommand(sessionCmd)
}
