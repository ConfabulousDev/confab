// ABOUTME: CLI command for fetching session transcripts for the /retro skill.
// ABOUTME: Fetches condensed transcript from backend; with --output-dir, writes JSON and transcript XML to files.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var (
	retroMaxChars  int
	retroOutputDir string
)

var retroCmd = &cobra.Command{
	Use:   "retro <id>",
	Short: "Fetch a session transcript for retrospective",
	Long: `Fetch a condensed session transcript from the backend for review.

This command is typically invoked by the /retro skill, not directly by users.
It outputs the full JSON response (metadata + transcript) to stdout.

With --output-dir, it also writes two files:
  <dir>/response.json    — full JSON response (metadata + transcript)
  <dir>/transcript.xml   — just the condensed transcript XML

Examples:
  confab retro abc123-uuid-here
  confab retro --output-dir /tmp/retro abc123-uuid-here`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return runRetro(args[0], retroMaxChars, retroOutputDir)
	},
}

func init() {
	retroCmd.Flags().IntVar(&retroMaxChars, "max-chars", 0, "Truncate transcript to last N characters")
	retroCmd.Flags().StringVar(&retroOutputDir, "output-dir", "", "Write response.json and transcript.xml to this directory")
	rootCmd.AddCommand(retroCmd)
}

func runRetro(id string, maxChars int, outputDir string) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	raw, pretty, err := fetchCondensedTranscript(client, id, maxChars)
	if err != nil {
		return err
	}

	fmt.Println(pretty.String())

	if outputDir != "" {
		return writeRetroFiles(outputDir, pretty.Bytes(), raw)
	}

	return nil
}

// writeRetroFiles writes response.json and transcript.xml to outputDir.
func writeRetroFiles(outputDir string, prettyJSON []byte, raw json.RawMessage) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	jsonPath := filepath.Join(outputDir, "response.json")
	if err := os.WriteFile(jsonPath, prettyJSON, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", jsonPath, err)
	}

	var parsed struct {
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("failed to parse transcript from response: %w", err)
	}

	xmlPath := filepath.Join(outputDir, "transcript.xml")
	if err := os.WriteFile(xmlPath, []byte(parsed.Transcript), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", xmlPath, err)
	}

	fmt.Fprintf(os.Stderr, "Wrote %s\n", jsonPath)
	fmt.Fprintf(os.Stderr, "Wrote %s\n", xmlPath)
	return nil
}
