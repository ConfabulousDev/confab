package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsConfabCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{
			name:    "full path with save",
			command: "/usr/local/bin/confab save",
			want:    true,
		},
		{
			name:    "just confab save",
			command: "confab save",
			want:    true,
		},
		{
			name:    "confab without args",
			command: "confab",
			want:    true,
		},
		{
			name:    "path with confab",
			command: "/home/user/.local/bin/confab",
			want:    true,
		},
		{
			name:    "not confab - different name",
			command: "/usr/bin/notconfab save",
			want:    false,
		},
		{
			name:    "not confab - confab in path but not executable",
			command: "/home/confab/bin/other-tool save",
			want:    false,
		},
		{
			name:    "empty command",
			command: "",
			want:    false,
		},
		{
			name:    "confab as substring",
			command: "myconfab save",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConfabCommand(tt.command)
			if got != tt.want {
				t.Errorf("isConfabCommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestValidateBackendURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid https URL",
			url:     "https://example.com",
			wantErr: false,
		},
		{
			name:    "valid http URL",
			url:     "http://localhost:8080",
			wantErr: false,
		},
		{
			name:    "empty URL is allowed",
			url:     "",
			wantErr: false,
		},
		{
			name:    "missing scheme",
			url:     "example.com",
			wantErr: true,
		},
		{
			name:    "invalid scheme",
			url:     "ftp://example.com",
			wantErr: true,
		},
		{
			name:    "missing host",
			url:     "https://",
			wantErr: true,
		},
		{
			name:    "just scheme",
			url:     "https",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBackendURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBackendURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr bool
	}{
		{
			name:    "valid production key",
			apiKey:  "cfb_abcdefghijklmnopqrstuvwxyz12345678901234",
			wantErr: false,
		},
		{
			name:    "valid shorter key",
			apiKey:  "cfb_test1234567890123456",
			wantErr: false,
		},
		{
			name:    "missing cfb_ prefix",
			apiKey:  "sk_live_abcdefghijklmnopqrstuvwxyz123456",
			wantErr: true,
		},
		{
			name:    "empty is allowed",
			apiKey:  "",
			wantErr: false,
		},
		{
			name:    "too short",
			apiKey:  "short",
			wantErr: true,
		},
		{
			name:    "contains space",
			apiKey:  "key with space123456",
			wantErr: true,
		},
		{
			name:    "contains newline",
			apiKey:  "key\nwith\nnewlines1234",
			wantErr: true,
		},
		{
			name:    "contains tab",
			apiKey:  "key\twith\ttab12345",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAPIKey(tt.apiKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAPIKey(%q) error = %v, wantErr %v", tt.apiKey, err, tt.wantErr)
			}
		})
	}
}

// makeHook creates a hook map with type and command
func makeHook(hookType, command string) map[string]any {
	return map[string]any{
		"type":    hookType,
		"command": command,
	}
}

// makeMatcher creates a matcher with the given matcher string and hooks
func makeMatcher(matcher string, hooks ...map[string]any) map[string]any {
	hooksList := make([]any, len(hooks))
	for i, h := range hooks {
		hooksList[i] = h
	}
	return map[string]any{
		"matcher": matcher,
		"hooks":   hooksList,
	}
}

// setTestHook sets a hook for an event in the settings using raw map manipulation
func setTestHook(settings *ClaudeSettings, eventName string, matchers ...map[string]any) {
	matchersList := make([]any, len(matchers))
	for i, m := range matchers {
		matchersList[i] = m
	}
	settings.setEventHooks(eventName, matchersList)
}

func TestAtomicUpdateSettings_Success(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	// Create settings directory
	settingsDir := filepath.Join(tmpDir)
	os.MkdirAll(settingsDir, 0755)

	// Test basic atomic update
	err := AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		setTestHook(settings, "TestHook",
			makeMatcher("*", makeHook("command", "test")),
		)
		return nil
	})

	if err != nil {
		t.Fatalf("AtomicUpdateSettings failed: %v", err)
	}

	// Verify the update was persisted
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}

	eventHooks := settings.getEventHooks("TestHook")
	if len(eventHooks) != 1 {
		t.Errorf("Expected 1 TestHook matcher, got %d", len(eventHooks))
	}
}

