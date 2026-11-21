package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

var enableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Resume automatic sync after 'confab disable'",
	Long: `Clears the disabled flag set by 'confab disable' or 'confab uninstall'.

No daemon is started now: sync resumes on the next agent session or
prompt. After 'confab uninstall' the hooks are gone too; run 'confab setup'
to reinstall them (setup also re-enables sync).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running enable command")
		return runEnable(os.Stdout)
	},
}

// runEnable clears the sync_disabled flag. Idempotent.
func runEnable(w io.Writer) error {
	changed, err := config.SetSyncDisabled(false)
	if err != nil {
		return fmt.Errorf("failed to enable sync: %w", err)
	}
	if changed {
		fmt.Fprintln(w, "Sync enabled; it resumes on the next agent session or prompt.")
	} else {
		fmt.Fprintln(w, "Sync is already enabled.")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(enableCmd)
}
