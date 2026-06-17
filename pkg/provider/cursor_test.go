package provider

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/redactor"
)

// ---- T3 transcript-metadata fixtures (grounded in real captured Cursor JSONL,
// kata 6kys §7). Message lines are {role, message:{content:[...]}} with NO
// top-level "type" field; user text is wrapped in <user_query>…</user_query>;
// status lines are {type:"turn_ended", status}. Tool results never appear.

// cursorUserLine is a real user message line shape: a single text part whose
// text is wrapped in the <user_query> sentinel.
func cursorUserLine(text string) string {
	return `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>\n` + text + `\n</user_query>"}]}}`
}

// cursorAssistantToolLine is a real assistant line carrying a tool_use part.
func cursorAssistantToolLine(name, inputJSON string) string {
	return `{"role":"assistant","message":{"content":[{"type":"tool_use","name":"` + name + `","input":` + inputJSON + `}]}}`
}

const cursorTurnEndedLine = `{"type":"turn_ended","status":"success"}`

// cursorChunkStub is a minimal internal ChunkView double for AnnotateChunk tests.
type cursorChunkStub struct {
	fileType            string
	lines               []string
	filePath            string
	setSummary          string
	setFirstUserMessage string
	setLatestMessageAt  time.Time
	summarySet          bool
	firstUserMessageSet bool
	latestMessageAtSet  bool
}

func (s *cursorChunkStub) FileType() string                              { return s.fileType }
func (s *cursorChunkStub) FirstLine() int                                { return 1 }
func (s *cursorChunkStub) Lines() []string                               { return s.lines }
func (s *cursorChunkStub) FilePath() string                              { return s.filePath }
func (s *cursorChunkStub) FileCodexRollout() *CodexRolloutMetadata       { return nil }
func (s *cursorChunkStub) SetCodexRolloutMetadata(*CodexRolloutMetadata) {}
func (s *cursorChunkStub) SetSummary(v string)                           { s.setSummary = v; s.summarySet = true }
func (s *cursorChunkStub) SetFirstUserMessage(v string) {
	s.setFirstUserMessage = v
	s.firstUserMessageSet = true
}
func (s *cursorChunkStub) SetLatestMessageAt(ts time.Time) {
	s.setLatestMessageAt = ts
	s.latestMessageAtSet = true
}