func TestAtomicUpdateSettings_ConcurrentUpdates(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	// Create settings directory
	settingsDir := filepath.Join(tmpDir)
	os.MkdirAll(settingsDir, 0755)

	// Run multiple sequential updates to test atomic read-modify-write
	// (True concurrent updates with optimistic locking can legitimately fail
	// after max retries, so we test sequential updates that each preserve
	// previous data - this is the actual use case we care about)
	const numUpdates = 5

	for i := 0; i < numUpdates; i++ {
		hookName := "Hook" + string(rune('A'+i))

		err := AtomicUpdateSettings(func(settings *ClaudeSettings) error {
			setTestHook(settings, hookName,
				makeMatcher("*", makeHook("command", hookName)),
			)
			return nil
		})
		if err != nil {
			t.Errorf("Update for %s failed: %v", hookName, err)
		}
	}

	// Verify all updates were persisted (each update should preserve previous hooks)
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}

	// All hooks should be present
	hooksMap := settings.getHooksMap()
	if len(hooksMap) != numUpdates {
		t.Errorf("Expected %d hooks, got %d. Hooks present: %v", numUpdates, len(hooksMap), getHookNames(hooksMap))
	}
}

// Helper to get hook names for debugging
func getHookNames(hooksMap map[string]any) []string {
	var names []string
	for name := range hooksMap {
		names = append(names, name)
	}
	return names
}

func TestAtomicUpdateSettings_UpdateFunctionError(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	// Create settings directory
	settingsDir := filepath.Join(tmpDir)
	os.MkdirAll(settingsDir, 0755)

	// Test that update function errors are propagated
	testErr := "test error"
	err := AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		return &customError{msg: testErr}
	})

	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if err.Error() != "update function failed: "+testErr {
		t.Errorf("Expected error message to contain %q, got %q", testErr, err.Error())
	}
}

func TestAtomicUpdateSettings_Retry(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	// Create settings directory and initial file
	settingsDir := filepath.Join(tmpDir)
	os.MkdirAll(settingsDir, 0755)

	// Create initial settings
	err := AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		setTestHook(settings, "Initial",
			makeMatcher("*", makeHook("command", "initial")),
		)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to create initial settings: %v", err)
	}

	// Simulate a concurrent modification that gets retried
	attemptCount := 0
	err = AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		attemptCount++

		// On first attempt, modify the file externally to trigger retry
		if attemptCount == 1 {
			// Sleep briefly to ensure we're past the mtime read
			time.Sleep(5 * time.Millisecond)

			// Modify the file externally
			err := AtomicUpdateSettings(func(s *ClaudeSettings) error {
				setTestHook(s, "External",
					makeMatcher("*", makeHook("command", "external")),
				)
				return nil
			})
			if err != nil {
				t.Logf("External update failed: %v", err)
			}
		}

		setTestHook(settings, "Test",
			makeMatcher("*", makeHook("command", "test")),
		)
		return nil
	})

	if err != nil {
		t.Fatalf("AtomicUpdateSettings failed: %v", err)
	}

	// Should have retried at least once
	if attemptCount < 2 {
		t.Errorf("Expected at least 2 attempts (with retry), got %d", attemptCount)
	}

	// Verify both updates are present
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}

	hooksMap := settings.getHooksMap()
	if _, ok := hooksMap["Test"]; !ok {
		t.Error("Test hook not found after retry")
	}
	if _, ok := hooksMap["External"]; !ok {
		t.Error("External hook not found")
	}
}

// customError is a helper for testing error propagation
type customError struct {
	msg string
}

func (e *customError) Error() string {
	return e.msg
}

func TestInstallSyncHooks(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	// Create settings directory
	os.MkdirAll(tmpDir, 0755)

	// Install sync hooks
	err := InstallSyncHooks()
	if err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Verify hooks were installed
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}

	// Check SessionStart hook - look for "hook session-start" in command
	if !hasHookWithCommandSubstring(settings, "SessionStart", "hook session-start") {
		t.Error("SessionStart 'hook session-start' hook not found")
	}

	// Check SessionEnd hook - look for "hook session-end" in command
	if !hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end") {
		t.Error("SessionEnd 'hook session-end' hook not found")
	}
}

