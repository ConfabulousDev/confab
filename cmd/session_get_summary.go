// ABOUTME: CLI command to fetch a condensed session transcript from the backend.
// ABOUTME: Hosts the shared fetchCondensedTranscript helper used by both get-summary and retro commands.
package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var sessionGetSummaryMaxChars int

var sessionGetSummaryCmd = &cobra.Command{
	Use:   "get-summary <id>",
	Short: "Get condensed session transcript",
	Long: `Fetch a condensed session transcript from the backend.

Outputs the full JSON response (metadata + transcript) to stdout.
The transcript is condensed XML — conversation flow without raw tool outputs,
designed for LLM consumption.

Examples:
  # Get a session by UUID
  confab session get-summary abc123-uuid-here

  # Get last 5000 chars of transcript
  confab session get-summary --max-chars 5000 abc123-uuid-here`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return runSessionGetSummary(args[0], sessionGetSummaryMaxChars)
	},
}

func init() {
	sessionGetSummaryCmd.Flags().IntVar(&sessionGetSummaryMaxChars, "max-chars", 0, "Truncate transcript to last N characters")
	sessionCmd.AddCommand(sessionGetSummaryCmd)
}

// buildSessionGetSummaryPath constructs the API path for the condensed transcript endpoint.
func buildSessionGetSummaryPath(id string, maxChars int) string {
	basePath := "/api/v1/sessions/" + url.PathEscape(id) + "/condensed-transcript"

	if maxChars > 0 {
		params := url.Values{}
		params.Set("max_chars", strconv.Itoa(maxChars))
		return basePath + "?" + params.Encode()
	}

	return basePath
}

func runSessionGetSummary(id string, maxChars int) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	_, pretty, err := fetchCondensedTranscript(client, id, maxChars)
	if err != nil {
		return err
	}

	fmt.Println(pretty.String())
	return nil
}

// fetchCondensedTranscript fetches a condensed transcript from the backend and
// returns both the raw JSON and pretty-printed form. Used by session get-summary
// and retro commands.
func fetchCondensedTranscript(client *confabhttp.Client, id string, maxChars int) (json.RawMessage, bytes.Buffer, error) {
	path := buildSessionGetSummaryPath(id, maxChars)

	var raw json.RawMessage
	if err := client.Get(path, &raw); err != nil {
		if errors.Is(err, confabhttp.ErrSessionNotFound) {
			return nil, bytes.Buffer{}, fmt.Errorf("session not found")
		}
		return nil, bytes.Buffer{}, fmt.Errorf("failed to fetch session: %w", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return nil, bytes.Buffer{}, fmt.Errorf("failed to format response: %w", err)
	}

	return raw, pretty, nil
}
