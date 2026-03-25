// ABOUTME: CLI command for fetching session transcripts for the /retro skill.
// ABOUTME: Thin wrapper that delegates to runSessionGet — outputs JSON for Claude to consume.
package cmd

import (
	"github.com/spf13/cobra"
)

var (
	retroExternalID bool
	retroMaxChars   int
)

var retroCmd = &cobra.Command{
	Use:   "retro <id>",
	Short: "Fetch a session transcript for retrospective",
	Long: `Fetch a condensed session transcript from the backend for review.

This command is typically invoked by the /retro skill, not directly by users.
It outputs the full JSON response (metadata + transcript) to stdout for Claude
to consume and discuss.

Examples:
  # Fetch by UUID
  confab retro abc123-uuid-here

  # Fetch by external (CLI) session ID
  confab retro --external-id my-session-id`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return runSessionGet(args[0], retroExternalID, retroMaxChars)
	},
}

func init() {
	retroCmd.Flags().BoolVar(&retroExternalID, "external-id", false, "Treat <id> as the CLI session external_id instead of UUID")
	retroCmd.Flags().IntVar(&retroMaxChars, "max-chars", 0, "Truncate transcript to last N characters")
	rootCmd.AddCommand(retroCmd)
}