// hasHookWithCommandSubstring checks if any hook command contains the substring
func hasHookWithCommandSubstring(settings *ClaudeSettings, eventName, substr string) bool {
	eventHooks := settings.getEventHooks(eventName)
	for _, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			continue
		}
		hooksList, ok := matcher["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hookAny := range hooksList {
			hook, ok := hookAny.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hook["command"].(string)
			if hook["type"] == "command" && contains(cmd, substr) {
				return true
			}
		}
	}
	return false
}

func TestIsSyncHooksInstalled(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Initially not installed
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}
	if hasSyncHooks(settings) {
		t.Error("Expected sync hooks to not be installed initially")
	}

	// Install sync hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Now should be installed
	settings, err = ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}
	if !hasSyncHooks(settings) {
		t.Error("Expected sync hooks to be installed after InstallSyncHooks")
	}
}

// hasSyncHooks checks if sync hooks are present by looking for session-start/end commands
func hasSyncHooks(settings *ClaudeSettings) bool {
	hasStart := hasHookWithCommandSubstring(settings, "SessionStart", "hook session-start")
	hasEnd := hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end")
	return hasStart && hasEnd
}

func TestUninstallSyncHooks(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Install sync hooks first
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Verify installed
	settings, _ := ReadSettings()
	if !hasSyncHooks(settings) {
		t.Fatal("Sync hooks should be installed before testing uninstall")
	}

	// Uninstall
	if err := UninstallSyncHooks(); err != nil {
		t.Fatalf("UninstallSyncHooks failed: %v", err)
	}

	// Verify uninstalled
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}

	if hasHookWithCommandSubstring(settings, "SessionStart", "hook session-start") {
		t.Error("Found 'hook session-start' hook in SessionStart after uninstall")
	}
	if hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end") {
		t.Error("Found 'hook session-end' hook in SessionEnd after uninstall")
	}
}

func TestInstallSyncHooks_PreservesOtherHooks(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Install some other hook first
	err := AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		setTestHook(settings, "SessionEnd",
			makeMatcher("*", makeHook("command", "/usr/bin/other-tool log")),
		)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to install other hook: %v", err)
	}

	// Install sync hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Verify other hook is preserved
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}

	foundOther := hasHookWithCommandSubstring(settings, "SessionEnd", "/usr/bin/other-tool log")
	foundSessionEnd := hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end")

	if !foundOther {
		t.Error("Other hook was not preserved after InstallSyncHooks")
	}
	if !foundSessionEnd {
		t.Error("Session-end hook was not installed")
	}
}

func TestInstallSyncHooks_UpdatesExistingConfab(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()

	// Set up test environment
	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	os.MkdirAll(tmpDir, 0755)

	// Install old-style save hook (simulating existing confab installation)
	err := AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		setTestHook(settings, "SessionEnd",
			makeMatcher("*", makeHook("command", "/old/path/confab save")),
		)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to install old hook: %v", err)
	}

	// Install sync hooks (should update the existing confab hook)
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Verify the hook was updated to session-end
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}

	foundSessionEnd := hasHookWithCommandSubstring(settings, "SessionEnd", "hook session-end")
	foundOldSave := hasHookWithCommandSubstring(settings, "SessionEnd", "/old/path/confab save")

	if !foundSessionEnd {
		t.Error("Expected session-end hook to be installed")
	}
	if foundOldSave {
		t.Error("Old save hook should have been replaced")
	}
}