func TestCursorExtractMetadata_FirstUserMessageUnwrapped(t *testing.T) {
	lines := []string{
		cursorUserLine("Reply with exactly the word: ok"),
		`{"role":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		cursorTurnEndedLine,
	}
	meta := (Cursor{}).ExtractMetadata(lines)
	if meta.FirstUserMessage != "Reply with exactly the word: ok" {
		t.Errorf("FirstUserMessage = %q, want unwrapped query text", meta.FirstUserMessage)
	}
	if meta.Summary != "" {
		t.Errorf("Summary = %q, want empty (Cursor has no inline summary)", meta.Summary)
	}
	if meta.SummaryLinks != nil {
		t.Errorf("SummaryLinks = %v, want nil (Cursor has none)", meta.SummaryLinks)
	}
}

// A transcript whose first line is a status line (not a message) must be
// skipped defensively — the first user message is still found.
func TestCursorExtractMetadata_SkipsLeadingStatusLine(t *testing.T) {
	lines := []string{
		cursorTurnEndedLine,
		cursorUserLine("first real prompt"),
	}
	meta := (Cursor{}).ExtractMetadata(lines)
	if meta.FirstUserMessage != "first real prompt" {
		t.Errorf("FirstUserMessage = %q, want %q", meta.FirstUserMessage, "first real prompt")
	}
}

// Only the FIRST user message wins; a later user line must not overwrite it.
func TestCursorExtractMetadata_FirstUserMessageWins(t *testing.T) {
	lines := []string{
		cursorUserLine("the first one"),
		`{"role":"assistant","message":{"content":[{"type":"text","text":"reply"}]}}`,
		cursorUserLine("the second one"),
	}
	meta := (Cursor{}).ExtractMetadata(lines)
	if meta.FirstUserMessage != "the first one" {
		t.Errorf("FirstUserMessage = %q, want the first", meta.FirstUserMessage)
	}
}

func TestCursorExtractMetadata_EmptyOnNoUserMessage(t *testing.T) {
	lines := []string{
		`{"role":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
		cursorTurnEndedLine,
	}
	meta := (Cursor{}).ExtractMetadata(lines)
	if meta.FirstUserMessage != "" {
		t.Errorf("FirstUserMessage = %q, want empty", meta.FirstUserMessage)
	}
}

func TestCursorAnnotateChunk_SetsFirstUserMessageOnTranscript(t *testing.T) {
	c := &cursorChunkStub{
		fileType: FileTypeTranscript,
		lines:    []string{cursorUserLine("hello cursor")},
	}
	res := (Cursor{}).AnnotateChunk(c, false, nil)
	if !c.firstUserMessageSet || c.setFirstUserMessage != "hello cursor" {
		t.Errorf("first_user_message = %q (set=%v), want %q", c.setFirstUserMessage, c.firstUserMessageSet, "hello cursor")
	}
	if c.summarySet && c.setSummary != "" {
		t.Errorf("Summary set to %q, want empty", c.setSummary)
	}
	if res.SummaryLinks != nil {
		t.Errorf("SummaryLinks = %v, want nil", res.SummaryLinks)
	}
	if res.IncludedFirstUserMessage {
		t.Error("IncludedFirstUserMessage = true, want false (match Claude)")
	}
}

func TestCursorAnnotateChunk_AppliesRedaction(t *testing.T) {
	c := &cursorChunkStub{
		fileType: FileTypeTranscript,
		lines:    []string{cursorUserLine("my key is AKIAIOSFODNN7EXAMPLE done")},
	}
	(Cursor{}).AnnotateChunk(c, false, func(s string) string {
		return strings.ReplaceAll(s, "AKIAIOSFODNN7EXAMPLE", "[REDACTED]")
	})
	if strings.Contains(c.setFirstUserMessage, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("first_user_message = %q still contains secret", c.setFirstUserMessage)
	}
	if !strings.Contains(c.setFirstUserMessage, "[REDACTED]") {
		t.Errorf("first_user_message = %q, want redacted", c.setFirstUserMessage)
	}
}

// Non-transcript (agent sidechain) chunks are a no-op.
func TestCursorAnnotateChunk_NonTranscriptNoOp(t *testing.T) {
	c := &cursorChunkStub{
		fileType: FileTypeAgent,
		lines:    []string{cursorUserLine("subagent prompt")},
	}
	(Cursor{}).AnnotateChunk(c, false, nil)
	if c.firstUserMessageSet || c.summarySet || c.latestMessageAtSet {
		t.Error("AnnotateChunk mutated a non-transcript chunk; want no-op")
	}
}

// On a transcript chunk, AnnotateChunk sets latest_message_at from the
// transcript file's mtime (the universal CLI+IDE recency signal — Cursor
// JSONL lines carry no timestamp, so the backend relies solely on this).
func TestCursorAnnotateChunk_SetsLatestMessageAtFromFileMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(cursorUserLine("hi")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	want := time.Date(2026, 6, 16, 8, 30, 0, 0, time.UTC)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	c := &cursorChunkStub{
		fileType: FileTypeTranscript,
		lines:    []string{cursorUserLine("hi")},
		filePath: path,
	}
	(Cursor{}).AnnotateChunk(c, false, nil)
	if !c.latestMessageAtSet {
		t.Fatal("latest_message_at not set on transcript chunk")
	}
	if !c.setLatestMessageAt.Equal(want) {
		t.Errorf("latest_message_at = %v, want file mtime %v", c.setLatestMessageAt, want)
	}
}

// A missing transcript file (path empty or stat fails) must not error the
// chunk: latest_message_at is simply left unset.
func TestCursorAnnotateChunk_LatestMessageAtAbsentWhenNoFile(t *testing.T) {
	c := &cursorChunkStub{
		fileType: FileTypeTranscript,
		lines:    []string{cursorUserLine("hi")},
		filePath: "/nonexistent/transcript.jsonl",
	}
	(Cursor{}).AnnotateChunk(c, false, nil)
	if c.latestMessageAtSet {
		t.Error("latest_message_at set despite missing file; want unset")
	}
}

// When a CLI meta.json with a title exists for the session, AnnotateChunk
// sets Summary from that title (redacted). The session id is derived from the
// transcript file path; meta.json lives at ~/.cursor/chats/<hash>/<id>/meta.json.
func TestCursorAnnotateChunk_SetsSummaryFromMetaJSONTitle(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(CursorStateDirEnv, stateDir)
	const id = "8b8e4d09-c7c9-402f-82c0-a61e11e7d0a5"

	metaDir := filepath.Join(stateDir, "chats", "cc8a6da7d6e3be93253f636ea5916b2e", id)
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	meta := `{"schemaVersion":1,"createdAtMs":1781651890835,"hasConversation":true,"title":"Code Explorer","updatedAtMs":1781651975907}`
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatalf("WriteFile meta: %v", err)
	}

	transcriptPath := filepath.Join(stateDir, "projects", "ws", "agent-transcripts", id, id+".jsonl")
	c := &cursorChunkStub{
		fileType: FileTypeTranscript,
		lines:    []string{cursorUserLine("hi")},
		filePath: transcriptPath,
	}
	(Cursor{}).AnnotateChunk(c, false, nil)
	if !c.summarySet || c.setSummary != "Code Explorer" {
		t.Errorf("Summary = %q (set=%v), want %q", c.setSummary, c.summarySet, "Code Explorer")
	}
}

