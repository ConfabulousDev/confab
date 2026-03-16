// ABOUTME: Tests for the confab til CLI command.
// ABOUTME: Validates request building, UUID extraction, and backend integration.
package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
)

func TestTilRequest_Fields(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		summary     string
		tags        []string
		wantTagsLen int
	}{
		{"basic", "TIL about proxies", "Proxy blocks OCP", nil, 0},
		{"with tags", "TIL", "Summary", []string{"go", "testing"}, 2},
		{"empty tags", "TIL", "Summary", []string{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := tt.tags
			if tags == nil {
				tags = []string{}
			}

			req := &tilRequest{
				Title:     tt.title,
				Summary:   tt.summary,
				SessionID: "sess-123",
				Tags:      tags,
			}

			if req.Title != tt.title {
				t.Errorf("Title = %s, want %s", req.Title, tt.title)
			}
			if req.Summary != tt.summary {
				t.Errorf("Summary = %s, want %s", req.Summary, tt.summary)
			}
			if len(req.Tags) != tt.wantTagsLen {
				t.Errorf("Tags count = %d, want %d", len(req.Tags), tt.wantTagsLen)
			}
			if req.Tags == nil {
				t.Error("Tags should not be nil")
			}
		})
	}
}

func TestExtractTilMessageUUID(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantUUID string
	}{
		{
			"fallback to last uuid",
			`{"type":"user","message":{"content":"Hello"},"uuid":"msg-001"}
{"type":"assistant","message":{"content":"Hi"},"uuid":"msg-002"}`,
			"msg-002",
		},
		{
			"no uuid field",
			`{"type":"assistant","message":{"content":"Hi"}}`,
			"",
		},
		{
			"empty file",
			"",
			"",
		},
		{
			"single line with uuid",
			`{"type":"user","uuid":"only-one"}`,
			"only-one",
		},
		{
			"trailing newline",
			`{"type":"user","uuid":"msg-001"}
{"type":"assistant","uuid":"msg-002"}
`,
			"msg-002",
		},
		{
			"til command with last-prompt after",
			`{"type":"user","message":{"content":"Hello"},"uuid":"msg-001"}
{"type":"user","message":{"content":"<command-message>til</command-message>\n<command-name>/til</command-name>\n<command-args>learned something</command-args>"},"uuid":"til-uuid"}
{"type":"user","message":{"content":"skill expansion"},"isMeta":true,"uuid":"meta-uuid"}
{"type":"assistant","uuid":"assist-uuid"}
{"type":"last-prompt","lastPrompt":"/til learned something"}`,
			"til-uuid",
		},
		{
			"til command found even with uuid-bearing lines after it",
			`{"type":"user","uuid":"msg-001"}
{"type":"user","message":{"content":"<command-message>til</command-message>\n<command-name>/til</command-name>\n<command-args>test</command-args>"},"uuid":"til-uuid"}
{"type":"assistant","uuid":"msg-003"}
{"type":"assistant","uuid":"msg-004"}`,
			"til-uuid",
		},
		{
			"no til command falls back to last uuid",
			`{"type":"user","uuid":"msg-001"}
{"type":"assistant","uuid":"msg-002"}
{"type":"last-prompt","lastPrompt":"hello"}`,
			"msg-002",
		},
		{
			"multiple uuid-less lines at end",
			`{"type":"user","uuid":"msg-001"}
{"type":"last-prompt"}
{"type":"last-prompt"}`,
			"msg-001",
		},
		{
			"no lines have uuid",
			`{"type":"last-prompt"}`,
			"",
		},
		{
			"til command with empty uuid falls back",
			`{"type":"user","uuid":"msg-001"}
{"type":"user","message":{"content":"<command-message>til</command-message>\n<command-name>/til</command-name>"},"uuid":""}
{"type":"assistant","uuid":"msg-003"}`,
			"msg-003",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "transcript.jsonl")
			os.WriteFile(path, []byte(tt.content), 0644)

			got := extractTilMessageUUID(path)
			if got != tt.wantUUID {
				t.Errorf("extractTilMessageUUID() = %q, want %q", got, tt.wantUUID)
			}
		})
	}
}

