package provider_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

// stubRegistrar records DescendantRegistrar calls so tests can assert
// Codex.DiscoverDescendants dispatched the right rows.
type stubRegistrar struct {
	tracked    map[string]bool
	registered []registeredCall
}

type registeredCall struct {
	path     string
	fileName string
	isRoot   bool
	meta     provider.CodexRolloutMetadata
}

func newStubRegistrar() *stubRegistrar {
	return &stubRegistrar{tracked: make(map[string]bool)}
}

func (s *stubRegistrar) IsTracked(fileName string) bool { return s.tracked[fileName] }

func (s *stubRegistrar) RegisterCodexRollout(path, fileName string, isRoot bool, meta provider.CodexRolloutMetadata) {
	s.registered = append(s.registered, registeredCall{
		path:     path,
		fileName: fileName,
		isRoot:   isRoot,
		meta:     meta,
	})
	s.tracked[fileName] = true
}

// stubAttacher records TranscriptRegistrar.SetCodexRollout calls.
type stubAttacher struct {
	attached *provider.CodexRolloutMetadata
}

func (s *stubAttacher) SetCodexRollout(m *provider.CodexRolloutMetadata) { s.attached = m }

// codextestOpts is a local convenience wrapper to keep test lines short.
func codextestOpts(role, nickname string) codextest.SubagentOpts {
	return codextest.SubagentOpts{AgentRole: role, AgentNickname: nickname}
}

// ---- Codex.DiscoverDescendants (migrated from pkg/sync/tracker_test.go) ----

func TestCodex_DiscoverDescendants_HappyPath_TwoChildren(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root-uuid").WithSessionMeta("/work", "model")
	childA := f.AddSubagent(root.ThreadUUID(), "child-a", codextestOpts("planner-a", "Planny-A")).
		WithSessionMeta("/work", "model")
	childB := f.AddSubagent(root.ThreadUUID(), "child-b", codextestOpts("planner-b", "Planny-B")).
		WithSessionMeta("/work", "model")

	reg := newStubRegistrar()
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}
	if got := len(reg.registered); got != 2 {
		t.Fatalf("registered = %d, want 2", got)
	}
	paths := map[string]bool{}
	for _, r := range reg.registered {
		paths[r.path] = true
		if r.isRoot {
			t.Errorf("registered %s as root, want isRoot=false (descendants are agents)", r.fileName)
		}
		if r.meta.ParentThreadUUID != root.ThreadUUID() {
			t.Errorf("registered %s parent = %q, want %q", r.fileName, r.meta.ParentThreadUUID, root.ThreadUUID())
		}
	}
	if !paths[childA.Path()] || !paths[childB.Path()] {
		t.Errorf("missing child paths: got %v", paths)
	}
}

func TestCodex_DiscoverDescendants_DeepTree_AllAddedAsAgents(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("R").WithSessionMeta("/", "m")
	f.AddSubagent("R", "B", codextestOpts("r-b", "B")).WithSessionMeta("/", "m")
	f.AddSubagent("B", "C", codextestOpts("r-c", "C")).WithSessionMeta("/", "m")

	reg := newStubRegistrar()
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}
	if got := len(reg.registered); got != 2 {
		t.Fatalf("registered = %d, want 2 (B + C)", got)
	}
	for _, r := range reg.registered {
		if r.isRoot {
			t.Errorf("registered %s as root, want isRoot=false at every depth", r.fileName)
		}
	}
	parents := map[string]string{}
	for _, r := range reg.registered {
		parents[r.meta.ThreadUUID] = r.meta.ParentThreadUUID
	}
	if parents["B"] != "R" {
		t.Errorf("B.parent = %q, want R", parents["B"])
	}
	if parents["C"] != "B" {
		t.Errorf("C.parent = %q, want B (immediate parent preserved)", parents["C"])
	}
}

func TestCodex_DiscoverDescendants_IdempotentAcrossCalls(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("R").WithSessionMeta("/", "m")
	f.AddSubagent("R", "A", codextestOpts("a", "A")).WithSessionMeta("/", "m")

	reg := newStubRegistrar()
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if got := len(reg.registered); got != 1 {
		t.Fatalf("first call registered = %d, want 1", got)
	}
	// Second call: registrar is already-tracked for A; nothing new should
	// land.
	prior := len(reg.registered)
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if delta := len(reg.registered) - prior; delta != 0 {
		t.Errorf("second call added %d entries, want 0 (idempotent)", delta)
	}
}

