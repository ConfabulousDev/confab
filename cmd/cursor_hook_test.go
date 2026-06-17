package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// setupCursorHookEnv wires a temp HOME + Cursor state dir so daemon state files
// (under ~/.confab/sync/cursor) and the canonical agent-transcripts layout
// resolve. Returns the temp home and a valid derived transcript path for
// sessionID.
func setupCursorHookEnv(t *testing.T, sessionID string) (tmpHome, transcriptPath string) {
	t.Helper()
	tmpHome = t.TempDir()
	t.Setenv("HOME", tmpHome)
	if err := os.MkdirAll(filepath.Join(tmpHome, ".confab", "sync"), 0o700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}
	cursorDir := filepath.Join(tmpHome, ".cursor")
	t.Setenv(provider.CursorStateDirEnv, cursorDir)
	transcriptDir := filepath.Join(cursorDir, "projects", "Users-jackie-dev-confab", "agent-transcripts", sessionID)
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	transcriptPath = filepath.Join(transcriptDir, sessionID+".jsonl")
	return tmpHome, transcriptPath
}

func runCursorSessionStart(t *testing.T, in []byte) error {
	t.Helper()
	orig := hookProviderName
	hookProviderName = provider.NameCursor
	defer func() { hookProviderName = orig }()
	return sessionStartFromReader(bytes.NewReader(in), io.Discard)
}

func runCursorSessionEnd(t *testing.T, in []byte) error {
	t.Helper()
	orig := hookProviderName
	hookProviderName = provider.NameCursor
	defer func() { hookProviderName = orig }()
	return sessionEndFromReader(bytes.NewReader(in))
}

// cursorSessionStartJSON builds a realistic Cursor sessionStart payload:
// transcript_path is null and workspace_roots drives path derivation.
func cursorSessionStartJSON(t *testing.T, sessionID, workspaceRoot string) []byte {
	t.Helper()
	b, err := json.Marshal(types.CursorHookInput{
		SessionID:      sessionID,
		ConversationID: sessionID,
		HookEventName:  "sessionStart",
		ComposerMode:   "agent",
		WorkspaceRoots: []string{workspaceRoot},
		// transcript_path intentionally omitted (null at sessionStart).
	})
	if err != nil {
		t.Fatalf("marshal cursor start: %v", err)
	}
	return b
}

// cursorSessionEndJSON builds a Cursor sessionEnd payload carrying the populated
// transcript_path + reason (the clean shutdown signal).
func cursorSessionEndJSON(t *testing.T, sessionID, transcriptPath, reason string) []byte {
	t.Helper()
	b, err := json.Marshal(types.CursorHookInput{
		SessionID:      sessionID,
		HookEventName:  "sessionEnd",
		TranscriptPath: transcriptPath,
		Reason:         reason,
		FinalStatus:    reason,
	})
	if err != nil {
		t.Fatalf("marshal cursor end: %v", err)
	}
	return b
}

// TestCursorHook_SessionStart_DerivesPathAndSpawns confirms the standard
// launch path (buildStandardLaunchArgs) handles Cursor with NO cmd/ change:
// a transcript_path:null payload yields a non-empty derived path and a spawn
// for provider=cursor.
func TestCursorHook_SessionStart_DerivesPathAndSpawns(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	const sessionID = "cursor-start-aaa"
	_, wantPath := setupCursorHookEnv(t, sessionID)

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := cursorSessionStartJSON(t, sessionID, "/Users/jackie/dev/confab")
	if err := runCursorSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called for a Cursor root session")
	}
	if captured.Provider != provider.NameCursor {
		t.Errorf("Provider = %q, want %q", captured.Provider, provider.NameCursor)
	}
	if captured.ExternalID != sessionID {
		t.Errorf("ExternalID = %q, want %q", captured.ExternalID, sessionID)
	}
	if captured.TranscriptPath == "" {
		t.Fatal("TranscriptPath empty; Cursor sessionStart must derive a non-empty path")
	}
	if captured.TranscriptPath != wantPath {
		t.Errorf("TranscriptPath = %q, want derived %q", captured.TranscriptPath, wantPath)
	}
}

// TestCursorHook_SessionEnd_StopsCursorDaemon confirms session-end routes to
// StopDaemonForProvider(NameCursor, ...). The daemon state lives under the
// cursor provider, so the Claude-only StopDaemon would never find it; the
// stop must succeed (the running PID is signalled, state removed).
func TestCursorHook_SessionEnd_StopsCursorDaemon(t *testing.T) {
	const sessionID = "cursor-end-bbb"
	_, transcriptPath := setupCursorHookEnv(t, sessionID)

	// Stand up a harmless long-lived child process to play the "running
	// daemon": IsDaemonRunning() reports alive, and StopDaemonForProvider's
	// SIGTERM lands on this sleep — NOT the test runner. We reap it.
	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	t.Cleanup(func() {
		_ = sleeper.Process.Kill()
		_, _ = sleeper.Process.Wait()
	})

	state := daemon.NewStateForProvider(provider.NameCursor, sessionID, transcriptPath, "/work", 0)
	state.PID = sleeper.Process.Pid
	if err := state.Save(); err != nil {
		t.Fatalf("save cursor state: %v", err)
	}

	// Sanity: the Claude-keyed lookup must NOT see the cursor daemon. This
	// is exactly why an explicit cursor route is required — the default
	// StopDaemon (hardcoded claude-code) would never find it.
	if st, _ := daemon.LoadStateForProvider(provider.NameClaudeCode, sessionID); st != nil {
		t.Fatal("precondition failed: cursor state leaked into claude-code provider namespace")
	}

	in := cursorSessionEndJSON(t, sessionID, transcriptPath, "completed")
	if err := runCursorSessionEnd(t, in); err != nil {
		t.Fatalf("session-end hook returned error: %v", err)
	}

	// The cursor daemon was found + signalled under the cursor namespace:
	// the inbox event (carrying the session_end + reason) was written, which
	// only StopDaemonForProvider(NameCursor, ...) does. A failed lookup would
	// not have produced the inbox file.
	st, err := daemon.LoadStateForProvider(provider.NameCursor, sessionID)
	if err != nil {
		t.Fatalf("reload cursor state: %v", err)
	}
	if st == nil {
		t.Fatal("cursor daemon state missing after session-end")
	}
	if st.InboxPath == "" {
		t.Fatal("cursor state has no inbox path")
	}
	if _, statErr := os.Stat(st.InboxPath); statErr != nil {
		t.Errorf("expected session_end inbox event written under cursor namespace: %v", statErr)
	}

	// And the forwarded inbox event carries the Cursor reason verbatim.
	data, readErr := os.ReadFile(st.InboxPath)
	if readErr != nil {
		t.Fatalf("read inbox: %v", readErr)
	}
	if !strings.Contains(string(data), `"reason":"completed"`) {
		t.Errorf("inbox event missing forwarded reason; got: %s", data)
	}
}

// TestCursorHook_SessionEnd_NotCodexErrorBranch guards that --provider cursor
// is NOT caught by the codex "session-end is not used" error branch.
func TestCursorHook_SessionEnd_NotCodexErrorBranch(t *testing.T) {
	const sessionID = "cursor-end-ccc"
	_, transcriptPath := setupCursorHookEnv(t, sessionID)

	// No daemon running — the handler should still return nil (clean no-op,
	// like Claude), never the codex hard error.
	in := cursorSessionEndJSON(t, sessionID, transcriptPath, "completed")
	if err := runCursorSessionEnd(t, in); err != nil {
		t.Fatalf("cursor session-end must not error (got codex branch?): %v", err)
	}
}
