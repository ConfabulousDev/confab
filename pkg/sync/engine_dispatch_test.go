package sync

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// stubProvider records calls into provider.Provider's three new sync-loop
// methods so tests can assert dispatch happens. All other Provider methods
// embed provider.ClaudeCode so the engine sees a fully-formed Provider.
type stubProvider struct {
	provider.ClaudeCode

	mu sync.Mutex

	initTranscriptCalls []stubInitTranscriptCall
	discoverCalls       []stubDiscoverCall
	annotateCalls       []stubAnnotateCall

	// Result returned from AnnotateChunk; tests set this to force the
	// engine to flip its sentFirstUserMessage flag or dispatch summary
	// links.
	annotateResult provider.AnnotationResult
}

type stubInitTranscriptCall struct {
	transcriptPath string
	externalID     string
	target         provider.TranscriptRegistrar
}

type stubDiscoverCall struct {
	externalID string
}

type stubAnnotateCall struct {
	fileType                  string
	firstLine                 int
	lineCount                 int
	sentFirstUserMessageInput bool
}

func (s *stubProvider) InitTranscript(target provider.TranscriptRegistrar, transcriptPath, externalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initTranscriptCalls = append(s.initTranscriptCalls, stubInitTranscriptCall{
		transcriptPath: transcriptPath,
		externalID:     externalID,
		target:         target,
	})
	return nil
}

func (s *stubProvider) DiscoverDescendants(_ provider.DescendantRegistrar, externalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discoverCalls = append(s.discoverCalls, stubDiscoverCall{externalID: externalID})
	return nil
}

func (s *stubProvider) AnnotateChunk(c provider.ChunkView, sentFirst bool, _ func(string) string) provider.AnnotationResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.annotateCalls = append(s.annotateCalls, stubAnnotateCall{
		fileType:                  c.FileType(),
		firstLine:                 c.FirstLine(),
		lineCount:                 len(c.Lines()),
		sentFirstUserMessageInput: sentFirst,
	})
	return s.annotateResult
}

// engineWithStub builds an engine wired up to mockBackend + a stubProvider.
func engineWithStub(t *testing.T) (*Engine, *stubProvider, *mockBackend, string) {
	t.Helper()
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)

	stub := &stubProvider{}
	engine := NewWithBackend(
		mustNewClient(t, server.URL, tmpDir),
		nil,
		EngineConfig{
			ExternalID:     "dispatch-test",
			TranscriptPath: transcriptPath,
			CWD:            tmpDir,
		},
	)
	engine.setProviderForTest(stub)
	return engine, stub, mock, transcriptPath
}

// TestEngine_NoProviderNameLiterals asserts the engine source contains no
// hard-coded provider name literals. Encodes CF-397's central architectural
// rule directly so future regressions trip CI immediately.
func TestEngine_NoProviderNameLiterals(t *testing.T) {
	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatalf("read engine.go: %v", err)
	}
	for _, banned := range []string{"NameCodex", "NameClaudeCode"} {
		if bytes.Contains(src, []byte(banned)) {
			t.Errorf("engine.go contains banned literal %q — provider-specific dispatch must live in pkg/provider, not the engine.", banned)
		}
	}
}

// TestEngine_Init_DispatchesInitTranscriptToProvider verifies Engine.Init
// calls provider.InitTranscript exactly once, with the right transcript
// path / externalID / target file. Captures the central CF-397 contract.
func TestEngine_Init_DispatchesInitTranscriptToProvider(t *testing.T) {
	engine, stub, _, transcriptPath := engineWithStub(t)
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if err := engine.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if got := len(stub.initTranscriptCalls); got != 1 {
		t.Fatalf("InitTranscript call count = %d, want 1", got)
	}
	call := stub.initTranscriptCalls[0]
	if call.transcriptPath != transcriptPath {
		t.Errorf("InitTranscript transcriptPath = %q, want %q", call.transcriptPath, transcriptPath)
	}
	if call.externalID != "dispatch-test" {
		t.Errorf("InitTranscript externalID = %q, want dispatch-test", call.externalID)
	}
	if call.target == nil {
		t.Error("InitTranscript target = nil, want non-nil TranscriptRegistrar")
	}
}

// TestEngine_SyncAll_DispatchesDiscoverDescendantsOncePerCycle verifies
// provider.DiscoverDescendants runs exactly once at the top of each SyncAll
// invocation. Two cycles → two calls.
func TestEngine_SyncAll_DispatchesDiscoverDescendantsOncePerCycle(t *testing.T) {
	engine, stub, _, transcriptPath := engineWithStub(t)
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if err := engine.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := engine.SyncAll(); err != nil {
		t.Fatalf("SyncAll #1: %v", err)
	}
	if _, err := engine.SyncAll(); err != nil {
		t.Fatalf("SyncAll #2: %v", err)
	}

	if got := len(stub.discoverCalls); got != 2 {
		t.Fatalf("DiscoverDescendants call count = %d, want 2 (one per SyncAll cycle)", got)
	}
	for i, call := range stub.discoverCalls {
		if call.externalID != "dispatch-test" {
			t.Errorf("DiscoverDescendants call %d externalID = %q, want dispatch-test", i, call.externalID)
		}
	}
}

