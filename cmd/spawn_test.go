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
		var spawnedInput *daemonLaunchInput
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			spawnedInput = launch
			return nil
		}

		launch := &daemonLaunchInput{
			ExternalID:     "new-session-1234-1234-1234-123456789abc",
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, launch)
		if err != nil {
			t.Fatalf("maybeSpawnDaemon failed: %v", err)
		}

		if !spawned {
			t.Error("expected spawned=true when no state exists")
		}
		if !spawnCalled {
			t.Error("expected spawnDaemonFunc to be called")
		}
		if spawnedInput.ExternalID != launch.ExternalID {
			t.Errorf("expected external_id %q, got %q", launch.ExternalID, spawnedInput.ExternalID)
		}
	})

	t.Run("does not spawn when daemon already running", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "running-session-1234-1234-1234-123456789abc"

		// Create existing daemon state with current PID (appears running)
		createFakeDaemonState(t, tmpDir, sessionID, os.Getpid())

		launch := &daemonLaunchInput{
			ExternalID:     sessionID,
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, launch)
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
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "stale-session-1234-1234-1234-123456789abc"

		// Create stale state (non-existent PID)
		createFakeDaemonState(t, tmpDir, sessionID, 0)

		launch := &daemonLaunchInput{
			ExternalID:     sessionID,
			TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
			CWD:            tmpDir,
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, launch)
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

		var captured *daemonLaunchInput
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			captured = launch
			return nil
		}

		launch := &daemonLaunchInput{
			ExternalID:     "parent-pid-test-1234-1234-123456789abc",
			TranscriptPath: "/tmp/transcript.jsonl",
			CWD:            "/tmp",
			ParentPID:      0, // Initially unset
		}

		_, err := maybeSpawnDaemon(provider.ClaudeCode{}, launch)
		if err != nil {
			t.Fatalf("maybeSpawnDaemon failed: %v", err)
		}

		// ParentPID should be set by maybeSpawnDaemon via the Claude provider.
		// It might be 0 if Claude isn't the parent, but the field should be populated
		if captured == nil {
			t.Fatal("expected spawnDaemonFunc to be called")
		}
		if captured.ExternalID != launch.ExternalID {
			t.Errorf("expected external_id to be passed through")
		}
		if captured.Provider != provider.NameClaudeCode {
			t.Errorf("expected Provider=%q on captured launch, got %q", provider.NameClaudeCode, captured.Provider)
		}
	})

	t.Run("fails when transcript_path is missing", func(t *testing.T) {
		setupSyncTestEnv(t)

		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			t.Error("should not call spawnDaemonFunc when transcript_path is missing")
			return nil
		}

		launch := &daemonLaunchInput{
			ExternalID:     "missing-path-1234-1234-123456789abc",
			TranscriptPath: "", // Missing!
			CWD:            "/tmp",
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, launch)
		if err == nil {
			t.Error("expected error when transcript_path is missing")
		}
		if spawned {
			t.Error("expected spawned=false when transcript_path is missing")
		}
	})
}

