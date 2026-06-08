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
	"github.com/ConfabulousDev/confab/pkg/sync"
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

// ============================================================================
// CF-538: OpenCode subagent sidechain capture
// ============================================================================

// buildRootAndChildFixture seeds a fixture DB with one root + one child,
// each carrying one complete user message + reply. Returns the DB path.
//
// Message ids carry a numeric prefix (msg_001_..., msg_002_...) so the
// collector's HWM filter (`m.id > ?`) surfaces them in the intended order.
// Using a/u as suffixes would NOT order correctly — 'a' < 'u' lexically,
// so msg_<id>_a would be excluded after HWM landed on msg_<id>_u.
func buildRootAndChildFixture(t *testing.T, rootID, childID string) string {
	t.Helper()
	db := opencodetest.NewDB(t)
	db.AddSession(rootID, "")
	db.AddSession(childID, rootID)

	// Root: one complete user message.
	db.AddMessage(rootID, "msg_001_root_u", opencodetest.UserTextMessage("hi"))
	db.AddPart("msg_001_root_u", "prt_root_u", opencodetest.TextPart("hi"))

	// Child: one user message + one finished assistant message.
	db.AddMessage(childID, "msg_001_child_u", opencodetest.UserTextMessage("explore X"))
	db.AddPart("msg_001_child_u", "prt_child_u", opencodetest.TextPart("explore X"))
	asst := opencodetest.AssistantMessageFinished("stop")
	asst["modelID"] = "claude-x"
	asst["providerID"] = "anthropic"
	db.AddMessage(childID, "msg_002_child_a", asst)
	db.AddPart("msg_002_child_a", "prt_child_a", opencodetest.TextPart("found X"))

	return db.Path()
}

// TestDaemonOpenCodeChildSidechainSync asserts that when the backend
// advertises opencode_subagent_files=true, the daemon discovers a child
// session, materializes its messages into a per-child JSONL file under
// ~/.confab/opencode/<root>/children/<child>/, and uploads it as
// file_type=agent with backend file_name "opencode/<child>/messages.jsonl".
func TestDaemonOpenCodeChildSidechainSync(t *testing.T) {
	const rootID = "ses_root_cf538"
	const childID = "ses_child_cf538"
	mock := newMockBackend(t)
	mock.caps = &sync.Capabilities{OpencodeSubagentFiles: true}
	backend := httptest.NewServer(mock)
	defer backend.Close()

	dbPath := buildRootAndChildFixture(t, rootID, childID)
	t.Setenv(provider.OpenCodeDBEnv, dbPath)

	tmpDir, _ := setupTestEnv(t, backend.URL)
	runOpenCodeDaemon(t, rootID, 800*time.Millisecond)

	// Root materialized file exists.
	rootPath := filepath.Join(tmpDir, ".confab", "opencode", rootID, "messages.jsonl")
	if _, err := os.Stat(rootPath); err != nil {
		t.Fatalf("root materialized file missing: %v", err)
	}

	// Child materialized file exists at the nested location.
	childPath := filepath.Join(tmpDir, ".confab", "opencode", rootID, "children", childID, "messages.jsonl")
	childData, err := os.ReadFile(childPath)
	if err != nil {
		t.Fatalf("child materialized file missing: %v", err)
	}
	if got := strings.Count(string(childData), "\n"); got < 2 {
		t.Errorf("child materialized %d lines, want >= 2:\n%s", got, childData)
	}

	// Child chunk uploaded with path-encoded backend file_name + file_type=agent.
	chunks := mock.getChunkRequests()
	wantChildName := "opencode/" + childID + "/messages.jsonl"
	var sawChild bool
	for _, c := range chunks {
		if c.FileName == wantChildName {
			sawChild = true
			if c.FileType != "agent" {
				t.Errorf("child chunk file_type = %q, want \"agent\"", c.FileType)
			}
		}
	}
	if !sawChild {
		var seen []string
		for _, c := range chunks {
			seen = append(seen, c.FileName)
		}
		t.Errorf("no chunk uploaded for %q; saw: %v", wantChildName, seen)
	}
}

// TestDaemonOpenCodeChildSidechainCapabilityGated asserts that when the
// backend reports opencode_subagent_files=false, no child sidechain
// uploads happen — only the root.
func TestDaemonOpenCodeChildSidechainCapabilityGated(t *testing.T) {
	const rootID = "ses_root_gated"
	const childID = "ses_child_gated"
	mock := newMockBackend(t)
	mock.caps = &sync.Capabilities{OpencodeSubagentFiles: false}
	backend := httptest.NewServer(mock)
	defer backend.Close()

	dbPath := buildRootAndChildFixture(t, rootID, childID)
	t.Setenv(provider.OpenCodeDBEnv, dbPath)

	_, _ = setupTestEnv(t, backend.URL)
	runOpenCodeDaemon(t, rootID, 600*time.Millisecond)

	wantChildName := "opencode/" + childID + "/messages.jsonl"
	for _, c := range mock.getChunkRequests() {
		if c.FileName == wantChildName {
			t.Errorf("child chunk uploaded despite capability=false: %+v", c)
		}
	}
}

