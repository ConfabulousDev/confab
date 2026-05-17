package provider

import (
	"bytes"
	"encoding/json"
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

	sessions, err := Codex{}.ScanCodexSessions()
	if err != nil {
		t.Fatalf("ScanCodexSessions() error = %v", err)
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

	gotID, gotPath, err := Codex{}.FindUserSession("44444444")
	if err != nil {
		t.Fatalf("FindUserSession() error = %v", err)
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

func TestCodexMatchesProcess(t *testing.T) {
	p := Codex{}
	tests := []struct {
		name    string
		cmd     string
		matches bool
	}{
		{"bare binary", "codex", true},
		{"absolute path", "/usr/local/bin/codex", true},
		{"with args", "codex --foo bar", true},
		{"mixed case", "Codex", true},
		{"node wrapper", "node /opt/codex/bin/codex.js", true},
		{"word boundary path", "/usr/local/bin/codex-cli", true},

		{"substring only", "xcoder", false},
		{"vscode", "/Applications/Visual Studio Code.app/Contents/MacOS/Code", false},
		{"precodex", "precodex", false},
		{"codexsmith", "/usr/bin/codexsmith", false},
		{"embedded substring", "mycodexapp", false},
		{"empty", "", false},
		{"unrelated", "/bin/bash", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.MatchesProcess(tt.cmd); got != tt.matches {
				t.Fatalf("MatchesProcess(%q) = %v, want %v", tt.cmd, got, tt.matches)
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

func TestCodexWriteHookResponse(t *testing.T) {
	var buf bytes.Buffer
	if err := (Codex{}).WriteHookResponse(&buf, false, "starting"); err != nil {
		t.Fatalf("WriteHookResponse() error = %v", err)
	}
	var got types.CodexHookResponse
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if !got.Continue {
		t.Error("Continue = false, want true")
	}
	if got.SuppressOutput {
		t.Error("SuppressOutput = true, want false")
	}
	if got.SystemMessage != "starting" {
		t.Errorf("SystemMessage = %q, want %q", got.SystemMessage, "starting")
	}
}

func TestCodexInstallSkillsIsNoOp(t *testing.T) {
	if err := (Codex{}).InstallSkills(); err != nil {
		t.Fatalf("Codex.InstallSkills() error = %v, want nil (no-op)", err)
	}
}

func TestCodexName(t *testing.T) {
	if got := (Codex{}).Name(); got != NameCodex {
		t.Fatalf("Name() = %q, want %q", got, NameCodex)
	}
}

func TestCodexParseSessionHook(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(CodexStateDirEnv, tmpDir)

	sessionsDir := filepath.Join(tmpDir, "sessions", "2026", "05", "15")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}
	id := "abcd1234-abcd-1234-abcd-1234abcd1234"
	rolloutPath := filepath.Join(sessionsDir, "rollout-2026-05-15T10-00-00-"+id+".jsonl")
	if err := os.WriteFile(rolloutPath, []byte(`{"type":"session_meta","payload":{"id":"`+id+`","thread_source":"user","cwd":"/work"}}`+"\n"), 0600); err != nil {
		t.Fatalf("failed to write rollout: %v", err)
	}

	payload := `{"session_id":"` + id + `","transcript_path":"` + rolloutPath + `","cwd":"/work/here","hook_event_name":"session_start"}`

	in, err := (Codex{}).ParseSessionHook(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("ParseSessionHook() error = %v", err)
	}
	if got := in.SessionID(); got != id {
		t.Errorf("SessionID() = %q", got)
	}
	if got := in.TranscriptPath(); got != rolloutPath {
		t.Errorf("TranscriptPath() = %q", got)
	}
	if got := in.CWD(); got != "/work/here" {
		t.Errorf("CWD() = %q", got)
	}
	if got := in.HookEventName(); got != "session_start" {
		t.Errorf("HookEventName() = %q", got)
	}

	if _, ok := in.(codexHookInputAdapter); !ok {
		t.Fatalf("ParseSessionHook returned %T, want codexHookInputAdapter", in)
	}
}

func TestCodexShouldSpawnForInput(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(CodexStateDirEnv, tmpDir)

	sessionsDir := filepath.Join(tmpDir, "sessions", "2026", "05", "15")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}

	userID := "11111111-1111-1111-1111-111111111111"
	subagentID := "22222222-2222-2222-2222-222222222222"
	writeCodexRollout(t, sessionsDir, userID, `"thread_source":"user","cwd":"/work/user"`)
	writeCodexRollout(t, sessionsDir, subagentID, `"thread_source":"subagent","cwd":"/work/agent","agent_role":"reviewer"`)

	userPath := filepath.Join(sessionsDir, "rollout-2026-05-12T18-06-53-"+userID+".jsonl")
	subagentPath := filepath.Join(sessionsDir, "rollout-2026-05-12T18-06-53-"+subagentID+".jsonl")
	missingPath := filepath.Join(sessionsDir, "rollout-2026-05-12T18-06-53-99999999-9999-9999-9999-999999999999.jsonl")

	tests := []struct {
		name           string
		transcriptPath string
		want           bool
	}{
		{"user session", userPath, true},
		{"subagent rollout", subagentPath, false},
		// File-not-yet-written race: permissive (true). Daemon will
		// re-validate on first sync cycle.
		{"missing rollout file (spawn race)", missingPath, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := codexHookInputAdapter{inner: &types.CodexHookInput{
				SessionID:      "abcd1234-abcd-1234-abcd-1234abcd1234",
				TranscriptPath: tt.transcriptPath,
			}}
			if got := (Codex{}).ShouldSpawnForInput(in); got != tt.want {
				t.Fatalf("ShouldSpawnForInput(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestCodexIsHooksInstalled(t *testing.T) {
	const confabCommand = "/usr/local/bin/confab hook session-start --provider codex"
	const otherCommand = "/usr/bin/some-other-tool"

	confabHooksTOML := `[features]
hooks = true

[[hooks.SessionStart]]
matcher = "startup|resume|clear"
[[hooks.SessionStart.hooks]]
type = "command"
command = "` + confabCommand + `"
statusMessage = "Starting Confab sync"
`

	otherHooksTOML := `[features]
hooks = true

[[hooks.SessionStart]]
matcher = "startup"
[[hooks.SessionStart.hooks]]
type = "command"
command = "` + otherCommand + `"
`

	tests := []struct {
		name    string
		content string // "" means file is absent
		want    bool
	}{
		{"missing config file", "", false},
		{"empty config", "# just a comment\n", false},
		{"confab hooks installed", confabHooksTOML, true},
		{"non-confab SessionStart hook", otherHooksTOML, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv(CodexStateDirEnv, tmpDir)
			if tt.content != "" {
				if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(tt.content), 0600); err != nil {
					t.Fatalf("failed to write config: %v", err)
				}
			}
			got, err := (Codex{}).IsHooksInstalled()
			if err != nil {
				t.Fatalf("IsHooksInstalled() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("IsHooksInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCodexInstallHooksWritesToConfigPath verifies the Codex provider's
// InstallHooks/UninstallHooks delegation lands at the path resolved by
// ConfigPath(). The underlying TOML mutation is tested in
// pkg/hookconfig — this test guards the path-resolution + delegation
// seam at codex.go:333,342.
//
// Note: IsHooksInstalled() can't be used as a positive-check round-trip
// here because installation writes the actual test-binary path
// (`provider.test`) into config.toml, and isConfabCommand requires the
// basename to be exactly `confab`. The negative-side uninstall check is
// still meaningful: IsHooksInstalled() must return false after
// UninstallHooks regardless of the binary used to install.
func TestCodexInstallHooksWritesToConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(CodexStateDirEnv, tmpDir)

	wantPath := filepath.Join(tmpDir, "config.toml")
	gotPath, err := (Codex{}).InstallHooks()
	if err != nil {
		t.Fatalf("Codex.InstallHooks() error = %v", err)
	}
	if gotPath != wantPath {
		t.Errorf("Codex.InstallHooks() returned %q, want %q", gotPath, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("config.toml not written at expected path: %v", err)
	}
	// Verify the SessionStart hook was written with the codex provider arg.
	// The binary path varies (it's the test binary), so just check the
	// trailing command shape.
	if !strings.Contains(string(data), "hook session-start --provider codex") {
		t.Errorf("config.toml missing confab SessionStart hook after InstallHooks; got:\n%s", data)
	}

	uninstallPath, err := (Codex{}).UninstallHooks()
	if err != nil {
		t.Fatalf("Codex.UninstallHooks() error = %v", err)
	}
	if uninstallPath != wantPath {
		t.Errorf("Codex.UninstallHooks() returned %q, want %q", uninstallPath, wantPath)
	}
	data, err = os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("config.toml gone after uninstall: %v", err)
	}
	if strings.Contains(string(data), "hook session-start --provider codex") {
		t.Errorf("config.toml still contains SessionStart hook after UninstallHooks; got:\n%s", data)
	}
}