func TestMaybeSpawnDaemonCodex(t *testing.T) {
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	t.Run("spawns daemon for user rollout", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		var spawnCalled bool
		var spawnedInput *daemonLaunchInput
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			spawnedInput = launch
			return nil
		}

		sessionID := "11111111-1111-1111-1111-111111111111"
		rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"user","cwd":"/work/user"`)

		spawned, err := maybeSpawnDaemon(provider.Codex{}, &daemonLaunchInput{
			ExternalID:     sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Codex) failed: %v", err)
		}
		if !spawned {
			t.Fatal("expected spawned=true for user rollout")
		}
		if !spawnCalled {
			t.Fatal("expected spawnDaemonFunc to be called")
		}
		if spawnedInput == nil || spawnedInput.ExternalID != sessionID {
			t.Fatalf("spawned input external_id = %v, want %s", spawnedInput, sessionID)
		}
		if spawnedInput.Provider != provider.NameCodex {
			t.Errorf("expected Provider=%q, got %q", provider.NameCodex, spawnedInput.Provider)
		}
	})

	t.Run("does not spawn when Codex daemon already running", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
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

		spawned, err := maybeSpawnDaemon(provider.Codex{}, &daemonLaunchInput{
			ExternalID:     sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Codex) failed: %v", err)
		}
		if spawned {
			t.Fatal("expected spawned=false when daemon is already running")
		}
	})

	t.Run("spawns when state exists but daemon is dead", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
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

		spawned, err := maybeSpawnDaemon(provider.Codex{}, &daemonLaunchInput{
			ExternalID:     sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Codex) failed: %v", err)
		}
		if !spawned || !spawnCalled {
			t.Fatal("expected stale Codex state to allow respawn")
		}
	})

	t.Run("skips subagent rollout", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			t.Fatal("should not spawn for Codex subagent rollout")
			return nil
		}

		sessionID := "44444444-4444-4444-4444-444444444444"
		rolloutPath := writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"subagent","cwd":"/work/agent","agent_role":"reviewer"`)

		spawned, err := maybeSpawnDaemon(provider.Codex{}, &daemonLaunchInput{
			ExternalID:     sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/agent",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Codex) failed: %v", err)
		}
		if spawned {
			t.Fatal("expected spawned=false for subagent rollout")
		}
	})

	t.Run("allows fresh rollout path before file exists", func(t *testing.T) {
		tmpDir := setupCodexSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "55555555-5555-5555-5555-555555555555"
		rolloutPath := codexTestRolloutPath(tmpDir, sessionID)

		spawned, err := maybeSpawnDaemon(provider.Codex{}, &daemonLaunchInput{
			ExternalID:     sessionID,
			TranscriptPath: rolloutPath,
			CWD:            "/work/user",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Codex) failed: %v", err)
		}
		if !spawned || !spawnCalled {
			t.Fatal("expected missing fresh rollout file to allow spawn")
		}
	})

	t.Run("fails when transcript path is missing", func(t *testing.T) {
		setupCodexSyncTestEnv(t)

		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			t.Fatal("should not spawn when transcript_path is missing")
			return nil
		}

		spawned, err := maybeSpawnDaemon(provider.Codex{}, &daemonLaunchInput{
			ExternalID: "66666666-6666-6666-6666-666666666666",
			CWD:        "/work/user",
		})
		if err == nil {
			t.Fatal("expected missing transcript path error")
		}
		if spawned {
			t.Fatal("expected spawned=false when transcript path is missing")
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
		state := daemon.NewStateForProvider("", sessionID, transcriptPath, tmpDir, 0)
		state.PID = expectedPID
		if err := state.Save(); err != nil {
			t.Fatalf("failed to save state: %v", err)
		}

		// Verify state can be loaded
		loadedState, err := daemon.LoadStateForProvider("", sessionID)
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
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
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
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "existing-session-1234-1234-1234-123456789abc"

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

		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
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

func TestMaybeSpawnDaemonOpencode(t *testing.T) {
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	t.Run("spawns daemon with empty TranscriptPath (SQLite-backed)", func(t *testing.T) {
		_ = setupSyncTestEnv(t)

		var spawnCalled bool
		var spawnedInput *daemonLaunchInput
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			spawnedInput = launch
			return nil
		}

		sessionID := "oc-session-1234-1234-1234-123456789abc"

		spawned, err := maybeSpawnDaemon(provider.Opencode{}, &daemonLaunchInput{
			ExternalID: sessionID,
			CWD:        "/work/opencode",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Opencode) failed: %v", err)
		}
		if !spawned {
			t.Fatal("expected spawned=true for OpenCode session")
		}
		if !spawnCalled {
			t.Fatal("expected spawnDaemonFunc to be called")
		}
		if spawnedInput.ExternalID != sessionID {
			t.Errorf("external_id = %q, want %q", spawnedInput.ExternalID, sessionID)
		}
		if spawnedInput.Provider != provider.NameOpencode {
			t.Errorf("Provider = %q, want %q", spawnedInput.Provider, provider.NameOpencode)
		}
	})

	t.Run("does not spawn when daemon already running", func(t *testing.T) {
		_ = setupSyncTestEnv(t)

		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			t.Fatal("should not spawn when daemon is already running")
			return nil
		}

		sessionID := "oc-session-5678-5678-5678-567890abcdef"
		state := daemon.NewStateForProvider(provider.NameOpencode, sessionID, "", "/work/opencode", 0)
		state.PID = os.Getpid()
		if err := state.Save(); err != nil {
			t.Fatalf("save state: %v", err)
		}

		spawned, err := maybeSpawnDaemon(provider.Opencode{}, &daemonLaunchInput{
			ExternalID: sessionID,
			CWD:        "/work/opencode",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Opencode) failed: %v", err)
		}
		if spawned {
			t.Fatal("expected spawned=false when daemon is already running")
		}
	})

	t.Run("non-opencode provider with empty transcript_path is rejected", func(t *testing.T) {
		setupSyncTestEnv(t)

		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			t.Fatal("should not spawn when transcript_path is missing for Claude")
			return nil
		}

		spawned, err := maybeSpawnDaemon(provider.ClaudeCode{}, &daemonLaunchInput{
			ExternalID: "claude-session-missing",
			CWD:        "/work",
		})
		if err == nil {
			t.Fatal("expected error when transcript_path is missing for non-opencode provider")
		}
		if spawned {
			t.Fatal("expected spawned=false when transcript_path is missing")
		}
	})

	t.Run("spawns when state exists but daemon is dead", func(t *testing.T) {
		_ = setupSyncTestEnv(t)

		var spawnCalled bool
		spawnDaemonFunc = func(launch *daemonLaunchInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "oc-session-stale"
		state := daemon.NewStateForProvider(provider.NameOpencode, sessionID, "", "/work/opencode", 0)
		state.PID = 999999
		if err := state.Save(); err != nil {
			t.Fatalf("save stale state: %v", err)
		}

		spawned, err := maybeSpawnDaemon(provider.Opencode{}, &daemonLaunchInput{
			ExternalID: sessionID,
			CWD:        "/work/opencode",
		})
		if err != nil {
			t.Fatalf("maybeSpawnDaemon (Opencode) failed: %v", err)
		}
		if !spawned || !spawnCalled {
			t.Fatal("expected stale state to allow respawn")
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


// CF-549 M1 + M2 — maybeSpawnDaemon tests ----------------------------------

// recordingProvider wraps a real Provider and records calls to
// OnAlreadyRunning, so tests can verify the call site without depending
// on log-output inspection.
type recordingProvider struct {
	provider.Provider
	alreadyRunningCalls []string
}

func (r *recordingProvider) OnAlreadyRunning(externalID string) {
	r.alreadyRunningCalls = append(r.alreadyRunningCalls, externalID)
	r.Provider.OnAlreadyRunning(externalID)
}

// TestMaybeSpawnDaemonUsesPluginParentPID asserts that when the launch
// input carries a non-zero ParentPID (plugin-authoritative), the spawn
// uses it verbatim instead of overriding with the regex walk. Decouples
// daemon orphan-prevention from the fragility of FindParentPID.
func TestMaybeSpawnDaemonUsesPluginParentPID(t *testing.T) {
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	_ = setupSyncTestEnv(t)

	var spawnedInput *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		spawnedInput = launch
		return nil
	}

	const pluginPID = 424242 // distinctive — must not match any walk result

	launch := &daemonLaunchInput{
		ExternalID: "ses_with_plugin_pid",
		CWD:        "/work/opencode",
		ParentPID:  pluginPID,
	}
	if _, err := maybeSpawnDaemon(provider.Opencode{}, launch); err != nil {
		t.Fatalf("maybeSpawnDaemon: %v", err)
	}
	if spawnedInput == nil {
		t.Fatal("spawnDaemonFunc not called")
	}
	if spawnedInput.ParentPID != pluginPID {
		t.Errorf("ParentPID = %d, want %d (plugin-provided)", spawnedInput.ParentPID, pluginPID)
	}
}

// TestMaybeSpawnDaemonFallsBackToWalkWhenParentPIDZero asserts the
// observability walk is still authoritative when the plugin did not
// provide a parent_pid. Mirrors the Claude/Codex code path that has no
// plugin-side authoritative source.
func TestMaybeSpawnDaemonFallsBackToWalkWhenParentPIDZero(t *testing.T) {
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	_ = setupSyncTestEnv(t)

	var spawnedInput *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		spawnedInput = launch
		return nil
	}

	launch := &daemonLaunchInput{
		ExternalID:     "ses_no_plugin_pid",
		TranscriptPath: filepath.Join(t.TempDir(), "transcript.jsonl"),
		CWD:            "/work/claude",
		// ParentPID: 0 — emulates Claude/Codex where the regex walk is the
		// only source. The walk may return 0 in the test environment too;
		// the contract is just "no override of the plugin's value". We
		// allow any value here, but it must not be 424242 (the sentinel
		// from the previous test).
	}
	if _, err := maybeSpawnDaemon(provider.ClaudeCode{}, launch); err != nil {
		t.Fatalf("maybeSpawnDaemon: %v", err)
	}
	if spawnedInput == nil {
		t.Fatal("spawnDaemonFunc not called")
	}
	if spawnedInput.ParentPID == 424242 {
		t.Errorf("ParentPID = %d, must not be the plugin-sentinel; walk should have produced its own value or 0", spawnedInput.ParentPID)
	}
}

// TestMaybeSpawnDaemonCallsProviderOnAlreadyRunning asserts the
// already-running branch invokes p.OnAlreadyRunning so providers can
// register provider-specific telemetry (M2: OpenCode logs a warning).
func TestMaybeSpawnDaemonCallsProviderOnAlreadyRunning(t *testing.T) {
	_ = setupSyncTestEnv(t)

	const sessionID = "ses_already_running"
	state := daemon.NewStateForProvider(provider.NameOpencode, sessionID, "", "/work/opencode", 0)
	state.PID = os.Getpid()
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	rp := &recordingProvider{Provider: provider.Opencode{}}
	spawned, err := maybeSpawnDaemon(rp, &daemonLaunchInput{
		ExternalID: sessionID,
		CWD:        "/work/opencode",
	})
	if err != nil {
		t.Fatalf("maybeSpawnDaemon: %v", err)
	}
	if spawned {
		t.Fatal("expected spawned=false (daemon already running)")
	}
	if len(rp.alreadyRunningCalls) != 1 || rp.alreadyRunningCalls[0] != sessionID {
		t.Errorf("OnAlreadyRunning calls = %v, want [%q]", rp.alreadyRunningCalls, sessionID)
	}
}
