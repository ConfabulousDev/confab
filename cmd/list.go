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
	Long: `List local sessions for the selected provider.

Shows session ID (truncated), title/summary, and last activity time.
Copy the session ID to use with 'confab save <session-id>'.

Examples:
  confab list                     # List sessions for the default provider
  confab list --provider codex    # List Codex sessions
  confab list -d 5d               # Sessions from last 5 days`,
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

	providerFlag := ""
	if p.Name() == provider.NameCodex {
		providerFlag = "--provider codex "
	}
	fmt.Printf("\n%d session(s) found. Use 'confab save %s<id>' to upload.\n", len(sessions), providerFlag)
}

// formatSessionRow formats a single session for display.
func formatSessionRow(session provider.SessionInfo) (id, title, activity string) {
	id = utils.TruncateSecret(session.SessionID, 8, 0)

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