// An IDE session (no meta.json) leaves Summary empty — first_user_message
// alone keeps it listable. No error.
func TestCursorAnnotateChunk_SummaryEmptyWhenNoMetaJSON(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(CursorStateDirEnv, stateDir)
	const id = "ide-session-no-meta"
	transcriptPath := filepath.Join(stateDir, "projects", "ws", "agent-transcripts", id, id+".jsonl")
	c := &cursorChunkStub{
		fileType: FileTypeTranscript,
		lines:    []string{cursorUserLine("hi")},
		filePath: transcriptPath,
	}
	(Cursor{}).AnnotateChunk(c, false, nil)
	if c.summarySet && c.setSummary != "" {
		t.Errorf("Summary = %q, want empty (no meta.json for IDE session)", c.setSummary)
	}
}

// A meta.json that exists but carries no title (also a real shape) leaves
// Summary empty.
func TestCursorAnnotateChunk_SummaryEmptyWhenMetaJSONHasNoTitle(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv(CursorStateDirEnv, stateDir)
	const id = "124c525a-6723-48f8-a683-e71cfc6dae13"
	metaDir := filepath.Join(stateDir, "chats", "cc8a6da7d6e3be93253f636ea5916b2e", id)
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	meta := `{"schemaVersion":1,"createdAtMs":1781654342896,"hasConversation":true,"updatedAtMs":1781654347667}`
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatalf("WriteFile meta: %v", err)
	}
	transcriptPath := filepath.Join(stateDir, "projects", "ws", "agent-transcripts", id, id+".jsonl")
	c := &cursorChunkStub{
		fileType: FileTypeTranscript,
		lines:    []string{cursorUserLine("hi")},
		filePath: transcriptPath,
	}
	(Cursor{}).AnnotateChunk(c, false, nil)
	if c.summarySet && c.setSummary != "" {
		t.Errorf("Summary = %q, want empty (meta.json has no title)", c.setSummary)
	}
}

// The standard ReadChunk redaction path (RedactJSONLine) scrubs a secret in a
// tool_use input.command — verifying the captured Cursor line shape is handled.
func TestCursorTranscriptRedaction_ScrubsToolUseSecret(t *testing.T) {
	red, err := redactor.NewFromConfig(&config.RedactionConfig{Enabled: true}) // default high-precision patterns
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	if red == nil {
		t.Fatal("expected a non-nil redactor with default patterns")
	}
	line := cursorAssistantToolLine("Shell", `{"command":"aws --key AKIAIOSFODNN7EXAMPLE s3 ls","description":"list"}`)
	out := red.RedactJSONLine(line)
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("redacted line still contains AWS key: %s", out)
	}
}

func writeCursorFixtureSession(t *testing.T, stateDir, workspaceRoot, sessionID string, lines []string) string {
	t.Helper()
	dir := filepath.Join(stateDir, "projects", sanitizeWorkspaceRoot(workspaceRoot), "agent-transcripts", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestCursorScanSessions_ResolvesFixture(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CursorStateDirEnv, dir)
	const id = "8b8e4d09-c7c9-402f-82c0-a61e11e7d0a5"
	path := writeCursorFixtureSession(t, dir, "/Users/jackie/dev/confab", id, []string{
		cursorUserLine("scan me"),
		cursorTurnEndedLine,
	})

	sessions, err := (Cursor{}).ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ScanSessions returned %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.SessionID != id {
		t.Errorf("SessionID = %q, want %q", s.SessionID, id)
	}
	if s.TranscriptPath != path {
		t.Errorf("TranscriptPath = %q, want %q", s.TranscriptPath, path)
	}
	if s.FirstUserMessage != "scan me" {
		t.Errorf("FirstUserMessage = %q, want %q", s.FirstUserMessage, "scan me")
	}
}

func TestCursorFindSessionByID_PrefixMatch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CursorStateDirEnv, dir)
	const id = "8b8e4d09-c7c9-402f-82c0-a61e11e7d0a5"
	path := writeCursorFixtureSession(t, dir, "/Users/jackie/dev/confab", id, []string{
		cursorUserLine("find me"),
	})

	gotID, gotPath, err := (Cursor{}).FindSessionByID("8b8e4d09")
	if err != nil {
		t.Fatalf("FindSessionByID: %v", err)
	}
	if gotID != id {
		t.Errorf("id = %q, want %q", gotID, id)
	}
	if gotPath != path {
		t.Errorf("path = %q, want %q", gotPath, path)
	}
}