func TestCodex_DiscoverDescendants_FiltersMissingFiles(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("R").WithSessionMeta("/", "m")
	gone := f.AddSubagent("R", "gone", codextestOpts("g", "G")).WithSessionMeta("/", "m")
	f.AddSubagent("R", "kept", codextestOpts("k", "K")).WithSessionMeta("/", "m")
	f.DeleteRolloutFile(gone.ThreadUUID())

	reg := newStubRegistrar()
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}
	if got := len(reg.registered); got != 1 {
		t.Fatalf("registered = %d, want 1 (missing file skipped)", got)
	}
	if reg.registered[0].meta.ThreadUUID != "kept" {
		t.Errorf("kept = %q, want kept", reg.registered[0].meta.ThreadUUID)
	}
}

func TestCodex_DiscoverDescendants_FiltersNonAgentRollouts(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("R").WithSessionMeta("/", "m")
	// DB edge says R→suspect is a subagent, but suspect's session_meta
	// declares thread_source=user with no agent_* fields. The rollout
	// itself isn't really a subagent — DiscoverDescendants must refuse.
	f.AddSubagent("R", "suspect", codextestOpts("", "")).
		WithRawLine(`{"type":"session_meta","payload":{"id":"suspect","thread_source":"user"}}`)

	reg := newStubRegistrar()
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}
	if got := len(reg.registered); got != 0 {
		t.Errorf("registered = %d, want 0 (suspect filtered by IsUserSession check)", got)
	}
}

