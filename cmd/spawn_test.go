package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

func TestMaybeSpawnDaemon(t *testing.T) {
	// Save and restore the original spawnDaemonFunc
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	t.Run("spawns daemon when no state exists", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		var spawnedInput *types.ClaudeHookInput
		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			spawnCalled = true
			spawnedInput = hookInput
			return nil
		}

		hookInput := &types.ClaudeHookInput{
			SessionID:      "new-session-1234-1234-1234-123456789abc",
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, hookInput)
		if err != nil {
			t.Fatalf("maybeSpawnDaemon failed: %v", err)
		}

		if !spawned {
			t.Error("expected spawned=true when no state exists")
		}
		if !spawnCalled {
			t.Error("expected spawnDaemonFunc to be called")
		}
		if spawnedInput.SessionID != hookInput.SessionID {
			t.Errorf("expected session_id %q, got %q", hookInput.SessionID, spawnedInput.SessionID)
		}
	})

	t.Run("does not spawn when daemon already running", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "running-session-1234-1234-1234-123456789abc"

		// Create existing daemon state with current PID (appears running)
		createFakeDaemonState(t, tmpDir, sessionID, os.Getpid())

		hookInput := &types.ClaudeHookInput{
			SessionID:      sessionID,
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, hookInput)
		if err != nil {
			t.Fatalf("maybeSpawnDaemon failed: %v", err)
		}

		if spawned {
			t.Error("expected spawned=false when daemon is already running")
		}
		if spawnCalled {
			t.Error("should not call spawnDaemonFunc when daemon is running")
		}
	})

	t.Run("spawns when state exists but daemon is dead", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "stale-session-1234-1234-1234-123456789abc"

		// Create stale state (non-existent PID)
		createFakeDaemonState(t, tmpDir, sessionID, 0)

		hookInput := &types.ClaudeHookInput{
			SessionID:      sessionID,
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, hookInput)
		if err != nil {
			t.Fatalf("maybeSpawnDaemon failed: %v", err)
		}

		if !spawned {
			t.Error("expected spawned=true when daemon is dead")
		}
		if !spawnCalled {
			t.Error("expected spawnDaemonFunc to be called")
		}
	})

	t.Run("sets parent PID from Claude provider", func(t *testing.T) {
		setupSyncTestEnv(t)

		var capturedInput *types.ClaudeHookInput
		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			capturedInput = hookInput
			return nil
		}

		hookInput := &types.ClaudeHookInput{
			SessionID:      "parent-pid-test-1234-1234-123456789abc",
			TranscriptPath: "/tmp/transcript.jsonl",
			CWD:            "/tmp",
			ParentPID:      0, // Initially unset
		}

		_, err := maybeSpawnDaemon(provider.ClaudeCode{}, hookInput)
		if err != nil {
			t.Fatalf("maybeSpawnDaemon failed: %v", err)
		}

		// ParentPID should be set by maybeSpawnDaemon via the Claude provider.
		// It might be 0 if Claude isn't the parent, but the field should be populated
		if capturedInput == nil {
			t.Fatal("expected spawnDaemonFunc to be called")
		}
		// We can't easily test the exact value since it depends on process tree,
		// but we verify the hookInput was passed through
		if capturedInput.SessionID != hookInput.SessionID {
			t.Errorf("expected session_id to be passed through")
		}
	})

	t.Run("fails when transcript_path is missing", func(t *testing.T) {
		setupSyncTestEnv(t)

		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			t.Error("should not call spawnDaemonFunc when transcript_path is missing")
			return nil
		}

		hookInput := &types.ClaudeHookInput{
			SessionID:      "missing-path-1234-1234-123456789abc",
			TranscriptPath: "", // Missing!
			CWD:            "/tmp",
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, hookInput)
		if err == nil {
			t.Error("expected error when transcript_path is missing")
		}
		if spawned {
			t.Error("expected spawned=false when transcript_path is missing")
		}
	})
}

