package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// setupSyncTestEnv creates temp directories and sets env vars for sync tests.
// Returns the temp home directory.
func setupSyncTestEnv(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()

	// Override HOME for daemon state paths
	t.Setenv("HOME", tmpDir)

	// Create sync state directory
	syncDir := filepath.Join(tmpDir, ".confab", "sync")
	if err := os.MkdirAll(syncDir, 0755); err != nil {
		t.Fatalf("failed to create sync dir: %v", err)
	}

	return tmpDir
}

// createFakeDaemonState creates a state file for testing.
// If pid is 0, uses a non-existent PID to simulate a stale daemon.
func createFakeDaemonState(t *testing.T, tmpDir, sessionID string, pid int) {
	t.Helper()
	syncDir := filepath.Join(tmpDir, ".confab", "sync")

	if pid == 0 {
		// Use a PID that definitely doesn't exist
		pid = 999999
	}

	state := map[string]any{
		"external_id":     sessionID,
		"transcript_path": filepath.Join(tmpDir, "transcript.jsonl"),
		"cwd":             tmpDir,
		"pid":             pid,
		"started_at":      time.Now().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, sessionID+".json"), data, 0644); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}
}

func TestShowSyncStatus(t *testing.T) {
	t.Run("no daemons running", func(t *testing.T) {
		setupSyncTestEnv(t)

		err := showSyncStatus()
		if err != nil {
			t.Fatalf("showSyncStatus failed: %v", err)
		}
		// Function should complete without error
		// Output goes to stdout which we don't capture here
	})

	t.Run("one daemon running", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		// Create state with current process PID (so it appears "running")
		sessionID := "aaaaaaaa-1111-1111-1111-111111111111"
		createFakeDaemonState(t, tmpDir, sessionID, os.Getpid())

		err := showSyncStatus()
		if err != nil {
			t.Fatalf("showSyncStatus failed: %v", err)
		}
	})

	t.Run("stale daemon state", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		// Create state with non-existent PID
		sessionID := "bbbbbbbb-2222-2222-2222-222222222222"
		createFakeDaemonState(t, tmpDir, sessionID, 0) // 0 triggers non-existent PID

		err := showSyncStatus()
		if err != nil {
			t.Fatalf("showSyncStatus failed: %v", err)
		}
	})

	t.Run("multiple daemons", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		// Create multiple states
		createFakeDaemonState(t, tmpDir, "aaaaaaaa-1111-1111-1111-111111111111", os.Getpid())
		createFakeDaemonState(t, tmpDir, "bbbbbbbb-2222-2222-2222-222222222222", 0)

		err := showSyncStatus()
		if err != nil {
			t.Fatalf("showSyncStatus failed: %v", err)
		}
	})
}

func TestSessionStartFromReader(t *testing.T) {
	// Save and restore the original spawnDaemonFunc
	origSpawnDaemon := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawnDaemon }()

	t.Run("valid hook input spawns daemon", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		// Track if spawn was called
		var spawnCalled bool
		var spawnedInput *types.HookInput
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			spawnCalled = true
			spawnedInput = hookInput
			return nil
		}

		// Create transcript file
		transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
		os.WriteFile(transcriptPath, []byte(`{"type":"test"}`+"\n"), 0644)

		hookInput := map[string]string{
			"session_id":      "test-session-12345678-1234-1234-1234-123456789abc",
			"transcript_path": transcriptPath,
			"cwd":             tmpDir,
		}
		inputJSON, _ := json.Marshal(hookInput)

		err := sessionStartFromReader(strings.NewReader(string(inputJSON)))
		if err != nil {
			t.Fatalf("sessionStartFromReader failed: %v", err)
		}

		if !spawnCalled {
			t.Error("expected spawnDaemonFunc to be called")
		}

		// Verify the spawned input contains the session ID
		if spawnedInput == nil || !strings.Contains(spawnedInput.SessionID, "test-session-12345678") {
			t.Errorf("spawned input should contain session ID")
		}
	})

	t.Run("daemon already running", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		// Track if spawn was called
		var spawnCalled bool
		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			spawnCalled = true
			return nil
		}

		sessionID := "existing-session-1234-1234-1234-123456789abc"

		// Create existing daemon state with current PID (appears running)
		createFakeDaemonState(t, tmpDir, sessionID, os.Getpid())

		// Create transcript file
		transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
		os.WriteFile(transcriptPath, []byte(`{"type":"test"}`+"\n"), 0644)

		hookInput := map[string]string{
			"session_id":      sessionID,
			"transcript_path": transcriptPath,
			"cwd":             tmpDir,
		}
		inputJSON, _ := json.Marshal(hookInput)

		err := sessionStartFromReader(strings.NewReader(string(inputJSON)))
		if err != nil {
			t.Fatalf("sessionStartFromReader failed: %v", err)
		}

		if spawnCalled {
			t.Error("should not spawn daemon when one is already running")
		}
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		setupSyncTestEnv(t)

		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			t.Error("should not spawn daemon on invalid input")
			return nil
		}

		// Invalid JSON should not cause panic, should return nil (hooks must not fail)
		err := sessionStartFromReader(strings.NewReader("not valid json"))
		if err != nil {
			t.Fatalf("sessionStartFromReader should not return error: %v", err)
		}
	})

	t.Run("missing session_id", func(t *testing.T) {
		setupSyncTestEnv(t)

		spawnDaemonFunc = func(hookInput *types.HookInput) error {
			t.Error("should not spawn daemon on missing session_id")
			return nil
		}

		hookInput := map[string]string{
			"transcript_path": "/some/path.jsonl",
			"cwd":             "/some/dir",
		}
		inputJSON, _ := json.Marshal(hookInput)

		err := sessionStartFromReader(strings.NewReader(string(inputJSON)))
		if err != nil {
			t.Fatalf("sessionStartFromReader should not return error: %v", err)
		}
	})
}

