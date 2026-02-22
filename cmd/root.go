package cmd

import (
	"fmt"
	"os"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "confab",
	Short: "Archive and query your Claude Code sessions",
	Long: `Confab automatically captures Claude Code session transcripts and agent sidechains
and uploads them to the backend for retrieval, search, and analytics.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Initialize logger for all commands (except --help which doesn't run this)
		logger.Init()
		// Apply log level from config
		config.ApplyLogLevel()
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		// Close logger after all commands
		logger.Close()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