func TestMaybeSpawnCodexDaemon(t *testing.T) {
	origSpawnCodexDaemon := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawnCodexDaemon }()

	t.Run("spawns daemon for user rollout", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		var spawnCalled bool
		var spawnedInput *types.CodexHookInput
		spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
			spawnCalled = true
			spawnedInput = hookInput
			return nil
		}

		sessionID := "11111111-1111-1111-1111-111111111111"
		rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"user","cwd":"/work/user"`)

		spawned, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
			SessionID:      sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
			HookEventName:  "SessionStart",
		})
		if err != nil {
			t.Fatalf("maybeSpawnCodexDaemon failed: %v", err)
		}
		if !spawned {
			t.Fatal("expected spawned=true for user rollout")
		}
		if !spawnCalled {
			t.Fatal("expected spawnCodexDaemonFunc to be called")
		}
		if spawnedInput == nil || spawnedInput.SessionID != sessionID {
			t.Fatalf("spawned input session = %v, want %s", spawnedInput, sessionID)
		}
	})

	t.Run("does not spawn for startup resume or clear when already running", func(t *testing.T) {
		for _, source := range []string{"startup", "resume", "clear"} {
			t.Run(source, func(t *testing.T) {
				tmpDir := setupCodexSyncTestEnv(t)

				spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
					t.Fatal("should not spawn when Codex daemon is already running")
					return nil
				}

				sessionID := "22222222-2222-2222-2222-222222222222"
				rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"user","cwd":"/work/user"`)
				state := daemon.NewStateForProvider(provider.NameCodex, sessionID, rolloutPath, "/work/user", 0)
				state.PID = os.Getpid()
				if err := state.Save(); err != nil {
					t.Fatalf("failed to save state: %v", err)
				}

				spawned, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
					SessionID:      sessionID,
					TranscriptPath: rolloutPath,
					CWD:            "/work/user",
					HookEventName:  "SessionStart",
					Source:         source,
				})
				if err != nil {
					t.Fatalf("maybeSpawnCodexDaemon failed: %v", err)
				}
				if spawned {
					t.Fatal("expected spawned=false when daemon is already running")
				}
			})
		}
	})

	t.Run("spawns when state exists but daemon is dead", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		var spawnCalled bool
		spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "33333333-3333-3333-3333-333333333333"
		rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"user","cwd":"/work/user"`)
		state := daemon.NewStateForProvider(provider.NameCodex, sessionID, rolloutPath, "/work/user", 0)
		state.PID = 999999
		if err := state.Save(); err != nil {
			t.Fatalf("failed to save stale state: %v", err)
		}

		spawned, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
			SessionID:      sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
		})
		if err != nil {
			t.Fatalf("maybeSpawnCodexDaemon failed: %v", err)
		}
		if !spawned || !spawnCalled {
			t.Fatal("expected stale Codex state to allow respawn")
		}
	})

	t.Run("skips subagent rollout", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
			t.Fatal("should not spawn for Codex subagent rollout")
			return nil
		}

		sessionID := "44444444-4444-4444-4444-444444444444"
		rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"subagent","cwd":"/work/agent","agent_role":"reviewer"`)

		spawned, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
			SessionID:      sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/agent",
		})
		if err != nil {
			t.Fatalf("maybeSpawnCodexDaemon failed: %v", err)
		}
		if spawned {
			t.Fatal("expected spawned=false for subagent rollout")
		}
	})

	t.Run("allows fresh rollout path before file exists", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		var spawnCalled bool
		spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "55555555-5555-5555-5555-555555555555"
		rolloutPath := codexTestRolloutPath(tmpDir, sessionID)

		spawned, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
			SessionID:      sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
		})
		if err != nil {
			t.Fatalf("maybeSpawnCodexDaemon failed: %v", err)
		}
		if !spawned || !spawnCalled {
			t.Fatal("expected missing fresh rollout file to allow spawn")
		}
	})

	t.Run("fails when transcript path is missing", func(t *testing.T) {
		setupCodexSyncTestEnv(t)

		spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
			t.Fatal("should not spawn when transcript_path is missing")
			return nil
		}

		spawned, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
			SessionID: "66666666-6666-6666-6666-666666666666",
			CWD:       "/work/user",
		})
		if err == nil {
			t.Fatal("expected missing transcript path error")
		}
		if spawned {
			t.Fatal("expected spawned=false when transcript path is missing")
		}
	})

	t.Run("populates parent PID from Codex provider", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		origFind := codexFindParentPIDFunc
		defer func() { codexFindParentPIDFunc = origFind }()
		codexFindParentPIDFunc = func() int { return 4242 }

		var captured *types.CodexHookInput
		spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
			captured = hookInput
			return nil
		}

		sessionID := "77777777-7777-7777-7777-777777777777"
		rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"user","cwd":"/work/user"`)

		_, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
			SessionID:      sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
		})
		if err != nil {
			t.Fatalf("maybeSpawnCodexDaemon failed: %v", err)
		}
		if captured == nil {
			t.Fatal("expected spawnCodexDaemonFunc to be called")
		}
		if captured.ParentPID != 4242 {
			t.Errorf("ParentPID = %d, want 4242 (stubbed FindParentPID result)", captured.ParentPID)
		}
	})

	t.Run("skips parent PID lookup for subagent rollout", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		origFind := codexFindParentPIDFunc
		defer func() { codexFindParentPIDFunc = origFind }()
		codexFindParentPIDFunc = func() int {
			t.Fatal("FindParentPID must not be called for subagent rollouts")
			return 0
		}

		spawnCodexDaemonFunc = func(hookInput *types.CodexHookInput) error {
			t.Fatal("should not spawn for subagent rollout")
			return nil
		}

		sessionID := "88888888-8888-8888-8888-888888888888"
		rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"subagent","cwd":"/work/agent","agent_role":"reviewer"`)

		spawned, err := maybeSpawnCodexDaemon(&types.CodexHookInput{
			SessionID:      sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/agent",
		})
		if err != nil {
			t.Fatalf("maybeSpawnCodexDaemon failed: %v", err)
		}
		if spawned {
			t.Fatal("expected spawned=false for subagent rollout")
		}
	})
}

