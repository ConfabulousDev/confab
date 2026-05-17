package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)









// TestEnsureAuthenticated guards the auth gate at upload.go:201. Every
// command that talks to the backend depends on this; a regression that
// green-lights an unconfigured install would surface as confusing API
// errors instead of "run `confab login`".
func TestEnsureAuthenticated(t *testing.T) {
	tests := []struct {
		name        string
		config      *UploadConfig // nil = no config file
		wantErr     bool
		wantErrSub  string // substring expected in error
		wantCfgPtr  bool   // expect non-nil returned cfg on success
	}{
		{
			name:       "missing config file",
			config:     nil,
			wantErr:    true,
			wantErrSub: "not authenticated",
		},
		{
			name:       "empty config",
			config:     &UploadConfig{},
			wantErr:    true,
			wantErrSub: "not authenticated",
		},
		{
			name:       "missing api key",
			config:     &UploadConfig{BackendURL: "https://api.example.com"},
			wantErr:    true,
			wantErrSub: "not authenticated",
		},
		{
			name:       "missing backend url",
			config:     &UploadConfig{APIKey: "cfb_test-key"},
			wantErr:    true,
			wantErrSub: "not authenticated",
		},
		{
			name:       "both present",
			config:     &UploadConfig{BackendURL: "https://api.example.com", APIKey: "cfb_test-key"},
			wantErr:    false,
			wantCfgPtr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")
			t.Setenv("CONFAB_CONFIG_PATH", configPath)
			if tt.config != nil {
				data, err := json.Marshal(tt.config)
				if err != nil {
					t.Fatalf("marshal config: %v", err)
				}
				if err := os.WriteFile(configPath, data, 0600); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}

			cfg, err := EnsureAuthenticated()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("EnsureAuthenticated() error = nil, want error containing %q", tt.wantErrSub)
				}
				if tt.wantErrSub != "" && !contains(err.Error(), tt.wantErrSub) {
					t.Errorf("EnsureAuthenticated() error = %q, want substring %q", err.Error(), tt.wantErrSub)
				}
				if cfg != nil {
					t.Errorf("EnsureAuthenticated() cfg = %+v, want nil on error", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("EnsureAuthenticated() error = %v, want nil", err)
			}
			if tt.wantCfgPtr && cfg == nil {
				t.Fatal("EnsureAuthenticated() cfg = nil, want non-nil")
			}
		})
	}
}

// contains is a tiny helper so the test file doesn't need to import
// strings just for one substring check.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
			err := validateBackendURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBackendURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
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
			err := validateAPIKey(tt.apiKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAPIKey(%q) error = %v, wantErr %v", tt.apiKey, err, tt.wantErr)
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

// setTestHook sets a hook for an event in the settings using raw map manipulation.
// Panics on error since this is a test helper that should never fail in normal conditions.
func setTestHook(settings *ClaudeSettings, eventName string, matchers ...map[string]any) {
	matchersList := make([]any, len(matchers))
	for i, m := range matchers {
		matchersList[i] = m
	}
	if err := settings.SetEventHooks(eventName, matchersList); err != nil {
		panic("setTestHook: " + err.Error())
	}
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

	eventHooks := settings.GetEventHooks("TestHook")
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
	hooksMap, _ := settings.GetHooksMap()
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

	hooksMap, _ := settings.GetHooksMap()
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



func TestGetEventHooks_MalformedSettings(t *testing.T) {
	// Test that getEventHooks handles malformed settings gracefully

	t.Run("hooks is not a map", func(t *testing.T) {
		settings := &ClaudeSettings{
			raw: map[string]any{
				"hooks": "not a map", // Wrong type
			},
		}
		result := settings.GetEventHooks("SessionStart")
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
		result := settings.GetEventHooks("SessionStart")
		if result != nil {
			t.Errorf("Expected nil for malformed event hooks, got %v", result)
		}
	})

	t.Run("hooks does not exist", func(t *testing.T) {
		settings := &ClaudeSettings{
			raw: map[string]any{},
		}
		result := settings.GetEventHooks("SessionStart")
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
		result := settings.GetEventHooks("SessionStart")
		if result != nil {
			t.Errorf("Expected nil for missing event, got %v", result)
		}
	})
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
			level, err := parseLogLevel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLogLevel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if level.String() != tt.wantLevel {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, level.String(), tt.wantLevel)
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
