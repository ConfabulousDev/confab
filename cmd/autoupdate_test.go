package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
)

func TestIsAutoUpdateEnabled(t *testing.T) {
	tests := []struct {
		name       string
		autoUpdate *bool
		want       bool
	}{
		{"nil defaults to true", nil, true},
		{"explicit true", boolPtr(true), true},
		{"explicit false", boolPtr(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.UploadConfig{AutoUpdate: tt.autoUpdate}
			if got := cfg.IsAutoUpdateEnabled(); got != tt.want {
				t.Errorf("IsAutoUpdateEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// writeTestConfig marshals cfg to JSON and writes it to configPath.
func writeTestConfig(t *testing.T, configPath string, cfg *config.UploadConfig) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// setupUpdateTest sets version to a non-dev value and creates a temp config path.
// Returns the config path. Restores the original version on cleanup.
func setupUpdateTest(t *testing.T) string {
	t.Helper()
	origVersion := version
	t.Cleanup(func() { version = origVersion })
	version = "1.0.0"

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)
	return configPath
}

func TestShouldCheckForUpdateRespectsConfig(t *testing.T) {
	configPath := setupUpdateTest(t)

	// With auto_update disabled, shouldCheckForUpdate returns false
	writeTestConfig(t, configPath, &config.UploadConfig{AutoUpdate: boolPtr(false)})

	if shouldCheckForUpdate() {
		t.Error("shouldCheckForUpdate() = true, want false when auto_update is disabled")
	}

	// With auto_update enabled, shouldCheckForUpdate returns true
	// (no last-check file exists, so time check passes)
	writeTestConfig(t, configPath, &config.UploadConfig{AutoUpdate: boolPtr(true)})

	if !shouldCheckForUpdate() {
		t.Error("shouldCheckForUpdate() = false, want true when auto_update is enabled")
	}
}

func TestShouldCheckForUpdateDefaultEnabled(t *testing.T) {
	configPath := setupUpdateTest(t)

	// With no auto_update field (nil), defaults to enabled
	writeTestConfig(t, configPath, &config.UploadConfig{})

	if !shouldCheckForUpdate() {
		t.Error("shouldCheckForUpdate() = false, want true when auto_update is nil (default)")
	}
}

func boolPtr(b bool) *bool {
	return &b
}
