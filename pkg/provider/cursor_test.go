package provider

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestCursorNotInOrderedNames(t *testing.T) {
	// T2 registers cursor but does NOT surface it in auto-detection (T7).
	for _, n := range OrderedNames() {
		if n == NameCursor {
			t.Errorf("OrderedNames() includes %q; cursor must stay out of auto-detect until T7", NameCursor)
		}
	}
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
