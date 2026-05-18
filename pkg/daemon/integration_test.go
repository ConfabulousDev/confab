package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	stdsync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/sync"
	"github.com/klauspost/compress/zstd"
)

// zstd decoder for decompressing request bodies in tests
var zstdDecoder, _ = zstd.NewReader(nil)

// readRequestBody reads and decompresses the request body if needed
func readRequestBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Decompress if zstd encoded
	if r.Header.Get("Content-Encoding") == "zstd" {
		return zstdDecoder.DecodeAll(body, nil)
	}

	return body, nil
}

// mockBackend tracks requests and provides configurable responses
type mockBackend struct {
	t              *testing.T
	mu             stdsync.Mutex // protects initRequests and chunkRequests
	initRequests   []sync.InitRequest
	chunkRequests  []sync.ChunkRequest
	initResponse   *sync.InitResponse
	initError      bool
	chunkError     bool
	chunkStatus    int // if non-zero, /sync/chunk returns this status code with empty body
	requestCount   int32
	failUntilCount int32 // fail requests until this count is reached
}

// getInitRequests returns a snapshot of the init requests received so far.
func (m *mockBackend) getInitRequests() []sync.InitRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sync.InitRequest(nil), m.initRequests...)
}

// getChunkRequests returns a snapshot of the chunk requests received so far.
func (m *mockBackend) getChunkRequests() []sync.ChunkRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sync.ChunkRequest(nil), m.chunkRequests...)
}

func newMockBackend(t *testing.T) *mockBackend {
	return &mockBackend{
		t: t,
		initResponse: &sync.InitResponse{
			SessionID: "test-session-id",
			Files:     make(map[string]sync.FileState),
		},
	}
}

