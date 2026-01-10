package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// SetVersionInfo is called from main to set version info from ldflags
func SetVersionInfo(v, c, d string) {
	version = v
	commit = c
	date = d
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("confab %s (commit: %s, built: %s)\n", version, commit, date)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
