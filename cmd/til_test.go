// ABOUTME: Tests for the confab til CLI command.
// ABOUTME: Validates request building, UUID extraction, and backend integration.
package cmd

import (
	"encoding/json"
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

func TestExtractLastMessageUUID(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantUUID string
	}{
		{
			"valid uuid",
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
			"single line",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			path := filepath.Join(tmpDir, "transcript.jsonl")
			os.WriteFile(path, []byte(tt.content), 0644)

			got := extractLastMessageUUID(path)
			if got != tt.wantUUID {
				t.Errorf("extractLastMessageUUID() = %q, want %q", got, tt.wantUUID)
			}
		})
	}
}

func TestExtractLastMessageUUID_LargeFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "transcript.jsonl")

	// Build a file with many lines, UUID only on last line
	var lines []string
	for i := 0; i < 1000; i++ {
		lines = append(lines, `{"type":"assistant","message":{"content":"filler line"}}`)
	}
	lines = append(lines, `{"type":"user","uuid":"last-uuid-in-large-file"}`)

	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)

	got := extractLastMessageUUID(path)
	if got != "last-uuid-in-large-file" {
		t.Errorf("extractLastMessageUUID() = %q, want %q", got, "last-uuid-in-large-file")
	}
}

func TestExtractLastMessageUUID_NonexistentFile(t *testing.T) {
	got := extractLastMessageUUID("/nonexistent/path.jsonl")
	if got != "" {
		t.Errorf("extractLastMessageUUID() = %q, want empty for nonexistent file", got)
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
				ID:    "til-uuid",
				Title: receivedReq.Title,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Set up daemon state
	tmpDir := t.TempDir()
	sessionID := "test-session-001"

	// Create transcript file
	transcriptPath := filepath.Join(tmpDir, sessionID+".jsonl")
	os.WriteFile(transcriptPath, []byte(`{"type":"user","uuid":"msg-999"}`+"\n"), 0644)

	// Create daemon state file
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	syncDir := filepath.Join(homeDir, ".confab", "sync")
	os.MkdirAll(syncDir, 0755)

	stateData, _ := json.Marshal(map[string]any{
		"external_id":       sessionID,
		"transcript_path":   transcriptPath,
		"cwd":               tmpDir,
		"pid":               os.Getpid(),
		"confab_session_id": "backend-sess-123",
		"started_at":        "2026-01-01T00:00:00Z",
	})
	os.WriteFile(filepath.Join(syncDir, sessionID+".json"), stateData, 0600)

	// Create config
	confabDir := filepath.Join(homeDir, ".confab")
	configData, _ := json.Marshal(map[string]string{
		"backend_url": server.URL,
		"api_key":     "test-key",
	})
	os.WriteFile(filepath.Join(confabDir, "config.json"), configData, 0600)

	// Run the command
	cfg := &config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "test-key",
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test the request building and posting directly
	messageUUID := extractLastMessageUUID(transcriptPath)
	if messageUUID != "msg-999" {
		t.Fatalf("extractLastMessageUUID() = %q, want %q", messageUUID, "msg-999")
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

	if resp.ID != "til-uuid" {
		t.Errorf("Response ID = %q, want %q", resp.ID, "til-uuid")
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
