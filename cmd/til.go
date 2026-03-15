// ABOUTME: CLI command for saving TILs (Today I Learned) to the backend.
// ABOUTME: Invoked by the /til Claude Code skill — looks up daemon state, extracts message UUID, POSTs to API.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var (
	tilSession string
	tilTitle   string
	tilSummary string
	tilTags    []string
)

type tilRequest struct {
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	SessionID   string   `json:"session_id"`
	MessageUUID string   `json:"message_uuid,omitempty"`
	Tags        []string `json:"tags"`
}

type tilResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

var tilCmd = &cobra.Command{
	Use:   "til",
	Short: "Save a TIL (Today I Learned) to the backend",
	Long: `Save a TIL captured during a Claude Code session.

This command is typically invoked by the /til skill, not directly by users.
It looks up the active daemon state for the given session, extracts the
current transcript position (message UUID), and POSTs the TIL to the backend.

Examples:
  confab til --session abc123 --title "Proxy blocks OCP" --summary "When upgrading..."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return runTil(tilSession, tilTitle, tilSummary, tilTags)
	},
}

func init() {
	tilCmd.Flags().StringVar(&tilSession, "session", "", "Claude Code session ID (required)")
	tilCmd.Flags().StringVar(&tilTitle, "title", "", "TIL title (required)")
	tilCmd.Flags().StringVar(&tilSummary, "summary", "", "TIL summary (required)")
	tilCmd.Flags().StringArrayVar(&tilTags, "tag", nil, "Tags (repeatable)")
	tilCmd.MarkFlagRequired("session")
	tilCmd.MarkFlagRequired("title")
	tilCmd.MarkFlagRequired("summary")
	rootCmd.AddCommand(tilCmd)
}

func runTil(session, title, summary string, tags []string) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Look up daemon state for this session
	state, err := daemon.LoadState(session)
	if err != nil {
		return fmt.Errorf("failed to load session state: %w", err)
	}
	if state == nil {
		return fmt.Errorf("no active session found for %s — run /til from within a Claude Code session", utils.TruncateSecret(session, 8, 0))
	}

	if state.ConfabSessionID == "" {
		return fmt.Errorf("session %s has no backend session ID — daemon may still be initializing", utils.TruncateSecret(session, 8, 0))
	}

	// Extract message UUID from last line of transcript
	messageUUID := extractLastMessageUUID(state.TranscriptPath)
	logger.Debug("Transcript position: uuid=%s path=%s", messageUUID, state.TranscriptPath)

	if tags == nil {
		tags = []string{}
	}

	req := &tilRequest{
		Title:       title,
		Summary:     summary,
		SessionID:   state.ConfabSessionID,
		MessageUUID: messageUUID,
		Tags:        tags,
	}

	var resp tilResponse
	if err := client.Post("/api/v1/tils", req, &resp); err != nil {
		if errors.Is(err, confabhttp.ErrSessionNotFound) {
			return fmt.Errorf("TILs not yet supported by your backend. Update confab-web to enable this feature")
		}
		return fmt.Errorf("failed to save TIL: %w", err)
	}

	fmt.Fprintln(os.Stderr, "TIL saved.")
	return nil
}

// extractLastMessageUUID reads the last line of a JSONL file by seeking backward
// from the end, and extracts the "uuid" field.
func extractLastMessageUUID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		logger.Debug("Failed to open transcript for UUID extraction: %v", err)
		return ""
	}
	defer f.Close()

	lastLine := readLastLine(f)
	if lastLine == "" {
		return ""
	}

	var msg struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal([]byte(lastLine), &msg); err != nil {
		logger.Debug("Failed to parse last transcript line: %v", err)
		return ""
	}

	return msg.UUID
}

// readLastLine reads the last non-empty line from a file by seeking backward.
func readLastLine(f *os.File) string {
	stat, err := f.Stat()
	if err != nil || stat.Size() == 0 {
		return ""
	}

	// Read last 4KB — a single JSONL line is almost certainly within this
	const chunkSize = 4096
	size := stat.Size()
	offset := size - chunkSize
	if offset < 0 {
		offset = 0
	}

	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return ""
	}

	// Find last non-empty line
	content := strings.TrimRight(string(buf), "\n\r")
	idx := strings.LastIndex(content, "\n")
	if idx >= 0 {
		return content[idx+1:]
	}
	return content
}
