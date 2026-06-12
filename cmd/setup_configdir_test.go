package cmd

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/pathcanon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

// TestRunSetup_ConfigDir_WritesBindingNotTopLevel: `setup --provider
// claude-code --config-dir <custom>` writes the credentials under
// bindings[claude-code][canonical(custom)], installs hooks into the custom
// dir, and leaves the default top-level binding untouched (kata hpec).
func TestRunSetup_ConfigDir_WritesBindingNotTopLevel(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)

	tmpDir, configPath := setupSetupTestEnv(t, server.URL)

	// Seed a default (top-level) binding for a DIFFERENT backend; the
	// custom-dir setup must not clobber it.
	seed := config.UploadConfig{BackendURL: "https://default.example", APIKey: "cfb_default_key_0000000000"}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	customDir := filepath.Join(tmpDir, "work-claude")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("mkdir custom: %v", err)
	}

	setupProviderName = provider.NameClaudeCode
	setupConfigDir = customDir
	t.Cleanup(func() { setupProviderName = ""; setupConfigDir = "" })

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_work_key_11111111111", "")

	if err := runSetup(cmd, []string{}); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	cfg, err := config.GetUploadConfig()
	if err != nil {
		t.Fatalf("GetUploadConfig: %v", err)
	}
	if cfg.BackendURL != "https://default.example" {
		t.Errorf("top-level backend = %q, want default untouched", cfg.BackendURL)
	}
	key := pathcanon.CanonicalDir(customDir)
	got, ok := cfg.Bindings[provider.NameClaudeCode][key]
	if !ok {
		t.Fatalf("no binding under claude-code/%q; bindings=%v", key, cfg.Bindings)
	}
	if got.BackendURL != server.URL || got.APIKey != "cfb_work_key_11111111111" {
		t.Errorf("binding = %+v, want server.URL + work key", got)
	}
	if _, err := os.Stat(filepath.Join(customDir, "settings.json")); err != nil {
		t.Errorf("settings.json not installed into custom dir: %v", err)
	}
}

// TestRunSetup_ConfigDir_SymlinkedDirKeyMatchesRuntime: when --config-dir has a
// symlinked ancestor and does not exist yet, setup must canonicalize the
// binding key the same way runtime derivation will (kata hpec). Setup creates
// the dir before canonicalizing, so the stored key equals
// pathcanon.CanonicalDir of the dir resolved through the symlink.
func TestRunSetup_ConfigDir_SymlinkedDirKeyMatchesRuntime(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)

	tmpDir, _ := setupSetupTestEnv(t, server.URL)

	// realParent/<dir> is reached via a symlink (linkParent -> realParent).
	realParent := filepath.Join(tmpDir, "real")
	if err := os.MkdirAll(realParent, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	linkParent := filepath.Join(tmpDir, "link")
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// The config dir itself does NOT exist yet (setup must create it).
	configDir := filepath.Join(linkParent, "work-claude")

	setupProviderName = provider.NameClaudeCode
	setupConfigDir = configDir
	t.Cleanup(func() { setupProviderName = ""; setupConfigDir = "" })

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_sym_key_33333333333", "")
	if err := runSetup(cmd, []string{}); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	// The stored key must equal the canonical (symlink-resolved) dir — the same
	// value a runtime hook derives from a transcript under this dir.
	wantKey := pathcanon.CanonicalDir(configDir)
	cfg, _ := config.GetUploadConfig()
	if _, ok := cfg.Bindings[provider.NameClaudeCode][wantKey]; !ok {
		t.Fatalf("binding not stored under canonical key %q; bindings=%v", wantKey, cfg.Bindings)
	}
}

// TestRunSetup_ConfigDir_RequiresProvider: --config-dir without --provider is
// rejected before any auth.
func TestRunSetup_ConfigDir_RequiresProvider(t *testing.T) {
	setupConfigDir = "/some/dir"
	setupProviderName = ""
	t.Cleanup(func() { setupConfigDir = ""; setupProviderName = "" })

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", "https://x.example", "")
	cmd.Flags().String("api-key", "", "")

	err := runSetup(cmd, []string{})
	if err == nil || !strings.Contains(err.Error(), "config-dir requires --provider") {
		t.Fatalf("expected --config-dir requires --provider error, got %v", err)
	}
}

// TestRunSetup_ConfigDir_DefaultCollapse: passing the default dir explicitly
// writes the top-level config and creates NO bindings key (== bare setup).
func TestRunSetup_ConfigDir_DefaultCollapse(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)

	_, configPath := setupSetupTestEnv(t, server.URL)
	defaultDir := os.Getenv("CONFAB_CLAUDE_DIR")

	setupProviderName = provider.NameClaudeCode
	setupConfigDir = defaultDir
	t.Cleanup(func() { setupProviderName = ""; setupConfigDir = "" })

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_def_key_22222222222", "")

	if err := runSetup(cmd, []string{}); err != nil {
		t.Fatalf("runSetup: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), "bindings") {
		t.Errorf("explicit default config-dir wrote a bindings key:\n%s", raw)
	}
	cfg, _ := config.GetUploadConfig()
	if cfg.BackendURL != server.URL {
		t.Errorf("top-level backend = %q, want server", cfg.BackendURL)
	}
}
