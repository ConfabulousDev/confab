package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// opencodeServer stands in for a running OpenCode HTTP server: it serves a
// session's messages and a minimal SSE stream that closes immediately (the
// collector reconnects + re-reconciles, and its initial reconcile already
// materializes data).
func opencodeServer(sessionID, messagesJSON string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/"+sessionID+"/message", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, messagesJSON)
	})
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
	})
	return httptest.NewServer(mux)
}

func runOpenCodeDaemon(t *testing.T, opencodeURL, externalID string, d time.Duration) {
	t.Helper()
	dm := New(Config{
		Provider:          provider.NameOpencode,
		ExternalID:        externalID,
		OpenCodeServerURL: opencodeURL,
		CWD:               t.TempDir(),
		SyncInterval:      50 * time.Millisecond,
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

	ocMessages := `[
		{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"hi"}]},
		{"info":{"id":"msg_2","role":"assistant","finish":"stop","providerID":"anthropic","modelID":"claude-x"},"parts":[{"type":"text","text":"yo"}]}
	]`
	oc := opencodeServer(externalID, ocMessages)
	defer oc.Close()

	tmpDir, _ := setupTestEnv(t, backend.URL)
	runOpenCodeDaemon(t, oc.URL, externalID, 600*time.Millisecond)

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
	oc := opencodeServer(externalID,
		`[{"info":{"id":"msg_1","role":"assistant"},"parts":[{"type":"text","text":"..."}]}]`)
	defer oc.Close()

	tmpDir, _ := setupTestEnv(t, backend.URL)
	runOpenCodeDaemon(t, oc.URL, externalID, 400*time.Millisecond)

	// No materialized file, so backendSyncEnabled stays false: no empty session.
	matPath := filepath.Join(tmpDir, ".confab", "opencode", externalID, "messages.jsonl")
	if _, err := os.Stat(matPath); !os.IsNotExist(err) {
		t.Errorf("expected no materialized file, stat err = %v", err)
	}
	if inits := mock.getInitRequests(); len(inits) != 0 {
		t.Errorf("expected no init (no complete message), got %d", len(inits))
	}
}