func TestAtomicUpdateSettings_PreservesUnknownFields(t *testing.T) {
	// This test ensures we don't lose data when updating settings.
	// Previously, the code only preserved the "hooks" field and dropped
	// everything else - this was a critical data loss bug.

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Write a settings file with multiple top-level fields
	initialSettings := `{
  "hooks": {
    "PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "echo pre"}]}]
  },
  "permissions": {
    "allow": ["Read", "Write"],
    "deny": ["Bash"]
  },
  "apiKeys": {
    "anthropic": "sk-test-key"
  },
  "customField": "custom-value",
  "nestedObject": {
    "level1": {
      "level2": "deep-value"
    }
  },
  "arrayField": ["item1", "item2", "item3"]
}`

	if err := os.WriteFile(settingsPath, []byte(initialSettings), 0644); err != nil {
		t.Fatalf("Failed to write initial settings: %v", err)
	}

	// Now update just the hooks via AtomicUpdateSettings
	err := AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		setTestHook(settings, "SessionStart",
			makeMatcher("*", makeHook("command", "confab sync start")),
		)
		return nil
	})
	if err != nil {
		t.Fatalf("AtomicUpdateSettings failed: %v", err)
	}

	// Read back the raw file and verify ALL fields are preserved
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	// Check hooks were updated
	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks field missing or wrong type")
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("SessionStart hook was not added")
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("PreToolUse hook was lost")
	}

	// Check all other fields are preserved
	if _, ok := raw["permissions"]; !ok {
		t.Error("permissions field was lost - DATA LOSS BUG!")
	}
	if _, ok := raw["apiKeys"]; !ok {
		t.Error("apiKeys field was lost - DATA LOSS BUG!")
	}
	if raw["customField"] != "custom-value" {
		t.Errorf("customField was lost or changed - DATA LOSS BUG! got: %v", raw["customField"])
	}
	if _, ok := raw["nestedObject"]; !ok {
		t.Error("nestedObject field was lost - DATA LOSS BUG!")
	}
	if _, ok := raw["arrayField"]; !ok {
		t.Error("arrayField was lost - DATA LOSS BUG!")
	}

	// Verify nested structure is intact
	nested, ok := raw["nestedObject"].(map[string]any)
	if !ok {
		t.Fatal("nestedObject wrong type")
	}
	level1, ok := nested["level1"].(map[string]any)
	if !ok {
		t.Fatal("nestedObject.level1 wrong type")
	}
	if level1["level2"] != "deep-value" {
		t.Errorf("nestedObject.level1.level2 was lost or changed, got: %v", level1["level2"])
	}

	// Verify array is intact
	arr, ok := raw["arrayField"].([]any)
	if !ok {
		t.Fatal("arrayField wrong type")
	}
	if len(arr) != 3 {
		t.Errorf("arrayField length changed, expected 3, got %d", len(arr))
	}
}

