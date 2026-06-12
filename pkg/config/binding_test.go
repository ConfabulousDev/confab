package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempConfig points CONFAB_CONFIG_PATH at a fresh temp file and returns
// its path. Optionally seeds it with the given config.
func withTempConfig(t *testing.T, seed *UploadConfig) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", path)
	if seed != nil {
		data, err := json.MarshalIndent(seed, "", "  ")
		if err != nil {
			t.Fatalf("marshal seed: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write seed: %v", err)
		}
	}
	return path
}

func readRawConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return m
}

// TestResolveBindingDefaultCollapse: passing the default dir (incl. a
// trailing-slash spelling) resolves to the default binding; a different dir
// does not.
func TestResolveBindingDefaultCollapse(t *testing.T) {
	def := t.TempDir()

	if b := ResolveBinding("claude-code", "", def); !b.IsDefault {
		t.Errorf("empty dir: IsDefault=false, want true")
	}
	if b := ResolveBinding("claude-code", def, def); !b.IsDefault {
		t.Errorf("explicit default dir: IsDefault=false, want true")
	}
	if b := ResolveBinding("claude-code", def+"/", def); !b.IsDefault {
		t.Errorf("trailing-slash default dir: IsDefault=false, want true")
	}

	custom := t.TempDir()
	b := ResolveBinding("claude-code", custom, def)
	if b.IsDefault {
		t.Errorf("custom dir: IsDefault=true, want false")
	}
	if b.Dir == "" {
		t.Errorf("custom dir: Dir is empty, want canonical dir")
	}
}

// TestSetBindingCredentialsDefaultWritesTopLevel: the default binding writes
// the top-level fields and creates NO bindings key (byte-equivalent to bare
// setup).
func TestSetBindingCredentialsDefaultWritesTopLevel(t *testing.T) {
	path := withTempConfig(t, nil)
	def := t.TempDir()

	b := ResolveBinding("claude-code", def, def) // default
	if err := SetBindingCredentials(b, "https://b0.example", "cfb_default_key_000000"); err != nil {
		t.Fatalf("SetBindingCredentials: %v", err)
	}

	cfg, err := GetUploadConfig()
	if err != nil {
		t.Fatalf("GetUploadConfig: %v", err)
	}
	if cfg.BackendURL != "https://b0.example" || cfg.APIKey != "cfb_default_key_000000" {
		t.Errorf("top-level creds = %q/%q, want b0", cfg.BackendURL, cfg.APIKey)
	}
	if _, ok := readRawConfig(t, path)["bindings"]; ok {
		t.Errorf("default write created a bindings key; want none")
	}
}

// TestSetBindingCredentialsCustomNested: a custom binding writes
// bindings[provider][dir] and leaves the top-level creds untouched.
func TestSetBindingCredentialsCustomNested(t *testing.T) {
	withTempConfig(t, &UploadConfig{BackendURL: "https://b0.example", APIKey: "cfb_default_key_000000"})
	def := t.TempDir()
	custom := t.TempDir()

	b := ResolveBinding("claude-code", custom, def)
	if err := SetBindingCredentials(b, "https://b1.example", "cfb_custom_key_111111"); err != nil {
		t.Fatalf("SetBindingCredentials: %v", err)
	}

	cfg, err := GetUploadConfig()
	if err != nil {
		t.Fatalf("GetUploadConfig: %v", err)
	}
	if cfg.BackendURL != "https://b0.example" {
		t.Errorf("top-level backend changed to %q, want b0 untouched", cfg.BackendURL)
	}
	got, ok := cfg.Bindings["claude-code"][b.Dir]
	if !ok {
		t.Fatalf("no binding stored under claude-code/%q; bindings=%v", b.Dir, cfg.Bindings)
	}
	if got.BackendURL != "https://b1.example" || got.APIKey != "cfb_custom_key_111111" {
		t.Errorf("binding creds = %q/%q, want b1", got.BackendURL, got.APIKey)
	}
}