func TestCursorFindSessionByID_NotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CursorStateDirEnv, dir)
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, _, err := (Cursor{}).FindSessionByID("nope"); err == nil {
		t.Error("FindSessionByID returned nil error for missing session")
	}
}

// Subagent sidechain files live under subagents/ and must NOT surface as
// top-level sessions in ScanSessions.
func TestCursorScanSessions_IgnoresSubagentFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CursorStateDirEnv, dir)
	const id = "8b8e4d09-c7c9-402f-82c0-a61e11e7d0a5"
	writeCursorFixtureSession(t, dir, "/Users/jackie/dev/confab", id, []string{cursorUserLine("root")})
	subDir := filepath.Join(dir, "projects", "Users-jackie-dev-confab", "agent-transcripts", id, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	subID := "9c4d4938-b532-406a-b0b4-9ad8e765e24c"
	if err := os.WriteFile(filepath.Join(subDir, subID+".jsonl"), []byte(cursorUserLine("child")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sessions, err := (Cursor{}).ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != id {
		t.Fatalf("ScanSessions = %+v, want only the root session %q", sessions, id)
	}
}

func TestCursorValidateTranscriptPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CursorStateDirEnv, dir)
	projects := filepath.Join(dir, "projects")
	good := filepath.Join(projects, "Users-jackie-dev-confab", "agent-transcripts", "id", "id.jsonl")

	if err := (Cursor{}).ValidateTranscriptPath(good); err != nil {
		t.Errorf("ValidateTranscriptPath(%q) = %v, want nil", good, err)
	}
	if err := (Cursor{}).ValidateTranscriptPath("relative/path.jsonl"); err == nil {
		t.Error("ValidateTranscriptPath accepted a relative path")
	}
	if err := (Cursor{}).ValidateTranscriptPath(filepath.Join(projects, "..", "etc", "passwd")); err == nil {
		t.Error("ValidateTranscriptPath accepted a path with '..'")
	}
	if err := (Cursor{}).ValidateTranscriptPath("/somewhere/else/id.jsonl"); err == nil {
		t.Error("ValidateTranscriptPath accepted a path outside projects/")
	}
}

func TestCursorReadSessionHookInput_RequiresTranscriptPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CursorStateDirEnv, dir)
	// No transcript_path → rejected (the validating variant per T3).
	in := `{"session_id":"abc","hook_event_name":"sessionEnd"}`
	if _, err := (Cursor{}).ReadSessionHookInput(strings.NewReader(in)); err == nil {
		t.Error("ReadSessionHookInput accepted input with no transcript_path")
	}

	good := filepath.Join(dir, "projects", "Users-jackie-dev-confab", "agent-transcripts", "abc", "abc.jsonl")
	ok := `{"session_id":"abc","transcript_path":"` + good + `"}`
	if _, err := (Cursor{}).ReadSessionHookInput(strings.NewReader(ok)); err != nil {
		t.Errorf("ReadSessionHookInput rejected a valid payload: %v", err)
	}
}

// Compile-time interface satisfaction (mirrors provider_test.go).
var _ Provider = Cursor{}

func TestCursorName(t *testing.T) {
	if got := (Cursor{}).Name(); got != NameCursor {
		t.Errorf("Name() = %q, want %q", got, NameCursor)
	}
}

func TestCursorCLIBinaryName(t *testing.T) {
	if got := (Cursor{}).CLIBinaryName(); got != "cursor-agent" {
		t.Errorf("CLIBinaryName() = %q, want %q", got, "cursor-agent")
	}
}

func TestCursorSupportsCommitLinking(t *testing.T) {
	if (Cursor{}).SupportsCommitLinking() {
		t.Error("SupportsCommitLinking() = true, want false")
	}
}