func TestExtractTilMessageUUID_LargeFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")

	// Build a large file: many filler lines, then /til command, then UUID-less lines
	var lines []string
	for i := 0; i < 1000; i++ {
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"content":"filler line %d"},"uuid":"filler-%d"}`, i, i))
	}
	lines = append(lines, `{"type":"user","message":{"content":"<command-message>til</command-message>\n<command-name>/til</command-name>\n<command-args>big file test</command-args>"},"uuid":"til-in-large-file"}`)
	lines = append(lines, `{"type":"assistant","uuid":"after-til"}`)
	lines = append(lines, `{"type":"last-prompt","lastPrompt":"/til big file test"}`)

	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)

	got := extractTilMessageUUID(path)
	if got != "til-in-large-file" {
		t.Errorf("extractTilMessageUUID() = %q, want %q", got, "til-in-large-file")
	}
}

func TestExtractTilMessageUUID_LargeFileFallback(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")

	// Large file with no /til command — should fall back to last UUID
	var lines []string
	for i := 0; i < 1000; i++ {
		lines = append(lines, `{"type":"assistant","message":{"content":"filler line"}}`)
	}
	lines = append(lines, `{"type":"user","uuid":"last-uuid-in-large-file"}`)

	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)

	got := extractTilMessageUUID(path)
	if got != "last-uuid-in-large-file" {
		t.Errorf("extractTilMessageUUID() = %q, want %q", got, "last-uuid-in-large-file")
	}
}

func TestExtractTilMessageUUID_NonexistentFile(t *testing.T) {
	got := extractTilMessageUUID("/nonexistent/path.jsonl")
	if got != "" {
		t.Errorf("extractTilMessageUUID() = %q, want empty for nonexistent file", got)
	}
}

func TestReadTailLines(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		maxLines  int
		wantCount int
		wantLast  string
	}{
		{
			"fewer lines than max",
			"line1\nline2\nline3",
			100,
			3,
			"line3",
		},
		{
			"exactly max lines",
			"line1\nline2\nline3",
			3,
			3,
			"line3",
		},
		{
			"more lines than max",
			"line1\nline2\nline3\nline4\nline5",
			3,
			3,
			"line5",
		},
		{
			"empty file",
			"",
			100,
			0,
			"",
		},
		{
			"trailing newline",
			"line1\nline2\n",
			100,
			2,
			"line2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "test.jsonl")
			os.WriteFile(path, []byte(tt.content), 0644)

			lines, err := readTailLines(path, tt.maxLines)
			if err != nil {
				t.Fatalf("readTailLines() error = %v", err)
			}
			if len(lines) != tt.wantCount {
				t.Errorf("readTailLines() returned %d lines, want %d", len(lines), tt.wantCount)
			}
			if tt.wantLast != "" && len(lines) > 0 && lines[len(lines)-1] != tt.wantLast {
				t.Errorf("last line = %q, want %q", lines[len(lines)-1], tt.wantLast)
			}
		})
	}
}

func TestReadTailLines_NonexistentFile(t *testing.T) {
	_, err := readTailLines("/nonexistent/path.jsonl", 100)
	if err == nil {
		t.Error("readTailLines() should return error for nonexistent file")
	}
}

func TestRunTil_Integration(t *testing.T) {
	// Set up test backend
	var receivedReq tilRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tils" && r.Method == "POST" {
			json.NewDecoder(r.Body).Decode(&receivedReq)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(tilResponse{
				ID:    42,
				Title: receivedReq.Title,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Create transcript with /til command followed by last-prompt (realistic scenario)
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "test-session-001.jsonl")
	transcript := strings.Join([]string{
		`{"type":"user","message":{"content":"Hello"},"uuid":"msg-001"}`,
		`{"type":"user","message":{"content":"<command-message>til</command-message>\n<command-name>/til</command-name>\n<command-args>test</command-args>"},"uuid":"msg-999"}`,
		`{"type":"assistant","uuid":"msg-after"}`,
		`{"type":"last-prompt","lastPrompt":"/til test"}`,
	}, "\n") + "\n"
	os.WriteFile(transcriptPath, []byte(transcript), 0644)

	// Verify UUID extraction finds the /til command line
	messageUUID := extractTilMessageUUID(transcriptPath)
	if messageUUID != "msg-999" {
		t.Fatalf("extractTilMessageUUID() = %q, want %q", messageUUID, "msg-999")
	}

	// Build and POST the TIL request
	cfg := &config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "test-key",
	}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	req := &tilRequest{
		Title:       "Test TIL",
		Summary:     "Test summary",
		SessionID:   "backend-sess-123",
		MessageUUID: messageUUID,
		Tags:        []string{},
	}

	var resp tilResponse
	if err := client.Post("/api/v1/tils", req, &resp); err != nil {
		t.Fatalf("POST failed: %v", err)
	}

	if resp.ID != 42 {
		t.Errorf("Response ID = %d, want %d", resp.ID, 42)
	}
	if receivedReq.Title != "Test TIL" {
		t.Errorf("Received title = %q, want %q", receivedReq.Title, "Test TIL")
	}
	if receivedReq.MessageUUID != "msg-999" {
		t.Errorf("Received message_uuid = %q, want %q", receivedReq.MessageUUID, "msg-999")
	}
	if receivedReq.SessionID != "backend-sess-123" {
		t.Errorf("Received session_id = %q, want %q", receivedReq.SessionID, "backend-sess-123")
	}
}
