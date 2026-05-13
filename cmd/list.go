package cmd

import (
	"fmt"
	"time"

	"github.com/ConfabulousDev/confab/pkg/discovery"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var listDuration string
var listProviderName string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List local sessions",
	Long: `List all sessions found in ~/.claude/projects/.

Shows session ID (truncated), title/summary, and last activity time.
Copy the session ID to use with 'confab save <session-id>'.

Examples:
  confab list          # List all sessions
  confab list -d 5d    # List sessions from last 5 days
  confab list -d 12h   # List sessions from last 12 hours`,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		providerName, err := provider.NormalizeName(listProviderName)
		if err != nil {
			return err
		}
		return listSessions(providerName, listDuration)
	},
}

// listSessions scans and displays all local sessions
func listSessions(providerName, durationStr string) error {
	sessions, err := scanAndFilterSessions(providerName, durationStr)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		if durationStr != "" {
			fmt.Printf("No sessions found within the last %s\n", durationStr)
		} else if providerName == provider.NameCodex {
			fmt.Println("No sessions found in ~/.codex/sessions/")
		} else {
			fmt.Println("No sessions found in ~/.claude/projects/")
		}
		return nil
	}

	// Print table
	printSessionTable(providerName, sessions)

	return nil
}

// printSessionTable displays sessions in a formatted table
func printSessionTable(providerName string, sessions []discovery.SessionInfo) {
	// Print header
	fmt.Printf("%-8s  %-50s  %s\n", "ID", "TITLE", "LAST ACTIVITY")
	fmt.Printf("%-8s  %-50s  %s\n", "--------", "--------------------------------------------------", "-------------")

	// Print rows
	for _, session := range sessions {
		id, title, activity := formatSessionRow(session)
		fmt.Printf("%-8s  %-50s  %s\n", id, title, activity)
	}

	if providerName == provider.NameCodex {
		fmt.Printf("\n%d session(s) found. Use 'confab save --provider codex <id>' to sync to the backend.\n", len(sessions))
		return
	}
	if len(sessions) == 1 {
		fmt.Printf("\n1 session found. Use 'confab save <id>' to upload.\n")
		return
	}
	fmt.Printf("\n%d session(s) found. Use 'confab save <id>' to upload.\n", len(sessions))
}

// formatSessionRow formats a single session for display
func formatSessionRow(session discovery.SessionInfo) (id, title, activity string) {
	// Truncate session ID to first 8 chars (for copying)
	if len(session.SessionID) >= 8 {
		id = session.SessionID[:8]
	} else {
		id = session.SessionID
	}

	// Derive title from summary or first user message (for display only)
	displayTitle := session.Summary
	if displayTitle == "" {
		displayTitle = session.FirstUserMessage
	}

	// Format title (or dash if empty)
	if displayTitle != "" {
		title = utils.TruncateEnd(displayTitle, 50)
	} else {
		title = "-"
	}

	// Format last activity as relative time
	activity = formatDuration(time.Since(session.ModTime))

	return id, title, activity
}

// formatDuration formats a duration as a human-readable relative time
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func init() {
	listCmd.Flags().StringVarP(&listDuration, "duration", "d", "", "Filter sessions by duration (e.g., 5d, 12h, 30m)")
	listCmd.Flags().StringVar(&listProviderName, "provider", provider.NameClaudeCode, "Provider to list sessions from (claude-code or codex)")
	rootCmd.AddCommand(listCmd)
}
