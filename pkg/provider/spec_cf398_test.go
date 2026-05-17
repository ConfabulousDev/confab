// Spec tests for CF-398: the two behavioral changes that ship alongside
// the structural refactor.
//
//   1. Codex.ScanSessions populates SessionInfo.FirstUserMessage from the
//      rollout's first event_msg.user_message (previously: CWD).
//   2. Codex.FindSessionByID walks subagent UUIDs up to the root so callers
//      get the top-most user session, not the subagent rollout.
//
// Both tests fail today against the Phase 3b stubs and pass once Phase 4
// commit 2 fills them in. Other migrated tests cover the structural moves.
package provider_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

func TestCodexScanSessionsExtractsFirstUserMessage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(provider.CodexStateDirEnv, tmpDir)

	sessionsDir := filepath.Join(tmpDir, "sessions", "2026", "05", "16")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	const (
		sessionID = "11111111-1111-1111-1111-111111111111"
		userMsg   = "hello from the spec test"
	)
	path := filepath.Join(sessionsDir, "rollout-2026-05-16T10-00-00-"+sessionID+".jsonl")
	content := strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","thread_source":"user","cwd":"/work/user"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"` + userMsg + `"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	sessions, err := provider.Codex{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 user session, got %d", len(sessions))
	}
	got := sessions[0]
	if got.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, sessionID)
	}
	if got.FirstUserMessage != userMsg {
		t.Errorf("FirstUserMessage = %q, want %q (ScanSessions must extract first user_message, not stuff CWD here)",
			got.FirstUserMessage, userMsg)
	}
	if got.TranscriptPath != path {
		t.Errorf("TranscriptPath = %q, want %q", got.TranscriptPath, path)
	}
}

func TestCodexFindSessionByIDWalksUpToRoot(t *testing.T) {
	f := codextest.NewFixture(t)

	const (
		rootID  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		childID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)
	root := f.AddRoot(rootID).WithSessionMeta("/work/root", "model-x")
	child := f.AddSubagent(root.ThreadUUID(), childID, codextest.SubagentOpts{AgentRole: "reviewer"}).
		WithSessionMeta("/work/root", "model-x")

	// Caller passes the subagent's UUID prefix. The interface contract says
	// FindSessionByID must return the ROOT — not the subagent — so the
	// caller transparently uploads the whole tree.
	gotID, gotPath, err := provider.Codex{}.FindSessionByID(childID[:8])
	if err != nil {
		t.Fatalf("FindSessionByID(%q): %v", childID[:8], err)
	}
	if gotID != rootID {
		t.Errorf("id = %q, want %q (must walk subagent up to root)", gotID, rootID)
	}
	if gotPath != root.Path() {
		t.Errorf("path = %q, want %q (root path, not subagent path)", gotPath, root.Path())
	}
	if gotPath == child.Path() {
		t.Errorf("path = subagent path %q — must NOT return the un-walked rollout", gotPath)
	}
}