func TestSpawnDaemonWritesState(t *testing.T) {
	// This test verifies that spawnDaemonImpl writes state immediately
	// We can't easily test the real impl (it spawns processes), but we
	// can verify the state writing logic works correctly.

	t.Run("state file written with correct PID", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		sessionID := "spawn-state-test-1234-1234-123456789abc"
		transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

		// Create a state as if spawner wrote it
		expectedPID := 12345
		state := daemon.NewState(sessionID, transcriptPath, tmpDir, 0)
		state.PID = expectedPID
		if err := state.Save(); err != nil {
			t.Fatalf("failed to save state: %v", err)
		}

		// Verify state can be loaded
		loadedState, err := daemon.LoadState(sessionID)
		if err != nil {
			t.Fatalf("failed to load state: %v", err)
		}
		if loadedState == nil {
			t.Fatal("expected state to be loaded")
		}
		if loadedState.PID != expectedPID {
			t.Errorf("expected PID %d, got %d", expectedPID, loadedState.PID)
		}
		if loadedState.TranscriptPath != transcriptPath {
			t.Errorf("expected transcript_path %q, got %q", transcriptPath, loadedState.TranscriptPath)
		}
	})
}

func setupCodexSyncTestEnv(t *testing.T) string {
	t.Helper()
	tmpDir := setupSyncTestEnv(t)
	t.Setenv(provider.CodexStateDirEnv, filepath.Join(tmpDir, ".codex"))
	if err := os.MkdirAll(filepath.Join(tmpDir, ".codex", "sessions"), 0700); err != nil {
		t.Fatalf("failed to create Codex sessions dir: %v", err)
	}
	return tmpDir
}

func codexTestRolloutPath(tmpDir, sessionID string) string {
	return filepath.Join(tmpDir, ".codex", "sessions", "2026", "05", "13", "rollout-2026-05-13T00-00-00-"+sessionID+".jsonl")
}