// TestEngine_SyncAll_DispatchesAnnotateChunkPerChunk verifies every chunk
// read from a tracked file results in one provider.AnnotateChunk call, and
// the engine flips sentFirstUserMessage when the result requests it.
func TestEngine_SyncAll_DispatchesAnnotateChunkPerChunk(t *testing.T) {
	engine, stub, _, transcriptPath := engineWithStub(t)
	if err := os.WriteFile(transcriptPath, []byte(
		`{"type":"system","line":1}`+"\n"+
			`{"type":"user","line":2}`+"\n"+
			`{"type":"assistant","line":3}`+"\n",
	), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	// Make the stub claim it included the first user message — engine must
	// flip its flag on the next call.
	stub.annotateResult = provider.AnnotationResult{IncludedFirstUserMessage: true}

	if err := engine.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := engine.SyncAll(); err != nil {
		t.Fatalf("SyncAll #1: %v", err)
	}

	if got := len(stub.annotateCalls); got != 1 {
		t.Fatalf("AnnotateChunk call count after first SyncAll = %d, want 1", got)
	}
	first := stub.annotateCalls[0]
	if first.fileType != "transcript" {
		t.Errorf("first AnnotateChunk fileType = %q, want transcript", first.fileType)
	}
	if first.firstLine != 1 {
		t.Errorf("first AnnotateChunk firstLine = %d, want 1", first.firstLine)
	}
	if first.lineCount != 3 {
		t.Errorf("first AnnotateChunk lineCount = %d, want 3", first.lineCount)
	}
	if first.sentFirstUserMessageInput {
		t.Error("first AnnotateChunk sentFirst flag = true, want false on first chunk")
	}

	// Append more lines and sync again — second call must observe the
	// flipped flag.
	f, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	if _, err := f.WriteString(`{"type":"assistant","line":4}` + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	if _, err := engine.SyncAll(); err != nil {
		t.Fatalf("SyncAll #2: %v", err)
	}
	if got := len(stub.annotateCalls); got != 2 {
		t.Fatalf("AnnotateChunk call count after second SyncAll = %d, want 2", got)
	}
	if !stub.annotateCalls[1].sentFirstUserMessageInput {
		t.Error("second AnnotateChunk sentFirst flag = false, want true — engine should have flipped after first call returned IncludedFirstUserMessage")
	}
}

// TestEngine_SyncAll_AppliesSummaryLinksFromAnnotateChunk verifies the
// engine drains AnnotationResult.SummaryLinks and dispatches each
// entry to the backend via UpdateSessionSummary. Plants two previous-
// session transcript files (one per leafUUID) so the lookup in
// FindSessionByLeafUUID succeeds; asserts the mock backend records
// both PATCH requests with the right external_id + summary.
//
// Without the backend-side assertion, a silent drop of SummaryLinks
// from the engine loop at engine.go:315 would still pass.
func TestEngine_SyncAll_AppliesSummaryLinksFromAnnotateChunk(t *testing.T) {
	engine, stub, mock, transcriptPath := engineWithStub(t)
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"system","line":1}`+"\n"), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	// Plant two "previous session" transcripts in the same directory
	// so FindSessionByLeafUUID can resolve each leafUUID to a session
	// ID (the file's basename without .jsonl).
	transcriptDir := filepath.Dir(transcriptPath)
	prev1Path := filepath.Join(transcriptDir, "prev-session-aaa.jsonl")
	prev2Path := filepath.Join(transcriptDir, "prev-session-bbb.jsonl")
	if err := os.WriteFile(prev1Path, []byte(`{"type":"system","uuid":"leaf-1"}`+"\n"), 0644); err != nil {
		t.Fatalf("write prev1: %v", err)
	}
	if err := os.WriteFile(prev2Path, []byte(`{"type":"system","uuid":"leaf-2"}`+"\n"), 0644); err != nil {
		t.Fatalf("write prev2: %v", err)
	}

	stub.annotateResult = provider.AnnotationResult{
		SummaryLinks: []provider.SummaryLink{
			{Summary: "first", LeafUUID: "leaf-1"},
			{Summary: "second", LeafUUID: "leaf-2"},
		},
	}

	if err := engine.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := engine.SyncAll(); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}

	if got := len(stub.annotateCalls); got != 1 {
		t.Fatalf("AnnotateChunk call count = %d, want 1", got)
	}

	if got := len(mock.summaryRequests); got != 2 {
		t.Fatalf("UpdateSessionSummary call count = %d, want 2 — engine dropped SummaryLinks", got)
	}
	want := map[string]string{
		"prev-session-aaa": "first",
		"prev-session-bbb": "second",
	}
	got := map[string]string{}
	for _, req := range mock.summaryRequests {
		got[req.ExternalID] = req.Summary
	}
	for id, summary := range want {
		if got[id] != summary {
			t.Errorf("summary for %s = %q, want %q", id, got[id], summary)
		}
	}
}