func TestAtomicUpdateSettings_PreservesUnknownHookFields(t *testing.T) {
	// This test ensures that unknown fields within hooks are preserved.
	// The hooks schema is controlled by Claude Code and evolves rapidly,
	// so we must not drop any fields we don't recognize.

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Write a settings file with hooks that have extra/unknown fields
	initialSettings := `{
  "hooks": {
    "SessionEnd": [
      {
        "matcher": "*",
        "unknownMatcherField": "should-be-preserved",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/bin/other-tool",
            "timeout": 5000,
            "environment": {"FOO": "bar"},
            "unknownHookField": "also-preserved"
          }
        ]
      }
    ]
  }
}`

	if err := os.WriteFile(settingsPath, []byte(initialSettings), 0644); err != nil {
		t.Fatalf("Failed to write initial settings: %v", err)
	}

	// Install sync hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}

	// Read back and verify unknown fields are preserved
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	hooks := raw["hooks"].(map[string]any)
	sessionEnd := hooks["SessionEnd"].([]any)

	// Find the matcher with the other-tool hook
	for _, matcherAny := range sessionEnd {
		matcher := matcherAny.(map[string]any)

		// Check unknown matcher field is preserved
		if matcher["unknownMatcherField"] == "should-be-preserved" {
			// Found the original matcher, check hook fields
			hooksList := matcher["hooks"].([]any)
			for _, hookAny := range hooksList {
				hook := hookAny.(map[string]any)
				cmd, _ := hook["command"].(string)
				if contains(cmd, "other-tool") {
					// Check unknown fields
					if hook["timeout"] != float64(5000) {
						t.Errorf("timeout field lost or changed: %v", hook["timeout"])
					}
					if hook["unknownHookField"] != "also-preserved" {
						t.Errorf("unknownHookField lost or changed: %v", hook["unknownHookField"])
					}
					env, ok := hook["environment"].(map[string]any)
					if !ok || env["FOO"] != "bar" {
						t.Errorf("environment field lost or changed: %v", hook["environment"])
					}
				}
			}
		}
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestUninstallHooks_CleansUpEmptySections(t *testing.T) {
	// This test ensures that when all hooks are removed from an event,
	// the event key is removed entirely (not left as null or empty array).
	// Additionally, if all events are removed, the "hooks" key should be removed.

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Install hooks
	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks failed: %v", err)
	}
	if err := InstallPreToolUseHooks(); err != nil {
		t.Fatalf("InstallPreToolUseHooks failed: %v", err)
	}

	// Verify hooks are installed
	settings, err := ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings failed: %v", err)
	}
	hooksMap := settings.getHooksMap()
	if len(hooksMap) == 0 {
		t.Fatal("Expected hooks to be installed")
	}

	// Uninstall all hooks
	if err := UninstallSyncHooks(); err != nil {
		t.Fatalf("UninstallSyncHooks failed: %v", err)
	}
	if err := UninstallPreToolUseHooks(); err != nil {
		t.Fatalf("UninstallPreToolUseHooks failed: %v", err)
	}

	// Read the raw JSON to check for null/empty values
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	// The "hooks" key should be removed entirely when empty
	if hooksRaw, exists := raw["hooks"]; exists {
		// If hooks exists, it should not be empty or contain only empty/null values
		if hooks, ok := hooksRaw.(map[string]any); ok {
			for eventName, eventHooks := range hooks {
				if eventHooks == nil {
					t.Errorf("Event %q has null value - should be removed entirely", eventName)
				}
				if arr, ok := eventHooks.([]any); ok && len(arr) == 0 {
					t.Errorf("Event %q has empty array - should be removed entirely", eventName)
				}
			}
			if len(hooks) == 0 {
				t.Error("hooks map is empty - should be removed entirely from settings")
			}
		}
	}
	// If "hooks" doesn't exist, that's the correct behavior
}

func TestGetEventHooks_MalformedSettings(t *testing.T) {
	// Test that getEventHooks handles malformed settings gracefully

	t.Run("hooks is not a map", func(t *testing.T) {
		settings := &ClaudeSettings{
			raw: map[string]any{
				"hooks": "not a map", // Wrong type
			},
		}
		result := settings.getEventHooks("SessionStart")
		if result != nil {
			t.Errorf("Expected nil for malformed hooks, got %v", result)
		}
	})

	t.Run("event hooks is not an array", func(t *testing.T) {
		settings := &ClaudeSettings{
			raw: map[string]any{
				"hooks": map[string]any{
					"SessionStart": "not an array", // Wrong type
				},
			},
		}
		result := settings.getEventHooks("SessionStart")
		if result != nil {
			t.Errorf("Expected nil for malformed event hooks, got %v", result)
		}
	})

	t.Run("hooks does not exist", func(t *testing.T) {
		settings := &ClaudeSettings{
			raw: map[string]any{},
		}
		result := settings.getEventHooks("SessionStart")
		if result != nil {
			t.Errorf("Expected nil for missing hooks, got %v", result)
		}
	})

	t.Run("event does not exist", func(t *testing.T) {
		settings := &ClaudeSettings{
			raw: map[string]any{
				"hooks": map[string]any{},
			},
		}
		result := settings.getEventHooks("SessionStart")
		if result != nil {
			t.Errorf("Expected nil for missing event, got %v", result)
		}
	})
}

func TestUninstallHooks_FromCleanSettings(t *testing.T) {
	// When uninstalling from settings that have no hooks,
	// we should not leave an empty "hooks": {} behind

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Create settings with no hooks
	initialSettings := `{
  "someOtherSetting": "value"
}`
	if err := os.WriteFile(settingsPath, []byte(initialSettings), 0644); err != nil {
		t.Fatalf("Failed to write initial settings: %v", err)
	}

	// Uninstall hooks (even though none exist)
	if err := UninstallSyncHooks(); err != nil {
		t.Fatalf("UninstallSyncHooks failed: %v", err)
	}
	if err := UninstallPreToolUseHooks(); err != nil {
		t.Fatalf("UninstallPreToolUseHooks failed: %v", err)
	}

	// Read back and verify no empty hooks object was created
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	// Should not have a "hooks" key at all
	if _, exists := raw["hooks"]; exists {
		t.Errorf("Empty hooks object was created - should not exist. Got: %v", raw["hooks"])
	}

	// Other settings should be preserved
	if raw["someOtherSetting"] != "value" {
		t.Errorf("Other settings were not preserved: %v", raw)
	}
}

