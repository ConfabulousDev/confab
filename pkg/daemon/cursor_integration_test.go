package daemon

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/sync"
)

// cursorTranscriptLines returns realistic Cursor JSONL transcript lines.
// Cursor messages are {role, message:{content:[...]}} with NO top-level
// "type" field; user content carries a <user_query> sentinel; assistant
// content carries text + tool_use elements; turn boundaries are
// {type:"turn_ended", status}. Verified shape (kata 6kys §7).
func cursorTranscriptLines() []string {
	return []string{
		`{"role":"user","message":{"content":[{"type":"text","text":"<user_query>add a widget</user_query>"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"On it."},{"type":"tool_use","name":"Shell","input":{"command":"ls"}}]}}`,
		`{"type":"turn_ended","status":"completed"}`,
	}
}

// setupCursorTestEnv mirrors setupTestEnv but lays the transcript out under a
// Cursor-shaped projects path so provider=cursor validation (transcript under
// the Cursor projects dir) passes if exercised. The daemon itself does not
// re-validate the path, but keeping the layout realistic guards against future
// path-coupling regressions.
func setupCursorTestEnv(t *testing.T, serverURL string) (tmpDir, transcriptPath string) {
	t.Helper()
	tmpDir = t.TempDir()

	confabDir := filepath.Join(tmpDir, ".confab")
	if err := os.MkdirAll(confabDir, 0o755); err != nil {
		t.Fatalf("mkdir confab: %v", err)
	}
	configPath := filepath.Join(confabDir, "config.json")
	configJSON := `{"backend_url":"` + serverURL + `","api_key":"test-api-key-12345678"}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFAB_CONFIG_PATH", configPath)
	t.Setenv("HOME", tmpDir)

	// Point the Cursor state dir at the temp home and build the canonical
	// agent-transcripts layout: <state>/projects/<sanitized>/agent-transcripts/<id>/<id>.jsonl
	cursorDir := filepath.Join(tmpDir, ".cursor")
	t.Setenv(provider.CursorStateDirEnv, cursorDir)
	sessionID := "cursor-session-abc123"
	transcriptDir := filepath.Join(cursorDir, "projects", "ws", "agent-transcripts", sessionID)
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatalf("mkdir transcript: %v", err)
	}
	transcriptPath = filepath.Join(transcriptDir, sessionID+".jsonl")
	return tmpDir, transcriptPath
}

// TestCursorDaemonSyncCycle proves the vertical slice: a provider=cursor daemon
// runs the standard file-watch path (no opencode collector), inits the mock
// backend, and uploads the transcript as a "transcript" chunk. Cursor reuses
// the Claude file-first lifecycle with NO daemon-side cursor branch.
func TestCursorDaemonSyncCycle(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupCursorTestEnv(t, server.URL)

	content := strings.Join(cursorTranscriptLines(), "\n") + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d := New(Config{
		Provider:       provider.NameCursor,
		ExternalID:     "cursor-session-abc123",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit")
	}

	// Init must carry provider=cursor and the cursor transcript path.
	initReqs := mock.getInitRequests()
	if len(initReqs) == 0 {
		t.Fatal("expected init request, got none")
	}
	if initReqs[0].Provider != provider.NameCursor {
		t.Errorf("init provider = %q, want %q", initReqs[0].Provider, provider.NameCursor)
	}
	if initReqs[0].TranscriptPath != transcriptPath {
		t.Errorf("init transcript_path = %q, want %q", initReqs[0].TranscriptPath, transcriptPath)
	}

	// Transcript chunk uploaded with all three lines.
	chunkReqs := mock.getChunkRequests()
	if len(chunkReqs) == 0 {
		t.Fatal("expected chunk request, got none")
	}
	var transcriptChunk *sync.ChunkRequest
	for i := range chunkReqs {
		if chunkReqs[i].FileType == "transcript" {
			transcriptChunk = &chunkReqs[i]
			break
		}
	}
	if transcriptChunk == nil {
		t.Fatal("expected a transcript chunk upload")
	}
	if len(transcriptChunk.Lines) != 3 {
		t.Errorf("transcript chunk lines = %d, want 3", len(transcriptChunk.Lines))
	}
	if transcriptChunk.FirstLine != 1 {
		t.Errorf("transcript chunk first_line = %d, want 1", transcriptChunk.FirstLine)
	}
}

// TestCursorDaemonIncrementalAndFinalSync drives the full lifecycle: an initial
// sync, an incremental append, and a final sync on shutdown — the same
// guarantees the Claude path gives, proven for provider=cursor.
func TestCursorDaemonIncrementalAndFinalSync(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupCursorTestEnv(t, server.URL)

	// Start with the first user line only.
	first := cursorTranscriptLines()[0] + "\n"
	if err := os.WriteFile(transcriptPath, []byte(first), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	d := New(Config{
		Provider:       provider.NameCursor,
		ExternalID:     "cursor-session-abc123",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Let the initial sync land.
	time.Sleep(150 * time.Millisecond)
	initialChunks := len(mock.getChunkRequests())
	if initialChunks == 0 {
		t.Fatal("expected an initial chunk upload")
	}

	// Append two more lines; the incremental sync must pick them up.
	f, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	for _, l := range cursorTranscriptLines()[1:] {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	f.Close()

	time.Sleep(200 * time.Millisecond)

	// Cancel to trigger final sync, then confirm clean exit.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("daemon Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not shut down")
	}

	// Across init + incremental + final sync, all three lines must arrive,
	// and a later chunk must start past line 1 (incremental progress).
	chunkReqs := mock.getChunkRequests()
	maxLine := 0
	sawIncremental := false
	for _, c := range chunkReqs {
		if c.FileType != "transcript" {
			continue
		}
		if c.FirstLine > 1 {
			sawIncremental = true
		}
		end := c.FirstLine + len(c.Lines) - 1
		if end > maxLine {
			maxLine = end
		}
	}
	if maxLine < 3 {
		t.Errorf("highest synced line = %d, want >= 3 (all lines uploaded)", maxLine)
	}
	if !sawIncremental {
		t.Error("expected at least one incremental chunk starting past line 1")
	}
}
