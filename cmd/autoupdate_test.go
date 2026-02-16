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

func TestShouldCheckForUpdateRespectsConfig(t *testing.T) {
	// Save original version and restore after test
	origVersion := version
	defer func() { version = origVersion }()
	version = "1.0.0"

	// Use a temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	// With auto_update disabled, shouldCheckForUpdate returns false
	disabled := false
	cfg := &config.UploadConfig{AutoUpdate: &disabled}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if shouldCheckForUpdate() {
		t.Error("shouldCheckForUpdate() = true, want false when auto_update is disabled")
	}

	// With auto_update enabled, shouldCheckForUpdate returns true
	// (no last-check file exists, so time check passes)
	enabled := true
	cfg.AutoUpdate = &enabled
	data, err = json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if !shouldCheckForUpdate() {
		t.Error("shouldCheckForUpdate() = false, want true when auto_update is enabled")
	}
}

func TestShouldCheckForUpdateDefaultEnabled(t *testing.T) {
	origVersion := version
	defer func() { version = origVersion }()
	version = "1.0.0"

	// Use a temp config file with no auto_update field (nil)
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	cfg := &config.UploadConfig{}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if !shouldCheckForUpdate() {
		t.Error("shouldCheckForUpdate() = false, want true when auto_update is nil (default)")
	}
}

func boolPtr(b bool) *bool {
	return &b
}