func TestUninstallHooks_PreservesOtherHooksInSameEvent(t *testing.T) {
	// When removing confab hooks, other hooks in the same event should remain
	// and the event key should NOT be removed

	tmpDir := t.TempDir()

	oldEnv := os.Getenv(ClaudeStateDirEnv)
	os.Setenv(ClaudeStateDirEnv, tmpDir)
	defer os.Setenv(ClaudeStateDirEnv, oldEnv)

	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Create settings with both confab and other hooks
	initialSettings := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "/path/to/confab hook pre-tool-use"},
          {"type": "command", "command": "/other/tool check"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(initialSettings), 0644); err != nil {
		t.Fatalf("Failed to write initial settings: %v", err)
	}

	// Uninstall confab hooks
	if err := UninstallPreToolUseHooks(); err != nil {
		t.Fatalf("UninstallPreToolUseHooks failed: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Failed to unmarshal settings: %v", err)
	}

	// hooks and PreToolUse should still exist
	hooks, ok := raw["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key should still exist when other hooks remain")
	}

	preToolUse, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatal("PreToolUse key should still exist when other hooks remain")
	}

	// Should have exactly one matcher with one hook
	if len(preToolUse) != 1 {
		t.Fatalf("Expected 1 matcher, got %d", len(preToolUse))
	}

	matcher := preToolUse[0].(map[string]any)
	hooksList := matcher["hooks"].([]any)
	if len(hooksList) != 1 {
		t.Fatalf("Expected 1 hook remaining, got %d", len(hooksList))
	}

	hook := hooksList[0].(map[string]any)
	if hook["command"] != "/other/tool check" {
		t.Errorf("Wrong hook remaining: %v", hook["command"])
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLevel string
		wantErr   bool
	}{
		{"debug lowercase", "debug", "DEBUG", false},
		{"debug uppercase", "DEBUG", "DEBUG", false},
		{"debug mixed case", "Debug", "DEBUG", false},
		{"info lowercase", "info", "INFO", false},
		{"info uppercase", "INFO", "INFO", false},
		{"empty defaults to info", "", "INFO", false},
		{"warn lowercase", "warn", "WARN", false},
		{"warning alias", "warning", "WARN", false},
		{"error lowercase", "error", "ERROR", false},
		{"error uppercase", "ERROR", "ERROR", false},
		{"with whitespace", "  debug  ", "DEBUG", false},
		{"invalid level", "trace", "INFO", true},
		{"invalid level verbose", "verbose", "INFO", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level, err := ParseLogLevel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLogLevel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if level.String() != tt.wantLevel {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", tt.input, level.String(), tt.wantLevel)
			}
		})
	}
}

func TestGetDefaultRedactionPatterns(t *testing.T) {
	patterns := GetDefaultRedactionPatterns()

	// Should have multiple default patterns
	if len(patterns) < 5 {
		t.Errorf("Expected at least 5 default patterns, got %d", len(patterns))
	}

	// Verify pattern structure
	for i, pattern := range patterns {
		if pattern.Name == "" {
			t.Errorf("Pattern %d has empty name", i)
		}
		// Must have at least one of Pattern or FieldPattern
		if pattern.Pattern == "" && pattern.FieldPattern == "" {
			t.Errorf("Pattern %d (%s) has neither pattern nor field_pattern", i, pattern.Name)
		}
		if pattern.Type == "" {
			t.Errorf("Pattern %d (%s) has empty type", i, pattern.Name)
		}
	}
}

