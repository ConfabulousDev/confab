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
	ID    int64 `json:"id"`
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

	// Extract message UUID from transcript — targets the /til invocation line
	messageUUID := extractTilMessageUUID(state.TranscriptPath)
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

// tilCommandMarker is the string that identifies a /til slash command invocation
// in a Claude Code transcript JSONL line.
const tilCommandMarker = "<command-name>/til</command-name>"

// extractTilMessageUUID scans backward through the last lines of a transcript
// JSONL file looking for the /til invocation line. Returns that line's UUID,
// or falls back to the most recent line with any UUID if the /til line isn't found.
func extractTilMessageUUID(path string) string {
	lines, err := readTailLines(path, 100)
	if err != nil {
		logger.Debug("Failed to read transcript tail for UUID extraction: %v", err)
		return ""
	}

	var fallbackUUID string

	// Scan backward: prefer /til invocation, fall back to last line with any UUID
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]

		var msg struct {
			UUID string `json:"uuid"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.UUID == "" {
			continue
		}

		if strings.Contains(line, tilCommandMarker) {
			return msg.UUID
		}

		if fallbackUUID == "" {
			fallbackUUID = msg.UUID
		}
	}

	return fallbackUUID
}

// readTailLines reads up to maxLines non-empty lines from the end of a file.
// It reads a tail chunk sized to cover the requested number of lines without
// loading the entire file into memory.
func readTailLines(path string, maxLines int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() == 0 {
		return nil, nil
	}

	// ~250 bytes/line average for JSONL transcripts
	const bytesPerLine = 250
	chunkSize := int64(maxLines * bytesPerLine)
	size := stat.Size()
	offset := size - chunkSize
	if offset < 0 {
		offset = 0
	}

	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, err
	}

	content := strings.TrimRight(string(buf), "\n\r")
	if content == "" {
		return nil, nil
	}

	allLines := strings.Split(content, "\n")

	// If we read from a mid-file offset, the first "line" may be a partial —
	// drop it to avoid corrupt JSON parsing.
	if offset > 0 && len(allLines) > 0 {
		allLines = allLines[1:]
	}

	if len(allLines) > maxLines {
		allLines = allLines[len(allLines)-maxLines:]
	}

	return allLines, nil
}
