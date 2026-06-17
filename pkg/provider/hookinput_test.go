package provider

import (
	"testing"

	"github.com/ConfabulousDev/confab/pkg/types"
)

func TestClaudeHookInputAdapter(t *testing.T) {
	src := &types.ClaudeHookInput{
		SessionID:      "0199-claude-session",
		TranscriptPath: "/tmp/claude/transcript.jsonl",
		CWD:            "/work/claude",
		HookEventName:  "SessionStart",
		ParentPID:      4242,
	}
	a := claudeHookInputAdapter{inner: src}

	if got := a.SessionID(); got != src.SessionID {
		t.Errorf("SessionID() = %q, want %q", got, src.SessionID)
	}
	if got := a.TranscriptPath(); got != src.TranscriptPath {
		t.Errorf("TranscriptPath() = %q, want %q", got, src.TranscriptPath)
	}
	if got := a.CWD(); got != src.CWD {
		t.Errorf("CWD() = %q, want %q", got, src.CWD)
	}
	if got := a.HookEventName(); got != src.HookEventName {
		t.Errorf("HookEventName() = %q, want %q", got, src.HookEventName)
	}
	if got := a.ParentPID(); got != src.ParentPID {
		t.Errorf("ParentPID() = %d, want %d", got, src.ParentPID)
	}
}

func TestOpencodeHookInputAdapter(t *testing.T) {
	src := &types.OpenCodeHookInput{
		SessionID: "0199-opencode-session",
		CWD:       "/work/opencode",
		ParentPID: 7777,
	}
	a := opencodeHookInputAdapter{inner: src}

	if got := a.SessionID(); got != src.SessionID {
		t.Errorf("SessionID() = %q, want %q", got, src.SessionID)
	}
	if got := a.TranscriptPath(); got != "" {
		t.Errorf("TranscriptPath() = %q, want \"\"", got)
	}
	if got := a.CWD(); got != src.CWD {
		t.Errorf("CWD() = %q, want %q", got, src.CWD)
	}
	if got := a.HookEventName(); got != "" {
		t.Errorf("HookEventName() = %q, want \"\"", got)
	}
	if got := a.ParentPID(); got != src.ParentPID {
		t.Errorf("ParentPID() = %d, want %d", got, src.ParentPID)
	}
}

// TestCursorHookInputAdapter_Model verifies the cursor adapter exposes the
// model from the sessionStart payload via an optional Model() accessor — the
// universal model signal (Cursor JSONL carries none), read by the hook handler
// to plumb it onto chunk metadata.
func TestCursorHookInputAdapter_Model(t *testing.T) {
	src := &types.CursorHookInput{
		SessionID:      "cursor-session-abc",
		WorkspaceRoots: []string{"/work/cursor"},
		Model:          "composer-2.5-fast",
	}
	a := cursorHookInputAdapter{inner: src}

	if got := a.Model(); got != "composer-2.5-fast" {
		t.Errorf("Model() = %q, want %q", got, "composer-2.5-fast")
	}
	if got := a.CWD(); got != "/work/cursor" {
		t.Errorf("CWD() = %q, want first workspace root", got)
	}
}

func TestCodexHookInputAdapter(t *testing.T) {
	src := &types.CodexHookInput{
		SessionID:      "11111111-1111-1111-1111-111111111111",
		TranscriptPath: "/tmp/codex/rollout.jsonl",
		CWD:            "/work/codex",
		HookEventName:  "session_start",
		ParentPID:      9999,
	}
	a := codexHookInputAdapter{inner: src}

	if got := a.SessionID(); got != src.SessionID {
		t.Errorf("SessionID() = %q, want %q", got, src.SessionID)
	}
	if got := a.TranscriptPath(); got != src.TranscriptPath {
		t.Errorf("TranscriptPath() = %q, want %q", got, src.TranscriptPath)
	}
	if got := a.CWD(); got != src.CWD {
		t.Errorf("CWD() = %q, want %q", got, src.CWD)
	}
	if got := a.HookEventName(); got != src.HookEventName {
		t.Errorf("HookEventName() = %q, want %q", got, src.HookEventName)
	}
	if got := a.ParentPID(); got != src.ParentPID {
		t.Errorf("ParentPID() = %d, want %d", got, src.ParentPID)
	}
}
