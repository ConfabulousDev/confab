// ABOUTME: CLI command to list raw transcript files for a session.
// ABOUTME: Prints a human-readable table of file metadata (name, type, lines, last updated).
package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var sessionListFilesCmd = &cobra.Command{
	Use:   "list-files <id>",
	Short: "List transcript files for a session",
	Long: `List raw transcript files available for a session.

Prints a table of file metadata including name, type, synced lines, and last update time.

Examples:
  confab session list-files abc123-uuid-here`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return runSessionListFiles(args[0])
	},
}

func init() {
	sessionCmd.AddCommand(sessionListFilesCmd)
}

func runSessionListFiles(id string) error {
	client, err := newAuthedClient()
	if err != nil {
		return err
	}

	var filesResp sessionFilesResponse
	if err := client.Get(buildSessionFilesPath(id), &filesResp); err != nil {
		return translateSessionErr(err, "list session files")
	}

	if len(filesResp.Files) == 0 {
		return fmt.Errorf("no files found for session")
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FILE_NAME\tFILE_TYPE\tLINES\tUPDATED")
	for _, f := range filesResp.Files {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
			f.FileName,
			f.FileType,
			f.LastSyncedLine,
			f.UpdatedAt.Local().Format("Jan 02 15:04"),
		)
	}
	return w.Flush()
}
