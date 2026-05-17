package cmd

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

// runStatusCapture runs the status command end-to-end against a real
// httptest backend and returns the captured stdout. backendValid drives
// whether the auth-validate endpoint reports the API key as valid.
func runStatusCapture(t *testing.T, backendValid bool) string {
	t.Helper()

	backend := &setupTestBackend{validateValid: backendValid}
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)

	tmpDir, configPath := setupSetupTestEnv(t, server.URL)
	t.Setenv(provider.CodexStateDirEnv, filepath.Join(tmpDir, ".codex"))

	cfg := config.UploadConfig{BackendURL: server.URL, APIKey: "cfb_status-test-key-12345678"}
	cfgData, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, cfgData, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out bytes.Buffer
	origOut, origErr := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr()
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"status"})
	t.Cleanup(func() {
		rootCmd.SetOut(origOut)
		rootCmd.SetErr(origErr)
		rootCmd.SetArgs(nil)
	})

	output := captureStdout(t, func() {
		_ = rootCmd.Execute()
	})

	return output + out.String()
}

// TestStatus_DropsProviderFlag asserts the CF-422 change: status no
// longer accepts --provider. Old usage must produce an unknown-flag
// error.
func TestStatus_DropsProviderFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv(provider.CodexStateDirEnv, filepath.Join(tmpDir, ".codex"))
	t.Setenv("CONFAB_CLAUDE_DIR", filepath.Join(tmpDir, ".claude"))

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"status", "--provider", "codex"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected unknown-flag error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("expected `unknown flag` error, got: %v", err)
	}
}

func TestStatus_BothInstalled(t *testing.T) {
	stubProviderDetect(t, "claude", "codex")

	output := runStatusCapture(t, true)

	// Claude block: CLI on PATH, no hooks installed in fresh test env.
	wantSnippets := []string{
		"Backend Sync:",
		"Provider: claude-code",
		"CLI: ✓ on PATH",
		"Provider: codex",
	}
	for _, want := range wantSnippets {
		if !strings.Contains(output, want) {
			t.Fatalf("status output missing %q\noutput:\n%s", want, output)
		}
	}

	// Backend section must appear before any provider block.
	idxBackend := strings.Index(output, "Backend Sync:")
	idxClaude := strings.Index(output, "Provider: claude-code")
	if idxBackend < 0 || idxClaude < 0 || idxBackend >= idxClaude {
		t.Fatalf("Backend Sync must appear BEFORE provider blocks. backend=%d claude=%d\noutput:\n%s",
			idxBackend, idxClaude, output)
	}

	// Block order is fixed: claude-code BEFORE codex.
	idxCodex := strings.Index(output, "Provider: codex")
	if idxCodex < 0 || idxClaude >= idxCodex {
		t.Fatalf("Provider block order must be claude-code, then codex; got claude=%d codex=%d", idxClaude, idxCodex)
	}
}

func TestStatus_ClaudeOnlyCLI(t *testing.T) {
	stubProviderDetect(t, "claude")

	output := runStatusCapture(t, true)

	if !strings.Contains(output, "Provider: claude-code") {
		t.Fatalf("claude-code block missing\noutput:\n%s", output)
	}
	if !strings.Contains(output, "Provider: codex") {
		t.Fatalf("codex block must appear even when CLI absent\noutput:\n%s", output)
	}
	if !strings.Contains(output, "CLI: ✗ not on PATH") {
		t.Fatalf("expected `CLI: ✗ not on PATH` for codex\noutput:\n%s", output)
	}
}

func TestStatus_OrphanCodexHooks(t *testing.T) {
	// Stub: claude present, codex absent.
	stubProviderDetect(t, "claude")

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, configPath := setupSetupTestEnv(t, server.URL)
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)

	cfg := config.UploadConfig{BackendURL: server.URL, APIKey: "cfb_orphan-test-key-12345678"}
	cfgData, _ := json.Marshal(cfg)
	if err := os.WriteFile(configPath, cfgData, 0600); err != nil {
		t.Fatalf("config: %v", err)
	}

	// Pre-populate ~/.codex/config.toml with a `confab`-named hook so
	// IsHooksInstalled returns true. (Test binaries aren't named
	// `confab`, so the hookconfig pkg's isConfabCommand wouldn't match
	// hooks installed by the test binary itself.)
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	codexCfg := filepath.Join(codexDir, "config.toml")
	confabCodexCfg := `[features]
hooks = true

[[hooks.SessionStart]]
matcher = "startup|resume|clear"
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/local/bin/confab hook session-start --provider codex"
`
	if err := os.WriteFile(codexCfg, []byte(confabCodexCfg), 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"status"})
	defer rootCmd.SetArgs(nil)

	output := captureStdout(t, func() {
		_ = rootCmd.Execute()
	})

	wantSnippets := []string{
		"Provider: codex",
		"CLI: ✗ not on PATH",
		"Hooks: ✓ Installed (orphaned",
		"confab hooks remove --provider codex",
	}
	for _, want := range wantSnippets {
		if !strings.Contains(output, want) {
			t.Fatalf("status orphan output missing %q\noutput:\n%s", want, output)
		}
	}
}

func TestStatus_BackendSectionPresent(t *testing.T) {
	stubProviderDetect(t)

	output := runStatusCapture(t, true)

	if !strings.Contains(output, "Backend Sync:") {
		t.Fatalf("expected Backend Sync section\noutput:\n%s", output)
	}
	if !strings.Contains(output, "Backend:") {
		t.Fatalf("expected Backend URL line\noutput:\n%s", output)
	}
}

// TestStatus_BackendNotConfigured covers the case where no config exists.
func TestStatus_BackendNotConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv(provider.CodexStateDirEnv, filepath.Join(tmpDir, ".codex"))
	t.Setenv("CONFAB_CLAUDE_DIR", filepath.Join(tmpDir, ".claude"))
	t.Setenv("CONFAB_CONFIG_PATH", filepath.Join(tmpDir, ".confab", "config.json"))

	stubProviderDetect(t)

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"status"})
	defer rootCmd.SetArgs(nil)

	output := captureStdout(t, func() {
		_ = rootCmd.Execute()
	})

	if !strings.Contains(output, "Not configured") && !strings.Contains(output, "not configured") {
		t.Fatalf("expected unconfigured backend message\noutput:\n%s", output)
	}
}

