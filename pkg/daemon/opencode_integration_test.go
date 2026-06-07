package daemon

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/opencodetest"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

func runOpenCodeDaemon(t *testing.T, externalID string, d time.Duration) {
	t.Helper()
	dm := New(Config{
		Provider:     provider.NameOpencode,
		ExternalID:   externalID,
		CWD:          t.TempDir(),
		SyncInterval: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- dm.Run(ctx) }()
	time.Sleep(d)
	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit")
	}
}

func TestDaemonOpenCodeMaterializesAndUploads(t *testing.T) {
	const externalID = "ses_test"
	mock := newMockBackend(t)
	backend := httptest.NewServer(mock)
	defer backend.Close()

	// Build a fixture DB with two complete messages.
	db := opencodetest.NewDB(t)
	db.AddSession(externalID, "")
	db.AddMessage(externalID, "msg_1", opencodetest.UserTextMessage("hi"))
	db.AddPart("msg_1", "prt_1", opencodetest.TextPart("hi"))
	asst := opencodetest.AssistantMessageFinished("stop")
	asst["modelID"] = "claude-x"
	asst["providerID"] = "anthropic"
	db.AddMessage(externalID, "msg_2", asst)
	db.AddPart("msg_2", "prt_2", opencodetest.TextPart("yo"))

	// Point the production reader at the fixture via the env-var hook.
	t.Setenv(provider.OpenCodeDBEnv, db.Path())

	tmpDir, _ := setupTestEnv(t, backend.URL)
	runOpenCodeDaemon(t, externalID, 600*time.Millisecond)

	// Materialized file exists with both complete messages.
	matPath := filepath.Join(tmpDir, ".confab", "opencode", externalID, "messages.jsonl")
	data, err := os.ReadFile(matPath)
	if err != nil {
		t.Fatalf("materialized file missing: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Fatalf("materialized %d lines, want 2:\n%s", got, data)
	}

	// Init happened with the OpenCode provider + materialized transcript path.
	inits := mock.getInitRequests()
	if len(inits) == 0 {
		t.Fatal("expected an init request")
	}
	if inits[0].Provider != provider.NameOpencode {
		t.Errorf("init provider = %q, want %q", inits[0].Provider, provider.NameOpencode)
	}
	if inits[0].ExternalID != externalID {
		t.Errorf("init external_id = %q, want %q", inits[0].ExternalID, externalID)
	}
	if inits[0].TranscriptPath != matPath {
		t.Errorf("init transcript_path = %q, want %q", inits[0].TranscriptPath, matPath)
	}

	// Chunk uploaded as a transcript with both lines.
	chunks := mock.getChunkRequests()
	if len(chunks) == 0 {
		t.Fatal("expected a chunk upload")
	}
	total := 0
	for _, c := range chunks {
		if c.FileType != "transcript" {
			t.Errorf("chunk file_type = %q, want transcript", c.FileType)
		}
		total += len(c.Lines)
	}
	if total != 2 {
		t.Errorf("uploaded %d lines total, want 2", total)
	}
}

func TestDaemonOpenCodeNoEmptySession(t *testing.T) {
	const externalID = "ses_incomplete"
	mock := newMockBackend(t)
	backend := httptest.NewServer(mock)
	defer backend.Close()

	// Only an incomplete assistant message (no finish) -> nothing to emit.
	db := opencodetest.NewDB(t)
	db.AddSession(externalID, "")
	db.AddMessage(externalID, "msg_1", opencodetest.AssistantMessageStreaming())
	db.AddPart("msg_1", "prt_1", opencodetest.TextPart("..."))

	t.Setenv(provider.OpenCodeDBEnv, db.Path())

	tmpDir, _ := setupTestEnv(t, backend.URL)
	runOpenCodeDaemon(t, externalID, 400*time.Millisecond)

	// No materialized file, so backendSyncEnabled stays false: no empty session.
	matPath := filepath.Join(tmpDir, ".confab", "opencode", externalID, "messages.jsonl")
	if _, err := os.Stat(matPath); !os.IsNotExist(err) {
		t.Errorf("expected no materialized file, stat err = %v", err)
	}
	if inits := mock.getInitRequests(); len(inits) != 0 {
		t.Errorf("expected no init (no complete message), got %d", len(inits))
	}
}

// TestDaemonOpenCodeFinalReconcileCatchesLateMessages asserts that messages
// written to the SQLite DB after the collector's last poll but before shutdown
// are materialized and uploaded (CF-545).
func TestDaemonOpenCodeFinalReconcileCatchesLateMessages(t *testing.T) {
	const externalID = "ses_late"
	mock := newMockBackend(t)
	backend := httptest.NewServer(mock)
	defer backend.Close()

	// Build a fixture DB with only msg_1 initially.
	db := opencodetest.NewDB(t)
	db.AddSession(externalID, "")
	db.AddMessage(externalID, "msg_1", opencodetest.UserTextMessage("hi"))
	db.AddPart("msg_1", "prt_1", opencodetest.TextPart("hi"))

	t.Setenv(provider.OpenCodeDBEnv, db.Path())

	tmpDir, _ := setupTestEnv(t, backend.URL)

	// Start the daemon with a short sync interval so the collector polls quickly.
	dm := New(Config{
		Provider:     provider.NameOpencode,
		ExternalID:   externalID,
		CWD:          t.TempDir(),
		SyncInterval: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- dm.Run(ctx) }()

	// Wait for the first collector poll to materialize msg_1.
	time.Sleep(200 * time.Millisecond)

	// Now add msg_2 to the DB — simulating a message arriving after the last poll.
	asst := opencodetest.AssistantMessageFinished("stop")
	asst["modelID"] = "claude-x"
	asst["providerID"] = "anthropic"
	db.AddMessage(externalID, "msg_2", asst)
	db.AddPart("msg_2", "prt_2", opencodetest.TextPart("yo"))

	// Cancel — triggers shutdown with final reconcile.
	cancel()
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not exit")
	}

	// Materialized file should contain both messages.
	matPath := filepath.Join(tmpDir, ".confab", "opencode", externalID, "messages.jsonl")
	data, err := os.ReadFile(matPath)
	if err != nil {
		t.Fatalf("materialized file missing: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Fatalf("materialized %d lines, want 2 (final reconcile should have caught msg_2):\n%s", got, data)
	}

	// Both lines were uploaded.
	chunks := mock.getChunkRequests()
	if len(chunks) == 0 {
		t.Fatal("expected a chunk upload")
	}
	total := 0
	for _, c := range chunks {
		total += len(c.Lines)
	}
	if total != 2 {
		t.Errorf("uploaded %d lines total, want 2", total)
	}
}