func TestCursorStateDir_Default(t *testing.T) {
	t.Setenv(CursorStateDirEnv, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	got, err := (Cursor{}).StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join(home, ".cursor")
	if got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
}

func TestCursorStateDir_WithEnv(t *testing.T) {
	t.Setenv(CursorStateDirEnv, "/custom/cursor")
	got, err := (Cursor{}).StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if got != "/custom/cursor" {
		t.Errorf("StateDir() = %q, want %q", got, "/custom/cursor")
	}
}

func TestCursorProjectsDir(t *testing.T) {
	t.Setenv(CursorStateDirEnv, "/custom/cursor")
	got, err := (Cursor{}).ProjectsDir()
	if err != nil {
		t.Fatalf("ProjectsDir: %v", err)
	}
	want := filepath.Join("/custom/cursor", "projects")
	if got != want {
		t.Errorf("ProjectsDir() = %q, want %q", got, want)
	}
}

func TestCursorSanitizeWorkspaceRoot(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/Users/jackie/dev/confab", "Users-jackie-dev-confab"},
		{"/Users/jackie/dev/confab-web", "Users-jackie-dev-confab-web"},
		{"/a//b", "a-b"},                  // collapse runs of separators
		{"/trailing/", "trailing"},        // strip trailing
		{"/with space/x", "with-space-x"}, // non-alnum → hyphen
		{"plain", "plain"},
	}
	for _, tt := range tests {
		if got := sanitizeWorkspaceRoot(tt.in); got != tt.want {
			t.Errorf("sanitizeWorkspaceRoot(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestCursorParseSessionHook_DerivesTranscriptPath is the key behavior:
// transcript_path is null at sessionStart, so ParseSessionHook must DERIVE it
// from <stateDir>/projects/<sanitize(root)>/agent-transcripts/<id>/<id>.jsonl.
func TestCursorParseSessionHook_DerivesTranscriptPath(t *testing.T) {
	t.Setenv(CursorStateDirEnv, "/custom/cursor")
	const id = "124c525a-aaaa-bbbb-cccc-000000000001"
	input := `{"session_id":"` + id + `","conversation_id":"` + id + `","hook_event_name":"sessionStart","workspace_roots":["/Users/jackie/dev/confab"],"transcript_path":null}`

	hi, err := (Cursor{}).ParseSessionHook(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSessionHook: %v", err)
	}
	if hi.SessionID() != id {
		t.Errorf("SessionID() = %q, want %q", hi.SessionID(), id)
	}
	want := filepath.Join("/custom/cursor", "projects", "Users-jackie-dev-confab", "agent-transcripts", id, id+".jsonl")
	if hi.TranscriptPath() != want {
		t.Errorf("TranscriptPath() = %q, want derived %q", hi.TranscriptPath(), want)
	}
	if hi.HookEventName() != "sessionStart" {
		t.Errorf("HookEventName() = %q", hi.HookEventName())
	}
}

// When the payload already carries a transcript_path (sessionEnd), keep it.
func TestCursorParseSessionHook_KeepsExplicitTranscriptPath(t *testing.T) {
	const id = "124c525a-aaaa-bbbb-cccc-000000000001"
	explicit := "/Users/jackie/.cursor/projects/Users-jackie-dev-confab/agent-transcripts/" + id + "/" + id + ".jsonl"
	input := `{"session_id":"` + id + `","transcript_path":"` + explicit + `"}`
	hi, err := (Cursor{}).ParseSessionHook(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSessionHook: %v", err)
	}
	if hi.TranscriptPath() != explicit {
		t.Errorf("TranscriptPath() = %q, want %q", hi.TranscriptPath(), explicit)
	}
}

func TestCursorWalkUpToRoot_Identity(t *testing.T) {
	id, path, err := (Cursor{}).WalkUpToRoot("abc")
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if id != "abc" || path != "" {
		t.Errorf("WalkUpToRoot = (%q, %q), want (abc, \"\")", id, path)
	}
}

func TestCursorShouldSpawnForInput_AlwaysTrue(t *testing.T) {
	hi, err := (Cursor{}).ParseSessionHook(strings.NewReader(`{"session_id":"abc","workspace_roots":["/x"]}`))
	if err != nil {
		t.Fatalf("ParseSessionHook: %v", err)
	}
	if !(Cursor{}).ShouldSpawnForInput(hi) {
		t.Error("ShouldSpawnForInput = false, want true")
	}
}

func TestCursorDefaultCWD(t *testing.T) {
	got := (Cursor{}).DefaultCWD("/x/agent-transcripts/id/id.jsonl")
	want := "/x/agent-transcripts/id"
	if got != want {
		t.Errorf("DefaultCWD() = %q, want %q", got, want)
	}
}

func TestCursorWriteHookResponse_EmptyObject(t *testing.T) {
	var buf bytes.Buffer
	if err := (Cursor{}).WriteHookResponse(&buf, true, "ignored message"); err != nil {
		t.Fatalf("WriteHookResponse: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "{}" {
		t.Errorf("WriteHookResponse wrote %q, want %q", got, "{}")
	}
}

func TestCursorMatchesProcess(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		matches bool
	}{
		{"CLI cursor-agent via local bin", "/Users/jackie/.local/bin/cursor-agent ...", true},
		{"CLI cursor-agent bundle path", "node /Users/jackie/.local/share/cursor-agent/versions/1.2.3/index.js", true},
		{"IDE app", "/Applications/Cursor.app/Contents/MacOS/Cursor", true},
		{"IDE helper plugin", "Cursor Helper (Plugin): extension-host (retrieval) /Users/jackie/dev/confab", true},
		{"lowercase dotcursor path is not a match", "/bin/sh /Users/jackie/.cursor/confab-hook-capture.sh", false},
		{"unrelated claude", "claude", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Cursor{}).MatchesProcess(tt.cmd); got != tt.matches {
				t.Errorf("MatchesProcess(%q) = %v, want %v", tt.cmd, got, tt.matches)
			}
		})
	}
}

func TestCursorGetReturnsProvider(t *testing.T) {
	p, err := Get(NameCursor)
	if err != nil {
		t.Fatalf("Get(%q): %v", NameCursor, err)
	}
	if p.Name() != NameCursor {
		t.Errorf("Get(%q).Name() = %q", NameCursor, p.Name())
	}
}

func TestCursorInOrderedNames(t *testing.T) {
	// T7 (r5mg) surfaces cursor in auto-detection: it must now appear in
	// the canonical registry order, after opencode.
	var found bool
	for _, n := range OrderedNames() {
		if n == NameCursor {
			found = true
		}
	}
	if !found {
		t.Errorf("OrderedNames() = %v; want it to include %q (cursor auto-detect enabled in T7)", OrderedNames(), NameCursor)
	}
}

// TestDetectInstalled_Cursor covers the new cursor permutations: detected
// when its CLI binary (cursor-agent) is on PATH, detected when only its
// state dir (~/.cursor) is present (IDE-only user), and absent otherwise.
func TestDetectInstalled_Cursor(t *testing.T) {
	t.Run("cli only: cursor-agent on PATH", func(t *testing.T) {
		stubLookPath(t, "cursor-agent")
		stubStateDir(t)
		got := DetectInstalled()
		want := []string{NameCursor}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("DetectInstalled() = %v, want %v", got, want)
		}
	})

	t.Run("statedir only: ~/.cursor present (IDE-only)", func(t *testing.T) {
		stubLookPath(t)
		stubStateDir(t, NameCursor)
		got := DetectInstalled()
		want := []string{NameCursor}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("DetectInstalled() = %v, want %v", got, want)
		}
	})

	t.Run("neither: cursor absent", func(t *testing.T) {
		stubLookPath(t)
		stubStateDir(t)
		got := DetectInstalled()
		for _, n := range got {
			if n == NameCursor {
				t.Fatalf("DetectInstalled() = %v; cursor must be absent when neither CLI nor state dir present", got)
			}
		}
	})

	t.Run("order: cursor follows opencode", func(t *testing.T) {
		stubLookPath(t, "opencode", "cursor-agent")
		stubStateDir(t)
		got := DetectInstalled()
		want := []string{NameOpencode, NameCursor}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("DetectInstalled() = %v, want %v (fixed opencode, cursor order)", got, want)
		}
	})
}

func TestCursorGetWithDir_Unsupported(t *testing.T) {
	if _, err := GetWithDir(NameCursor, t.TempDir()); err == nil {
		t.Errorf("GetWithDir(%q, dir) = nil error, want unsupported error", NameCursor)
	}
}

func TestCursorInitTranscriptNoOp(t *testing.T) {
	if err := (Cursor{}).InitTranscript(nil, "/x.jsonl", "id"); err != nil {
		t.Errorf("InitTranscript = %v, want nil", err)
	}
}
