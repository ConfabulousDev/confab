// ABOUTME: Parent command for session-related subcommands (get-summary, download, list-files).
// ABOUTME: Groups commands for querying and retrieving session data from the backend.
package cmd

import "github.com/spf13/cobra"

// Shared binding-selection flags for the session subcommands (kata szwk). They
// target a per-(provider, config-dir) backend; both empty keeps the unchanged
// default-binding path.
var (
	sessionProviderName string
	sessionConfigDir    string
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Query and retrieve sessions",
	Long:  `Commands for querying and retrieving session data from the backend. Works for sessions captured from any supported provider.`,
}

func init() {
	sessionCmd.PersistentFlags().StringVar(&sessionProviderName, "provider", "", "Target the backend bound to this provider (default: top-level backend)")
	sessionCmd.PersistentFlags().StringVar(&sessionConfigDir, "config-dir", "", "Target the backend bound to this provider's config dir (requires --provider)")
	rootCmd.AddCommand(sessionCmd)
}