func TestCodex_DiscoverDescendants_NewDescendantPickedUpOnNextCall(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("R").WithSessionMeta("/", "m")

	reg := newStubRegistrar()
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if got := len(reg.registered); got != 0 {
		t.Fatalf("first call registered = %d, want 0", got)
	}
	// Add a child after the first call and call again — it should appear.
	f.AddSubagent("R", "child-late", codextestOpts("l", "L")).WithSessionMeta("/", "m")
	if err := (provider.Codex{}).DiscoverDescendants(reg, root.ThreadUUID()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := len(reg.registered); got != 1 {
		t.Errorf("second call registered = %d, want 1 (new descendant)", got)
	}
}

// ---- Codex.InitTranscript ----

func TestCodex_InitTranscript_SetsRolloutMetadataFromSessionMeta(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root-init").WithSessionMeta("/work/dir", "claude-opus-4")

	att := &stubAttacher{}
	if err := (provider.Codex{}).InitTranscript(att, root.Path(), root.ThreadUUID()); err != nil {
		t.Fatalf("InitTranscript: %v", err)
	}
	if att.attached == nil {
		t.Fatal("attached metadata = nil; want populated")
	}
	if att.attached.ThreadUUID != root.ThreadUUID() {
		t.Errorf("ThreadUUID = %q, want %q", att.attached.ThreadUUID, root.ThreadUUID())
	}
	if att.attached.RolloutPath != root.Path() {
		t.Errorf("RolloutPath = %q, want %q", att.attached.RolloutPath, root.Path())
	}
	if att.attached.CWD != "/work/dir" {
		t.Errorf("CWD = %q, want /work/dir", att.attached.CWD)
	}
	if att.attached.Model != "claude-opus-4" {
		t.Errorf("Model = %q, want claude-opus-4", att.attached.Model)
	}
	if att.attached.ParentThreadUUID != "" {
		t.Errorf("ParentThreadUUID = %q, want \"\" (root)", att.attached.ParentThreadUUID)
	}
}

// TestCodex_InitTranscript_MissingRolloutAttachesPartial pins behavior
// parity with the pre-CF-397 engine code: when session_meta can't be read,
// the provider must STILL attach a bare-minimum CodexRolloutMetadata
// (ThreadUUID + RolloutPath) so the backend can upsert a row for the
// rollout. Empty CWD/Model is acceptable; missing-row is not.
func TestCodex_InitTranscript_MissingRolloutAttachesPartial(t *testing.T) {
	att := &stubAttacher{}
	missing := "/nonexistent/path/rollout-00000000-0000-0000-0000-000000000000.jsonl"
	err := (provider.Codex{}).InitTranscript(att, missing, "any-uuid")
	if err != nil {
		t.Fatalf("InitTranscript: %v, want nil (read failure must not surface)", err)
	}
	if att.attached == nil {
		t.Fatal("attached metadata = nil; want partial metadata even on read failure")
	}
	if att.attached.ThreadUUID != "any-uuid" {
		t.Errorf("ThreadUUID = %q, want any-uuid", att.attached.ThreadUUID)
	}
	if att.attached.RolloutPath != missing {
		t.Errorf("RolloutPath = %q, want %q", att.attached.RolloutPath, missing)
	}
	if att.attached.CWD != "" || att.attached.Model != "" {
		t.Errorf("CWD/Model = %q/%q, want empty (session_meta unreadable)",
			att.attached.CWD, att.attached.Model)
	}
}

// ---- Codex.AnnotateChunk ----

// stubChunkView is the minimal ChunkView implementation used by provider
// tests to drive AnnotateChunk without depending on pkg/sync's adapter.
type stubChunkView struct {
	fileType      string
	firstLine     int
	lines         []string
	filePath      string
	codexFromFile *provider.CodexRolloutMetadata

	setRollout          *provider.CodexRolloutMetadata
	setSummary          string
	setFirstUserMessage string
	setLatestMessageAt  time.Time
}

func (s *stubChunkView) FileType() string                                 { return s.fileType }
func (s *stubChunkView) FirstLine() int                                   { return s.firstLine }
func (s *stubChunkView) Lines() []string                                  { return s.lines }
func (s *stubChunkView) FilePath() string                                 { return s.filePath }
func (s *stubChunkView) FileCodexRollout() *provider.CodexRolloutMetadata { return s.codexFromFile }
func (s *stubChunkView) SetCodexRolloutMetadata(m *provider.CodexRolloutMetadata) {
	s.setRollout = m
}
func (s *stubChunkView) SetSummary(v string)            { s.setSummary = v }
func (s *stubChunkView) SetFirstUserMessage(v string)   { s.setFirstUserMessage = v }
func (s *stubChunkView) SetLatestMessageAt(t time.Time) { s.setLatestMessageAt = t }

func TestCodex_AnnotateChunk_FirstChunkAttachesCodexRollout(t *testing.T) {
	roll := &provider.CodexRolloutMetadata{ThreadUUID: "tA", RolloutPath: "/x.jsonl"}
	cv := &stubChunkView{
		fileType:      "transcript",
		firstLine:     1,
		lines:         []string{`{"type":"session_meta","payload":{}}`},
		codexFromFile: roll,
	}
	(provider.Codex{}).AnnotateChunk(cv, false, nil)
	if cv.setRollout == nil {
		t.Fatal("SetCodexRolloutMetadata not called on first chunk")
	}
	if cv.setRollout.ThreadUUID != "tA" {
		t.Errorf("attached ThreadUUID = %q, want tA", cv.setRollout.ThreadUUID)
	}
}

func TestCodex_AnnotateChunk_NonFirstChunkOmitsCodexRollout(t *testing.T) {
	roll := &provider.CodexRolloutMetadata{ThreadUUID: "tA"}
	cv := &stubChunkView{
		fileType:      "transcript",
		firstLine:     42, // not 1 → must NOT attach
		lines:         []string{`{"type":"response_item"}`},
		codexFromFile: roll,
	}
	(provider.Codex{}).AnnotateChunk(cv, false, nil)
	if cv.setRollout != nil {
		t.Errorf("SetCodexRolloutMetadata called on FirstLine=%d; metadata must only ride on FirstLine=1", cv.firstLine)
	}
}

func TestCodex_AnnotateChunk_ExtractsFirstUserMessageOnce(t *testing.T) {
	lines := []string{
		`{"type":"session_meta","payload":{"id":"s"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"  hello world  "}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"second"}}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	result := (provider.Codex{}).AnnotateChunk(cv, false, nil)
	if !result.IncludedFirstUserMessage {
		t.Error("IncludedFirstUserMessage = false, want true on first call")
	}
	if cv.setFirstUserMessage != "hello world" {
		t.Errorf("SetFirstUserMessage = %q, want %q (trimmed)", cv.setFirstUserMessage, "hello world")
	}
	// Second call with sentFirst=true must NOT re-extract.
	cv2 := &stubChunkView{fileType: "transcript", firstLine: 5, lines: lines}
	result2 := (provider.Codex{}).AnnotateChunk(cv2, true, nil)
	if result2.IncludedFirstUserMessage {
		t.Error("second call IncludedFirstUserMessage = true, want false (already sent)")
	}
	if cv2.setFirstUserMessage != "" {
		t.Errorf("second call SetFirstUserMessage = %q, want \"\"", cv2.setFirstUserMessage)
	}
}

func TestCodex_AnnotateChunk_RedactionAppliedToFirstUserMessage(t *testing.T) {
	lines := []string{
		`{"type":"event_msg","payload":{"type":"user_message","message":"secret AKIA-EXAMPLE here"}}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	redact := func(s string) string { return strings.ReplaceAll(s, "AKIA-EXAMPLE", "[REDACTED]") }
	(provider.Codex{}).AnnotateChunk(cv, false, redact)
	if strings.Contains(cv.setFirstUserMessage, "AKIA-EXAMPLE") {
		t.Errorf("SetFirstUserMessage = %q; redaction not applied", cv.setFirstUserMessage)
	}
	if !strings.Contains(cv.setFirstUserMessage, "[REDACTED]") {
		t.Errorf("SetFirstUserMessage = %q; want redaction marker", cv.setFirstUserMessage)
	}
}

// ---- ClaudeCode.AnnotateChunk ----

func TestClaudeCode_AnnotateChunk_ExtractsSummaryAndFirstUserMessage(t *testing.T) {
	lines := []string{
		`{"type":"user","message":{"content":"please help"}}`,
		`{"type":"summary","summary":"a thing happened"}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	(provider.ClaudeCode{}).AnnotateChunk(cv, false, nil)
	if cv.setSummary != "a thing happened" {
		t.Errorf("SetSummary = %q, want %q", cv.setSummary, "a thing happened")
	}
	if cv.setFirstUserMessage != "please help" {
		t.Errorf("SetFirstUserMessage = %q, want %q", cv.setFirstUserMessage, "please help")
	}
}

func TestClaudeCode_AnnotateChunk_ReturnsSummaryLinks(t *testing.T) {
	lines := []string{
		`{"type":"summary","summary":"parent summary","leafUuid":"abc-123"}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	result := (provider.ClaudeCode{}).AnnotateChunk(cv, false, nil)
	if len(result.SummaryLinks) != 1 {
		t.Fatalf("SummaryLinks count = %d, want 1", len(result.SummaryLinks))
	}
	link := result.SummaryLinks[0]
	if link.Summary != "parent summary" || link.LeafUUID != "abc-123" {
		t.Errorf("SummaryLink = %+v, want {Summary: parent summary, LeafUUID: abc-123}", link)
	}
}

func TestClaudeCode_AnnotateChunk_NonTranscriptFileNoop(t *testing.T) {
	lines := []string{`{"type":"summary","summary":"agent-side summary"}`}
	cv := &stubChunkView{fileType: "agent", firstLine: 1, lines: lines}
	(provider.ClaudeCode{}).AnnotateChunk(cv, false, nil)
	if cv.setSummary != "" {
		t.Errorf("SetSummary = %q on agent file; Claude extracts summary from transcripts only", cv.setSummary)
	}
}

// ---- No-op confirmations ----

func TestClaudeCode_InitTranscript_Noop(t *testing.T) {
	att := &stubAttacher{}
	if err := (provider.ClaudeCode{}).InitTranscript(att, "/irrelevant.jsonl", "uuid"); err != nil {
		t.Errorf("InitTranscript: %v, want nil", err)
	}
	if att.attached != nil {
		t.Errorf("attached = %+v, want nil (Claude is a no-op)", att.attached)
	}
}

func TestClaudeCode_DiscoverDescendants_Noop(t *testing.T) {
	reg := newStubRegistrar()
	if err := (provider.ClaudeCode{}).DiscoverDescendants(reg, "any-uuid"); err != nil {
		t.Errorf("DiscoverDescendants: %v, want nil", err)
	}
	if len(reg.registered) != 0 {
		t.Errorf("registered = %d, want 0 (Claude is a no-op)", len(reg.registered))
	}
}
