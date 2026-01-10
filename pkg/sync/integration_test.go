package sync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
)

// TestEngine_IncrementalSync tests that engine only uploads new lines
func TestEngine_IncrementalSync(t *testing.T) {
	mock := newMockBackend(t)
	// Simulate backend already has first 2 lines
	mock.initResponse.Files = map[string]FileState{
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

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "incremental-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if chunks != 1 {
		t.Errorf("expected 1 chunk, got %d", chunks)
	}

	// Verify only new lines were uploaded
	if len(mock.chunkRequests) == 0 {
		t.Fatal("Expected chunk request, got none")
	}

	chunkReq := mock.chunkRequests[0]
	if chunkReq.FirstLine != 3 {
		t.Errorf("Expected first_line 3 (after synced line 2), got %d", chunkReq.FirstLine)
	}
	if len(chunkReq.Lines) != 2 {
		t.Errorf("Expected 2 new lines, got %d", len(chunkReq.Lines))
	}
}

// TestEngine_MultipleSyncCycles tests that engine continues syncing new content
func TestEngine_MultipleSyncCycles(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Start with initial content
	os.WriteFile(transcriptPath, []byte(`{"type":"system","line":1}`+"\n"), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "multi-cycle-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First sync
	chunks1, _ := engine.SyncAll()
	if chunks1 != 1 {
		t.Errorf("expected 1 chunk on first sync, got %d", chunks1)
	}

	// Append more content
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"user","line":2}` + "\n")
	f.WriteString(`{"type":"assistant","line":3}` + "\n")
	f.Close()

	// Force file change detection
	for _, file := range engine.tracker.GetTrackedFiles() {
		file.LastModTime = file.LastModTime.Add(-1)
	}

	// Second sync
	chunks2, _ := engine.SyncAll()
	if chunks2 != 1 {
		t.Errorf("expected 1 chunk on second sync, got %d", chunks2)
	}

	// Verify we have 2 chunk uploads total
	if len(mock.chunkRequests) != 2 {
		t.Fatalf("expected 2 chunk requests, got %d", len(mock.chunkRequests))
	}

	// First chunk should be line 1
	if mock.chunkRequests[0].FirstLine != 1 {
		t.Errorf("First chunk should start at line 1, got %d", mock.chunkRequests[0].FirstLine)
	}

	// Second chunk should be lines 2-3
	secondChunk := mock.chunkRequests[1]
	if secondChunk.FirstLine != 2 {
		t.Errorf("Second chunk should start at line 2, got %d", secondChunk.FirstLine)
	}
	if len(secondChunk.Lines) != 2 {
		t.Errorf("Second chunk should have 2 lines, got %d", len(secondChunk.Lines))
	}
}

// TestEngine_MultipleAgentFiles tests discovery and sync of multiple agent files
func TestEngine_MultipleAgentFiles(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	transcriptDir := filepath.Dir(transcriptPath)

	// Create transcript referencing multiple agents
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"aaaaaaaa","result":"done"}}
{"type":"user","toolUseResult":{"agentId":"bbbbbbbb","result":"done"}}
{"type":"user","toolUseResult":{"agentId":"cccccccc","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// Create all three agent files
	for _, id := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		agentPath := filepath.Join(transcriptDir, fmt.Sprintf("agent-%s.jsonl", id))
		os.WriteFile(agentPath, []byte(fmt.Sprintf(`{"agent":"%s","line":1}`+"\n", id)), 0644)
	}

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "multi-agent-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First sync - transcript
	engine.SyncAll()

	// Force file change detection for agents
	for _, file := range engine.tracker.GetTrackedFiles() {
		file.LastModTime = file.LastModTime.Add(-1)
	}

	// Second sync - agents
	engine.SyncAll()

	// Count uploads by type
	transcriptUploads := 0
	agentFiles := make(map[string]bool)
	for _, req := range mock.chunkRequests {
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

// TestEngine_BackendRollback tests that engine respects backend's lastSyncedLine even if lower.
func TestEngine_BackendRollback(t *testing.T) {
	var initCount int32
	var chunkRequests []ChunkRequest
	var mu sync.Mutex

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
				lastSynced = 3
			}

			json.NewEncoder(w).Encode(InitResponse{
				SessionID: "rollback-test-session",
				Files: map[string]FileState{
					"transcript.jsonl": {LastSyncedLine: lastSynced},
				},
			})

		case "/api/v1/sync/chunk":
			var req ChunkRequest
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			mu.Lock()
			chunkRequests = append(chunkRequests, req)
			mu.Unlock()

			lastLine := req.FirstLine + len(req.Lines) - 1
			json.NewEncoder(w).Encode(ChunkResponse{
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

	// First engine: syncs all 6 lines
	engine1 := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "rollback-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine1.Init(); err != nil {
		t.Fatalf("First init failed: %v", err)
	}
	engine1.SyncAll()

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

	// Second engine (simulating restart) - backend reports lastSyncedLine=3
	engine2 := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "rollback-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine2.Init(); err != nil {
		t.Fatalf("Second init failed: %v", err)
	}
	engine2.SyncAll()

	// Check that second engine re-uploaded from line 4
	mu.Lock()
	totalChunks := len(chunkRequests)
	var secondChunkFirstLine int
	var secondChunkLines int
	if totalChunks > firstSyncChunks {
		secondChunk := chunkRequests[firstSyncChunks]
		secondChunkFirstLine = secondChunk.FirstLine
		secondChunkLines = len(secondChunk.Lines)
	}
	mu.Unlock()

	if totalChunks <= firstSyncChunks {
		t.Fatal("Expected chunk upload on second sync after rollback")
	}

	if secondChunkFirstLine != 4 {
		t.Errorf("After rollback, expected re-upload from line 4, got line %d", secondChunkFirstLine)
	}
	if secondChunkLines != 3 {
		t.Errorf("After rollback, expected 3 lines (4,5,6), got %d", secondChunkLines)
	}
}

// TestEngine_BackendHasMoreLines tests resuming when backend has more lines than expected
func TestEngine_BackendHasMoreLines(t *testing.T) {
	mock := newMockBackend(t)
	// Backend says it already has 5 lines
	mock.initResponse.Files = map[string]FileState{
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

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "backend-ahead-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	chunks, _ := engine.SyncAll()
	if chunks != 1 {
		t.Errorf("expected 1 chunk, got %d", chunks)
	}

	if len(mock.chunkRequests) == 0 {
		t.Fatal("Expected chunk request")
	}

	// Should start from line 6 (after backend's line 5)
	chunkReq := mock.chunkRequests[0]
	if chunkReq.FirstLine != 6 {
		t.Errorf("Expected first_line 6, got %d", chunkReq.FirstLine)
	}
	if len(chunkReq.Lines) != 2 {
		t.Errorf("Expected 2 lines (6 and 7), got %d", len(chunkReq.Lines))
	}
}

// TestEngine_EmptyTranscript tests handling of empty transcript file
func TestEngine_EmptyTranscript(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create empty transcript
	os.WriteFile(transcriptPath, []byte(""), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "empty-transcript-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	// Init should happen
	if len(mock.initRequests) == 0 {
		t.Error("Expected init request even for empty transcript")
	}

	// No chunks should be uploaded (nothing to sync)
	if chunks != 0 {
		t.Errorf("Expected 0 chunks for empty transcript, got %d", chunks)
	}
}

// TestEngine_LargeFile tests that engine can handle large transcript files (~10MB for speed)
func TestEngine_LargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large file test in short mode")
	}

	var totalLinesReceived int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			json.NewEncoder(w).Encode(InitResponse{
				SessionID: "large-file-session",
				Files:     make(map[string]FileState),
			})

		case "/api/v1/sync/chunk":
			var req ChunkRequest
			if json.Unmarshal(body, &req) == nil {
				atomic.AddInt32(&totalLinesReceived, int32(len(req.Lines)))
				lastLine := req.FirstLine + len(req.Lines) - 1
				json.NewEncoder(w).Encode(ChunkResponse{
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

	// Create a moderately large transcript file (~10MB, 10K lines)
	f, err := os.Create(transcriptPath)
	if err != nil {
		t.Fatalf("Failed to create transcript: %v", err)
	}

	numLines := 10000
	padding := strings.Repeat("x", 900) // ~900 bytes padding per line
	for i := 0; i < numLines; i++ {
		line := fmt.Sprintf(`{"type":"msg","line":%d,"padding":"%s"}`, i+1, padding)
		f.WriteString(line + "\n")
	}
	f.Close()

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "large-file-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if chunks == 0 {
		t.Error("Expected at least 1 chunk for large file")
	}

	received := atomic.LoadInt32(&totalLinesReceived)
	if received != int32(numLines) {
		t.Errorf("Expected %d lines uploaded, got %d", numLines, received)
	}
}

// TestEngine_RetryAfterReset tests that engine can be reset and re-initialized
func TestEngine_RetryAfterReset(t *testing.T) {
	var initCount int32
	mock := newMockBackend(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only count /init requests for failure logic
		if r.URL.Path == "/api/v1/sync/init" {
			count := atomic.AddInt32(&initCount, 1)
			if count <= 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}

		mock.ServeHTTP(w, r)
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644)

	cfg := &config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "test-api-key-12345678",
	}
	client, _ := NewClient(cfg)

	engine := NewWithClient(
		client,
		nil,
		EngineConfig{
			ExternalID:     "retry-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	// First init should fail
	err := engine.Init()
	if err == nil {
		t.Error("Expected first init to fail")
	}

	// Reset and try again
	engine.Reset()

	// Second init should succeed
	err = engine.Init()
	if err != nil {
		t.Fatalf("Second init should succeed: %v", err)
	}

	if !engine.IsInitialized() {
		t.Error("Expected engine to be initialized after retry")
	}
}
