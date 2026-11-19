package cmd

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/hookconfig"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

// outdatedCodexConfig is an installed-but-outdated Codex config in the
// broken shape from kata cd4q: the old marker comments, a pre-rename status
// message, and Codex-owned tables that Codex's own config writer placed
// between confab's last table and the end marker.
const outdatedCodexConfig = `model = "gpt-5"

[mcp_servers.linear]
command = "npx"

[features]
hooks = true

# >>> confab codex hooks >>>
[[hooks.SessionStart]]
matcher = "startup|resume|clear"
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/local/bin/confab hook session-start --provider codex"
statusMessage = "Starting Confab sync"

[[hooks.PreToolUse]]
matcher = "Bash"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/local/bin/confab hook pre-tool-use --provider codex"
statusMessage = ""

[[hooks.PostToolUse]]
matcher = "Bash"
[[hooks.PostToolUse.hooks]]
type = "command"
command = "/usr/local/bin/confab hook post-tool-use --provider codex"
statusMessage = ""

[desktop]
sidebar = true

[plugins."browser@openai-bundled"]
enabled = true
# <<< confab codex hooks <<<`

// codexForeignTables are the non-confab tables of outdatedCodexConfig; every
// hook command must leave them byte-identical.
var codexForeignTables = []string{
	"model = \"gpt-5\"\n",
	"[mcp_servers.linear]\ncommand = \"npx\"\n",
	"[desktop]\nsidebar = true\n",
	"[plugins.\"browser@openai-bundled\"]\nenabled = true\n",
}

// stubCodexBinaryPath pins the binary path written into Codex hook commands
// to one named "confab". Test binaries have another name, so without this an
// install made by the test binary never reads as installed.
func stubCodexBinaryPath(t *testing.T) {
	t.Helper()
	orig := hookconfig.BinaryPath
	hookconfig.BinaryPath = func() (string, error) { return "/usr/local/bin/confab", nil }
	t.Cleanup(func() { hookconfig.BinaryPath = orig })
}

// writeCodexConfigFixture points CONFAB_CODEX_DIR at a temp dir holding
// content as config.toml and returns the config path.
func writeCodexConfigFixture(t *testing.T, tmpDir, content string) string {
	t.Helper()
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	path := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}
	return path
}

func assertCodexForeignTablesKept(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	content := string(data)
	for _, want := range codexForeignTables {
		if !strings.Contains(content, want) {
			t.Errorf("Codex-owned config %q was lost\n%s", want, content)
		}
	}
	if strings.Contains(content, "confab codex hooks") {
		t.Errorf("stale marker comments must be removed\n%s", content)
	}
	return content
}

func TestRunSetup_RefreshesOutdatedCodexHooks(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, _ := setupSetupTestEnv(t)
	codexCfg := writeCodexConfigFixture(t, tmpDir, outdatedCodexConfig)
	stubCodexBinaryPath(t)
	resetSetupProviderName(t)
	stubProviderDetect(t, "codex")

	runOnce := func() string {
		cmd := &cobra.Command{}
		cmd.Flags().String("backend-url", server.URL, "")
		cmd.Flags().String("api-key", "cfb_refresh-test-key-1234567890", "")
		return captureStdout(t, func() {
			if err := runSetup(cmd, nil); err != nil {
				t.Fatalf("runSetup failed: %v", err)
			}
		})
	}

	output := runOnce()
	if !strings.Contains(output, "✓ hooks updated") {
		t.Fatalf("setup on an outdated Codex install must print `hooks updated`, got:\n%s", output)
	}
	content := assertCodexForeignTablesKept(t, codexCfg)
	if !strings.Contains(content, `statusMessage = "Starting Confabulous Code sync"`) || strings.Contains(content, "Starting Confab sync") {
		t.Fatalf("setup must refresh the outdated hook\n%s", content)
	}
	if n := strings.Count(content, "hook session-start --provider codex"); n != 1 {
		t.Fatalf("want exactly one SessionStart hook after refresh, got %d\n%s", n, content)
	}

	output = runOnce()
	if !strings.Contains(output, "hooks already installed (no changes)") {
		t.Fatalf("a second setup must find the hooks up to date, got:\n%s", output)
	}
	if after, _ := os.ReadFile(codexCfg); string(after) != content {
		t.Fatalf("a second setup must not change config.toml\n%s", after)
	}
}

