// ABOUTME: CLI command to fetch a condensed session transcript from the backend.
// ABOUTME: Outputs the full JSON response (metadata + transcript) pretty-printed to stdout.
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

var (
	sessionGetExternalID bool
	sessionGetMaxChars   int
)

var sessionGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get condensed session transcript",
	Long: `Fetch a condensed session transcript from the backend.

Outputs the full JSON response (metadata + transcript) to stdout.
The transcript is condensed XML — conversation flow without raw tool outputs,
designed for LLM consumption.

Examples:
  # Get a session by UUID
  confab session get abc123-uuid-here

  # Get a session by external (CLI) ID
  confab session get --external-id my-session-id

  # Get last 5000 chars of transcript
  confab session get --max-chars 5000 abc123-uuid-here`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return runSessionGet(args[0], sessionGetExternalID, sessionGetMaxChars)
	},
}

func init() {
	sessionGetCmd.Flags().BoolVar(&sessionGetExternalID, "external-id", false, "Treat <id> as the CLI session external_id instead of UUID")
	sessionGetCmd.Flags().IntVar(&sessionGetMaxChars, "max-chars", 0, "Truncate transcript to last N characters")
	sessionCmd.AddCommand(sessionGetCmd)
}

// buildSessionGetPath constructs the API path for the condensed transcript endpoint.
func buildSessionGetPath(id string, externalID bool, maxChars int) string {
	params := url.Values{}

	var basePath string
	if externalID {
		basePath = "/api/v1/sessions/condensed-transcript"
		params.Set("external_id", id)
	} else {
		basePath = "/api/v1/sessions/" + url.PathEscape(id) + "/condensed-transcript"
	}

	if maxChars > 0 {
		params.Set("max_chars", strconv.Itoa(maxChars))
	}

	if len(params) == 0 {
		return basePath
	}
	return basePath + "?" + params.Encode()
}

func runSessionGet(id string, externalID bool, maxChars int) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	path := buildSessionGetPath(id, externalID, maxChars)

	var raw json.RawMessage
	if err := client.Get(path, &raw); err != nil {
		if errors.Is(err, confabhttp.ErrSessionNotFound) {
			return fmt.Errorf("session not found")
		}
		return fmt.Errorf("failed to fetch session: %w", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return fmt.Errorf("failed to format response: %w", err)
	}

	fmt.Println(pretty.String())
	return nil
}