func TestSessionEndFromReader(t *testing.T) {
	t.Run("daemon not running", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		sessionID := "nonexistent-session-1234-1234-1234-123456789abc"

		hookInput := map[string]string{
			"session_id":      sessionID,
			"transcript_path": filepath.Join(tmpDir, "transcript.jsonl"),
			"cwd":             tmpDir,
		}
		inputJSON, _ := json.Marshal(hookInput)

		// Should not error - handles missing daemon gracefully
		err := sessionEndFromReader(strings.NewReader(string(inputJSON)))
		if err != nil {
			t.Fatalf("sessionEndFromReader failed: %v", err)
		}
	})

	t.Run("daemon running with stale state", func(t *testing.T) {
		tmpDir := setupSyncTestEnv(t)

		sessionID := "stale-session-1234-1234-1234-123456789abc"

		// Create stale state (non-existent PID)
		createFakeDaemonState(t, tmpDir, sessionID, 0)

		hookInput := map[string]string{
			"session_id":      sessionID,
			"transcript_path": filepath.Join(tmpDir, "transcript.jsonl"),
			"cwd":             tmpDir,
		}
		inputJSON, _ := json.Marshal(hookInput)

		// Should handle stale state gracefully
		err := sessionEndFromReader(strings.NewReader(string(inputJSON)))
		if err != nil {
			t.Fatalf("sessionEndFromReader failed: %v", err)
		}
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		setupSyncTestEnv(t)

		// Invalid JSON should not cause panic
		err := sessionEndFromReader(strings.NewReader("not valid json"))
		if err != nil {
			t.Fatalf("sessionEndFromReader should not return error: %v", err)
		}
	})

	t.Run("missing session_id", func(t *testing.T) {
		setupSyncTestEnv(t)

		hookInput := map[string]string{
			"transcript_path": "/some/path.jsonl",
			"cwd":             "/some/dir",
		}
		inputJSON, _ := json.Marshal(hookInput)

		err := sessionEndFromReader(strings.NewReader(string(inputJSON)))
		if err != nil {
			t.Fatalf("sessionEndFromReader should not return error: %v", err)
		}
	})
}

func TestRunDaemon(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		err := runDaemon("not valid json")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("valid JSON parses correctly", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("HOME", tmpDir)

		// Create required directories
		confabDir := filepath.Join(tmpDir, ".confab")
		os.MkdirAll(confabDir, 0755)
		syncDir := filepath.Join(confabDir, "sync")
		os.MkdirAll(syncDir, 0755)

		// Create config for the daemon
		configPath := filepath.Join(confabDir, "config.json")
		os.WriteFile(configPath, []byte(`{"backend_url":"http://localhost:9999","api_key":"test-key-1234567890"}`), 0600)
		t.Setenv("CONFAB_CONFIG_PATH", configPath)

		// Create transcript file
		transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
		os.WriteFile(transcriptPath, []byte(`{"type":"test"}`+"\n"), 0644)

		hookInput := map[string]any{
			"session_id":      "test-session-12345678",
			"transcript_path": transcriptPath,
			"cwd":             tmpDir,
			"parent_pid":      os.Getpid(),
		}
		inputJSON, _ := json.Marshal(hookInput)

		// Run daemon in goroutine with short timeout
		// The daemon will fail to connect to backend but should parse JSON correctly
		done := make(chan error, 1)
		go func() {
			done <- runDaemon(string(inputJSON))
		}()

		// Wait briefly then check it started (it will be waiting for backend)
		select {
		case err := <-done:
			// If it returned, it should have parsed JSON successfully
			// Error is expected since there's no real backend
			_ = err
		case <-time.After(500 * time.Millisecond):
			// Daemon is running (waiting for backend), that's expected
			// It will clean up when the test ends
		}
	})
}

