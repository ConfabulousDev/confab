package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/pathcanon"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

// TestConfigDirForHook covers the no-bindings short-circuit, Claude derivation,
// and the non-Claude guard (kata hpec).
func TestConfigDirForHook(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)

	claudeDir := t.TempDir()
	transcript := filepath.Join(claudeDir, "projects", "enc-cwd", "11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// No bindings -> short-circuit returns "" even for a valid layout.
	if got := configDirForHook(provider.NameClaudeCode, transcript); got != "" {
		t.Errorf("short-circuit (no bindings): got %q, want \"\"", got)
	}

	// With a binding present, derivation runs and returns the canonical dir.
	seed := config.UploadConfig{
		BackendURL: "https://b0", APIKey: "cfb_x_11111111111",
		Bindings: map[string]map[string]config.BindingCreds{
			provider.NameClaudeCode: {"/whatever": {BackendURL: "https://b1", APIKey: "cfb_y_22222222222"}},
		},
	}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if got, want := configDirForHook(provider.NameClaudeCode, transcript), pathcanon.CanonicalDir(claudeDir); got != want {
		t.Errorf("derive: got %q, want %q", got, want)
	}

	// Non-Claude providers always get "" (not wired for --config-dir yet).
	if got := configDirForHook(provider.NameCodex, transcript); got != "" {
		t.Errorf("non-claude: got %q, want \"\"", got)
	}
}
