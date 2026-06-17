// ABOUTME: Tests for clientForFlags — the shared --provider/--config-dir resolver
// ABOUTME: used by the retrieval commands to target per-(provider, config-dir) backends.
package cmd

import (
	"errors"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/pathcanon"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

// TestClientForFlags_NoFlagsUsesDefault confirms the no-flag path resolves the
// default (top-level) binding unchanged.
func TestClientForFlags_NoFlagsUsesDefault(t *testing.T) {
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	client, err := clientForFlags("", "")
	if err != nil {
		t.Fatalf("clientForFlags() error = %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// TestClientForFlags_ExplicitBinding resolves the backend stored under the
// non-default binding for the given (provider, config-dir).
func TestClientForFlags_ExplicitBinding(t *testing.T) {
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

	client, err := clientForFlags(provider.NameClaudeCode, customDir)
	if err != nil {
		t.Fatalf("clientForFlags() error = %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// TestClientForFlags_ConfigDirRequiresProvider mirrors setup.go validation:
// --config-dir without --provider is an error.
func TestClientForFlags_ConfigDirRequiresProvider(t *testing.T) {
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	_, err := clientForFlags("", "/some/dir")
	if err == nil {
		t.Fatal("expected error: --config-dir requires --provider")
	}
}

// TestClientForFlags_NoBindingSurfaced surfaces ErrNoBinding when the requested
// binding has no stored credentials.
func TestClientForFlags_NoBindingSurfaced(t *testing.T) {
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	_, err := clientForFlags(provider.NameClaudeCode, "/no/such/dir")
	if err == nil {
		t.Fatal("expected error for missing binding")
	}
	if !errors.Is(err, config.ErrNoBinding) {
		t.Errorf("expected ErrNoBinding, got %v", err)
	}
}

// TestClientForFlags_UnknownProvider rejects an unrecognized provider name.
func TestClientForFlags_UnknownProvider(t *testing.T) {
	seedConfig(t, config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     "cfb_default_11111111111",
	})

	_, err := clientForFlags("not-a-provider", "/some/dir")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
