package hookconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeStateDirEnv mirrors pkg/config.ClaudeStateDirEnv and
// pkg/provider.ClaudeStateDirEnv. Inlined to keep this test file
// from depending on either package's constant export.
const claudeStateDirEnv = "CONFAB_CLAUDE_DIR"

func TestInstallSyncHooksWritesSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(claudeStateDirEnv, tmpDir)

	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	for _, want := range []string{"hook session-start", "hook session-end"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("settings.json missing %q after InstallSyncHooks()\n%s", want, string(data))
		}
	}
}

func TestInstallPreToolUseHooksWritesSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(claudeStateDirEnv, tmpDir)

	if err := InstallPreToolUseHooks(); err != nil {
		t.Fatalf("InstallPreToolUseHooks() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), "hook pre-tool-use") {
		t.Errorf("settings.json missing 'hook pre-tool-use'\n%s", string(data))
	}
}

func TestInstallPostToolUseHooksWritesSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(claudeStateDirEnv, tmpDir)

	if err := InstallPostToolUseHooks(); err != nil {
		t.Fatalf("InstallPostToolUseHooks() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), "hook post-tool-use") {
		t.Errorf("settings.json missing 'hook post-tool-use'\n%s", string(data))
	}
}

func TestInstallUserPromptSubmitHookWritesSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(claudeStateDirEnv, tmpDir)

	if err := InstallUserPromptSubmitHook(); err != nil {
		t.Fatalf("InstallUserPromptSubmitHook() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), "hook user-prompt-submit") {
		t.Errorf("settings.json missing 'hook user-prompt-submit'\n%s", string(data))
	}
}

func TestUninstallSyncHooksRemovesEntries(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(claudeStateDirEnv, tmpDir)

	if err := InstallSyncHooks(); err != nil {
		t.Fatalf("InstallSyncHooks() error = %v", err)
	}
	if err := UninstallSyncHooks(); err != nil {
		t.Fatalf("UninstallSyncHooks() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
	for _, notWant := range []string{"hook session-start", "hook session-end"} {
		if strings.Contains(string(data), notWant) {
			t.Errorf("settings.json still contains %q after UninstallSyncHooks()\n%s", notWant, string(data))
		}
	}
}

func TestIsSyncHooksInstalledRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(claudeStateDirEnv, tmpDir)

	// Pre-populate settings with confab-named commands so the binary-name
	// check (isConfabCommand) passes in test mode.
	const confabSettings = `{
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-start"}]}],
    "SessionEnd":   [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-end"}]}]
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(confabSettings), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	ok, err := IsSyncHooksInstalled()
	if err != nil {
		t.Fatalf("IsSyncHooksInstalled() error = %v", err)
	}
	if !ok {
		t.Fatal("IsSyncHooksInstalled() = false; want true")
	}
}