func (m *mockBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	count := atomic.AddInt32(&m.requestCount, 1)

	// Simulate failures until failUntilCount
	if m.failUntilCount > 0 && count <= m.failUntilCount {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Service Unavailable"))
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Read and decompress request body
	body, err := readRequestBody(r)
	if err != nil {
		m.t.Errorf("Failed to read request body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch r.URL.Path {
	case "/api/v1/sync/init":
		if m.initError {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "init failed"})
			return
		}

		var req sync.InitRequest
		if err := json.Unmarshal(body, &req); err != nil {
			m.t.Errorf("Failed to decode init request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.initRequests = append(m.initRequests, req)
		m.mu.Unlock()
		json.NewEncoder(w).Encode(m.initResponse)

	case "/api/v1/sync/chunk":
		if m.chunkStatus != 0 {
			// Per-endpoint status override (used by S9 to force 404s
			// on every chunk upload). Still record the attempt so the
			// test can count retries.
			var req sync.ChunkRequest
			_ = json.Unmarshal(body, &req)
			m.mu.Lock()
			m.chunkRequests = append(m.chunkRequests, req)
			m.mu.Unlock()
			w.WriteHeader(m.chunkStatus)
			return
		}
		if m.chunkError {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "chunk failed"})
			return
		}

		var req sync.ChunkRequest
		if err := json.Unmarshal(body, &req); err != nil {
			m.t.Errorf("Failed to decode chunk request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.chunkRequests = append(m.chunkRequests, req)
		m.mu.Unlock()

		// Return last synced line as first + len(lines) - 1
		lastLine := req.FirstLine + len(req.Lines) - 1
		json.NewEncoder(w).Encode(sync.ChunkResponse{
			LastSyncedLine: lastLine,
		})

	default:
		m.t.Errorf("Unexpected request to %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// setupTestEnv creates a temporary environment for daemon testing
func setupTestEnv(t *testing.T, serverURL string) (tmpDir string, transcriptPath string) {
	tmpDir = t.TempDir()

	// Set up config
	confabDir := filepath.Join(tmpDir, ".confab")
	os.MkdirAll(confabDir, 0755)
	configPath := filepath.Join(confabDir, "config.json")
	configJSON := fmt.Sprintf(`{"backend_url":"%s","api_key":"test-api-key-12345678"}`, serverURL)
	os.WriteFile(configPath, []byte(configJSON), 0600)
	t.Setenv("CONFAB_CONFIG_PATH", configPath)
	t.Setenv("HOME", tmpDir)

	// Create transcript directory
	transcriptDir := filepath.Join(tmpDir, "sessions")
	os.MkdirAll(transcriptDir, 0755)
	transcriptPath = filepath.Join(transcriptDir, "transcript.jsonl")

	return tmpDir, transcriptPath
}

// TestDaemonSyncCycle tests a full init + sync cycle with mock server
func TestDaemonSyncCycle(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with some content
	transcriptContent := `{"type":"system","message":"hello"}
{"type":"user","message":"world"}
{"type":"assistant","message":"response"}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// Create and run daemon
	d := New(Config{
		ExternalID:     "test-external-id",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond, // Fast for testing
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run daemon in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for at least one sync cycle
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Wait for daemon to exit
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Daemon did not exit")
	}

	// Verify init was called
	initReqs := mock.getInitRequests()
	if len(initReqs) == 0 {
		t.Fatal("Expected init request, got none")
	}
	initReq := initReqs[0]
	if initReq.ExternalID != "test-external-id" {
		t.Errorf("Expected external_id 'test-external-id', got %q", initReq.ExternalID)
	}
	if initReq.TranscriptPath != transcriptPath {
		t.Errorf("Expected transcript_path %q, got %q", transcriptPath, initReq.TranscriptPath)
	}

	// Verify chunk was uploaded
	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) == 0 {
		t.Fatal("Expected chunk request, got none")
	}
	chunkReq := chunkReqs[0]
	if chunkReq.SessionID != "test-session-id" {
		t.Errorf("Expected session_id 'test-session-id', got %q", chunkReq.SessionID)
	}
	if chunkReq.FileType != "transcript" {
		t.Errorf("Expected file_type 'transcript', got %q", chunkReq.FileType)
	}
	if len(chunkReq.Lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(chunkReq.Lines))
	}
	if chunkReq.FirstLine != 1 {
		t.Errorf("Expected first_line 1, got %d", chunkReq.FirstLine)
	}
}

// TestDaemonRetryOnBackendError tests that daemon retries when backend is unavailable
func TestDaemonRetryOnBackendError(t *testing.T) {
	mock := newMockBackend(t)
	mock.failUntilCount = 2 // Fail first 2 requests
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript
	os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "retry-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   100 * time.Millisecond, // Needs to be long enough to trigger retries
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for retries - need enough time for multiple sync intervals
	time.Sleep(800 * time.Millisecond)
	cancel()

	<-errCh

	// Should have had multiple attempts to init endpoint
	totalRequests := atomic.LoadInt32(&mock.requestCount)
	if totalRequests < 3 {
		t.Errorf("Expected at least 3 requests (2 failures + 1 success), got %d", totalRequests)
	}

	// Eventually should have succeeded with init
	if len(mock.getInitRequests()) == 0 {
		t.Error("Expected at least one successful init request after retries")
	}

	// Stronger assertion: after the backend recovered, the daemon must
	// have actually uploaded the transcript chunk. Without this, a
	// retry loop that gives up after init never proves the recovery
	// path delivers data.
	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) == 0 {
		t.Fatal("Expected at least one chunk upload after backend recovered; daemon retried init but never delivered data")
	}
	got := chunkReqs[0]
	if got.FileType != "transcript" {
		t.Errorf("first chunk file_type = %q, want %q", got.FileType, "transcript")
	}
	if len(got.Lines) != 1 || got.Lines[0] != `{"type":"system"}` {
		t.Errorf("first chunk lines = %v, want one transcript line matching the seeded content", got.Lines)
	}
	if got.FirstLine != 1 {
		t.Errorf("first chunk first_line = %d, want 1 (full upload from line 1)", got.FirstLine)
	}
}

// TestDaemonAgentDiscovery tests that daemon discovers and uploads agent files
func TestDaemonAgentDiscovery(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript that references an agent
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"abc12345","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// Create the agent file in subagents directory
	subagentsDir := filepath.Join(filepath.Dir(transcriptPath), "transcript", "subagents")
	os.MkdirAll(subagentsDir, 0755)
	agentPath := filepath.Join(subagentsDir, "agent-abc12345.jsonl")
	agentContent := `{"type":"agent","message":"agent line 1"}
{"type":"agent","message":"agent line 2"}
`
	os.WriteFile(agentPath, []byte(agentContent), 0644)

	d := New(Config{
		ExternalID:     "agent-discovery-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for sync
	time.Sleep(300 * time.Millisecond)
	cancel()

	<-errCh

	// Verify both transcript and agent were uploaded
	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) < 2 {
		t.Fatalf("Expected at least 2 chunk requests (transcript + agent), got %d", len(chunkReqs))
	}

	// Find transcript and agent uploads
	var transcriptChunk, agentChunk *sync.ChunkRequest
	for i := range chunkReqs {
		req := &chunkReqs[i]
		if req.FileType == "transcript" {
			transcriptChunk = req
		} else if req.FileType == "agent" {
			agentChunk = req
		}
	}

	if transcriptChunk == nil {
		t.Error("Expected transcript chunk upload")
	}
	if agentChunk == nil {
		t.Error("Expected agent chunk upload")
	} else {
		if agentChunk.FileName != "agent-abc12345.jsonl" {
			t.Errorf("Expected agent file name 'agent-abc12345.jsonl', got %q", agentChunk.FileName)
		}
		if len(agentChunk.Lines) != 2 {
			t.Errorf("Expected 2 agent lines, got %d", len(agentChunk.Lines))
		}
	}
}

// TestDaemonIncrementalSync tests that daemon only uploads new lines
func TestDaemonIncrementalSync(t *testing.T) {
	mock := newMockBackend(t)
	// Simulate backend already has first 2 lines
	mock.initResponse.Files = map[string]sync.FileState{
		"transcript.jsonl": {LastSyncedLine: 2},
	}
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with 4 lines (backend has 2, we should upload 2)
	transcriptContent := `{"type":"system","line":1}
{"type":"user","line":2}
{"type":"assistant","line":3}
{"type":"user","line":4}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	d := New(Config{
		ExternalID:     "incremental-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	<-errCh

	// Verify only new lines were uploaded
	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) == 0 {
		t.Fatal("Expected chunk request, got none")
	}

	chunkReq := chunkReqs[0]
	if chunkReq.FirstLine != 3 {
		t.Errorf("Expected first_line 3 (after synced line 2), got %d", chunkReq.FirstLine)
	}
	if len(chunkReq.Lines) != 2 {
		t.Errorf("Expected 2 new lines, got %d", len(chunkReq.Lines))
	}
}

// TestDaemonMultipleSyncCycles tests that daemon continues syncing new content
func TestDaemonMultipleSyncCycles(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Start with initial content
	os.WriteFile(transcriptPath, []byte(`{"type":"system","line":1}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "multi-cycle-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   100 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for first sync
	time.Sleep(150 * time.Millisecond)

	// Append more content
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"user","line":2}` + "\n")
	f.WriteString(`{"type":"assistant","line":3}` + "\n")
	f.Close()

	// Wait for second sync
	time.Sleep(200 * time.Millisecond)
	cancel()

	<-errCh

	// Should have multiple chunk uploads
	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) < 2 {
		t.Errorf("Expected at least 2 chunk uploads (initial + appended), got %d", len(chunkReqs))
	}

	// First chunk should be line 1
	if chunkReqs[0].FirstLine != 1 {
		t.Errorf("First chunk should start at line 1, got %d", chunkReqs[0].FirstLine)
	}

	// Second chunk should be lines 2-3
	if len(chunkReqs) >= 2 {
		secondChunk := chunkReqs[1]
		if secondChunk.FirstLine != 2 {
			t.Errorf("Second chunk should start at line 2, got %d", secondChunk.FirstLine)
		}
	}
}

// TestDaemonTranscriptAppearsLate tests that daemon waits for transcript then syncs
func TestDaemonTranscriptAppearsLate(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// DON'T create transcript yet - it will appear later

	d := New(Config{
		ExternalID:     "late-transcript-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait a bit, then create transcript
	time.Sleep(100 * time.Millisecond)

	// Transcript should not exist yet, no init should have happened
	if len(mock.getInitRequests()) > 0 {
		t.Error("Init should not happen before transcript exists")
	}

	// Now create the transcript
	os.MkdirAll(filepath.Dir(transcriptPath), 0755)
	os.WriteFile(transcriptPath, []byte(`{"type":"system","message":"late"}`+"\n"), 0644)

	// Wait for daemon to notice and sync (poll interval is 2s, so wait longer)
	time.Sleep(2500 * time.Millisecond)
	cancel()

	<-errCh

	// Now init should have happened
	if len(mock.getInitRequests()) == 0 {
		t.Error("Expected init request after transcript appeared")
	}
	if len(mock.getChunkRequests()) == 0 {
		t.Error("Expected chunk upload after transcript appeared")
	}
}

// TestDaemonAgentFileNotExistYet tests that missing agent files are skipped and picked up later
func TestDaemonAgentFileNotExistYet(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create subagents directory (but DON'T create the agent file)
	subagentsDir := filepath.Join(filepath.Dir(transcriptPath), "transcript", "subagents")
	os.MkdirAll(subagentsDir, 0755)

	// Create transcript that references an agent, but DON'T create the agent file
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"def67890","result":"pending"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	d := New(Config{
		ExternalID:     "agent-not-exist-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for first sync cycle
	time.Sleep(150 * time.Millisecond)

	// Should have synced transcript but no agent (file doesn't exist)
	transcriptUploads := 0
	agentUploads := 0
	for _, req := range mock.getChunkRequests() {
		if req.FileType == "transcript" {
			transcriptUploads++
		} else if req.FileType == "agent" {
			agentUploads++
		}
	}
	if transcriptUploads == 0 {
		t.Error("Expected transcript upload")
	}
	if agentUploads > 0 {
		t.Error("Should not upload agent that doesn't exist yet")
	}

	// Now create the agent file in subagents dir
	agentPath := filepath.Join(subagentsDir, "agent-def67890.jsonl")
	os.WriteFile(agentPath, []byte(`{"type":"agent","message":"now exists"}`+"\n"), 0644)

	// Wait for next sync cycle
	time.Sleep(150 * time.Millisecond)
	cancel()

	<-errCh

	// Now should have agent upload
	agentUploads = 0
	for _, req := range mock.getChunkRequests() {
		if req.FileType == "agent" {
			agentUploads++
		}
	}
	if agentUploads == 0 {
		t.Error("Expected agent upload after file appeared")
	}
}

// TestDaemonBackendHasMoreLines tests resuming when backend has more lines than expected
func TestDaemonBackendHasMoreLines(t *testing.T) {
	mock := newMockBackend(t)
	// Backend says it already has 5 lines
	mock.initResponse.Files = map[string]sync.FileState{
		"transcript.jsonl": {LastSyncedLine: 5},
	}
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with 7 lines (backend has 5, we upload 2)
	var lines []string
	for i := 1; i <= 7; i++ {
		lines = append(lines, fmt.Sprintf(`{"type":"msg","line":%d}`, i))
	}
	os.WriteFile(transcriptPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	d := New(Config{
		ExternalID:     "backend-ahead-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	<-errCh

	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) == 0 {
		t.Fatal("Expected chunk request")
	}

	// Should start from line 6 (after backend's line 5)
	chunkReq := chunkReqs[0]
	if chunkReq.FirstLine != 6 {
		t.Errorf("Expected first_line 6, got %d", chunkReq.FirstLine)
	}
	if len(chunkReq.Lines) != 2 {
		t.Errorf("Expected 2 lines (6 and 7), got %d", len(chunkReq.Lines))
	}
}

// TestDaemonEmptyTranscript tests handling of empty transcript file
func TestDaemonEmptyTranscript(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create empty transcript
	os.WriteFile(transcriptPath, []byte(""), 0644)

	d := New(Config{
		ExternalID:     "empty-transcript-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	<-errCh

	// Init should happen
	if len(mock.getInitRequests()) == 0 {
		t.Error("Expected init request even for empty transcript")
	}

	// No chunks should be uploaded (nothing to sync)
	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) > 0 {
		t.Errorf("Expected no chunk uploads for empty transcript, got %d", len(chunkReqs))
	}
}

// TestDaemonShutdownFinalSync tests that final sync happens on shutdown
func TestDaemonShutdownFinalSync(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript
	os.WriteFile(transcriptPath, []byte(`{"type":"system","line":1}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "shutdown-sync-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   10 * time.Second, // Very long - won't trigger during test
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for initial sync (happens immediately on start)
	time.Sleep(100 * time.Millisecond)

	initialChunks := len(mock.getChunkRequests())

	// Append content that won't be synced by interval (10s is too long)
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"user","line":2}` + "\n")
	f.Close()

	// Give a moment for file to be written
	time.Sleep(50 * time.Millisecond)

	// Cancel - should trigger final sync
	cancel()

	<-errCh

	// Should have more chunks after shutdown (final sync picked up line 2)
	finalChunks := len(mock.getChunkRequests())
	if finalChunks <= initialChunks {
		t.Errorf("Expected final sync to upload new content, had %d chunks before, %d after",
			initialChunks, finalChunks)
	}
}

// TestDaemonMultipleAgentFiles tests discovery and sync of multiple agent files
func TestDaemonMultipleAgentFiles(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create subagents directory
	subagentsDir := filepath.Join(filepath.Dir(transcriptPath), "transcript", "subagents")
	os.MkdirAll(subagentsDir, 0755)

	// Create transcript referencing multiple agents
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"aaaaaaaa","result":"done"}}
{"type":"user","toolUseResult":{"agentId":"bbbbbbbb","result":"done"}}
{"type":"user","toolUseResult":{"agentId":"cccccccc","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// Create all three agent files in subagents directory
	for _, id := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		agentPath := filepath.Join(subagentsDir, fmt.Sprintf("agent-%s.jsonl", id))
		os.WriteFile(agentPath, []byte(fmt.Sprintf(`{"agent":"%s","line":1}`+"\n", id)), 0644)
	}

	d := New(Config{
		ExternalID:     "multi-agent-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	<-errCh

	// Count uploads by type
	transcriptUploads := 0
	agentFiles := make(map[string]bool)
	for _, req := range mock.getChunkRequests() {
		if req.FileType == "transcript" {
			transcriptUploads++
		} else if req.FileType == "agent" {
			agentFiles[req.FileName] = true
		}
	}

	if transcriptUploads == 0 {
		t.Error("Expected transcript upload")
	}
	if len(agentFiles) != 3 {
		t.Errorf("Expected 3 different agent files uploaded, got %d: %v", len(agentFiles), agentFiles)
	}
	for _, id := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		expectedName := fmt.Sprintf("agent-%s.jsonl", id)
		if !agentFiles[expectedName] {
			t.Errorf("Expected agent file %s to be uploaded", expectedName)
		}
	}
}

// TestDaemonAgentAppearsMidSession tests agent discovered after initial sync
func TestDaemonAgentAppearsMidSession(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Start with transcript that has NO agent references
	os.WriteFile(transcriptPath, []byte(`{"type":"system","message":"start"}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "mid-session-agent-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   100 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for initial sync
	time.Sleep(150 * time.Millisecond)

	// Verify no agent uploads yet
	agentUploadsBefore := 0
	for _, req := range mock.getChunkRequests() {
		if req.FileType == "agent" {
			agentUploadsBefore++
		}
	}
	if agentUploadsBefore > 0 {
		t.Error("Should have no agent uploads before agent is referenced")
	}

	// Now append agent reference to transcript AND create agent file
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"user","toolUseResult":{"agentId":"12345678","result":"done"}}` + "\n")
	f.Close()

	subagentsDir := filepath.Join(filepath.Dir(transcriptPath), "transcript", "subagents")
	os.MkdirAll(subagentsDir, 0755)
	agentPath := filepath.Join(subagentsDir, "agent-12345678.jsonl")
	os.WriteFile(agentPath, []byte(`{"type":"agent","message":"mid-session agent"}`+"\n"), 0644)

	// Wait for sync to pick up the new agent
	time.Sleep(250 * time.Millisecond)
	cancel()

	<-errCh

	// Now should have agent upload
	agentUploadsAfter := 0
	for _, req := range mock.getChunkRequests() {
		if req.FileType == "agent" && req.FileName == "agent-12345678.jsonl" {
			agentUploadsAfter++
		}
	}
	if agentUploadsAfter == 0 {
		t.Error("Expected agent upload after agent appeared mid-session")
	}
}

// TestDaemonConcurrentStartup tests that a second daemon for the same session
// detects the first is running and exits gracefully (or the first continues if second starts).
// The key behavior: at least one daemon should successfully sync, no data corruption.
func TestDaemonConcurrentStartup(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript
	os.WriteFile(transcriptPath, []byte(`{"type":"system","message":"concurrent test"}`+"\n"), 0644)

	// Start first daemon
	d1 := New(Config{
		ExternalID:     "concurrent-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx1, cancel1 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel1()

	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- d1.Run(ctx1)
	}()

	// Give first daemon time to start and save state
	time.Sleep(100 * time.Millisecond)

	// Start second daemon with same external ID
	d2 := New(Config{
		ExternalID:     "concurrent-test", // Same ID!
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()

	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- d2.Run(ctx2)
	}()

	// Wait for both to run for a bit
	time.Sleep(300 * time.Millisecond)

	// Cancel both
	cancel1()
	cancel2()

	// Wait for both to exit
	<-errCh1
	<-errCh2

	// Key assertion: at least one successful init and chunk upload happened
	// (we don't care which daemon "won", just that syncing worked)
	initReqs := mock.getInitRequests()
	chunkReqs := mock.getChunkRequests()
	if len(initReqs) == 0 {
		t.Error("Expected at least one init request from concurrent daemons")
	}
	if len(chunkReqs) == 0 {
		t.Error("Expected at least one chunk upload from concurrent daemons")
	}

	// Verify no duplicate uploads of the same content (idempotency)
	// Both daemons might upload, but the backend should handle dedup
	t.Logf("Concurrent test: %d init requests, %d chunk requests",
		len(initReqs), len(chunkReqs))
}

// TestDaemonFileTruncation tests that daemon handles file truncation gracefully.
// If a transcript file is truncated mid-session, daemon should:
// 1. Not crash
// 2. Continue running
// 3. Sync whatever content is available
func TestDaemonFileTruncation(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with multiple lines
	initialContent := `{"type":"system","line":1}
{"type":"user","line":2}
{"type":"assistant","line":3}
{"type":"user","line":4}
{"type":"assistant","line":5}
`
	os.WriteFile(transcriptPath, []byte(initialContent), 0644)

	d := New(Config{
		ExternalID:     "truncation-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   100 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for initial sync
	time.Sleep(200 * time.Millisecond)

	// Verify initial content was synced
	initialChunks := len(mock.getChunkRequests())
	if initialChunks == 0 {
		t.Fatal("Expected initial chunk upload")
	}

	// Now truncate the file to just 2 lines (simulating corruption or reset)
	truncatedContent := `{"type":"system","line":1}
{"type":"user","line":2}
`
	os.WriteFile(transcriptPath, []byte(truncatedContent), 0644)

	// Wait for next sync cycle
	time.Sleep(200 * time.Millisecond)

	// Daemon should still be running (not crashed)
	select {
	case err := <-errCh:
		t.Fatalf("Daemon crashed after truncation: %v", err)
	default:
		// Good - daemon still running
	}

	// Now append new content after truncation
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"assistant","line":3,"new":true}` + "\n")
	f.Close()

	// Wait for sync
	time.Sleep(200 * time.Millisecond)

	cancel()
	<-errCh

	// Daemon should have continued running and completed gracefully
	t.Logf("Truncation test: daemon handled truncation, total chunks=%d", len(mock.getChunkRequests()))
}

// TestDaemonHTTPErrors tests that daemon handles various HTTP errors gracefully.
// When HTTP requests fail (timeout, connection reset, server errors), daemon should:
// 1. Not crash
// 2. Log the error
// 3. Continue running and retry on next cycle
func TestDaemonHTTPErrors(t *testing.T) {
	var requestCount int32

	// Create a server that returns various errors then recovers
	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)

		// Simulate various error conditions
		switch count {
		case 1:
			// Connection reset / abrupt close
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
				return
			}
			// Fallback if hijacking not supported
			w.WriteHeader(http.StatusInternalServerError)
		case 2:
			// Rate limited
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Rate limited"))
		case 3:
			// Server error
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal error"))
		default:
			// After errors, succeed (simulating recovery)
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/api/v1/sync/init" {
				json.NewEncoder(w).Encode(sync.InitResponse{
					SessionID: "recovered-session",
					Files:     make(map[string]sync.FileState),
				})
			} else if r.URL.Path == "/api/v1/sync/chunk" {
				var req sync.ChunkRequest
				json.NewDecoder(r.Body).Decode(&req)
				lastLine := req.FirstLine + len(req.Lines) - 1
				json.NewEncoder(w).Encode(sync.ChunkResponse{
					LastSyncedLine: lastLine,
				})
			}
		}
	}))
	defer errorServer.Close()

	tmpDir, transcriptPath := setupTestEnv(t, errorServer.URL)

	// Create transcript
	os.WriteFile(transcriptPath, []byte(`{"type":"system","message":"error test"}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "http-error-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   100 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	startTime := time.Now()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for daemon to experience failures and recover
	time.Sleep(600 * time.Millisecond)

	// Daemon should still be running despite errors
	select {
	case err := <-errCh:
		t.Fatalf("Daemon crashed on HTTP error: %v", err)
	default:
		// Good - daemon still running
	}

	// Wait for more cycles to allow recovery
	time.Sleep(1000 * time.Millisecond)
	cancel()
	<-errCh

	elapsed := time.Since(startTime)
	finalCount := atomic.LoadInt32(&requestCount)

	// Daemon should have:
	// 1. Survived all error types
	// 2. Eventually recovered and made successful requests
	if finalCount < 4 {
		t.Errorf("Expected at least 4 requests (3 errors + recovery), got %d", finalCount)
	}

	t.Logf("HTTP error test: daemon survived %.1fs, %d total requests (first 3 had errors)",
		elapsed.Seconds(), finalCount)
}

// TestDaemonLargeFile tests that daemon can handle large transcript files (~100MB).
// This tests memory efficiency and streaming behavior.
func TestDaemonLargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large file test in short mode")
	}

	var totalLinesReceived int32
	var totalBytesReceived int64 // tracks compressed bytes received

	// Custom server that tracks received data
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Read raw body first to track compressed size
		rawBody, _ := io.ReadAll(r.Body)
		atomic.AddInt64(&totalBytesReceived, int64(len(rawBody)))

		// Decompress if needed
		var body []byte
		if r.Header.Get("Content-Encoding") == "zstd" {
			body, _ = zstdDecoder.DecodeAll(rawBody, nil)
		} else {
			body = rawBody
		}

		switch r.URL.Path {
		case "/api/v1/sync/init":
			json.NewEncoder(w).Encode(sync.InitResponse{
				SessionID: "large-file-session",
				Files:     make(map[string]sync.FileState),
			})

		case "/api/v1/sync/chunk":
			var req sync.ChunkRequest
			if json.Unmarshal(body, &req) == nil {
				atomic.AddInt32(&totalLinesReceived, int32(len(req.Lines)))
				lastLine := req.FirstLine + len(req.Lines) - 1
				json.NewEncoder(w).Encode(sync.ChunkResponse{
					LastSyncedLine: lastLine,
				})
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create a large transcript file (~1MB)
	// Each line is ~1KB of JSON, 1K lines = ~1MB
	f, err := os.Create(transcriptPath)
	if err != nil {
		t.Fatalf("Failed to create transcript: %v", err)
	}

	numLines := 1000
	padding := strings.Repeat("x", 900) // ~900 bytes padding per line
	for i := 0; i < numLines; i++ {
		line := fmt.Sprintf(`{"type":"msg","line":%d,"padding":"%s"}`, i+1, padding)
		f.WriteString(line + "\n")
	}
	f.Close()

	// Verify file size
	info, _ := os.Stat(transcriptPath)
	fileSizeKB := float64(info.Size()) / 1024
	t.Logf("Large file test: created %d lines, %.2f KB", numLines, fileSizeKB)

	d := New(Config{
		ExternalID:     "large-file-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startTime := time.Now()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for sync - large file might take a while
	// Poll until we've received all lines or timeout
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&totalLinesReceived) >= int32(numLines) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	cancel()
	<-errCh

	elapsed := time.Since(startTime)

	// Verify all lines were uploaded
	received := atomic.LoadInt32(&totalLinesReceived)
	if received < int32(numLines) {
		t.Errorf("Expected %d lines uploaded, got %d", numLines, received)
	}

	bytesReceived := atomic.LoadInt64(&totalBytesReceived)
	bytesReceivedKB := float64(bytesReceived) / 1024

	t.Logf("Large file test: uploaded %d lines, %.2f KB compressed in %.1fs",
		received, bytesReceivedKB, elapsed.Seconds())
}

// TestDaemonChunkSizeLimit tests that files larger than DefaultMaxChunkBytes (14MB)
// are correctly split into multiple chunks.
func TestDaemonChunkSizeLimit(t *testing.T) {
	var chunkCount int32
	var totalLinesReceived int32
	var chunkSizes []int // Track size of each chunk in lines

	var mu stdsync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			json.NewEncoder(w).Encode(sync.InitResponse{
				SessionID: "chunk-limit-session",
				Files:     make(map[string]sync.FileState),
			})

		case "/api/v1/sync/chunk":
			var req sync.ChunkRequest
			if json.Unmarshal(body, &req) == nil {
				atomic.AddInt32(&chunkCount, 1)
				atomic.AddInt32(&totalLinesReceived, int32(len(req.Lines)))

				mu.Lock()
				chunkSizes = append(chunkSizes, len(req.Lines))
				mu.Unlock()

				lastLine := req.FirstLine + len(req.Lines) - 1
				json.NewEncoder(w).Encode(sync.ChunkResponse{
					LastSyncedLine: lastLine,
				})
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create a file larger than DefaultMaxChunkBytes (14MB)
	// Each line is ~100KB, so 200 lines = ~20MB (will require 2+ chunks)
	f, err := os.Create(transcriptPath)
	if err != nil {
		t.Fatalf("Failed to create transcript: %v", err)
	}

	numLines := 200
	// ~100KB per line (100,000 chars of padding)
	padding := strings.Repeat("x", 100000)
	for i := 0; i < numLines; i++ {
		line := fmt.Sprintf(`{"type":"msg","line":%d,"padding":"%s"}`, i+1, padding)
		f.WriteString(line + "\n")
	}
	f.Close()

	// Verify file size is > 14MB
	info, _ := os.Stat(transcriptPath)
	fileSizeMB := float64(info.Size()) / (1024 * 1024)
	t.Logf("Chunk limit test: created %d lines, %.2f MB", numLines, fileSizeMB)

	if fileSizeMB < 14 {
		t.Fatalf("Test file should be > 14MB to test chunking, got %.2f MB", fileSizeMB)
	}

	d := New(Config{
		ExternalID:     "chunk-limit-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for sync to complete
	deadline := time.Now().Add(55 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&totalLinesReceived) >= int32(numLines) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	cancel()
	<-errCh

	// Verify results
	received := atomic.LoadInt32(&totalLinesReceived)
	chunks := atomic.LoadInt32(&chunkCount)

	t.Logf("Chunk limit test: received %d lines in %d chunks", received, chunks)

	mu.Lock()
	t.Logf("Chunk sizes (lines): %v", chunkSizes)
	mu.Unlock()

	// Must have received all lines
	if received != int32(numLines) {
		t.Errorf("Expected %d lines, got %d", numLines, received)
	}

	// Must have used multiple chunks (file is ~20MB, limit is 14MB)
	if chunks < 2 {
		t.Errorf("Expected at least 2 chunks for %.2f MB file, got %d", fileSizeMB, chunks)
	}
}

// TestDaemonLineTooLarge tests that the daemon handles lines exceeding DefaultMaxChunkBytes.
// When a single line is too large to fit in a chunk, the sync should fail with an error
// and continue retrying (without crashing).
func TestDaemonLineTooLarge(t *testing.T) {
	var chunkCount int32
	var linesReceived int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			json.NewEncoder(w).Encode(sync.InitResponse{
				SessionID: "line-too-large-session",
				Files:     make(map[string]sync.FileState),
			})

		case "/api/v1/sync/chunk":
			var req sync.ChunkRequest
			if json.Unmarshal(body, &req) == nil {
				atomic.AddInt32(&chunkCount, 1)
				atomic.AddInt32(&linesReceived, int32(len(req.Lines)))
				lastLine := req.FirstLine + len(req.Lines) - 1
				json.NewEncoder(w).Encode(sync.ChunkResponse{
					LastSyncedLine: lastLine,
				})
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create a file with:
	// - Line 1: normal size (should sync successfully)
	// - Line 2: larger than 14MB (should fail)
	// - Line 3: normal size (won't be reached due to line 2 failure)
	f, err := os.Create(transcriptPath)
	if err != nil {
		t.Fatalf("Failed to create transcript: %v", err)
	}

	// Line 1: small line
	f.WriteString(`{"type":"msg","line":1}` + "\n")

	// Line 2: 15MB line (exceeds 14MB limit)
	hugePadding := strings.Repeat("x", 15*1024*1024)
	f.WriteString(fmt.Sprintf(`{"type":"msg","line":2,"padding":"%s"}`, hugePadding) + "\n")

	// Line 3: small line (won't be synced)
	f.WriteString(`{"type":"msg","line":3}` + "\n")

	f.Close()

	info, _ := os.Stat(transcriptPath)
	fileSizeMB := float64(info.Size()) / (1024 * 1024)
	t.Logf("Line too large test: created file with %.2f MB", fileSizeMB)

	d := New(Config{
		ExternalID:     "line-too-large-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   100 * time.Millisecond,
	})

	// Run for a short time - should sync line 1, then fail on line 2
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for daemon to attempt syncing
	time.Sleep(1500 * time.Millisecond)

	cancel()
	<-errCh

	// Verify results
	chunks := atomic.LoadInt32(&chunkCount)
	received := atomic.LoadInt32(&linesReceived)

	t.Logf("Line too large test: received %d lines in %d chunks", received, chunks)

	// Should have synced exactly 1 line (line 1), then failed on line 2
	if received != 1 {
		t.Errorf("Expected 1 line synced (before the too-large line), got %d", received)
	}

	// Should have made exactly 1 chunk request (for line 1)
	if chunks != 1 {
		t.Errorf("Expected 1 chunk request, got %d", chunks)
	}
}

// TestDaemonBadRequestRecovery tests that daemon recovers from 400 Bad Request errors.
// When the backend returns 400, the daemon should:
// 1. Not crash
// 2. Not advance the sync position (so data isn't lost)
// 3. Retry the same data on the next sync cycle
// 4. Successfully upload once the backend starts accepting requests
func TestDaemonBadRequestRecovery(t *testing.T) {
	var requestCount int32
	var chunkRequests []sync.ChunkRequest
	var mu stdsync.Mutex
	var failChunks int32 = 2 // Fail first 2 chunk requests with 400

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			json.NewEncoder(w).Encode(sync.InitResponse{
				SessionID: "bad-request-test-session",
				Files:     make(map[string]sync.FileState),
			})

		case "/api/v1/sync/chunk":
			count := atomic.AddInt32(&requestCount, 1)

			// Fail first N chunk requests with 400 Bad Request
			if count <= atomic.LoadInt32(&failChunks) {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error": "simulated bad request"}`))
				return
			}

			var req sync.ChunkRequest
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			mu.Lock()
			chunkRequests = append(chunkRequests, req)
			mu.Unlock()

			lastLine := req.FirstLine + len(req.Lines) - 1
			json.NewEncoder(w).Encode(sync.ChunkResponse{
				LastSyncedLine: lastLine,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with content
	transcriptContent := `{"type":"system","line":1}
{"type":"user","line":2}
{"type":"assistant","line":3}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	d := New(Config{
		ExternalID:     "bad-request-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for daemon to experience 400 errors and then recover
	// Need enough time for multiple sync cycles (50ms each + processing)
	time.Sleep(400 * time.Millisecond)

	// Daemon should still be running despite 400 errors
	select {
	case err := <-errCh:
		t.Fatalf("Daemon crashed on 400 error: %v", err)
	default:
		// Good - daemon still running
	}

	// Wait more time for recovery and successful upload
	time.Sleep(400 * time.Millisecond)
	cancel()
	<-errCh

	// Verify recovery behavior
	totalRequests := atomic.LoadInt32(&requestCount)
	if totalRequests < 3 {
		t.Errorf("Expected at least 3 chunk requests (2 failures + 1 success), got %d", totalRequests)
	}

	mu.Lock()
	successfulChunks := len(chunkRequests)
	mu.Unlock()

	if successfulChunks == 0 {
		t.Error("Expected at least one successful chunk upload after 400 errors")
	}

	// Key assertion: the successful upload should contain the SAME data
	// that was rejected (starting from line 1), proving we didn't lose data
	mu.Lock()
	if len(chunkRequests) > 0 {
		firstSuccessful := chunkRequests[0]
		if firstSuccessful.FirstLine != 1 {
			t.Errorf("After 400 recovery, expected upload to start at line 1, got %d", firstSuccessful.FirstLine)
		}
		if len(firstSuccessful.Lines) != 3 {
			t.Errorf("After 400 recovery, expected 3 lines, got %d", len(firstSuccessful.Lines))
		}
	}
	mu.Unlock()

	t.Logf("400 Bad Request recovery test: %d total requests, %d successful chunks", totalRequests, successfulChunks)
}

// TestDaemonSIGTERMFinalSync tests that daemon performs final sync when receiving SIGTERM.
// This is critical: if final sync breaks, users lose the last ~30s of transcript data.
func TestDaemonSIGTERMFinalSync(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create initial transcript
	os.WriteFile(transcriptPath, []byte(`{"type":"system","line":1}`+"\n"), 0644)

	d := New(Config{
		ExternalID:         "sigterm-test",
		TranscriptPath:     transcriptPath,
		CWD:                tmpDir,
		SyncInterval:       10 * time.Second, // Very long - won't trigger during test
		SyncIntervalJitter: 0,                // Disable jitter for predictable timing
	})

	ctx := context.Background()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for initial sync (happens immediately on first iteration)
	time.Sleep(150 * time.Millisecond)

	initialChunks := len(mock.getChunkRequests())
	if initialChunks == 0 {
		t.Fatal("Expected initial chunk upload")
	}

	// Append new content that WON'T be synced by interval (10s is too long)
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"user","line":2}` + "\n")
	f.WriteString(`{"type":"assistant","line":3}` + "\n")
	f.Close()

	// Give time for file write to complete
	time.Sleep(50 * time.Millisecond)

	// Send SIGTERM to trigger shutdown with final sync
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	// Use Stop() which triggers the same shutdown path as SIGTERM
	// (We can't send real SIGTERM to the test process without affecting all tests)
	d.Stop()

	// Wait for daemon to exit
	select {
	case <-errCh:
		// Daemon exited
	case <-time.After(5 * time.Second):
		t.Fatal("Daemon did not exit after Stop()")
	}

	_ = proc // Silence unused warning

	// Key assertion: final sync should have uploaded the new lines
	chunkReqs := mock.getChunkRequests()
	finalChunks := len(chunkReqs)
	if finalChunks <= initialChunks {
		t.Errorf("Expected final sync to upload new content: had %d chunks before shutdown, %d after",
			initialChunks, finalChunks)
	}

	// Verify the final chunk contains the new lines (2 and 3)
	if finalChunks > initialChunks {
		lastChunk := chunkReqs[finalChunks-1]
		if lastChunk.FirstLine != 2 {
			t.Errorf("Final sync chunk should start at line 2, got %d", lastChunk.FirstLine)
		}
		if len(lastChunk.Lines) != 2 {
			t.Errorf("Final sync should upload 2 new lines, got %d", len(lastChunk.Lines))
		}
	}

	t.Logf("SIGTERM final sync test: %d chunks before, %d after shutdown", initialChunks, finalChunks)
}

// TestDaemonParentProcessExit tests that daemon shuts down when parent process exits.
// This handles cases where the parent CLI (Claude Code or Codex) crashes or is
// killed without firing a session-end signal. The parent-exit branch in the
// daemon loop is provider-agnostic; this test pins that invariant.
func TestDaemonParentProcessExit(t *testing.T) {
	providers := []struct {
		name         string
		providerName string
	}{
		{"claude-code", provider.NameClaudeCode},
		{"codex", provider.NameCodex},
	}
	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockBackend(t)
			server := httptest.NewServer(mock)
			defer server.Close()

			tmpDir, transcriptPath := setupTestEnv(t, server.URL)

			// Create transcript
			os.WriteFile(transcriptPath, []byte(`{"type":"system","line":1}`+"\n"), 0644)

			// Start a subprocess that we can kill to simulate parent exit.
			// We use "sleep" as a simple long-running process.
			sleepCmd := exec.Command("sleep", "60")
			if err := sleepCmd.Start(); err != nil {
				t.Fatalf("Failed to start sleep process: %v", err)
			}
			parentPID := sleepCmd.Process.Pid

			// Ensure we clean up the sleep process
			defer func() {
				sleepCmd.Process.Kill()
				sleepCmd.Wait()
			}()

			d := New(Config{
				Provider:           tc.providerName,
				ExternalID:         "parent-exit-test-" + tc.name,
				TranscriptPath:     transcriptPath,
				CWD:                tmpDir,
				ParentPID:          parentPID, // Monitor the sleep process
				SyncInterval:       100 * time.Millisecond,
				SyncIntervalJitter: 0,
			})

			ctx := context.Background()

			errCh := make(chan error, 1)
			startTime := time.Now()
			go func() {
				errCh <- d.Run(ctx)
			}()

			// Wait for daemon to start and perform initial sync
			time.Sleep(200 * time.Millisecond)

			// Verify daemon is running and syncing
			if len(mock.getInitRequests()) == 0 {
				t.Fatal("Expected daemon to initialize before parent kill")
			}

			// Kill the "parent" process
			if err := sleepCmd.Process.Kill(); err != nil {
				t.Fatalf("Failed to kill sleep process: %v", err)
			}
			sleepCmd.Wait() // Reap the zombie

			// Daemon should detect parent exit and shut down within a few sync intervals
			select {
			case <-errCh:
				elapsed := time.Since(startTime)
				t.Logf("Parent exit test (%s): daemon shut down in %.1fs after parent killed", tc.name, elapsed.Seconds())
			case <-time.After(5 * time.Second):
				t.Fatal("Daemon did not exit after parent process was killed")
			}

			// Verify final sync occurred (shutdown should trigger it)
			if len(mock.getChunkRequests()) == 0 {
				t.Error("Expected at least one chunk upload (initial or final sync)")
			}
		})
	}
}

// TestDaemonBackendRollback tests that daemon respects backend's lastSyncedLine even if lower.
// Scenario: client synced lines 1-10, then backend "forgets" and reports lastSyncedLine=5.
// Expected: client re-uploads lines 6-10 from the backend's reported position.
func TestDaemonBackendRollback(t *testing.T) {
	var initCount int32
	var chunkRequests []sync.ChunkRequest
	var mu stdsync.Mutex

	// Server that simulates a "rollback" - first reports lines synced, then fewer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			count := atomic.AddInt32(&initCount, 1)

			var lastSynced int
			if count == 1 {
				// First init: backend has nothing
				lastSynced = 0
			} else {
				// Subsequent inits: backend "rolled back" to line 3
				// (simulates data loss, restore from backup, etc.)
				lastSynced = 3
			}

			json.NewEncoder(w).Encode(sync.InitResponse{
				SessionID: "rollback-test-session",
				Files: map[string]sync.FileState{
					"transcript.jsonl": {LastSyncedLine: lastSynced},
				},
			})

		case "/api/v1/sync/chunk":
			var req sync.ChunkRequest
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			mu.Lock()
			chunkRequests = append(chunkRequests, req)
			mu.Unlock()

			lastLine := req.FirstLine + len(req.Lines) - 1
			json.NewEncoder(w).Encode(sync.ChunkResponse{
				LastSyncedLine: lastLine,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with 6 lines
	transcriptContent := `{"type":"system","line":1}
{"type":"user","line":2}
{"type":"assistant","line":3}
{"type":"user","line":4}
{"type":"assistant","line":5}
{"type":"user","line":6}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// First daemon run: syncs all 6 lines
	d1 := New(Config{
		ExternalID:     "rollback-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx1, cancel1 := context.WithTimeout(context.Background(), 2*time.Second)
	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- d1.Run(ctx1)
	}()

	// Wait for initial sync
	time.Sleep(200 * time.Millisecond)
	cancel1()
	<-errCh1

	// Verify first sync uploaded all 6 lines from line 1
	mu.Lock()
	firstSyncChunks := len(chunkRequests)
	var firstChunkFirstLine int
	var firstChunkLines int
	if firstSyncChunks > 0 {
		firstChunkFirstLine = chunkRequests[0].FirstLine
		firstChunkLines = len(chunkRequests[0].Lines)
	}
	mu.Unlock()

	if firstSyncChunks == 0 {
		t.Fatal("Expected chunk upload on first sync")
	}
	if firstChunkFirstLine != 1 {
		t.Errorf("First sync should start at line 1, got %d", firstChunkFirstLine)
	}
	if firstChunkLines != 6 {
		t.Errorf("First sync should upload 6 lines, got %d", firstChunkLines)
	}

	t.Logf("First sync: uploaded %d lines starting at line %d", firstChunkLines, firstChunkFirstLine)

	// Now start a NEW daemon (simulating restart)
	// Backend will report lastSyncedLine=3 (rolled back from 6)
	d2 := New(Config{
		ExternalID:     "rollback-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- d2.Run(ctx2)
	}()

	// Wait for second sync
	time.Sleep(200 * time.Millisecond)
	cancel2()
	<-errCh2

	// Check that second daemon re-uploaded from line 4 (respecting backend's lastSyncedLine=3)
	mu.Lock()
	totalChunks := len(chunkRequests)
	var secondChunkFirstLine int
	var secondChunkLines int
	if totalChunks > firstSyncChunks {
		secondChunk := chunkRequests[firstSyncChunks] // First chunk from second daemon
		secondChunkFirstLine = secondChunk.FirstLine
		secondChunkLines = len(secondChunk.Lines)
	}
	mu.Unlock()

	if totalChunks <= firstSyncChunks {
		t.Fatal("Expected chunk upload on second sync after rollback")
	}

	// Key assertion: second daemon should start from line 4 (after backend's line 3)
	if secondChunkFirstLine != 4 {
		t.Errorf("After rollback, expected re-upload from line 4, got line %d", secondChunkFirstLine)
	}
	if secondChunkLines != 3 {
		t.Errorf("After rollback, expected 3 lines (4,5,6), got %d", secondChunkLines)
	}

	t.Logf("Second sync (after rollback): uploaded %d lines starting at line %d",
		secondChunkLines, secondChunkFirstLine)
}

// TestDaemonSessionDeleted tests that daemon stops after receiving 3 consecutive 404 errors.
// This handles the case where a session is deleted from the backend while daemon is running.
func TestDaemonSessionDeleted(t *testing.T) {
	var initCount int32
	var chunkCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			count := atomic.AddInt32(&initCount, 1)
			if count == 1 {
				// First init succeeds
				json.NewEncoder(w).Encode(sync.InitResponse{
					SessionID: "session-to-be-deleted",
					Files:     make(map[string]sync.FileState),
				})
			} else {
				// Subsequent inits also return 404 (session deleted)
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error": "session not found"}`))
			}

		case "/api/v1/sync/chunk":
			atomic.AddInt32(&chunkCount, 1)
			// Always return 404 - session was deleted
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error": "session not found"}`))

		default:
			_ = body
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with content
	transcriptContent := `{"type":"system","line":1}
{"type":"user","line":2}
{"type":"assistant","line":3}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	d := New(Config{
		ExternalID:     "session-deleted-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	startTime := time.Now()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Daemon should exit on its own after 3 consecutive 404s
	select {
	case <-errCh:
		elapsed := time.Since(startTime)
		t.Logf("Session deleted test: daemon shut down in %.2fs", elapsed.Seconds())
	case <-time.After(4 * time.Second):
		cancel()
		<-errCh
		t.Fatal("Daemon did not exit after 3 consecutive 404 errors")
	}

	// Verify we got at least 3 chunk requests (the 404 threshold).
	// May be 4 if final sync also attempted a request before shutdown completed.
	chunks := atomic.LoadInt32(&chunkCount)
	if chunks < 3 {
		t.Errorf("Expected at least 3 chunk requests (404 threshold), got %d", chunks)
	}

	t.Logf("Session deleted test: %d init requests, %d chunk requests before shutdown",
		atomic.LoadInt32(&initCount), chunks)
}

// TestDaemonSessionDeletedRecovery tests that the 404 counter resets on successful sync.
// If backend temporarily returns 404 then recovers, daemon should continue running.
func TestDaemonSessionDeletedRecovery(t *testing.T) {
	var chunkCount int32
	var failCount int32 = 2 // Fail first 2 chunk requests with 404, then succeed

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			json.NewEncoder(w).Encode(sync.InitResponse{
				SessionID: "recovery-test-session",
				Files:     make(map[string]sync.FileState),
			})

		case "/api/v1/sync/chunk":
			count := atomic.AddInt32(&chunkCount, 1)
			if count <= atomic.LoadInt32(&failCount) {
				// First N requests return 404
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"error": "session not found"}`))
				return
			}

			// After that, succeed
			var req sync.ChunkRequest
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			lastLine := req.FirstLine + len(req.Lines) - 1
			json.NewEncoder(w).Encode(sync.ChunkResponse{
				LastSyncedLine: lastLine,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript
	os.WriteFile(transcriptPath, []byte(`{"type":"system","line":1}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "404-recovery-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Wait for sync cycles - daemon should experience 404s then recover
	time.Sleep(500 * time.Millisecond)

	// Daemon should still be running (recovered from 404s before hitting threshold)
	select {
	case err := <-errCh:
		t.Fatalf("Daemon should not have exited: %v", err)
	default:
		// Good - daemon still running
	}

	cancel()
	<-errCh

	// Verify daemon made multiple requests and recovered
	chunks := atomic.LoadInt32(&chunkCount)
	if chunks < 3 {
		t.Errorf("Expected at least 3 chunk requests (2 failures + 1 success), got %d", chunks)
	}

	t.Logf("404 recovery test: daemon survived %d chunk requests (first 2 were 404s)", chunks)
}

// =================================================================================================
// Codex daemon integration tests (CF-387)
//
// These exercise the daemon end-to-end with a Codex root rollout + an
// in-memory SQLite fixture for descendant discovery. The daemon should:
//   - upload the root rollout as file_type=transcript
//   - discover descendants on each cycle via DiscoverCodexDescendants
//   - upload children as file_type=agent under the root's session
//   - attach codex_rollout metadata to the FIRST chunk of every rollout
// =================================================================================================

// setupCodexDaemonEnv combines a codextest fixture with the daemon's
// HOME-based config (config.json + sync state dir).
func setupCodexDaemonEnv(t *testing.T, backendURL string) *codextestFixtureShim {
	t.Helper()
	tmpHome := t.TempDir()

	// Config + auth.
	confabDir := filepath.Join(tmpHome, ".confab")
	if err := os.MkdirAll(confabDir, 0o755); err != nil {
		t.Fatalf("mkdir confab dir: %v", err)
	}
	cfg := fmt.Sprintf(`{"backend_url":"%s","api_key":"test-api-key-12345678"}`, backendURL)
	if err := os.WriteFile(filepath.Join(confabDir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFAB_CONFIG_PATH", filepath.Join(confabDir, "config.json"))
	t.Setenv("HOME", tmpHome)

	return newCodexFixtureShim(t)
}

func TestDaemonIntegration_CodexRoot_NoChildren_FullLifecycle(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	shim := setupCodexDaemonEnv(t, server.URL)
	root := shim.addRoot("11111111-1111-1111-1111-111111111111", "hello root")

	d := New(Config{
		Provider:           "codex",
		ExternalID:         root.threadUUID,
		TranscriptPath:     root.rolloutPath,
		CWD:                "/work",
		SyncInterval:       50 * time.Millisecond,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-errCh

	initReqs := mock.getInitRequests()
	if len(initReqs) == 0 || initReqs[0].Provider != "codex" {
		t.Fatalf("init request not for codex: %+v", initReqs)
	}
	if initReqs[0].ExternalID != root.threadUUID {
		t.Errorf("external_id = %q, want %q", initReqs[0].ExternalID, root.threadUUID)
	}

	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) < 1 {
		t.Fatalf("no chunk uploaded; got %d", len(chunkReqs))
	}
	got := chunkReqs[0]
	if got.FileType != "transcript" {
		t.Errorf("file_type = %q, want transcript", got.FileType)
	}
	if got.Metadata == nil || got.Metadata.CodexRollout == nil {
		t.Fatal("first chunk should carry codex_rollout meta")
	}
	if got.Metadata.CodexRollout.ThreadUUID != root.threadUUID {
		t.Errorf("codex_rollout.thread_uuid = %q, want %q",
			got.Metadata.CodexRollout.ThreadUUID, root.threadUUID)
	}
	if got.Metadata.CodexRollout.ParentThreadUUID != "" {
		t.Errorf("root codex_rollout.parent_thread_uuid = %q, want empty",
			got.Metadata.CodexRollout.ParentThreadUUID)
	}
}

func TestDaemonIntegration_CodexRoot_TwoChildren_FullLifecycle(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	shim := setupCodexDaemonEnv(t, server.URL)
	root := shim.addRoot("22222222-2222-2222-2222-222222222222", "root msg")
	childA := shim.addChild(root.threadUUID, "33333333-3333-3333-3333-333333333333", "child a", "reviewer")
	childB := shim.addChild(root.threadUUID, "44444444-4444-4444-4444-444444444444", "child b", "planner")

	d := New(Config{
		Provider:           "codex",
		ExternalID:         root.threadUUID,
		TranscriptPath:     root.rolloutPath,
		CWD:                "/work",
		SyncInterval:       50 * time.Millisecond,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	time.Sleep(400 * time.Millisecond)
	cancel()
	<-errCh

	chunkReqs := mock.getChunkRequests()
	seenFiles := map[string]sync.ChunkRequest{}
	for _, req := range chunkReqs {
		// Only keep the first chunk seen per file — that's the one expected
		// to carry codex_rollout meta.
		if _, dup := seenFiles[req.FileName]; !dup {
			seenFiles[req.FileName] = req
		}
	}
	rootChunk, ok := seenFiles[filepath.Base(root.rolloutPath)]
	if !ok {
		t.Fatal("root rollout never uploaded")
	}
	if rootChunk.FileType != "transcript" {
		t.Errorf("root file_type = %q, want transcript", rootChunk.FileType)
	}
	for _, c := range []codexShimEntry{childA, childB} {
		chunk, ok := seenFiles[filepath.Base(c.rolloutPath)]
		if !ok {
			t.Errorf("child %s never uploaded", c.threadUUID)
			continue
		}
		if chunk.FileType != "agent" {
			t.Errorf("child %s file_type = %q, want agent", c.threadUUID, chunk.FileType)
		}
		if chunk.Metadata == nil || chunk.Metadata.CodexRollout == nil {
			t.Errorf("child %s first chunk missing codex_rollout meta", c.threadUUID)
			continue
		}
		if chunk.Metadata.CodexRollout.ParentThreadUUID != root.threadUUID {
			t.Errorf("child %s parent = %q, want %q", c.threadUUID,
				chunk.Metadata.CodexRollout.ParentThreadUUID, root.threadUUID)
		}
	}
}

func TestDaemonIntegration_CodexDeepTree_3Levels_FullLifecycle(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	shim := setupCodexDaemonEnv(t, server.URL)
	root := shim.addRoot("55555555-5555-5555-5555-555555555555", "root")
	mid := shim.addChild(root.threadUUID, "66666666-6666-6666-6666-666666666666", "mid", "mid-role")
	leaf := shim.addChild(mid.threadUUID, "77777777-7777-7777-7777-777777777777", "leaf", "leaf-role")

	d := New(Config{
		Provider:           "codex",
		ExternalID:         root.threadUUID,
		TranscriptPath:     root.rolloutPath,
		CWD:                "/work",
		SyncInterval:       50 * time.Millisecond,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	time.Sleep(400 * time.Millisecond)
	cancel()
	<-errCh

	// Build parent map from emitted metadata (first chunk per file).
	parentOf := map[string]string{}
	for _, req := range mock.getChunkRequests() {
		if req.Metadata == nil || req.Metadata.CodexRollout == nil {
			continue
		}
		uuid := req.Metadata.CodexRollout.ThreadUUID
		if _, seen := parentOf[uuid]; seen {
			continue // only count first chunk per rollout
		}
		parentOf[uuid] = req.Metadata.CodexRollout.ParentThreadUUID
	}
	if got := parentOf[mid.threadUUID]; got != root.threadUUID {
		t.Errorf("mid.parent = %q, want %q", got, root.threadUUID)
	}
	if got := parentOf[leaf.threadUUID]; got != mid.threadUUID {
		t.Errorf("leaf.parent = %q, want %q (immediate parent, not root)",
			got, mid.threadUUID)
	}
	if got, ok := parentOf[root.threadUUID]; !ok || got != "" {
		t.Errorf("root.parent = (%q, %v), want (\"\", true)", got, ok)
	}
}

func TestDaemonIntegration_CodexNewChildAppears_DuringRun_PickedUpWithinOneCycle(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	shim := setupCodexDaemonEnv(t, server.URL)
	root := shim.addRoot("88888888-8888-8888-8888-888888888888", "root")

	d := New(Config{
		Provider:           "codex",
		ExternalID:         root.threadUUID,
		TranscriptPath:     root.rolloutPath,
		CWD:                "/work",
		SyncInterval:       80 * time.Millisecond,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Wait long enough for the first cycle to have happened with no children.
	time.Sleep(200 * time.Millisecond)
	beforeCount := len(mock.getChunkRequests())

	// Subagent appears in SQLite mid-run.
	late := shim.addChild(root.threadUUID, "99999999-9999-9999-9999-999999999999", "late arrival", "late")

	// Wait for at least one more sync cycle.
	time.Sleep(400 * time.Millisecond)
	cancel()
	<-errCh

	afterCount := len(mock.getChunkRequests())
	if afterCount <= beforeCount {
		t.Fatalf("no new chunks after subagent appeared (before=%d after=%d)", beforeCount, afterCount)
	}
	// Find the late child's first chunk.
	var lateChunk *sync.ChunkRequest
	for i, req := range mock.getChunkRequests() {
		if req.FileName == filepath.Base(late.rolloutPath) {
			lateChunk = &mock.getChunkRequests()[i]
			break
		}
	}
	if lateChunk == nil {
		t.Fatal("late subagent never uploaded")
	}
	if lateChunk.FileType != "agent" {
		t.Errorf("late file_type = %q, want agent", lateChunk.FileType)
	}
	if lateChunk.Metadata == nil || lateChunk.Metadata.CodexRollout == nil {
		t.Fatal("late chunk missing codex_rollout meta")
	}
	if lateChunk.Metadata.CodexRollout.ParentThreadUUID != root.threadUUID {
		t.Errorf("late.parent = %q, want %q",
			lateChunk.Metadata.CodexRollout.ParentThreadUUID, root.threadUUID)
	}
}

func TestDaemonIntegration_CodexBackendDown_DaemonRetries_AllChildrenSyncedOnRecovery(t *testing.T) {
	mock := newMockBackend(t)
	mock.failUntilCount = 2 // first 2 requests get 503; daemon retries.
	server := httptest.NewServer(mock)
	defer server.Close()

	shim := setupCodexDaemonEnv(t, server.URL)
	root := shim.addRoot("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "r")
	child := shim.addChild(root.threadUUID, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "c", "x")

	d := New(Config{
		Provider:           "codex",
		ExternalID:         root.threadUUID,
		TranscriptPath:     root.rolloutPath,
		CWD:                "/work",
		SyncInterval:       80 * time.Millisecond,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(1 * time.Second) // enough for retries to succeed.
	cancel()
	<-errCh

	totalRequests := atomic.LoadInt32(&mock.requestCount)
	if totalRequests < 3 {
		t.Errorf("expected ≥3 requests (failures + recovery), got %d", totalRequests)
	}
	chunkReqs := mock.getChunkRequests()
	seen := map[string]bool{}
	for _, req := range chunkReqs {
		seen[req.FileName] = true
	}
	if !seen[filepath.Base(root.rolloutPath)] {
		t.Errorf("root never uploaded after recovery; chunks=%v", seen)
	}
	if !seen[filepath.Base(child.rolloutPath)] {
		t.Errorf("child never uploaded after recovery; chunks=%v", seen)
	}
}

func TestDaemonIntegration_Codex_ShutdownPath_FinalSyncIncludesChildren(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	shim := setupCodexDaemonEnv(t, server.URL)
	root := shim.addRoot("cccccccc-cccc-cccc-cccc-cccccccccccc", "r")

	d := New(Config{
		Provider:       "codex",
		ExternalID:     root.threadUUID,
		TranscriptPath: root.rolloutPath,
		CWD:            "/work",
		// Long interval — we deliberately do NOT want a regular tick to fire
		// after the late child appears. Final SyncAll on shutdown must be
		// the one that discovers and uploads the late child.
		SyncInterval:       5 * time.Second,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Wait for the first (immediate) cycle to happen with no children.
	time.Sleep(300 * time.Millisecond)
	preShutdown := mock.getChunkRequests()
	if len(preShutdown) < 1 {
		t.Fatalf("expected at least the root to upload on first cycle, got %d", len(preShutdown))
	}

	// Add a child AFTER the last regular cycle, then signal shutdown.
	late := shim.addChild(root.threadUUID, "dddddddd-dddd-dddd-dddd-dddddddddddd",
		"late", "late-role")
	cancel() // triggers shutdown → final sync
	<-errCh

	// Final sync should have uploaded the late child.
	var found bool
	for _, req := range mock.getChunkRequests() {
		if req.FileName == filepath.Base(late.rolloutPath) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("final sync did not include late child %s", late.threadUUID)
	}
}

// TestDaemon_ExitsAfter3Consecutive404s guards the
// maxConsecutiveNotFound shutdown path at daemon.go (constant defined
// at daemon.go:37, branch at daemon.go:216-220). If the user deletes
// their backend session, the daemon's chunk uploads will 404 forever;
// without this exit the daemon would consume resources indefinitely.
//
// This is a bug-revealing test by spec: if the daemon doesn't exit
// after 3 consecutive 404s, the test fails and we fix the bug per the
// CF-451 bug-policy.
func TestDaemon_ExitsAfter3Consecutive404s(t *testing.T) {
	mock := newMockBackend(t)
	mock.chunkStatus = http.StatusNotFound
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644)

	d := New(Config{
		ExternalID:         "404-exit-test",
		TranscriptPath:     transcriptPath,
		CWD:                tmpDir,
		SyncInterval:       50 * time.Millisecond,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	select {
	case <-errCh:
		// Daemon exited on its own — good.
	case <-time.After(3 * time.Second):
		cancel()
		<-errCh
		t.Fatal("daemon did not exit within 3s after backend returned 404 on chunk uploads; the consecutiveNotFound shutdown is broken")
	}

	// At least 3 chunk attempts should have happened before exit.
	got := len(mock.getChunkRequests())
	if got < 3 {
		t.Errorf("got %d chunk attempts before exit, want >= 3 (one per cycle before shutdown)", got)
	}
}

// TestDaemonShutsDownWhenParentPIDDies guards the parent-process-died
// shutdown path at daemon.go:193. The whole reason Codex avoids using
// a Stop hook for shutdown is that this path must work. Without a
// test, a regression that broke parent-PID monitoring would leave
// orphaned daemons consuming resources on user machines after their
// editor exits.
//
// Spawns a real `sleep` subprocess, points the daemon at its PID,
// kills the subprocess, and asserts the daemon exits on its next
// sync cycle (well before the context deadline).
func TestDaemonShutsDownWhenParentPIDDies(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644)

	// Spawn a real child process to serve as the parent PID.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to spawn child process: %v", err)
	}
	parentPID := cmd.Process.Pid
	// Reap regardless of what the daemon does.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	d := New(Config{
		ExternalID:         "parent-pid-death-test",
		TranscriptPath:     transcriptPath,
		CWD:                tmpDir,
		ParentPID:          parentPID,
		SyncInterval:       100 * time.Millisecond,
		SyncIntervalJitter: 0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Let the daemon complete its first sync, then kill the parent.
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill parent: %v", err)
	}
	// Drain the process so the kill becomes visible to the kernel
	// (avoids the zombie window where signal-0 still reports running).
	if _, err := cmd.Process.Wait(); err != nil {
		t.Logf("wait parent (expected error after Kill): %v", err)
	}

	// The daemon must exit within a few sync intervals after the
	// parent dies — not when the context deadline fires.
	select {
	case <-errCh:
		// Good — daemon returned.
	case <-time.After(2 * time.Second):
		cancel()
		<-errCh
		t.Fatal("daemon did not exit within 2s after parent PID died; parent-PID monitoring is broken")
	}
}
