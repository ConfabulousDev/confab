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
	"strings"
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

// TestDownloadAllFiles_RejectsPathTraversal guards the path-traversal
// defense at session_download.go:140-145. A malicious backend returning
// file names like ".." or "/etc/passwd" must not write outside
// outputDir. The two layers of defense are filepath.Base (strips path
// components, so "../../etc/passwd" becomes "passwd" inside outputDir —
// benign) and the absDest/absOutputDir HasPrefix check (catches the
// edge cases like bare ".." that filepath.Base preserves).
func TestDownloadAllFiles_RejectsPathTraversal(t *testing.T) {
	// Every file is served with marker content; we then assert no file
	// containing the marker exists outside outputDir.
	const marker = "TRAVERSAL_MARKER_CONTENT"

	maliciousNames := []string{
		"..",
		"../",
		"../../etc/passwd",
		"/etc/passwd",
	}
	files := make([]sessionFile, 0, len(maliciousNames))
	fileContents := make(map[string]string)
	for _, name := range maliciousNames {
		files = append(files, sessionFile{FileName: name, FileType: "agent"})
		fileContents[name] = marker
	}
	server := newTestServer(files, fileContents)
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	outerDir := t.TempDir()
	outputDir := filepath.Join(outerDir, "session")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("mkdir outputDir: %v", err)
	}

	// Plant a sentinel above outputDir so we can prove nothing
	// overwrote it.
	sentinelPath := filepath.Join(outerDir, "passwd")
	if err := os.WriteFile(sentinelPath, []byte("SENTINEL"), 0600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// downloadAllFiles is expected to return an error for any path that
	// trips the HasPrefix check. filepath.Base may sanitize some inputs
	// down to benign basenames inside outputDir; those are not an
	// escape. The invariant we assert is: no file with the marker
	// content exists outside outputDir.
	_ = downloadAllFiles(client, "test-uuid", outputDir, files)

	if data, err := os.ReadFile(sentinelPath); err != nil {
		t.Errorf("sentinel disappeared: %v", err)
	} else if string(data) != "SENTINEL" {
		t.Errorf("sentinel was overwritten: %q", data)
	}

	// Walk outerDir; any file outside outputDir containing the marker
	// would be a confirmed escape.
	err = filepath.Walk(outerDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasPrefix(path, outputDir+string(filepath.Separator)) {
			return nil
		}
		if path == outputDir {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), marker) {
			t.Errorf("path traversal escaped to %q (content: %q)", path, data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
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
