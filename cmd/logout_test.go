package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/spf13/cobra"
)

// setupLogoutTestEnv creates temp directories and sets env vars for logout tests.
func setupLogoutTestEnv(t *testing.T) (tmpDir string, configPath string) {
	t.Helper()
	tmpDir = t.TempDir()

	// Create confab config directory
	confabDir := filepath.Join(tmpDir, ".confab")
	if err := os.MkdirAll(confabDir, 0755); err != nil {
		t.Fatalf("failed to create confab dir: %v", err)
	}

	configPath = filepath.Join(confabDir, "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	return tmpDir, configPath
}

// TestLogout_PreservesRedactionConfig verifies that logout preserves
// existing redaction settings in config.json
func TestLogout_PreservesRedactionConfig(t *testing.T) {
	_, configPath := setupLogoutTestEnv(t)

	// Pre-create config with redaction settings
	useDefaults := true
	existingCfg := config.UploadConfig{
		BackendURL: "https://example.com",
		APIKey:     "test-api-key-12345678",
		LogLevel:   "debug",
		Redaction: &config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "Custom Pattern", Pattern: `CUSTOM_[A-Z]+`, Type: "custom"},
				{Name: "Another Pattern", Pattern: `SECRET_[0-9]+`, Type: "secret"},
			},
		},
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Run logout
	cmd := &cobra.Command{}
	err := runLogout(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogout failed: %v", err)
	}

	// Read back config and verify
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var savedCfg config.UploadConfig
	if err := json.Unmarshal(data, &savedCfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify API key was cleared
	if savedCfg.APIKey != "" {
		t.Errorf("expected API key to be cleared, got %s", savedCfg.APIKey)
	}

	// Verify backend URL was preserved
	if savedCfg.BackendURL != "https://example.com" {
		t.Errorf("expected backend URL to be preserved, got %s", savedCfg.BackendURL)
	}

	// Verify redaction config was preserved
	if savedCfg.Redaction == nil {
		t.Fatal("redaction config was lost")
	}
	if !savedCfg.Redaction.Enabled {
		t.Error("redaction.enabled was changed")
	}
	if savedCfg.Redaction.UseDefaultPatterns == nil || !*savedCfg.Redaction.UseDefaultPatterns {
		t.Error("redaction.use_default_patterns was changed")
	}
	if len(savedCfg.Redaction.Patterns) != 2 {
		t.Errorf("expected 2 custom patterns, got %d", len(savedCfg.Redaction.Patterns))
	}
	if savedCfg.Redaction.Patterns[0].Name != "Custom Pattern" {
		t.Errorf("first pattern name was changed to %s", savedCfg.Redaction.Patterns[0].Name)
	}
	if savedCfg.Redaction.Patterns[1].Name != "Another Pattern" {
		t.Errorf("second pattern name was changed to %s", savedCfg.Redaction.Patterns[1].Name)
	}

	// Verify log_level was preserved
	if savedCfg.LogLevel != "debug" {
		t.Errorf("log_level was changed from 'debug' to '%s'", savedCfg.LogLevel)
	}
}

// TestLogout_PreservesDisabledRedaction verifies that logout preserves
// redaction config even when it's disabled
func TestLogout_PreservesDisabledRedaction(t *testing.T) {
	_, configPath := setupLogoutTestEnv(t)

	// Pre-create config with disabled redaction
	useDefaults := false
	existingCfg := config.UploadConfig{
		BackendURL: "https://example.com",
		APIKey:     "test-api-key-12345678",
		LogLevel:   "error",
		Redaction: &config.RedactionConfig{
			Enabled:            false, // Explicitly disabled
			UseDefaultPatterns: &useDefaults,
			Patterns:           []config.RedactionPattern{},
		},
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Run logout
	cmd := &cobra.Command{}
	err := runLogout(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogout failed: %v", err)
	}

	// Read back config and verify
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var savedCfg config.UploadConfig
	if err := json.Unmarshal(data, &savedCfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify API key was cleared
	if savedCfg.APIKey != "" {
		t.Errorf("expected API key to be cleared, got %s", savedCfg.APIKey)
	}

	// Verify redaction config was preserved with disabled state
	if savedCfg.Redaction == nil {
		t.Fatal("redaction config was lost")
	}
	if savedCfg.Redaction.Enabled {
		t.Error("redaction.enabled was changed (should remain false)")
	}
	if savedCfg.Redaction.UseDefaultPatterns == nil || *savedCfg.Redaction.UseDefaultPatterns {
		t.Error("use_default_patterns was changed (should remain false)")
	}

	// Verify log_level was preserved
	if savedCfg.LogLevel != "error" {
		t.Errorf("log_level was changed from 'error' to '%s'", savedCfg.LogLevel)
	}
}

// TestLogout_NoRedactionConfig verifies that logout works when there's no
// redaction config (older config format)
func TestLogout_NoRedactionConfig(t *testing.T) {
	_, configPath := setupLogoutTestEnv(t)

	// Pre-create config without redaction (simulates older config)
	existingCfg := config.UploadConfig{
		BackendURL: "https://example.com",
		APIKey:     "test-api-key-12345678",
		LogLevel:   "info",
		// No Redaction field
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Run logout
	cmd := &cobra.Command{}
	err := runLogout(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogout failed: %v", err)
	}

	// Read back config and verify
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var savedCfg config.UploadConfig
	if err := json.Unmarshal(data, &savedCfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify API key was cleared
	if savedCfg.APIKey != "" {
		t.Errorf("expected API key to be cleared, got %s", savedCfg.APIKey)
	}

	// Verify backend URL was preserved
	if savedCfg.BackendURL != "https://example.com" {
		t.Errorf("expected backend URL to be preserved, got %s", savedCfg.BackendURL)
	}

	// Redaction should still be nil (not added by logout)
	if savedCfg.Redaction != nil {
		t.Error("logout should not add redaction config")
	}

	// Verify log_level was preserved
	if savedCfg.LogLevel != "info" {
		t.Errorf("log_level was changed from 'info' to '%s'", savedCfg.LogLevel)
	}
}

// TestLogout_AlreadyLoggedOut verifies logout handles already logged out state
func TestLogout_AlreadyLoggedOut(t *testing.T) {
	_, configPath := setupLogoutTestEnv(t)

	// Pre-create config with no API key but with redaction
	useDefaults := true
	existingCfg := config.UploadConfig{
		BackendURL: "https://example.com",
		APIKey:     "", // Already logged out
		LogLevel:   "warn",
		Redaction: &config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "Keep This", Pattern: `KEEP_[A-Z]+`, Type: "custom"},
			},
		},
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Run logout (should succeed even if already logged out)
	cmd := &cobra.Command{}
	err := runLogout(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogout failed: %v", err)
	}

	// Read back config and verify nothing was corrupted
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var savedCfg config.UploadConfig
	if err := json.Unmarshal(data, &savedCfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Config should be unchanged (already logged out message shown but no changes)
	if savedCfg.Redaction == nil {
		t.Fatal("redaction config was lost")
	}
	if len(savedCfg.Redaction.Patterns) != 1 {
		t.Errorf("expected 1 custom pattern, got %d", len(savedCfg.Redaction.Patterns))
	}
	if savedCfg.LogLevel != "warn" {
		t.Errorf("log_level was changed from 'warn' to '%s'", savedCfg.LogLevel)
	}
}