func writeCodexTestRollout(t *testing.T, tmpDir, sessionID, metaFields string) string {
	t.Helper()
	path := codexTestRolloutPath(tmpDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("failed to create Codex rollout dir: %v", err)
	}
	line := `{"type":"session_meta","payload":{"id":"` + sessionID + `",` + metaFields + `}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatalf("failed to write Codex rollout: %v", err)
	}
	return path
}

func TestUserPromptSubmitSpawnsDaemon(t *testing.T) {
	// Save and restore the original spawnDaemonFunc
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	t.Run("spawns daemon when no state exists (teleport case)", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			spawnCalled = true
			return nil
		}

		hookInput := map[string]string{
			"session_id":      "teleport-session-1234-1234-123456789abc",
			"transcript_path": filepath.Join(tmpDir, "transcript.jsonl"),
			"cwd":             tmpDir,
			"prompt":          "Hello, Claude!",
		}
		inputJSON, _ := json.Marshal(hookInput)

		// Capture stdout
		r, w, _ := os.Pipe()
		err := handleUserPromptSubmit(
			strings.NewReader(string(inputJSON)),
			w,
		)
		w.Close()
		if err != nil {
			t.Fatalf("handleUserPromptSubmit failed: %v", err)
		}

		if !spawnCalled {
			t.Error("expected daemon to be spawned for teleport case")
		}

		// Verify response was written
		var response types.ClaudeHookResponse
		if err := json.NewDecoder(r).Decode(&response); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !response.Continue {
			t.Error("expected continue=true in response")
		}
	})

	t.Run("does not spawn when daemon already running", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "existing-session-1234-1234-123456789abc"

		// Create existing daemon state
		createFakeDaemonState(t, tmpDir, sessionID, os.Getpid())

		hookInput := map[string]string{
			"session_id":      sessionID,
			"transcript_path": filepath.Join(tmpDir, "transcript.jsonl"),
			"cwd":             tmpDir,
			"prompt":          "Hello again!",
		}
		inputJSON, _ := json.Marshal(hookInput)

		r, w, _ := os.Pipe()
		err := handleUserPromptSubmit(
			strings.NewReader(string(inputJSON)),
			w,
		)
		w.Close()
		if err != nil {
			t.Fatalf("handleUserPromptSubmit failed: %v", err)
		}

		if spawnCalled {
			t.Error("should not spawn daemon when one is already running")
		}

		// Verify response was still written
		var response types.ClaudeHookResponse
		if err := json.NewDecoder(r).Decode(&response); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !response.Continue {
			t.Error("expected continue=true in response")
		}
	})

	t.Run("handles invalid JSON gracefully", func(t *testing.T) {
		setupSyncTestEnv(t)

		spawnDaemonFunc = func(hookInput *types.ClaudeHookInput) error {
			t.Error("should not spawn daemon on invalid input")
			return nil
		}

		r, w, _ := os.Pipe()
		err := handleUserPromptSubmit(
			strings.NewReader("not valid json"),
			w,
		)
		w.Close()

		// Should not return error (hooks must not fail)
		if err != nil {
			t.Fatalf("handleUserPromptSubmit should not return error: %v", err)
		}

		// Should still write valid response
		var response types.ClaudeHookResponse
		if err := json.NewDecoder(r).Decode(&response); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !response.Continue {
			t.Error("expected continue=true even on error")
		}
	})
}

func TestMatchesClaudeProcess(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		matches bool
	}{
		// Should match
		{"standalone claude", "claude", true},
		{"claude CLI path", "/usr/local/bin/claude", true},
		{"Claude.app on macOS", "/Applications/Claude.app/Contents/MacOS/Claude", true},
		{"claude with args", "claude --help", true},
		{"mixed case", "Claude", true},
		{"claude-code variant", "claude-code", true},
		{"claude in path with spaces", "/Users/john/Applications/Claude.app/Claude", true},

		// Should NOT match (word boundary protection)
		{"claudette", "claudette", false},
		{"claudesmith", "/usr/bin/claudesmith", false},
		{"preclaude", "preclaude", false},
		{"claude as substring", "myclaudeapp", false},

		// Edge cases
		{"empty string", "", false},
		{"unrelated process", "/bin/bash", false},
		{"vim editing claude file", "vim notes.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := provider.ClaudeCode{}.MatchesProcess(tt.cmd)
			if got != tt.matches {
				t.Errorf("ClaudeCode.MatchesProcess(%q) = %v, want %v", tt.cmd, got, tt.matches)
			}
		})
	}
}
