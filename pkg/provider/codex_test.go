package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ConfabulousDev/confab/pkg/types"
)

func TestCodexSessionIDFromRolloutPath(t *testing.T) {
	id := "019e1edf-5437-7ea1-9cde-2e2a781e29ba"
	path := filepath.Join("/tmp", "rollout-2026-05-12T18-06-53-"+id+".jsonl")

	got, ok := Codex{}.SessionIDFromRolloutPath(path)
	if !ok {
		t.Fatal("expected rollout path to match")
	}
	if got != id {
		t.Fatalf("SessionIDFromRolloutPath() = %q, want %q", got, id)
	}
}

func TestCodexReadHookInputAllowsNullTranscriptPath(t *testing.T) {
	input := strings.NewReader(`{"session_id":"019e1edf-5437-7ea1-9cde-2e2a781e29ba","transcript_path":null}`)

	got, err := Codex{}.ReadHookInput(input)
	if err != nil {
		t.Fatalf("ReadHookInput() error = %v", err)
	}
	if got.TranscriptPath != "" {
		t.Fatalf("TranscriptPath = %q, want empty string", got.TranscriptPath)
	}
}

func TestCodexScanSessionsFiltersSubagents(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(CodexStateDirEnv, tmpDir)

	sessionsDir := filepath.Join(tmpDir, "sessions", "2026", "05", "12")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}

	userID := "11111111-1111-1111-1111-111111111111"
	subagentID := "22222222-2222-2222-2222-222222222222"
	memoryID := "33333333-3333-3333-3333-333333333333"

	writeCodexRollout(t, sessionsDir, userID, `"thread_source":"user","cwd":"/work/user"`)
	writeCodexRollout(t, sessionsDir, subagentID, `"thread_source":"subagent","cwd":"/work/agent","agent_role":"reviewer"`)
	writeCodexRollout(t, sessionsDir, memoryID, `"thread_source":"memory_consolidation","cwd":"/work/memory"`)

	sessions, err := Codex{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 top-level user session, got %d", len(sessions))
	}
	if sessions[0].SessionID != userID {
		t.Fatalf("SessionID = %q, want %q", sessions[0].SessionID, userID)
	}
	if sessions[0].CWD != "/work/user" {
		t.Fatalf("CWD = %q", sessions[0].CWD)
	}
}

func TestCodexEnsureHooksConfig(t *testing.T) {
	input := `[projects."/repo"]
trust_level = "trusted"
`

	got := ensureCodexHooksConfig(input, "/usr/local/bin/confab")
	for _, want := range []string{
		"[features]",
		"hooks = true",
		confabCodexHooksStart,
		"[[hooks.SessionStart]]",
		"command = \"/usr/local/bin/confab hook session-start --provider codex\"",
		"[[hooks.Stop]]",
		"command = \"/usr/local/bin/confab hook session-end --provider codex\"",
		confabCodexHooksEnd,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated config to contain %q\n%s", want, got)
		}
	}
}

func TestCodexEnsureHooksConfigIsIdempotent(t *testing.T) {
	once := ensureCodexHooksConfig("[features]\ncodex_hooks = false\n", "/usr/local/bin/confab")
	twice := ensureCodexHooksConfig(once, "/usr/local/bin/confab")
	if once != twice {
		t.Fatalf("expected idempotent config update\nonce:\n%s\n\ntwice:\n%s", once, twice)
	}
	if strings.Count(twice, confabCodexHooksStart) != 1 {
		t.Fatalf("expected one managed block, got:\n%s", twice)
	}
	if strings.Contains(twice, "codex_hooks") {
		t.Fatalf("expected deprecated feature flag to be removed:\n%s", twice)
	}
	if !strings.Contains(twice, "hooks = true") {
		t.Fatalf("expected hooks feature flag to be enabled:\n%s", twice)
	}
}

func TestCodexFindSessionByIDUsesFilenameBeforeMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(CodexStateDirEnv, tmpDir)

	sessionsDir := filepath.Join(tmpDir, "sessions", "2026", "05", "12")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}
	sessionID := "44444444-4444-4444-4444-444444444444"
	path := filepath.Join(sessionsDir, "rollout-2026-05-12T18-06-53-"+sessionID+".jsonl")
	line := `{"type":"session_meta","payload":{"id":"different-id","thread_source":"user","cwd":"/work/user"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatalf("failed to write rollout: %v", err)
	}

	gotID, gotPath, err := Codex{}.FindSessionByID("44444444")
	if err != nil {
		t.Fatalf("FindSessionByID() error = %v", err)
	}
	if gotID != sessionID {
		t.Fatalf("id = %q, want %q", gotID, sessionID)
	}
	if !strings.Contains(gotPath, sessionID) {
		t.Fatalf("path = %q, want filename-derived session", gotPath)
	}
}

func TestCodexExtractFirstUserMessageFromLines(t *testing.T) {
	lines := []string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>ignore me</environment_context>"}]}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"ignore me too"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"  Ship Codex support  "}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"second prompt"}}`,
	}

	got := Codex{}.ExtractFirstUserMessageFromLines(lines)
	if got != "Ship Codex support" {
		t.Fatalf("ExtractFirstUserMessageFromLines() = %q, want first event_msg user_message", got)
	}
}

func TestCodexExtractFirstUserMessageFromLinesSkipsEmptyAndNonUser(t *testing.T) {
	lines := []string{
		`not json`,
		`{"type":"session_meta","payload":{"id":"session-id"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"   "}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"assistant text"}}`,
	}

	got := Codex{}.ExtractFirstUserMessageFromLines(lines)
	if got != "" {
		t.Fatalf("ExtractFirstUserMessageFromLines() = %q, want empty", got)
	}
}