// TestDaemonOpenCodeChildShutdownCancelsAllCollectors asserts the daemon's
// shutdown cancels every child collector goroutine and that a final
// reconcile fires for each — late-arriving messages must reach disk before
// the daemon exits (mirrors CF-545's root-collector guarantee).
func TestDaemonOpenCodeChildShutdownCancelsAllCollectors(t *testing.T) {
	const rootID = "ses_root_shutdown"
	const childA = "ses_child_A_shutdown"
	const childB = "ses_child_B_shutdown"
	mock := newMockBackend(t)
	mock.caps = &sync.Capabilities{OpencodeSubagentFiles: true}
	backend := httptest.NewServer(mock)
	defer backend.Close()

	// Fixture: 2 children, each with 1 complete message at start.
	// Message IDs use numeric prefixes so subsequent "late" messages sort
	// lexicographically AFTER the initial ones — the collector's HWM is a
	// strict `>` filter, so a late message must lex-greater than the HWM.
	db := opencodetest.NewDB(t)
	db.AddSession(rootID, "").AddSession(childA, rootID).AddSession(childB, rootID)
	db.AddMessage(rootID, "msg_001_root_u", opencodetest.UserTextMessage("hi"))
	db.AddPart("msg_001_root_u", "prt_root_u", opencodetest.TextPart("hi"))
	for _, ch := range []string{childA, childB} {
		mid := "msg_001_" + ch + "_u"
		db.AddMessage(ch, mid, opencodetest.UserTextMessage("u "+ch))
		db.AddPart(mid, "prt_"+ch+"_u", opencodetest.TextPart("u "+ch))
	}
	t.Setenv(provider.OpenCodeDBEnv, db.Path())

	tmpDir, _ := setupTestEnv(t, backend.URL)

	dm := New(Config{
		Provider:     provider.NameOpencode,
		ExternalID:   rootID,
		CWD:          t.TempDir(),
		SyncInterval: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- dm.Run(ctx) }()

	// Let the daemon discover children + materialize first lines.
	time.Sleep(300 * time.Millisecond)

	// Add a late message to childA — must be caught by the final reconcile
	// during shutdown, NOT silently dropped. The msg id MUST sort
	// lexicographically AFTER the initial msg_001 so the collector's HWM
	// `m.id > ?` filter surfaces it.
	asst := opencodetest.AssistantMessageFinished("stop")
	asst["modelID"] = "claude-x"
	asst["providerID"] = "anthropic"
	lateMID := "msg_002_" + childA + "_late"
	db.AddMessage(childA, lateMID, asst)
	db.AddPart(lateMID, "prt_late", opencodetest.TextPart("late"))

	// Trigger shutdown.
	cancel()
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not exit within 3s — child collectors may be hung")
	}

	// childA's materialized file should contain BOTH lines.
	childAPath := filepath.Join(tmpDir, ".confab", "opencode", rootID, "children", childA, "messages.jsonl")
	data, err := os.ReadFile(childAPath)
	if err != nil {
		t.Fatalf("child A materialized file missing: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Errorf("child A materialized %d lines, want 2 (final reconcile missed late message):\n%s", got, data)
	}
}

// TestDaemonOpenCodeChildSidechainResume asserts that a daemon restart
// against an OpenCode session with an existing per-child materialized
// file does not re-upload already-synced lines, and does upload any
// genuinely new ones.
func TestDaemonOpenCodeChildSidechainResume(t *testing.T) {
	const rootID = "ses_root_resume"
	const childID = "ses_child_resume"
	mock := newMockBackend(t)
	mock.caps = &sync.Capabilities{OpencodeSubagentFiles: true}
	// Pre-seed the backend's known files so it reports the child file already
	// has 1 line synced; resume must respect that.
	mock.initResponse = &sync.InitResponse{
		SessionID: "test-session-id",
		Files: map[string]sync.FileState{
			"messages.jsonl": {LastSyncedLine: 0},
			"opencode/" + childID + "/messages.jsonl": {LastSyncedLine: 1},
		},
	}
	backend := httptest.NewServer(mock)
	defer backend.Close()

	dbPath := buildRootAndChildFixture(t, rootID, childID)
	t.Setenv(provider.OpenCodeDBEnv, dbPath)

	tmpDir, _ := setupTestEnv(t, backend.URL)

	// Pre-populate the child materialized file with 1 line, simulating a
	// previous daemon run that already uploaded msg_001_child_u. The line's
	// info.id must match the fixture's id so the collector's seed() walks
	// past it on resume.
	childPath := filepath.Join(tmpDir, ".confab", "opencode", rootID, "children", childID, "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(childPath), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	preLine := `{"info":{"id":"msg_001_child_u","sessionID":"` + childID + `","role":"user"},"parts":[]}` + "\n"
	if err := os.WriteFile(childPath, []byte(preLine), 0600); err != nil {
		t.Fatalf("pre-seed write: %v", err)
	}

	runOpenCodeDaemon(t, rootID, 800*time.Millisecond)

	// Only the assistant message (msg_002_child_a) should have been uploaded —
	// the user message at line 1 was already counted as synced by the backend.
	wantChildName := "opencode/" + childID + "/messages.jsonl"
	var lines int
	for _, c := range mock.getChunkRequests() {
		if c.FileName == wantChildName {
			lines += len(c.Lines)
		}
	}
	if lines != 1 {
		t.Errorf("uploaded %d child lines on resume, want 1 (resume should skip already-synced)", lines)
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