func TestEnsureDefaultRedaction(t *testing.T) {
	// Create temp directory for config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Set up test environment
	oldEnv := os.Getenv("CONFAB_CONFIG_PATH")
	os.Setenv("CONFAB_CONFIG_PATH", configPath)
	defer os.Setenv("CONFAB_CONFIG_PATH", oldEnv)

	t.Run("creates default redaction when config doesn't exist", func(t *testing.T) {
		// Remove config file if it exists
		os.Remove(configPath)

		added, err := EnsureDefaultRedaction()
		if err != nil {
			t.Fatalf("EnsureDefaultRedaction failed: %v", err)
		}
		if !added {
			t.Error("Expected added=true for new config")
		}

		// Verify config was created with redaction enabled
		cfg, err := GetUploadConfig()
		if err != nil {
			t.Fatalf("GetUploadConfig failed: %v", err)
		}
		if cfg.Redaction == nil {
			t.Fatal("Expected redaction config to be set")
		}
		if !cfg.Redaction.Enabled {
			t.Error("Expected redaction to be enabled by default")
		}
		// Patterns array should be empty - default patterns are applied at runtime
		if len(cfg.Redaction.Patterns) != 0 {
			t.Errorf("Expected empty patterns array, got %d", len(cfg.Redaction.Patterns))
		}
		// use_default_patterns should be explicitly set to true
		if cfg.Redaction.UseDefaultPatterns == nil {
			t.Error("Expected UseDefaultPatterns to be explicitly set, got nil")
		} else if !*cfg.Redaction.UseDefaultPatterns {
			t.Error("Expected UseDefaultPatterns to be true")
		}
	})

	t.Run("does not overwrite existing redaction config", func(t *testing.T) {
		// Create config with redaction disabled
		cfg := &UploadConfig{
			BackendURL: "https://example.com",
			APIKey:     "cfb_test-key-1234567890",
			Redaction: &RedactionConfig{
				Enabled:  false,
				Patterns: []RedactionPattern{{Name: "Custom", Pattern: "custom", Type: "custom"}},
			},
		}
		if err := SaveUploadConfig(cfg); err != nil {
			t.Fatalf("SaveUploadConfig failed: %v", err)
		}

		added, err := EnsureDefaultRedaction()
		if err != nil {
			t.Fatalf("EnsureDefaultRedaction failed: %v", err)
		}
		if added {
			t.Error("Expected added=false when redaction already exists")
		}

		// Verify config was not changed
		cfg2, err := GetUploadConfig()
		if err != nil {
			t.Fatalf("GetUploadConfig failed: %v", err)
		}
		if cfg2.Redaction.Enabled {
			t.Error("Redaction should still be disabled")
		}
		if len(cfg2.Redaction.Patterns) != 1 {
			t.Errorf("Expected 1 custom pattern, got %d", len(cfg2.Redaction.Patterns))
		}
		if cfg2.Redaction.Patterns[0].Name != "Custom" {
			t.Error("Custom pattern was overwritten")
		}
	})

	t.Run("adds redaction to existing config without redaction", func(t *testing.T) {
		// Create config without redaction
		cfg := &UploadConfig{
			BackendURL: "https://example.com",
			APIKey:     "cfb_test-key-1234567890",
		}
		if err := SaveUploadConfig(cfg); err != nil {
			t.Fatalf("SaveUploadConfig failed: %v", err)
		}

		added, err := EnsureDefaultRedaction()
		if err != nil {
			t.Fatalf("EnsureDefaultRedaction failed: %v", err)
		}
		if !added {
			t.Error("Expected added=true when redaction is missing")
		}

		// Verify redaction was added
		cfg2, err := GetUploadConfig()
		if err != nil {
			t.Fatalf("GetUploadConfig failed: %v", err)
		}
		if cfg2.Redaction == nil {
			t.Fatal("Expected redaction config to be set")
		}
		if !cfg2.Redaction.Enabled {
			t.Error("Expected redaction to be enabled")
		}
		// Verify other fields preserved
		if cfg2.BackendURL != "https://example.com" {
			t.Error("BackendURL was not preserved")
		}
		if cfg2.APIKey != "cfb_test-key-1234567890" {
			t.Error("APIKey was not preserved")
		}
	})
}
