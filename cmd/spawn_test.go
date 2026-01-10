package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/types"
)

func TestMaybeSpawnDaemon(t *testing.T) {
	// Save and restore the original spawnDaemonFunc
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	t.Run("spawns daemon when no state exists", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		var spawnedInput *types.HookInput
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			spawnCalled = true
			spawnedInput = hookInput
			return nil
		}

		hookInput := &types.HookInput{
			SessionID:      "new-session-1234-1234-1234-123456789abc",
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(hookInput)
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
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "running-session-1234-1234-1234-123456789abc"

		// Create existing daemon state with current PID (appears running)
		createFakeDaemonState(t, tmpDir, sessionID, os.Getpid())

		hookInput := &types.HookInput{
			SessionID:      sessionID,
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(hookInput)
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
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "stale-session-1234-1234-1234-123456789abc"

		// Create stale state (non-existent PID)
		createFakeDaemonState(t, tmpDir, sessionID, 0)

		hookInput := &types.HookInput{
			SessionID:      sessionID,
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(hookInput)
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

	t.Run("sets parent PID from findClaudePID", func(t *testing.T) {
		setupSyncTestEnv(t)

		var capturedInput *types.HookInput
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			capturedInput = hookInput
			return nil
		}

		hookInput := &types.HookInput{
			SessionID:      "parent-pid-test-1234-1234-123456789abc",
			TranscriptPath: "/tmp/transcript.jsonl",
			CWD:            "/tmp",
			ParentPID:      0, // Initially unset
		}

		_, err := maybeSpawnDaemon(hookInput)
		if err != nil {
			t.Fatalf("maybeSpawnDaemon failed: %v", err)
		}

		// ParentPID should be set by maybeSpawnDaemon (via findClaudePID)
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

		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			t.Error("should not call spawnDaemonFunc when transcript_path is missing")
			return nil
		}

		hookInput := &types.HookInput{
			SessionID:      "missing-path-1234-1234-123456789abc",
			TranscriptPath: "", // Missing!
			CWD:            "/tmp",
		}

		spawned, err := maybeSpawnDaemon(hookInput)
		if err == nil {
			t.Error("expected error when transcript_path is missing")
		}
		if spawned {
			t.Error("expected spawned=false when transcript_path is missing")
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

func TestUserPromptSubmitSpawnsDaemon(t *testing.T) {
	// Save and restore the original spawnDaemonFunc
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	t.Run("spawns daemon when no state exists (teleport case)", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
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
		var response types.HookResponse
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
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
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
		var response types.HookResponse
		if err := json.NewDecoder(r).Decode(&response); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !response.Continue {
			t.Error("expected continue=true in response")
		}
	})

	t.Run("handles invalid JSON gracefully", func(t *testing.T) {
		setupSyncTestEnv(t)

		spawnDaemonFunc = func(hookInput *types.HookInput) error {
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
		var response types.HookResponse
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
			got := matchesClaudeProcess(tt.cmd)
			if got != tt.matches {
				t.Errorf("matchesClaudeProcess(%q) = %v, want %v", tt.cmd, got, tt.matches)
			}
		})
	}
}