// TestGetUploadConfigForCustomMergesGlobal: a custom binding's effective
// config carries the binding's creds AND the global redaction/log level.
func TestGetUploadConfigForCustomMergesGlobal(t *testing.T) {
	def := t.TempDir()
	custom := t.TempDir()
	cdir := ResolveBinding("claude-code", custom, def).Dir
	enabled := true
	withTempConfig(t, &UploadConfig{
		BackendURL: "https://b0.example",
		APIKey:     "cfb_default_key_000000",
		LogLevel:   "debug",
		Redaction:  &RedactionConfig{Enabled: true, UseDefaultPatterns: &enabled},
		Bindings: map[string]map[string]BindingCreds{
			"claude-code": {cdir: {BackendURL: "https://b1.example", APIKey: "cfb_custom_key_111111"}},
		},
	})

	cfg, err := GetUploadConfigFor(ResolveBinding("claude-code", custom, def))
	if err != nil {
		t.Fatalf("GetUploadConfigFor: %v", err)
	}
	if cfg.BackendURL != "https://b1.example" || cfg.APIKey != "cfb_custom_key_111111" {
		t.Errorf("effective creds = %q/%q, want b1", cfg.BackendURL, cfg.APIKey)
	}
	if cfg.LogLevel != "debug" || cfg.Redaction == nil || !cfg.Redaction.Enabled {
		t.Errorf("global fields not merged: logLevel=%q redaction=%v", cfg.LogLevel, cfg.Redaction)
	}
}

// TestGetUploadConfigForMissingBindingIsLeakFree: a non-default binding with
// no stored creds returns ErrNoBinding and must NOT fall back to top-level.
func TestGetUploadConfigForMissingBindingIsLeakFree(t *testing.T) {
	def := t.TempDir()
	custom := t.TempDir()
	withTempConfig(t, &UploadConfig{BackendURL: "https://b0.example", APIKey: "cfb_default_key_000000"})

	cfg, err := GetUploadConfigFor(ResolveBinding("claude-code", custom, def))
	if !errors.Is(err, ErrNoBinding) {
		t.Fatalf("err = %v, want ErrNoBinding", err)
	}
	if cfg != nil && cfg.BackendURL == "https://b0.example" {
		t.Errorf("leaked top-level backend for an unbound custom dir")
	}
}

// TestGetUploadConfigForDefaultBackwardCompat: an old config.json with no
// bindings key resolves the default binding to the top-level creds.
func TestGetUploadConfigForDefaultBackwardCompat(t *testing.T) {
	def := t.TempDir()
	withTempConfig(t, &UploadConfig{BackendURL: "https://b0.example", APIKey: "cfb_default_key_000000"})

	cfg, err := GetUploadConfigFor(ResolveBinding("claude-code", "", def))
	if err != nil {
		t.Fatalf("GetUploadConfigFor(default): %v", err)
	}
	if cfg.BackendURL != "https://b0.example" {
		t.Errorf("default backend = %q, want b0", cfg.BackendURL)
	}
}

// TestExplicitDefaultDirEqualsBare: writing creds for the explicit default
// dir is byte-equivalent to a bare write — no bindings key appears.
func TestExplicitDefaultDirEqualsBare(t *testing.T) {
	path := withTempConfig(t, nil)
	def := t.TempDir()

	b := ResolveBinding("claude-code", def, def)
	if err := SetBindingCredentials(b, "https://b0.example", "cfb_default_key_000000"); err != nil {
		t.Fatalf("SetBindingCredentials: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "bindings") {
		t.Errorf("explicit default dir wrote a bindings key:\n%s", raw)
	}
}

// TestHasBindingsShortCircuit: HasBindings is false for a config with no
// bindings (single-dir users) and true once a binding exists.
func TestHasBindingsShortCircuit(t *testing.T) {
	withTempConfig(t, &UploadConfig{BackendURL: "https://b0.example", APIKey: "cfb_default_key_000000"})
	if has, err := HasBindings("claude-code"); err != nil || has {
		t.Errorf("HasBindings(no bindings) = %v,%v; want false,nil", has, err)
	}

	withTempConfig(t, &UploadConfig{
		BackendURL: "https://b0.example", APIKey: "cfb_default_key_000000",
		Bindings: map[string]map[string]BindingCreds{
			"claude-code": {"/some/dir": {BackendURL: "https://b1.example", APIKey: "cfb_custom_key_111111"}},
		},
	})
	if has, err := HasBindings("claude-code"); err != nil || !has {
		t.Errorf("HasBindings(with binding) = %v,%v; want true,nil", has, err)
	}
}
