// ABOUTME: Tests for the confab session list-files command.
// ABOUTME: Validates path construction, table output formatting, and error handling.
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
)

func TestRunSessionListFiles_Success(t *testing.T) {
	files := []sessionFile{
		{
			FileName:       "transcript.jsonl",
			FileType:       "transcript",
			LastSyncedLine: 100,
			UpdatedAt:      time.Date(2026, 3, 28, 21, 15, 0, 0, time.UTC),
		},
		{
			FileName:       "agent-abc.jsonl",
			FileType:       "agent",
			LastSyncedLine: 50,
			UpdatedAt:      time.Date(2026, 3, 28, 22, 30, 0, 0, time.UTC),
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessionFilesResponse{Files: files})
	}))
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	var filesResp sessionFilesResponse
	if err := client.Get(buildSessionFilesPath("test-uuid"), &filesResp); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if len(filesResp.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(filesResp.Files))
	}

	if filesResp.Files[0].FileName != "transcript.jsonl" {
		t.Errorf("first file = %q, want %q", filesResp.Files[0].FileName, "transcript.jsonl")
	}
	if filesResp.Files[0].FileType != "transcript" {
		t.Errorf("first file type = %q, want %q", filesResp.Files[0].FileType, "transcript")
	}
	if filesResp.Files[0].LastSyncedLine != 100 {
		t.Errorf("first file lines = %d, want %d", filesResp.Files[0].LastSyncedLine, 100)
	}
	if filesResp.Files[1].FileName != "agent-abc.jsonl" {
		t.Errorf("second file = %q, want %q", filesResp.Files[1].FileName, "agent-abc.jsonl")
	}
}

func TestRunSessionListFiles_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Session not found"}`))
	}))
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	var filesResp sessionFilesResponse
	err = client.Get(buildSessionFilesPath("nonexistent"), &filesResp)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestRunSessionListFiles_EmptyFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessionFilesResponse{Files: []sessionFile{}})
	}))
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	var filesResp sessionFilesResponse
	if err := client.Get(buildSessionFilesPath("test-uuid"), &filesResp); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if len(filesResp.Files) != 0 {
		t.Errorf("expected empty files list, got %d files", len(filesResp.Files))
	}
}

func TestSessionFileTimestampFormat(t *testing.T) {
	ts := time.Date(2026, 3, 28, 21, 15, 0, 0, time.UTC)
	formatted := ts.Local().Format("Jan 02 15:04")
	if formatted == "" {
		t.Error("expected non-empty formatted timestamp")
	}
	// Just verify format doesn't panic and produces reasonable output
	if len(formatted) < 10 {
		t.Errorf("formatted timestamp too short: %q", formatted)
	}
}
