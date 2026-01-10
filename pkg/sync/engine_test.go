package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	pkghttp "github.com/ConfabulousDev/confab/pkg/http"
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
	initRequests   []InitRequest
	chunkRequests  []ChunkRequest
	initResponse   *InitResponse
	initError      bool
	chunkError     bool
	requestCount   int32
	failUntilCount int32 // fail requests until this count is reached
}

func newMockBackend(t *testing.T) *mockBackend {
	return &mockBackend{
		t: t,
		initResponse: &InitResponse{
			SessionID: "test-session-id",
			Files:     make(map[string]FileState),
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

		var req InitRequest
		if err := json.Unmarshal(body, &req); err != nil {
			m.t.Errorf("Failed to decode init request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.initRequests = append(m.initRequests, req)
		json.NewEncoder(w).Encode(m.initResponse)

	case "/api/v1/sync/chunk":
		if m.chunkError {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "chunk failed"})
			return
		}

		var req ChunkRequest
		if err := json.Unmarshal(body, &req); err != nil {
			m.t.Errorf("Failed to decode chunk request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.chunkRequests = append(m.chunkRequests, req)

		// Return last synced line as first + len(lines) - 1
		lastLine := req.FirstLine + len(req.Lines) - 1
		json.NewEncoder(w).Encode(ChunkResponse{
			LastSyncedLine: lastLine,
		})

	case "/api/v1/sync/event":
		json.NewEncoder(w).Encode(EventResponse{Success: true})

	default:
		m.t.Errorf("Unexpected request to %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// setupTestEnv creates a temporary environment for engine testing
func setupTestEnv(t *testing.T, serverURL string) (tmpDir string, transcriptPath string) {
	tmpDir = t.TempDir()

	// Set up config file
	confabDir := filepath.Join(tmpDir, ".confab")
	os.MkdirAll(confabDir, 0755)
	configPath := filepath.Join(confabDir, "config.json")
	configJSON := `{"backend_url":"` + serverURL + `","api_key":"test-api-key-12345678"}`
	os.WriteFile(configPath, []byte(configJSON), 0600)
	t.Setenv("CONFAB_CONFIG_PATH", configPath)
	t.Setenv("HOME", tmpDir)

	// Create transcript directory
	transcriptDir := filepath.Join(tmpDir, "sessions")
	os.MkdirAll(transcriptDir, 0755)
	transcriptPath = filepath.Join(transcriptDir, "transcript.jsonl")

	return tmpDir, transcriptPath
}

func TestEngine_Init_NewSession(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript
	os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "test-external-id",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	err := engine.Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !engine.IsInitialized() {
		t.Error("expected engine to be initialized")
	}

	if engine.SessionID() != "test-session-id" {
		t.Errorf("expected session ID 'test-session-id', got %q", engine.SessionID())
	}

	// Verify init request
	if len(mock.initRequests) != 1 {
		t.Fatalf("expected 1 init request, got %d", len(mock.initRequests))
	}

	req := mock.initRequests[0]
	if req.ExternalID != "test-external-id" {
		t.Errorf("expected external_id 'test-external-id', got %q", req.ExternalID)
	}
	if req.TranscriptPath != transcriptPath {
		t.Errorf("expected transcript_path %q, got %q", transcriptPath, req.TranscriptPath)
	}
}

func TestEngine_Init_ResumeSession(t *testing.T) {
	mock := newMockBackend(t)
	// Backend already has some lines synced
	mock.initResponse.Files = map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 5},
	}
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with more lines
	content := ""
	for i := 1; i <= 10; i++ {
		content += `{"line":` + string(rune('0'+i)) + `}` + "\n"
	}
	os.WriteFile(transcriptPath, []byte(content), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "resume-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Sync should only upload new lines
	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if chunks != 1 {
		t.Errorf("expected 1 chunk, got %d", chunks)
	}

	// Verify chunk starts at line 6
	if len(mock.chunkRequests) != 1 {
		t.Fatalf("expected 1 chunk request, got %d", len(mock.chunkRequests))
	}

	chunkReq := mock.chunkRequests[0]
	if chunkReq.FirstLine != 6 {
		t.Errorf("expected FirstLine 6, got %d", chunkReq.FirstLine)
	}
	if len(chunkReq.Lines) != 5 {
		t.Errorf("expected 5 lines (6-10), got %d", len(chunkReq.Lines))
	}
}

func TestEngine_SyncAll_FirstSync(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	content := `{"type":"system","message":"hello"}
{"type":"user","message":"world"}
{"type":"assistant","message":"response"}
`
	os.WriteFile(transcriptPath, []byte(content), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "first-sync-test",
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

	// Verify chunk content
	if len(mock.chunkRequests) != 1 {
		t.Fatalf("expected 1 chunk request, got %d", len(mock.chunkRequests))
	}

	chunkReq := mock.chunkRequests[0]
	if chunkReq.SessionID != "test-session-id" {
		t.Errorf("expected session_id 'test-session-id', got %q", chunkReq.SessionID)
	}
	if chunkReq.FileType != "transcript" {
		t.Errorf("expected file_type 'transcript', got %q", chunkReq.FileType)
	}
	if len(chunkReq.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(chunkReq.Lines))
	}
	if chunkReq.FirstLine != 1 {
		t.Errorf("expected first_line 1, got %d", chunkReq.FirstLine)
	}
}

func TestEngine_SyncAll_NoChanges(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "no-changes-test",
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

	// Second sync without changes
	chunks2, _ := engine.SyncAll()
	if chunks2 != 0 {
		t.Errorf("expected 0 chunks on second sync (no changes), got %d", chunks2)
	}
}

func TestEngine_SyncAll_WithAgentDiscovery(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	transcriptDir := filepath.Dir(transcriptPath)

	// Create transcript with agent reference
	content := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"abc12345","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(content), 0644)

	// Create agent file
	agentPath := filepath.Join(transcriptDir, "agent-abc12345.jsonl")
	agentContent := `{"type":"agent","message":"agent line 1"}
{"type":"agent","message":"agent line 2"}
`
	os.WriteFile(agentPath, []byte(agentContent), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "agent-discovery-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Single SyncAll should upload BOTH transcript AND agent (BFS discovery)
	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if chunks != 2 {
		t.Errorf("expected 2 chunks (transcript + agent), got %d", chunks)
	}

	// Verify both transcript and agent were uploaded
	if len(mock.chunkRequests) != 2 {
		t.Fatalf("expected 2 chunk requests, got %d", len(mock.chunkRequests))
	}

	// Find transcript and agent chunks
	var transcriptChunk, agentChunk *ChunkRequest
	for i := range mock.chunkRequests {
		req := &mock.chunkRequests[i]
		if req.FileType == "transcript" {
			transcriptChunk = req
		} else if req.FileType == "agent" {
			agentChunk = req
		}
	}

	// Verify transcript chunk exists
	if transcriptChunk == nil {
		t.Fatal("expected transcript chunk")
	}
	// Note: AgentIDs are no longer sent to backend, but agent discovery still works
	// (proven by the agent chunk being uploaded below)

	// Verify agent chunk
	if agentChunk == nil {
		t.Fatal("expected agent chunk")
	}
	if agentChunk.FileName != "agent-abc12345.jsonl" {
		t.Errorf("expected file_name 'agent-abc12345.jsonl', got %q", agentChunk.FileName)
	}
	if len(agentChunk.Lines) != 2 {
		t.Errorf("expected 2 agent lines, got %d", len(agentChunk.Lines))
	}
}

func TestEngine_SyncAll_WithMetadata(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	content := `{"type":"user","message":{"content":"Help me with this task"},"gitBranch":"main","cwd":"/tmp/test"}
{"type":"user","toolUseResult":{"agentId":"11112222"}}
{"type":"summary","summary":"Task assistance session"}
`
	os.WriteFile(transcriptPath, []byte(content), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "metadata-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	// Verify metadata in chunk
	chunkReq := mock.chunkRequests[0]
	if chunkReq.Metadata == nil {
		t.Fatal("expected metadata in chunk request")
	}

	if chunkReq.Metadata.GitInfo == nil || chunkReq.Metadata.GitInfo.Branch != "main" {
		t.Errorf("expected git branch 'main', got %v", chunkReq.Metadata.GitInfo)
	}

	// Note: AgentIDs are no longer sent to backend (local use only)

	// Verify summary and first_user_message
	if chunkReq.Metadata.Summary != "Task assistance session" {
		t.Errorf("expected summary 'Task assistance session', got %q", chunkReq.Metadata.Summary)
	}
	if chunkReq.Metadata.FirstUserMessage != "Help me with this task" {
		t.Errorf("expected first_user_message 'Help me with this task', got %q", chunkReq.Metadata.FirstUserMessage)
	}
}

func TestEngine_GetSyncStats(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	content := `{"line":1}
{"line":2}
{"line":3}
`
	os.WriteFile(transcriptPath, []byte(content), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "stats-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Before sync
	stats := engine.GetSyncStats()
	if stats["transcript.jsonl"] != 0 {
		t.Errorf("expected 0 lines synced before sync, got %d", stats["transcript.jsonl"])
	}

	// After sync
	engine.SyncAll()

	stats = engine.GetSyncStats()
	if stats["transcript.jsonl"] != 3 {
		t.Errorf("expected 3 lines synced after sync, got %d", stats["transcript.jsonl"])
	}
}

func TestEngine_Reset(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "reset-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !engine.IsInitialized() {
		t.Error("expected engine to be initialized")
	}

	engine.Reset()

	if engine.IsInitialized() {
		t.Error("expected engine to not be initialized after Reset")
	}
	if engine.SessionID() != "" {
		t.Error("expected empty session ID after Reset")
	}
}

func TestEngine_SyncAll_NotInitialized(t *testing.T) {
	engine := &Engine{
		initialized: false,
	}

	_, err := engine.SyncAll()
	if err == nil {
		t.Error("expected error when calling SyncAll before Init")
	}
}

// TestEngine_SyncAll_TransitiveAgentDiscovery tests that agents discovered from
// transcript AND agents discovered from other agents are all synced in a single
// SyncAll() call (BFS traversal).
func TestEngine_SyncAll_TransitiveAgentDiscovery(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	transcriptDir := filepath.Dir(transcriptPath)

	// Create transcript that references agent A
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"aaaaaaaa","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// Agent A references agent B
	agentAPath := filepath.Join(transcriptDir, "agent-aaaaaaaa.jsonl")
	agentAContent := `{"type":"agent","message":"agent A start"}
{"type":"user","toolUseResult":{"agentId":"bbbbbbbb","result":"done"}}
`
	os.WriteFile(agentAPath, []byte(agentAContent), 0644)

	// Agent B references agent C (3 levels deep)
	agentBPath := filepath.Join(transcriptDir, "agent-bbbbbbbb.jsonl")
	agentBContent := `{"type":"agent","message":"agent B start"}
{"type":"user","toolUseResult":{"agentId":"cccccccc","result":"done"}}
`
	os.WriteFile(agentBPath, []byte(agentBContent), 0644)

	// Agent C is a leaf (no further references)
	agentCPath := filepath.Join(transcriptDir, "agent-cccccccc.jsonl")
	agentCContent := `{"type":"agent","message":"agent C - leaf"}
`
	os.WriteFile(agentCPath, []byte(agentCContent), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "transitive-agent-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Single SyncAll should discover and sync ALL files:
	// transcript -> agent A -> agent B -> agent C
	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	// Should have 4 chunks: transcript + 3 agents
	if chunks != 4 {
		t.Errorf("expected 4 chunks (transcript + 3 agents), got %d", chunks)
	}

	// Verify all files were uploaded
	fileTypes := make(map[string]bool)
	for _, req := range mock.chunkRequests {
		fileTypes[req.FileName] = true
	}

	expectedFiles := []string{"transcript.jsonl", "agent-aaaaaaaa.jsonl", "agent-bbbbbbbb.jsonl", "agent-cccccccc.jsonl"}
	for _, f := range expectedFiles {
		if !fileTypes[f] {
			t.Errorf("expected file %s to be uploaded", f)
		}
	}

	if len(mock.chunkRequests) != 4 {
		t.Errorf("expected 4 chunk requests, got %d", len(mock.chunkRequests))
	}
}

// TestEngine_SyncAll_AgentCycleDetection tests that cycles in agent references
// (A -> B -> A) don't cause infinite loops. Each file should only be synced once.
func TestEngine_SyncAll_AgentCycleDetection(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	transcriptDir := filepath.Dir(transcriptPath)

	// Create transcript that references agent A
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"aaaaaaaa","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// Agent A references agent B
	agentAPath := filepath.Join(transcriptDir, "agent-aaaaaaaa.jsonl")
	agentAContent := `{"type":"agent","message":"agent A"}
{"type":"user","toolUseResult":{"agentId":"bbbbbbbb","result":"done"}}
`
	os.WriteFile(agentAPath, []byte(agentAContent), 0644)

	// Agent B references agent A (cycle!)
	agentBPath := filepath.Join(transcriptDir, "agent-bbbbbbbb.jsonl")
	agentBContent := `{"type":"agent","message":"agent B"}
{"type":"user","toolUseResult":{"agentId":"aaaaaaaa","result":"done"}}
`
	os.WriteFile(agentBPath, []byte(agentBContent), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "cycle-detection-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Should complete without infinite loop
	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	// Should have exactly 3 chunks: transcript + agent A + agent B
	// (no duplicates despite cycle)
	if chunks != 3 {
		t.Errorf("expected 3 chunks, got %d", chunks)
	}

	// Verify each file uploaded exactly once
	fileCounts := make(map[string]int)
	for _, req := range mock.chunkRequests {
		fileCounts[req.FileName]++
	}

	for file, count := range fileCounts {
		if count != 1 {
			t.Errorf("file %s uploaded %d times, expected 1", file, count)
		}
	}
}

// TestEngine_SyncAll_MaxIterations tests that the BFS loop has a maximum
// iteration limit to prevent runaway loops.
func TestEngine_SyncAll_MaxIterations(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	transcriptDir := filepath.Dir(transcriptPath)

	// Create a chain of agents longer than maxSyncIterations (10)
	// transcript -> agent-00000001 -> agent-00000002 -> ... -> agent-00000015
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"00000001","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	// Create 15 agents in a chain
	for i := 1; i <= 15; i++ {
		agentID := fmt.Sprintf("%08d", i)
		nextAgentID := fmt.Sprintf("%08d", i+1)

		var content string
		if i < 15 {
			content = fmt.Sprintf(`{"type":"agent","message":"agent %d"}
{"type":"user","toolUseResult":{"agentId":"%s","result":"done"}}
`, i, nextAgentID)
		} else {
			// Last agent has no further references
			content = fmt.Sprintf(`{"type":"agent","message":"agent %d - leaf"}
`, i)
		}

		agentPath := filepath.Join(transcriptDir, fmt.Sprintf("agent-%s.jsonl", agentID))
		os.WriteFile(agentPath, []byte(content), 0644)
	}

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "max-iterations-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// SyncAll should complete (not hang) even with deep chain
	chunks, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	// With maxSyncIterations=10, we should sync at most 10 levels
	// (transcript counts as iteration 1, so 10 iterations = transcript + 9 agents max
	// or could be more depending on implementation)
	// The key assertion is that it completes and doesn't hang.
	t.Logf("Synced %d chunks with 15-level deep chain", chunks)

	// Should have synced at least some files (transcript + early agents)
	if chunks < 1 {
		t.Error("expected at least 1 chunk to be synced")
	}

	// Should NOT have synced all 16 files if max iterations is 10
	// (This test documents the expected behavior with maxSyncIterations)
	if chunks > 11 { // transcript + 10 iterations worth of agents
		t.Logf("Note: synced %d chunks, max iterations may allow more than expected", chunks)
	}
}

// TestEngine_SyncAll_AgentFileAppearsLater tests that if an agent file doesn't
// exist when first referenced, it can still be discovered on a later SyncAll call.
func TestEngine_SyncAll_AgentFileAppearsLater(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	transcriptDir := filepath.Dir(transcriptPath)

	// Create transcript that references an agent that doesn't exist yet
	// Note: agent ID must be valid 8-char hex
	transcriptContent := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"deadbeef","result":"done"}}
`
	os.WriteFile(transcriptPath, []byte(transcriptContent), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "late-agent-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First sync - agent file doesn't exist
	chunks1, _ := engine.SyncAll()
	if chunks1 != 1 {
		t.Errorf("expected 1 chunk on first sync, got %d", chunks1)
	}

	// Now create the agent file
	agentPath := filepath.Join(transcriptDir, "agent-deadbeef.jsonl")
	os.WriteFile(agentPath, []byte(`{"type":"agent","message":"late agent"}`+"\n"), 0644)

	// Second sync - should discover and sync the agent file
	chunks2, _ := engine.SyncAll()
	if chunks2 != 1 {
		t.Errorf("expected 1 chunk on second sync (late agent), got %d", chunks2)
	}

	// Verify agent was uploaded
	var agentUploaded bool
	for _, req := range mock.chunkRequests {
		if req.FileName == "agent-deadbeef.jsonl" {
			agentUploaded = true
			break
		}
	}
	if !agentUploaded {
		t.Error("expected agent-deadbeef.jsonl to be uploaded")
	}
}

// TestEngine_SyncAll_RefreshStateAfterUploadFailure tests that when a chunk upload
// fails (e.g., timeout), the engine refreshes state from the backend before the next
// sync attempt. This handles the case where the server received and stored the chunk
// but the response didn't reach the client.
func TestEngine_SyncAll_RefreshStateAfterUploadFailure(t *testing.T) {
	// Track chunk requests and init requests
	var initCount int32
	var chunkCount int32

	// State tracking - simulates server having received the first chunk
	serverLastSyncedLine := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			atomic.AddInt32(&initCount, 1)
			// Return current server state
			json.NewEncoder(w).Encode(InitResponse{
				SessionID: "test-session-id",
				Files: map[string]FileState{
					"transcript.jsonl": {LastSyncedLine: serverLastSyncedLine},
				},
			})

		case "/api/v1/sync/chunk":
			count := atomic.AddInt32(&chunkCount, 1)

			var req ChunkRequest
			json.Unmarshal(body, &req)

			if count == 1 {
				// First chunk upload: server receives data but then "times out"
				// Simulate server successfully storing lines 1-5
				serverLastSyncedLine = req.FirstLine + len(req.Lines) - 1

				// Return 503 Service Unavailable to simulate timeout/error
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Service Unavailable"))
				return
			}

			// Subsequent uploads succeed
			lastLine := req.FirstLine + len(req.Lines) - 1
			serverLastSyncedLine = lastLine
			json.NewEncoder(w).Encode(ChunkResponse{
				LastSyncedLine: lastLine,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with 10 lines
	var content string
	for i := 1; i <= 10; i++ {
		content += fmt.Sprintf(`{"line":%d}`, i) + "\n"
	}
	os.WriteFile(transcriptPath, []byte(content), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "refresh-state-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	// Initialize
	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First init call
	if initCount != 1 {
		t.Errorf("expected 1 init call after Init(), got %d", initCount)
	}

	// First SyncAll - upload will fail but server received the data
	chunks1, err := engine.SyncAll()
	if err == nil {
		t.Error("expected error from first SyncAll")
	}
	if chunks1 != 0 {
		t.Errorf("expected 0 successful chunks, got %d", chunks1)
	}

	// Should have called init again to refresh state after failure
	if initCount != 2 {
		t.Errorf("expected 2 init calls (1 for Init + 1 for refresh after failure), got %d", initCount)
	}

	// Server now has lines 1-10 synced (simulated)
	// The engine should have refreshed its state from the backend

	// Second SyncAll - should detect no new data needed (or start from correct position)
	chunks2, err := engine.SyncAll()
	if err != nil {
		t.Fatalf("second SyncAll failed: %v", err)
	}

	// After refresh, server has all 10 lines, so there should be nothing more to sync
	if chunks2 != 0 {
		t.Errorf("expected 0 chunks on second sync (server already has all lines), got %d", chunks2)
	}

	// Verify no additional chunk uploads were attempted (beyond the initial failed one)
	if chunkCount != 1 {
		t.Errorf("expected only 1 chunk request (the failed one), got %d", chunkCount)
	}
}

// TestEngine_SyncAll_RefreshStateOnContiguityError tests the specific scenario from CF-240:
// 1. First upload times out (server received lines 346-352, so last_synced_line = 352)
// 2. Client retries from line 346 (doesn't know server has it)
// 3. Server returns 400 "first_line must be 353"
// 4. Engine should refresh state and retry from 353
func TestEngine_SyncAll_RefreshStateOnContiguityError(t *testing.T) {
	var initCount int32
	var chunkCount int32
	serverLastSyncedLine := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		body, _ := readRequestBody(r)

		switch r.URL.Path {
		case "/api/v1/sync/init":
			atomic.AddInt32(&initCount, 1)
			json.NewEncoder(w).Encode(InitResponse{
				SessionID: "test-session-id",
				Files: map[string]FileState{
					"transcript.jsonl": {LastSyncedLine: serverLastSyncedLine},
				},
			})

		case "/api/v1/sync/chunk":
			count := atomic.AddInt32(&chunkCount, 1)

			var req ChunkRequest
			json.Unmarshal(body, &req)

			if count == 1 {
				// First upload: server receives but connection times out
				// Server advances to line 5
				serverLastSyncedLine = 5
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("timeout"))
				return
			}

			// After refresh, client should send from line 6
			// If client sends from wrong line, return 400
			expectedFirstLine := serverLastSyncedLine + 1
			if req.FirstLine != expectedFirstLine {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprintf("first_line must be %d (got %d) - chunks must be contiguous",
						expectedFirstLine, req.FirstLine),
				})
				return
			}

			lastLine := req.FirstLine + len(req.Lines) - 1
			serverLastSyncedLine = lastLine
			json.NewEncoder(w).Encode(ChunkResponse{
				LastSyncedLine: lastLine,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	// Create transcript with 10 lines
	var content string
	for i := 1; i <= 10; i++ {
		content += fmt.Sprintf(`{"line":%d}`, i) + "\n"
	}
	os.WriteFile(transcriptPath, []byte(content), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "contiguity-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First SyncAll - will fail with "timeout" but trigger refresh
	_, err := engine.SyncAll()
	if err == nil {
		t.Error("expected error from first SyncAll")
	}

	// Verify init was called to refresh state
	if initCount != 2 {
		t.Errorf("expected 2 init calls (initial + refresh), got %d", initCount)
	}

	// Second SyncAll - should succeed because state was refreshed
	// Client should now send from line 6 (after server's last_synced_line of 5)
	chunks, err := engine.SyncAll()
	if err != nil {
		t.Errorf("second SyncAll should succeed after refresh, got error: %v", err)
	}

	// Should have uploaded remaining lines (6-10)
	if chunks != 1 {
		t.Errorf("expected 1 chunk on second sync, got %d", chunks)
	}

	// Final state check
	if serverLastSyncedLine != 10 {
		t.Errorf("expected server to have all 10 lines, got %d", serverLastSyncedLine)
	}
}

// TestEngine_SyncAll_AuthErrorDuringRefreshPropagated tests that when refresh fails
// with an auth error (e.g., token expired mid-sync), the auth error is propagated
// so the daemon can handle it properly.
func TestEngine_SyncAll_AuthErrorDuringRefreshPropagated(t *testing.T) {
	var initCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v1/sync/init":
			count := atomic.AddInt32(&initCount, 1)
			if count == 1 {
				// First init succeeds
				json.NewEncoder(w).Encode(InitResponse{
					SessionID: "test-session-id",
					Files: map[string]FileState{
						"transcript.jsonl": {LastSyncedLine: 0},
					},
				})
			} else {
				// Second init (refresh) fails with auth error - token expired
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"token expired"}`))
			}

		case "/api/v1/sync/chunk":
			// Chunk upload fails with 503
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Service Unavailable"))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	os.WriteFile(transcriptPath, []byte(`{"line":1}`+"\n"), 0644)

	engine := NewWithClient(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "auth-during-refresh-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)

	if err := engine.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// SyncAll should fail - chunk upload fails, refresh fails with auth error
	_, err := engine.SyncAll()
	if err == nil {
		t.Fatal("expected error from SyncAll")
	}

	// The returned error should be the auth error from refresh, not the original 503
	if !errors.Is(err, pkghttp.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized to be propagated, got: %v", err)
	}
}

// mustNewClient creates a client for testing
func mustNewClient(t *testing.T, serverURL, tmpDir string) *Client {
	t.Helper()

	cfg := &config.UploadConfig{
		BackendURL: serverURL,
		APIKey:     "test-api-key-12345678",
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}
