package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewFileTracker(t *testing.T) {
	ft := NewFileTracker("/path/to/transcript.jsonl")

	if ft.transcriptPath != "/path/to/transcript.jsonl" {
		t.Errorf("expected transcriptPath '/path/to/transcript.jsonl', got %q", ft.transcriptPath)
	}
	if ft.transcriptDir != "/path/to" {
		t.Errorf("expected transcriptDir '/path/to', got %q", ft.transcriptDir)
	}
	if ft.files == nil {
		t.Error("expected files map to be initialized")
	}
	if ft.knownAgentIDs == nil {
		t.Error("expected knownAgentIDs map to be initialized")
	}
}

func TestFileTracker_InitFromBackendState(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	ft := NewFileTracker(transcriptPath)

	state := map[string]FileState{
		"transcript.jsonl":     {LastSyncedLine: 100},
		"agent-abc12345.jsonl": {LastSyncedLine: 50},
		"agent-def67890.jsonl": {LastSyncedLine: 25},
	}

	ft.InitFromBackendState(state)

	files := ft.GetTrackedFiles()
	if len(files) != 3 {
		t.Errorf("expected 3 tracked files, got %d", len(files))
	}

	// Check transcript
	found := false
	for _, f := range files {
		if f.Name == "transcript.jsonl" {
			found = true
			if f.Type != "transcript" {
				t.Errorf("expected transcript type, got %q", f.Type)
			}
			if f.LastSyncedLine != 100 {
				t.Errorf("expected LastSyncedLine 100, got %d", f.LastSyncedLine)
			}
		}
	}
	if !found {
		t.Error("transcript not found in tracked files")
	}
}

func TestFileTracker_ReadChunk_AllLines(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create test file with some lines
	content := `{"line": 1}
{"line": 2}
{"line": 3}
{"line": 4}
{"line": 5}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk == nil {
		t.Fatal("expected chunk, got nil")
	}

	if len(chunk.Lines) != 5 {
		t.Errorf("expected 5 lines, got %d", len(chunk.Lines))
	}
	if chunk.FirstLine != 1 {
		t.Errorf("expected FirstLine 1, got %d", chunk.FirstLine)
	}
	if chunk.NewOffset == 0 {
		t.Error("expected NewOffset to be set")
	}
}

func TestFileTracker_ReadChunk_Incremental(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	content := `{"line": 1}
{"line": 2}
{"line": 3}
{"line": 4}
{"line": 5}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 2}, // Backend has first 2 lines
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk == nil {
		t.Fatal("expected chunk, got nil")
	}

	if len(chunk.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(chunk.Lines))
	}
	if chunk.FirstLine != 3 {
		t.Errorf("expected FirstLine 3, got %d", chunk.FirstLine)
	}
	if chunk.Lines[0] != `{"line": 3}` {
		t.Errorf("expected first line to be '{\"line\": 3}', got %q", chunk.Lines[0])
	}
}

func TestFileTracker_ReadChunk_NoNewLines(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	content := `{"line": 1}
{"line": 2}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 2}, // Already synced all lines
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk != nil {
		t.Errorf("expected nil chunk when no new lines, got %+v", chunk)
	}
}

func TestFileTracker_ReadChunk_ExtractsAgentIDs(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	content := `{"type": "user", "toolUseResult": {"agentId": "abc12345"}}
{"type": "assistant", "message": "hello"}
{"type": "user", "toolUseResult": {"agentId": "def67890"}}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if len(chunk.AgentIDs) != 2 {
		t.Errorf("expected 2 agent IDs, got %d", len(chunk.AgentIDs))
	}

	// Check that both IDs are present
	found := make(map[string]bool)
	for _, id := range chunk.AgentIDs {
		found[id] = true
	}
	if !found["abc12345"] || !found["def67890"] {
		t.Errorf("expected agent IDs abc12345 and def67890, got %v", chunk.AgentIDs)
	}
}

