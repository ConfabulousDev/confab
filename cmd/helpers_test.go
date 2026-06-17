// ABOUTME: Tests for newAuthedClientForBinding — the shared binding-aware client
// ABOUTME: resolver used by the retrieval commands (session get-summary/download/list-files + retro).
package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/pathcanon"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

// seedConfig writes an UploadConfig to a temp CONFAB_CONFIG_PATH for the test.
func seedConfig(t *testing.T, cfg config.UploadConfig) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", cfgPath)
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestNewAuthedClientForBinding_DefaultBinding resolves the top-level creds when
// the binding is the default one.
func TestNewAuthedClientForBinding_DefaultBinding(t *testing.T) {
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	b := config.Binding{Provider: provider.NameClaudeCode, IsDefault: true}
	client, err := newAuthedClientForBinding(b)
	if err != nil {
		t.Fatalf("newAuthedClientForBinding() error = %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// TestNewAuthedClientForBinding_NonDefaultBinding resolves the creds stored under
// bindings[provider][canonicalDir] rather than the top-level default.
func TestNewAuthedClientForBinding_NonDefaultBinding(t *testing.T) {
	customDir := pathcanon.CanonicalDir(t.TempDir())
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
		Bindings: map[string]map[string]config.BindingCreds{
			provider.NameClaudeCode: {
				customDir: {BackendURL: "https://custom.example", APIKey: "cfb_custom_22222222222"},
			},
		},
	})

	b := config.Binding{Provider: provider.NameClaudeCode, Dir: customDir}
	client, err := newAuthedClientForBinding(b)
	if err != nil {
		t.Fatalf("newAuthedClientForBinding() error = %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// TestNewAuthedClientForBinding_ErrNoBinding surfaces ErrNoBinding when a
// non-default binding has no stored credentials (no silent fallback to default).
func TestNewAuthedClientForBinding_ErrNoBinding(t *testing.T) {
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	b := config.Binding{Provider: provider.NameClaudeCode, Dir: "/no/such/binding"}
	_, err := newAuthedClientForBinding(b)
	if err == nil {
		t.Fatal("expected error for missing binding")
	}
	if !errors.Is(err, config.ErrNoBinding) {
		t.Errorf("expected ErrNoBinding, got %v", err)
	}
}

// TestNewAuthedClient_DelegatesToDefaultBinding confirms the no-flag path still
// reads the top-level default creds (byte-identical behavior).
func TestNewAuthedClient_DelegatesToDefaultBinding(t *testing.T) {
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	client, err := newAuthedClient()
	if err != nil {
		t.Fatalf("newAuthedClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}