// A Codex install whose hooks are hand-written as inline arrays reads as
// installed but cannot be checked or edited by span. Setup must say so, leave
// config.toml alone, and still finish the provider (skills included).
func TestRunSetup_CodexHooksThatCannotBeCheckedAreLeftAlone(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	const inline = `[hooks]
SessionStart = [{ matcher = "startup", hooks = [{ type = "command", command = "/usr/local/bin/confab hook session-start --provider codex" }] }]
PreToolUse = [{ matcher = "Bash", hooks = [{ type = "command", command = "/usr/local/bin/confab hook pre-tool-use --provider codex" }] }]
PostToolUse = [{ matcher = "Bash", hooks = [{ type = "command", command = "/usr/local/bin/confab hook post-tool-use --provider codex" }] }]
`
	tmpDir, _ := setupSetupTestEnv(t)
	codexCfg := writeCodexConfigFixture(t, tmpDir, inline)
	stubCodexBinaryPath(t)
	resetSetupProviderName(t)
	stubProviderDetect(t, "codex")

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_inline-test-key-12345678901", "")
	output := captureStdout(t, func() {
		if err := runSetup(cmd, nil); err != nil {
			t.Fatalf("runSetup must not fail when hooks cannot be checked: %v", err)
		}
	})

	for _, want := range []string{"could not check whether hooks are up to date", "hooks already installed (no changes)", "codex: installed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("setup output missing %q\n%s", want, output)
		}
	}
	if after, _ := os.ReadFile(codexCfg); string(after) != inline {
		t.Fatalf("setup must not modify a config it cannot edit safely\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(codexCfg), "skills", "retro", "SKILL.md")); err != nil {
		t.Fatalf("skills must still be installed: %v", err)
	}
}

func TestStatus_CodexHooksOutdated(t *testing.T) {
	stubProviderDetect(t, "codex")
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, configPath := setupSetupTestEnv(t)
	cfgData, _ := json.Marshal(config.UploadConfig{BackendURL: server.URL, APIKey: "cfb_outdated-test-key-12345678"})
	if err := os.WriteFile(configPath, cfgData, 0600); err != nil {
		t.Fatalf("config: %v", err)
	}
	codexCfg := writeCodexConfigFixture(t, tmpDir, outdatedCodexConfig)
	stubCodexBinaryPath(t)

	rootCmd.SetArgs([]string{"status"})
	defer rootCmd.SetArgs(nil)
	output := captureStdout(t, func() { _ = rootCmd.Execute() })

	for _, want := range []string{"Hooks: ✓ Installed", "outdated", "confab setup"} {
		if !strings.Contains(output, want) {
			t.Fatalf("status must report outdated Codex hooks and name `confab setup`, missing %q\n%s", want, output)
		}
	}
	if after, _ := os.ReadFile(codexCfg); string(after) != outdatedCodexConfig {
		t.Fatalf("status must not modify config.toml")
	}

	// Once refreshed, the plain "Installed" line returns.
	if _, err := (provider.Codex{}).RefreshHooks(); err != nil {
		t.Fatalf("RefreshHooks: %v", err)
	}
	rootCmd.SetArgs([]string{"status"})
	output = captureStdout(t, func() { _ = rootCmd.Execute() })
	if strings.Contains(output, "outdated") {
		t.Fatalf("status must not report up-to-date hooks as outdated\n%s", output)
	}
}

func TestHooksAddRemoveCodexKeepsForeignTables(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	codexCfg := writeCodexConfigFixture(t, tmpDir, outdatedCodexConfig)
	stubCodexBinaryPath(t)
	resetHooksProviderName(t)
	hooksProviderName = provider.NameCodex

	captureStdout(t, func() {
		if err := hooksAddCmd.RunE(hooksAddCmd, nil); err != nil {
			t.Fatalf("hooks add --provider codex: %v", err)
		}
	})
	content := assertCodexForeignTablesKept(t, codexCfg)
	for _, event := range []string{"session-start", "pre-tool-use", "post-tool-use"} {
		if n := strings.Count(content, "hook "+event+" --provider codex"); n != 1 {
			t.Fatalf("want exactly one %s hook after hooks add, got %d\n%s", event, n, content)
		}
	}
	if installed, err := (provider.Codex{}).IsHooksInstalled(); err != nil || !installed {
		t.Fatalf("IsHooksInstalled after hooks add = %v, %v", installed, err)
	}

	captureStdout(t, func() {
		if err := hooksRemoveCmd.RunE(hooksRemoveCmd, nil); err != nil {
			t.Fatalf("hooks remove --provider codex: %v", err)
		}
	})
	content = assertCodexForeignTablesKept(t, codexCfg)
	if strings.Contains(content, "--provider codex") || strings.Contains(content, "hooks.state") {
		t.Fatalf("hooks remove must delete confab's hook and trust-state tables\n%s", content)
	}
	if !strings.Contains(content, "[features]\nhooks = true\n") {
		t.Fatalf("hooks remove must leave [features] alone\n%s", content)
	}
}