func TestFileTracker_ReadChunk_ExtractsGitInfo(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	content := `{"type": "user", "message": "hello", "gitBranch": "main", "cwd": "/tmp/test"}
{"type": "assistant", "message": "hi"}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk.Metadata == nil {
		t.Fatal("expected metadata, got nil")
	}

	if chunk.Metadata.GitInfo == nil {
		t.Fatal("expected GitInfo, got nil")
	}

	if chunk.Metadata.GitInfo.Branch != "main" {
		t.Errorf("expected branch 'main', got %q", chunk.Metadata.GitInfo.Branch)
	}
}

func TestFileTracker_ByteOffset_Seeking(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create initial content
	content := `{"line": 1}
{"line": 2}
{"line": 3}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// First read - should get all 3 lines
	chunk1, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if len(chunk1.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(chunk1.Lines))
	}

	// Update state after "sync"
	ft.UpdateAfterSync(file, 3, chunk1.NewOffset)

	// Append more content
	f, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	f.WriteString(`{"line": 4}` + "\n")
	f.WriteString(`{"line": 5}` + "\n")
	f.Close()

	// Force file change detection
	file.LastModTime = file.LastModTime.Add(-1)

	// Second read - should only get lines 4-5 using byte offset
	chunk2, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}

	if chunk2 == nil {
		t.Fatal("expected chunk2, got nil")
	}

	if len(chunk2.Lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(chunk2.Lines))
	}
	if chunk2.FirstLine != 4 {
		t.Errorf("expected FirstLine 4, got %d", chunk2.FirstLine)
	}
	if chunk2.Lines[0] != `{"line": 4}` {
		t.Errorf("expected first line '{\"line\": 4}', got %q", chunk2.Lines[0])
	}
}

