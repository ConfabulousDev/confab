// ABOUTME: Tests for save's per-(provider, config-dir) binding selection (z0rt):
// ABOUTME: --config-dir routes uploads to the bound backend, requires --provider,
// ABOUTME: and is claude-code-only.
package cmd

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/pathcanon"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

// seedConfigAt writes an UploadConfig to a fixed config path (so a single test
// can both seed bindings and have the runtime read them back).
func seedConfigAt(t *testing.T, path string, cfg config.UploadConfig) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestSave_ConfigDir_RoutesToBoundBackend: `save --provider claude-code
// --config-dir C1` uploads to the backend stored under that binding, not the
// default backend.
func TestSave_ConfigDir_RoutesToBoundBackend(t *testing.T) {
	defaultBackend := &saveTestBackend{}
	defaultSrv := httptest.NewServer(defaultBackend)
	defer defaultSrv.Close()

	customBackend := &saveTestBackend{}
	customSrv := httptest.NewServer(customBackend)
	defer customSrv.Close()

	// Custom claude config dir hosting the session transcript.
	customDir := t.TempDir()
	canonDir := pathcanon.CanonicalDir(customDir)
	project := filepath.Join(customDir, "projects", "project1")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	sessionID := "aaaaaaaa-1111-1111-1111-111111111111"
	if err := os.WriteFile(filepath.Join(project, sessionID+".jsonl"), []byte(`{"type":"test"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)
	seedConfigAt(t, cfgPath, config.UploadConfig{
		BackendURL: defaultSrv.URL,
		APIKey:     "cfb_default_11111111111",
		Bindings: map[string]map[string]config.BindingCreds{
			provider.NameClaudeCode: {
				canonDir: {BackendURL: customSrv.URL, APIKey: "cfb_custom_22222222222"},
			},
		},
	})

	cfg, p, err := resolveSaveContext(provider.NameClaudeCode, customDir)
	if err != nil {
		t.Fatalf("resolveSaveContext: %v", err)
	}
	if err := saveSessionsForProvider(cfg, p, []string{sessionID}); err != nil {
		t.Fatalf("saveSessionsForProvider: %v", err)
	}

	if atomic.LoadInt32(&customBackend.initCount) != 1 {
		t.Errorf("custom backend init count = %d, want 1", customBackend.initCount)
	}
	if atomic.LoadInt32(&defaultBackend.initCount) != 0 {
		t.Errorf("default backend init count = %d, want 0 (must not route to default)", defaultBackend.initCount)
	}
}

// TestSave_ConfigDir_RequiresProvider: --config-dir without --provider errors.
func TestSave_ConfigDir_RequiresProvider(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)
	seedConfigAt(t, cfgPath, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	_, _, err := resolveSaveContext("", "/some/dir")
	if err == nil {
		t.Fatal("expected error: --config-dir requires --provider")
	}
}

// TestSave_ConfigDir_NonClaudeProviderRejected: --config-dir with a non-claude
// provider errors (local discovery via GetWithDir is claude-only).
func TestSave_ConfigDir_NonClaudeProviderRejected(t *testing.T) {
	customDir := t.TempDir()
	canonDir := pathcanon.CanonicalDir(customDir)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)
	seedConfigAt(t, cfgPath, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
		Bindings: map[string]map[string]config.BindingCreds{
			provider.NameCodex: {
				canonDir: {BackendURL: "https://custom.example", APIKey: "cfb_custom_22222222222"},
			},
		},
	})

	_, _, err := resolveSaveContext(provider.NameCodex, customDir)
	if err == nil {
		t.Fatal("expected error: --config-dir not supported for codex")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error should name the unsupported provider, got %v", err)
	}
}

// TestSave_NoConfigDir_UsesDefaultBinding: the no-flag path resolves the default
// (top-level) binding unchanged.
func TestSave_NoConfigDir_UsesDefaultBinding(t *testing.T) {
	backend := &saveTestBackend{}
	srv := httptest.NewServer(backend)
	defer srv.Close()

	_, sessionID, _ := setupSaveTestEnv(t, srv.URL)

	cfg, p, err := resolveSaveContext(provider.NameClaudeCode, "")
	if err != nil {
		t.Fatalf("resolveSaveContext: %v", err)
	}
	if err := saveSessionsForProvider(cfg, p, []string{sessionID}); err != nil {
		t.Fatalf("saveSessionsForProvider: %v", err)
	}
	if atomic.LoadInt32(&backend.initCount) != 1 {
		t.Errorf("default backend init count = %d, want 1", backend.initCount)
	}
}

// TestSave_ConfigDir_NoBindingSurfaced: a config dir with no stored binding
// surfaces ErrNoBinding (no silent fallback to the default backend).
func TestSave_ConfigDir_NoBindingSurfaced(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)
	seedConfigAt(t, cfgPath, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	_, _, err := resolveSaveContext(provider.NameClaudeCode, "/no/such/dir")
	if err == nil {
		t.Fatal("expected ErrNoBinding for unbound config dir")
	}
	if !errors.Is(err, config.ErrNoBinding) {
		t.Errorf("expected ErrNoBinding, got %v", err)
	}
}
