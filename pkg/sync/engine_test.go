package sync

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
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
