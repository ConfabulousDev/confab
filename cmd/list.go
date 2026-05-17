package cmd

import (
	"fmt"
	"time"

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
		p, err := provider.Get(listProviderName)
		if err != nil {
			return err
		}
		return listSessions(p, listDuration)
	},
}

// listSessions scans and displays all local sessions for the given provider.
func listSessions(p provider.Provider, durationStr string) error {
	sessions, err := scanAndFilterSessions(p, durationStr)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		switch {
		case durationStr != "":
			fmt.Printf("No sessions found within the last %s\n", durationStr)
		case p.Name() == provider.NameCodex:
			fmt.Println("No sessions found in ~/.codex/sessions/")
		default:
			fmt.Println("No sessions found in ~/.claude/projects/")
		}
		return nil
	}

	printSessionTable(p, sessions)
	return nil
}

// printSessionTable displays sessions in a formatted table.
func printSessionTable(p provider.Provider, sessions []provider.SessionInfo) {
	fmt.Printf("%-8s  %-50s  %s\n", "ID", "TITLE", "LAST ACTIVITY")
	fmt.Printf("%-8s  %-50s  %s\n", "--------", "--------------------------------------------------", "-------------")

	for _, session := range sessions {
		id, title, activity := formatSessionRow(session)
		fmt.Printf("%-8s  %-50s  %s\n", id, title, activity)
	}

	switch {
	case p.Name() == provider.NameCodex:
		fmt.Printf("\n%d session(s) found. Use 'confab save --provider codex <id>' to sync to the backend.\n", len(sessions))
	case len(sessions) == 1:
		fmt.Println("\n1 session found. Use 'confab save <id>' to upload.")
	default:
		fmt.Printf("\n%d session(s) found. Use 'confab save <id>' to upload.\n", len(sessions))
	}
}

// formatSessionRow formats a single session for display.
func formatSessionRow(session provider.SessionInfo) (id, title, activity string) {
	if len(session.SessionID) >= 8 {
		id = session.SessionID[:8]
	} else {
		id = session.SessionID
	}

	displayTitle := session.Summary
	if displayTitle == "" {
		displayTitle = session.FirstUserMessage
	}

	if displayTitle != "" {
		title = utils.TruncateEnd(displayTitle, 50)
	} else {
		title = "-"
	}

	activity = formatDuration(time.Since(session.ModTime))
	return id, title, activity
}

// formatDuration formats a duration as a human-readable relative time.
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
