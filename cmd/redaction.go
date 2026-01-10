package cmd

import (
	"fmt"
	"os"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/redactor"
	"github.com/spf13/cobra"
)

var redactionTestCmd = &cobra.Command{
	Use:   "redaction-test <file>",
	Short: "Test redaction rules against a JSONL file",
	Long: `Test your redaction configuration against a JSONL file.

This command reads the specified JSONL file, applies the redaction rules
from ~/.confab/config.json, and outputs the redacted content to stdout.

Use this to verify your custom redaction patterns are working correctly
before they're applied to real uploads.

Example:
  confab redaction-test transcript.jsonl
  confab redaction-test transcript.jsonl > redacted.jsonl`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath := args[0]
		logger.Info("Running redaction-test command on %s", filePath)

		// Load config
		cfg, err := config.GetUploadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Check if redaction is configured
		if cfg.Redaction == nil {
			return fmt.Errorf("redaction is not configured in ~/.confab/config.json")
		}

		// Create redactor (works even if disabled, for testing purposes)
		r, err := redactor.NewFromConfig(cfg.Redaction)
		if err != nil {
			return fmt.Errorf("failed to create redactor: %w", err)
		}
		if r == nil {
			return fmt.Errorf("no redaction patterns configured")
		}

		// Read input file
		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		// Apply redaction
		redacted := r.RedactJSONL(content)

		// Output to stdout
		fmt.Print(string(redacted))

		return nil
	},
}

func init() {
	rootCmd.AddCommand(redactionTestCmd)
}