func TestCodexExtractFirstUserMessageFromLinesTruncates(t *testing.T) {
	message := strings.Repeat("a", types.MaxFirstUserMessageLength+100)
	lines := []string{`{"type":"event_msg","payload":{"type":"user_message","message":"` + message + `"}}`}

	got := Codex{}.ExtractFirstUserMessageFromLines(lines)
	if len(got) != types.MaxFirstUserMessageLength {
		t.Fatalf("len(got) = %d, want %d", len(got), types.MaxFirstUserMessageLength)
	}
}

func TestCodexExtractFirstUserMessageFromLinesTruncatesAtUTF8Boundary(t *testing.T) {
	message := strings.Repeat("a", types.MaxFirstUserMessageLength-1) + "é"
	lines := []string{`{"type":"event_msg","payload":{"type":"user_message","message":"` + message + `"}}`}

	got := Codex{}.ExtractFirstUserMessageFromLines(lines)
	if len(got) > types.MaxFirstUserMessageLength {
		t.Fatalf("len(got) = %d, want <= %d", len(got), types.MaxFirstUserMessageLength)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("got invalid UTF-8 after truncation")
	}
	if strings.HasSuffix(got, "é") {
		t.Fatalf("expected partial multibyte rune to be omitted")
	}
}

// TestCodexReadSessionInfoParsesNestedSourceObject covers the real shape Codex
// v0.130.0 writes for spawned subagents: `source` is a nested object, not a
// string. Earlier struct typing (Source string) caused json.Unmarshal to fail
// and ReadSessionInfo silently returned an empty CodexSessionInfo, which then
// looked like a user-session and got skipped by DiscoverCodexDescendants.
// The flattening also has to fit the backend's 64-char `source` cap.
func TestCodexReadSessionInfoParsesNestedSourceObject(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(CodexStateDirEnv, tmpDir)

	sessionsDir := filepath.Join(tmpDir, "sessions", "2026", "05", "15")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}

	subagentID := "019e2ce8-8314-7560-a8c4-536edbb5e99a"
	rootID := "019e2ce8-2637-7db0-85c4-09ed1a293016"
	nestedSource := `"source":{"subagent":{"thread_spawn":{"parent_thread_id":"` + rootID + `","depth":1,"agent_nickname":"Turing","agent_role":"explorer"}}}`
	writeCodexRollout(t, sessionsDir, subagentID,
		nestedSource+`,"thread_source":"subagent","agent_nickname":"Turing","agent_role":"explorer","cwd":"/work"`)

	path := filepath.Join(sessionsDir, "rollout-2026-05-12T18-06-53-"+subagentID+".jsonl")
	info, err := Codex{}.ReadSessionInfo(path)
	if err != nil {
		t.Fatalf("ReadSessionInfo() error = %v", err)
	}
	if info.ThreadSource != "subagent" {
		t.Errorf("ThreadSource = %q, want %q", info.ThreadSource, "subagent")
	}
	if info.AgentNickname != "Turing" {
		t.Errorf("AgentNickname = %q, want %q", info.AgentNickname, "Turing")
	}
	if info.AgentRole != "explorer" {
		t.Errorf("AgentRole = %q, want %q", info.AgentRole, "explorer")
	}
	if info.IsUserSession() {
		t.Errorf("IsUserSession() = true; want false for a subagent rollout")
	}
	if info.Source != "subagent" {
		t.Errorf("Source = %q; want flattened top-level key %q", info.Source, "subagent")
	}
}

// TestCodexReadSessionInfoFlattensStringSource covers the legacy/user-session
// shape where `source` is a bare string ("cli"). Flattening passes it through
// unchanged.
func TestCodexReadSessionInfoFlattensStringSource(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(CodexStateDirEnv, tmpDir)

	sessionsDir := filepath.Join(tmpDir, "sessions", "2026", "05", "15")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}

	id := "55555555-5555-5555-5555-555555555555"
	writeCodexRollout(t, sessionsDir, id, `"source":"cli","thread_source":"user","cwd":"/work"`)

	path := filepath.Join(sessionsDir, "rollout-2026-05-12T18-06-53-"+id+".jsonl")
	info, err := Codex{}.ReadSessionInfo(path)
	if err != nil {
		t.Fatalf("ReadSessionInfo() error = %v", err)
	}
	if info.Source != "cli" {
		t.Errorf("Source = %q, want %q", info.Source, "cli")
	}
	if !info.IsUserSession() {
		t.Errorf("IsUserSession() = false; want true for user rollout")
	}
}

func TestFlattenCodexSource(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", ``, ""},
		{"string", `"cli"`, "cli"},
		{"object_subagent", `{"subagent":{"thread_spawn":{"depth":1}}}`, "subagent"},
		{"object_unknown_key", `{"some_future_variant":{}}`, "some_future_variant"},
		{"malformed", `not json`, ""},
		{"number", `42`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flattenCodexSource([]byte(tc.in))
			if got != tc.want {
				t.Errorf("flattenCodexSource(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func writeCodexRollout(t *testing.T, dir, id, metaFields string) {
	t.Helper()
	path := filepath.Join(dir, "rollout-2026-05-12T18-06-53-"+id+".jsonl")
	line := `{"type":"session_meta","payload":{"id":"` + id + `",` + metaFields + `}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatalf("failed to write rollout: %v", err)
	}
}
