// ABOUTME: Tests for the confab session download command.
// ABOUTME: Validates path construction, stdout streaming, --output-dir, and error handling.
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
)

func TestBuildSessionFilesPath(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{"simple uuid", "abc-123", "/api/v1/sessions/abc-123/files"},
		{"special characters", "id/with spaces", "/api/v1/sessions/id%2Fwith%20spaces/files"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSessionFilesPath(tt.id)
			if got != tt.want {
				t.Errorf("buildSessionFilesPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSessionFileDownloadPath(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		fileName string
		want     string
	}{
		{
			"transcript",
			"abc-123",
			"transcript.jsonl",
			"/api/v1/sessions/abc-123/files/download?file_name=transcript.jsonl",
		},
		{
			"agent file",
			"abc-123",
			"agent-xyz.jsonl",
			"/api/v1/sessions/abc-123/files/download?file_name=agent-xyz.jsonl",
		},
		{
			"special characters in id",
			"id/with spaces",
			"transcript.jsonl",
			"/api/v1/sessions/id%2Fwith%20spaces/files/download?file_name=transcript.jsonl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSessionFileDownloadPath(tt.id, tt.fileName)
			if got != tt.want {
				t.Errorf("buildSessionFileDownloadPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// newTestServer creates a server that handles both file list and download endpoints.
func newTestServer(files []sessionFile, fileContents map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/v1/sessions/test-uuid/files" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(sessionFilesResponse{Files: files})
			return
		}
		if path == "/api/v1/sessions/test-uuid/files/download" {
			fileName := r.URL.Query().Get("file_name")
			content, ok := fileContents[fileName]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error":"File not found"}`))
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(content))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Not found"}`))
	}))
}

func TestRunSessionDownload_DefaultMode(t *testing.T) {
	files := []sessionFile{
		{FileName: "transcript.jsonl", FileType: "transcript", LastSyncedLine: 100},
		{FileName: "agent-abc.jsonl", FileType: "agent", LastSyncedLine: 50},
	}
	fileContents := map[string]string{
		"transcript.jsonl": `{"type":"user","message":"hello"}` + "\n" + `{"type":"assistant","message":"hi"}` + "\n",
	}
	server := newTestServer(files, fileContents)
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Capture stdout by streaming to a buffer via GetRawToWriter
	var buf bytes.Buffer
	path := buildSessionFileDownloadPath("test-uuid", "transcript.jsonl")
	if err := client.GetRawToWriter(path, &buf); err != nil {
		t.Fatalf("GetRawToWriter() error = %v", err)
	}

	want := `{"type":"user","message":"hello"}` + "\n" + `{"type":"assistant","message":"hi"}` + "\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRunSessionDownload_OutputDir(t *testing.T) {
	files := []sessionFile{
		{FileName: "transcript.jsonl", FileType: "transcript", LastSyncedLine: 100},
		{FileName: "agent-abc.jsonl", FileType: "agent", LastSyncedLine: 50},
	}
	fileContents := map[string]string{
		"transcript.jsonl": `{"type":"user","message":"hello"}` + "\n",
		"agent-abc.jsonl":  `{"type":"tool","name":"bash"}` + "\n",
	}
	server := newTestServer(files, fileContents)
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	outputDir := t.TempDir()
	if err := downloadAllFiles(client, "test-uuid", outputDir, files); err != nil {
		t.Fatalf("downloadAllFiles() error = %v", err)
	}

	// Verify both files were written
	for fileName, wantContent := range fileContents {
		gotBytes, err := os.ReadFile(filepath.Join(outputDir, fileName))
		if err != nil {
			t.Fatalf("failed to read %s: %v", fileName, err)
		}
		if string(gotBytes) != wantContent {
			t.Errorf("%s: got %q, want %q", fileName, string(gotBytes), wantContent)
		}
	}
}

func TestRunSessionDownload_NotFound(t *testing.T) {
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

func TestRunSessionDownload_EmptyFileList(t *testing.T) {
	server := newTestServer([]sessionFile{}, nil)
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

func TestRunSessionDownload_NoTranscriptFile(t *testing.T) {
	// Only agent files, no transcript
	files := []sessionFile{
		{FileName: "agent-abc.jsonl", FileType: "agent", LastSyncedLine: 50},
	}
	server := newTestServer(files, nil)
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = downloadMainTranscript(client, "test-uuid", files)
	if err == nil {
		t.Fatal("expected error when no transcript file exists")
	}
	if err.Error() != "no transcript file found for session" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunSessionDownload_PartialFailure(t *testing.T) {
	files := []sessionFile{
		{FileName: "transcript.jsonl", FileType: "transcript", LastSyncedLine: 100},
		{FileName: "agent-abc.jsonl", FileType: "agent", LastSyncedLine: 50},
	}
	// Only serve the transcript, agent download will 404
	fileContents := map[string]string{
		"transcript.jsonl": `{"type":"user","message":"hello"}` + "\n",
	}
	server := newTestServer(files, fileContents)
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	outputDir := t.TempDir()
	err = downloadAllFiles(client, "test-uuid", outputDir, files)
	if err == nil {
		t.Fatal("expected error for partial failure")
	}

	// Verify the successful file was kept
	gotBytes, readErr := os.ReadFile(filepath.Join(outputDir, "transcript.jsonl"))
	if readErr != nil {
		t.Fatalf("successful file should be kept: %v", readErr)
	}
	if string(gotBytes) != fileContents["transcript.jsonl"] {
		t.Errorf("transcript content mismatch: got %q", string(gotBytes))
	}
}
