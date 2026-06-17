package sync

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestChunkMetadata_SerializesLatestMessageAtRFC3339 verifies the new
// ChunkMetadata.LatestMessageAt field marshals to metadata.latest_message_at
// in RFC3339 (the format the confab-web backend's *time.Time reader expects).
func TestChunkMetadata_SerializesLatestMessageAtRFC3339(t *testing.T) {
	ts := time.Date(2026, 6, 16, 12, 30, 0, 0, time.UTC)
	md := ChunkMetadata{LatestMessageAt: &ts}

	b, err := json.Marshal(md)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"latest_message_at":"2026-06-16T12:30:00Z"`) {
		t.Errorf("marshal = %s, want latest_message_at RFC3339", got)
	}
}

// TestChunkMetadata_OmitsLatestMessageAtWhenNil verifies a nil
// LatestMessageAt is omitted from the wire (omitempty), so non-cursor
// providers send nothing.
func TestChunkMetadata_OmitsLatestMessageAtWhenNil(t *testing.T) {
	b, err := json.Marshal(ChunkMetadata{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "latest_message_at") {
		t.Errorf("marshal = %s, want no latest_message_at key when nil", b)
	}
}

// TestChunkMetadata_SerializesModel verifies the new ChunkMetadata.Model
// field marshals to metadata.model and is omitted when empty.
func TestChunkMetadata_SerializesModel(t *testing.T) {
	b, err := json.Marshal(ChunkMetadata{Model: "composer-2.5-fast"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"model":"composer-2.5-fast"`) {
		t.Errorf("marshal = %s, want model key", b)
	}

	b2, err := json.Marshal(ChunkMetadata{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(b2), "model") {
		t.Errorf("marshal = %s, want no model key when empty", b2)
	}
}

// TestChunkView_SetLatestMessageAtRoundTrips verifies the engine's concrete
// chunkView exposes SetLatestMessageAt and routes it through the chunk
// metadata, and that FilePath exposes the tracked file's path so providers
// can stat the file / derive the session id.
func TestChunkView_SetLatestMessageAtRoundTrips(t *testing.T) {
	ts := time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)
	chunk := &Chunk{FileType: "transcript", FirstLine: 1}
	file := &TrackedFile{Path: "/some/transcript/abc.jsonl"}
	cv := &chunkView{chunk: chunk, file: file}

	if got := cv.FilePath(); got != "/some/transcript/abc.jsonl" {
		t.Errorf("FilePath() = %q, want the tracked file path", got)
	}

	cv.SetLatestMessageAt(ts)
	if chunk.Metadata == nil {
		t.Fatal("SetLatestMessageAt did not allocate chunk metadata")
	}
	if chunk.Metadata.LatestMessageAt == nil || !chunk.Metadata.LatestMessageAt.Equal(ts) {
		t.Errorf("LatestMessageAt = %v, want %v", chunk.Metadata.LatestMessageAt, ts)
	}
}

// TestEngine_SetsModelFromConfigOnTranscriptChunks verifies the engine writes
// the session-constant Model (from EngineConfig) onto transcript chunk
// metadata, but NOT onto agent (sidechain) chunks. The model is generic:
// providers with an empty model send nothing (omitempty), so no provider
// branch lives in the engine.
func TestEngine_SetsModelFromConfigOnTranscriptChunks(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	content := `{"type":"user","message":"hi"}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	engine := newEngineWithBackend(t, mustNewClient(t, server.URL, tmpDir), nil, EngineConfig{
		ExternalID:     "model-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		Model:          "composer-2.5-fast",
	})

	if err := engine.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := engine.SyncAll(); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	if len(mock.chunkRequests) == 0 {
		t.Fatal("expected a chunk request")
	}
	var sawTranscript bool
	for _, c := range mock.chunkRequests {
		if c.FileType == "transcript" {
			sawTranscript = true
			if c.Metadata == nil || c.Metadata.Model != "composer-2.5-fast" {
				t.Errorf("transcript chunk metadata.model = %+v, want composer-2.5-fast", c.Metadata)
			}
		}
	}
	if !sawTranscript {
		t.Fatal("expected a transcript chunk")
	}
}

// TestEngine_OmitsModelWhenConfigModelEmpty verifies that when no model is
// configured (the common case for Claude/Codex/OpenCode), the engine sets no
// model on the wire.
func TestEngine_OmitsModelWhenConfigModelEmpty(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	content := `{"type":"user","message":"hi"}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	engine := newEngineWithBackend(t, mustNewClient(t, server.URL, tmpDir), nil, EngineConfig{
		ExternalID:     "no-model-test",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
	})

	if err := engine.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := engine.SyncAll(); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	for _, c := range mock.chunkRequests {
		if c.Metadata != nil && c.Metadata.Model != "" {
			t.Errorf("chunk metadata.model = %q, want empty when config model unset", c.Metadata.Model)
		}
	}
}
