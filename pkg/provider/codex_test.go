package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func writeCodexRollout(t *testing.T, dir, id, metaFields string) {
	t.Helper()
	path := filepath.Join(dir, "rollout-2026-05-12T18-06-53-"+id+".jsonl")
	line := `{"type":"session_meta","payload":{"id":"` + id + `",` + metaFields + `}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatalf("failed to write rollout: %v", err)
	}
}