func TestFileTracker_HasFileChanged(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.jsonl")

	// Create initial file
	if err := os.WriteFile(testFile, []byte(`{"line": 1}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(filepath.Join(tmpDir, "transcript.jsonl"))
	tracked := &TrackedFile{
		Path:           testFile,
		Name:           "test.jsonl",
		Type:           "transcript",
		LastSyncedLine: 0,
	}

	// First call should return true (no cached state yet)
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true on first call")
	}

	// HasFileChanged does NOT cache values - only UpdateAfterSync does.
	// So calling it again should still return true (file still needs syncing)
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true again (no sync happened)")
	}

	// Simulate a successful sync - this updates the cached state
	ft.UpdateAfterSync(tracked, 1, 12)

	// Now HasFileChanged should return false (file synced, no new changes)
	if ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return false after successful sync")
	}

	// Modify file - should return true
	if err := os.WriteFile(testFile, []byte(`{"line": 1}{"line": 2}`), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true after file modification")
	}

	// Without a sync, should still return true (failed sync shouldn't prevent retry)
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true again (no sync after modification)")
	}

	// Simulate another successful sync
	ft.UpdateAfterSync(tracked, 2, 24)

	// Now should return false
	if ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return false after second successful sync")
	}
}

func TestFileTracker_DiscoverNewFiles(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create transcript (content doesn't matter for this test)
	if err := os.WriteFile(transcriptPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	// Create agent file
	agentPath := filepath.Join(tmpDir, "agent-abc12345.jsonl")
	if err := os.WriteFile(agentPath, []byte(`{"line": 1}`), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	// Discover new agent
	newFiles := ft.DiscoverNewFiles([]string{"abc12345"})

	if len(newFiles) != 1 {
		t.Errorf("expected 1 new file, got %d", len(newFiles))
	}

	if len(newFiles) > 0 {
		if newFiles[0].Name != "agent-abc12345.jsonl" {
			t.Errorf("expected agent-abc12345.jsonl, got %q", newFiles[0].Name)
		}
		if newFiles[0].Type != "agent" {
			t.Errorf("expected type 'agent', got %q", newFiles[0].Type)
		}
	}

	// Second discovery with same ID should return nothing
	newFiles2 := ft.DiscoverNewFiles([]string{"abc12345"})
	if len(newFiles2) != 0 {
		t.Errorf("expected 0 new files on second call, got %d", len(newFiles2))
	}
}

func TestFileTracker_DiscoverNewFiles_MissingAgent(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	if err := os.WriteFile(transcriptPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	// Try to discover agent that doesn't exist on disk
	newFiles := ft.DiscoverNewFiles([]string{"missing123"})

	if len(newFiles) != 0 {
		t.Errorf("expected 0 new files for missing agent, got %d", len(newFiles))
	}

	// Now create the file
	agentPath := filepath.Join(tmpDir, "agent-missing123.jsonl")
	if err := os.WriteFile(agentPath, []byte(`{"line": 1}`), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	// Call again - should now find the file since we re-check all known agent IDs
	newFiles2 := ft.DiscoverNewFiles([]string{}) // Empty list - just re-check known IDs
	if len(newFiles2) != 1 {
		t.Errorf("expected 1 new file after creation, got %d", len(newFiles2))
	}
}

func TestFileTracker_ReadChunk_MalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Mix of valid and invalid JSON
	content := `not valid json
{"type": "user", "toolUseResult": {"agentId": "abcd1234"}}
also not valid
{"type": "user", "gitBranch": "develop"}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	// Should still get all 4 lines
	if len(chunk.Lines) != 4 {
		t.Errorf("expected 4 lines, got %d", len(chunk.Lines))
	}

	// Should extract agent IDs from valid lines
	if len(chunk.AgentIDs) != 1 || chunk.AgentIDs[0] != "abcd1234" {
		t.Errorf("expected agent ID abcd1234, got %v", chunk.AgentIDs)
	}

	// Should extract git info into metadata
	if chunk.Metadata == nil || chunk.Metadata.GitInfo == nil || chunk.Metadata.GitInfo.Branch != "develop" {
		t.Errorf("expected branch 'develop', got %v", chunk.Metadata)
	}
}

func TestFileTracker_ReadChunk_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	if err := os.WriteFile(transcriptPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk != nil {
		t.Errorf("expected nil chunk for empty file, got %+v", chunk)
	}
}

func TestFileTracker_ReadChunk_LargeLines(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create a line with a large message field (500KB)
	largeMessage := make([]byte, 500*1024)
	for i := range largeMessage {
		largeMessage[i] = 'a'
	}

	content := `{"type": "session-start"}
{"type": "assistant", "message": "` + string(largeMessage) + `"}
{"type": "user", "gitBranch": "main"}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if len(chunk.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(chunk.Lines))
	}
}

func TestFileTracker_ReadChunk_ByteLimit(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create 10 lines of ~100 bytes each (~1KB total)
	var content string
	for i := 0; i < 10; i++ {
		content += `{"line":` + string(rune('0'+i)) + `,"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	}

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Use small limit (~300 bytes) to force chunking - should get ~3 lines per chunk
	maxBytes := 300

	// First read
	chunk1, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}

	if chunk1 == nil {
		t.Fatal("expected chunk1, got nil")
	}

	// Should have limited the chunk
	if len(chunk1.Lines) >= 10 {
		t.Errorf("expected chunk to be limited, but got all %d lines", len(chunk1.Lines))
	}
	if len(chunk1.Lines) < 1 {
		t.Errorf("expected at least 1 line in first chunk, got %d", len(chunk1.Lines))
	}

	t.Logf("First chunk: %d lines", len(chunk1.Lines))

	// Simulate sync
	ft.UpdateAfterSync(file, len(chunk1.Lines), chunk1.NewOffset)

	// Second read
	chunk2, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}

	if chunk2 == nil {
		t.Fatal("expected chunk2, got nil")
	}

	t.Logf("Second chunk: %d lines, FirstLine=%d", len(chunk2.Lines), chunk2.FirstLine)

	// FirstLine should continue from where we left off
	if chunk2.FirstLine != len(chunk1.Lines)+1 {
		t.Errorf("expected FirstLine %d, got %d", len(chunk1.Lines)+1, chunk2.FirstLine)
	}

	// Keep reading until done
	totalLines := len(chunk1.Lines) + len(chunk2.Lines)
	ft.UpdateAfterSync(file, chunk2.FirstLine+len(chunk2.Lines)-1, chunk2.NewOffset)

	for {
		chunk, err := ft.ReadChunk(file, nil, maxBytes)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if chunk == nil {
			break
		}
		totalLines += len(chunk.Lines)
		ft.UpdateAfterSync(file, chunk.FirstLine+len(chunk.Lines)-1, chunk.NewOffset)
	}

	// Total should be 10 lines
	if totalLines != 10 {
		t.Errorf("expected 10 total lines across all chunks, got %d", totalLines)
	}
}

func TestFileTracker_ReadChunk_SingleLineExceedsByteLimit(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create a line that's ~200 bytes
	content := `{"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	content += `{"line": 2}` + "\n"

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Use limit smaller than first line - should return an error
	maxBytes := 50

	_, err := ft.ReadChunk(file, nil, maxBytes)
	if err == nil {
		t.Fatal("expected error when line exceeds max chunk size, got nil")
	}

	// Error should mention the line number and sizes
	errStr := err.Error()
	if !strings.Contains(errStr, "line 1") || !strings.Contains(errStr, "exceeds max chunk size") {
		t.Errorf("expected error about line 1 exceeding max chunk size, got: %v", err)
	}
}

func TestFileTracker_ReadChunk_MiddleLineExceedsByteLimit(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create file where second line is too large
	content := `{"line": 1}` + "\n"
	content += `{"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	content += `{"line": 3}` + "\n"

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Limit allows first line but not second
	maxBytes := 50

	// First read should succeed with line 1
	chunk1, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if chunk1 == nil || len(chunk1.Lines) != 1 {
		t.Fatalf("expected 1 line in first chunk, got %v", chunk1)
	}

	ft.UpdateAfterSync(file, 1, chunk1.NewOffset)

	// Second read should fail on line 2
	_, err = ft.ReadChunk(file, nil, maxBytes)
	if err == nil {
		t.Fatal("expected error when line 2 exceeds max chunk size, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "line 2") || !strings.Contains(errStr, "exceeds max chunk size") {
		t.Errorf("expected error about line 2 exceeding max chunk size, got: %v", err)
	}
}

func TestFileTracker_HasFileChanged_ByteOffsetComparison(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.jsonl")

	// Create initial file with 3 lines
	content := `{"line": 1}
{"line": 2}
{"line": 3}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	info, _ := os.Stat(testFile)
	fileSize := info.Size()

	ft := NewFileTracker(filepath.Join(tmpDir, "transcript.jsonl"))

	// Simulate a file that's been partially synced with ByteOffset set
	tracked := &TrackedFile{
		Path:           testFile,
		Name:           "test.jsonl",
		Type:           "transcript",
		LastSyncedLine: 2,
		ByteOffset:     fileSize / 2, // Pretend we've read half the file
		LastModTime:    info.ModTime(),
		LastSize:       fileSize,
	}

	// File hasn't changed and ByteOffset < size, so there's more to read
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true when ByteOffset < file size")
	}

	// Now set ByteOffset to end of file
	tracked.ByteOffset = fileSize
	if ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return false when ByteOffset == file size and file unchanged")
	}

	// Append more data - file size increases, ByteOffset stays same
	f, _ := os.OpenFile(testFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"line": 4}` + "\n")
	f.Close()

	// ByteOffset < new size, so should return true
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true after file was appended")
	}
}

func TestFileTracker_ReadChunk_ByteLimitWithFileAppend(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create initial 5 lines of ~100 bytes each
	var content string
	for i := 0; i < 5; i++ {
		content += `{"line":` + string(rune('0'+i)) + `,"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	}

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Use limit that fits ~2 lines
	maxBytes := 220

	// First read - should get ~2 lines
	chunk1, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if chunk1 == nil || len(chunk1.Lines) < 1 {
		t.Fatal("expected chunk1 with at least 1 line")
	}

	firstChunkLines := len(chunk1.Lines)
	t.Logf("First chunk: %d lines", firstChunkLines)
	ft.UpdateAfterSync(file, firstChunkLines, chunk1.NewOffset)

	// Append more lines to the file WHILE we have pending data
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	for i := 5; i < 8; i++ {
		f.WriteString(`{"line":` + string(rune('0'+i)) + `,"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n")
	}
	f.Close()

	// Continue reading - should get remaining original lines plus new lines
	totalLines := firstChunkLines
	for {
		chunk, err := ft.ReadChunk(file, nil, maxBytes)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if chunk == nil {
			break
		}
		totalLines += len(chunk.Lines)
		ft.UpdateAfterSync(file, chunk.FirstLine+len(chunk.Lines)-1, chunk.NewOffset)
	}

	// Should have all 8 lines (5 original + 3 appended)
	if totalLines != 8 {
		t.Errorf("expected 8 total lines, got %d", totalLines)
	}
}

func TestFileTracker_ReadChunk_ByteLimitRespectsLineNumber(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create 6 lines with varying sizes
	content := `{"line": 1, "short": true}
{"line": 2, "data": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}
{"line": 3, "short": true}
{"line": 4, "data": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}
{"line": 5, "short": true}
{"line": 6, "short": true}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Read all chunks and verify line numbers are correct
	maxBytes := 150

	var allChunks []*Chunk
	for {
		chunk, err := ft.ReadChunk(file, nil, maxBytes)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if chunk == nil {
			break
		}
		allChunks = append(allChunks, chunk)
		ft.UpdateAfterSync(file, chunk.FirstLine+len(chunk.Lines)-1, chunk.NewOffset)
	}

	// Verify FirstLine values are consecutive
	expectedLine := 1
	for i, chunk := range allChunks {
		if chunk.FirstLine != expectedLine {
			t.Errorf("chunk %d: expected FirstLine %d, got %d", i, expectedLine, chunk.FirstLine)
		}
		expectedLine += len(chunk.Lines)
	}

	// Verify we got all 6 lines
	if expectedLine != 7 {
		t.Errorf("expected to end at line 7, ended at %d", expectedLine)
	}
}