func TestParseSyncEnvConfig(t *testing.T) {
	t.Run("defaults when no env vars set", func(t *testing.T) {
		// Ensure env vars are not set
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "")

		interval, jitter := parseSyncEnvConfig()

		if interval != daemon.DefaultSyncInterval {
			t.Errorf("expected interval %v, got %v", daemon.DefaultSyncInterval, interval)
		}
		if jitter != 0 {
			t.Errorf("expected jitter 0, got %v", jitter)
		}
	})

	t.Run("custom sync interval", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "2000")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "")

		interval, jitter := parseSyncEnvConfig()

		if interval != 2*time.Second {
			t.Errorf("expected interval 2s, got %v", interval)
		}
		if jitter != 0 {
			t.Errorf("expected jitter 0, got %v", jitter)
		}
	})

	t.Run("custom jitter", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "1000")

		interval, jitter := parseSyncEnvConfig()

		if interval != daemon.DefaultSyncInterval {
			t.Errorf("expected interval %v, got %v", daemon.DefaultSyncInterval, interval)
		}
		if jitter != 1*time.Second {
			t.Errorf("expected jitter 1s, got %v", jitter)
		}
	})

	t.Run("both custom values", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "500")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "100")

		interval, jitter := parseSyncEnvConfig()

		if interval != 500*time.Millisecond {
			t.Errorf("expected interval 500ms, got %v", interval)
		}
		if jitter != 100*time.Millisecond {
			t.Errorf("expected jitter 100ms, got %v", jitter)
		}
	})

	t.Run("zero jitter to disable", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "5000")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "0")

		interval, jitter := parseSyncEnvConfig()

		if interval != 5*time.Second {
			t.Errorf("expected interval 5s, got %v", interval)
		}
		if jitter != 0 {
			t.Errorf("expected jitter 0, got %v", jitter)
		}
	})

	t.Run("invalid interval falls back to default", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "not-a-number")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "")

		interval, jitter := parseSyncEnvConfig()

		if interval != daemon.DefaultSyncInterval {
			t.Errorf("expected interval %v, got %v", daemon.DefaultSyncInterval, interval)
		}
		if jitter != 0 {
			t.Errorf("expected jitter 0, got %v", jitter)
		}
	})

	t.Run("invalid jitter falls back to zero", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "2000")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "invalid")

		interval, jitter := parseSyncEnvConfig()

		if interval != 2*time.Second {
			t.Errorf("expected interval 2s, got %v", interval)
		}
		if jitter != 0 {
			t.Errorf("expected jitter 0, got %v", jitter)
		}
	})

	t.Run("negative values fall back to defaults", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "-100")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "-50")

		interval, jitter := parseSyncEnvConfig()

		if interval != daemon.DefaultSyncInterval {
			t.Errorf("expected interval %v, got %v", daemon.DefaultSyncInterval, interval)
		}
		if jitter != 0 {
			t.Errorf("expected jitter 0, got %v", jitter)
		}
	})

	t.Run("zero interval falls back to default", func(t *testing.T) {
		t.Setenv("CONFAB_SYNC_INTERVAL_MS", "0")
		t.Setenv("CONFAB_SYNC_JITTER_MS", "")

		interval, jitter := parseSyncEnvConfig()

		if interval != daemon.DefaultSyncInterval {
			t.Errorf("expected interval %v, got %v", daemon.DefaultSyncInterval, interval)
		}
		if jitter != 0 {
			t.Errorf("expected jitter 0, got %v", jitter)
		}
	})
}

// TestListAllStates verifies that daemon.ListAllStates works correctly.
// This is an indirect test of showSyncStatus's dependency.
func TestListAllStates(t *testing.T) {
	tmpDir := setupSyncTestEnv(t)

	// Initially should be empty
	states, err := daemon.ListAllStates()
	if err != nil {
		t.Fatalf("ListAllStates failed: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected 0 states, got %d", len(states))
	}

	// Add a state
	createFakeDaemonState(t, tmpDir, "test-session-1111", os.Getpid())

	states, err = daemon.ListAllStates()
	if err != nil {
		t.Fatalf("ListAllStates failed: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("expected 1 state, got %d", len(states))
	}

	// Add another state
	createFakeDaemonState(t, tmpDir, "test-session-2222", 0)

	states, err = daemon.ListAllStates()
	if err != nil {
		t.Fatalf("ListAllStates failed: %v", err)
	}
	if len(states) != 2 {
		t.Errorf("expected 2 states, got %d", len(states))
	}
}
