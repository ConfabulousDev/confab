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

