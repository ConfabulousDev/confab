package hookconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Uninstall with nothing confab-owned present must be a true no-op (kata 2n1f):
// no file, no directory, no backup, no rewrite.

var claudeUninstallers = []func(string) error{
	UninstallSyncHooks,
	UninstallPreToolUseHooks,
	UninstallPostToolUseHooks,
	UninstallUserPromptSubmitHook,
}

func TestClaudeUninstallWithNoSettingsCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	settingsPath := filepath.Join(dir, "settings.json")
	for _, uninstall := range claudeUninstallers {
		if err := uninstall(settingsPath); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("uninstall must not create the settings dir (stat err = %v)", err)
	}
}

func TestClaudeUninstallWithOnlyUserHooksLeavesFileUntouched(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	// Hand-formatted on purpose: a rewrite would re-indent it.
	original := []byte(`{"hooks": {"SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "echo hi"}]}],
  "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "my-linter"}]}]},
  "theme": "dark"}`)
	if err := os.WriteFile(settingsPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(settingsPath, past, past)

	for _, uninstall := range claudeUninstallers {
		if err := uninstall(settingsPath); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
	}
	got, _ := os.ReadFile(settingsPath)
	if string(got) != string(original) {
		t.Errorf("settings.json must be byte-identical\nwant: %s\ngot:  %s", original, got)
	}
	info, _ := os.Stat(settingsPath)
	if !info.ModTime().Equal(past) {
		t.Errorf("settings.json must not be rewritten (mtime changed to %v)", info.ModTime())
	}
}

func TestCursorUninstallWithNoConfabEntriesWritesNoBackup(t *testing.T) {
	dir := t.TempDir()
	hooksPath := filepath.Join(dir, "hooks.json")
	original := []byte(`{"version": 1, "hooks": {"sessionStart": [{"command": "echo hi", "type": "command"}], "stop": []}}`)
	if err := os.WriteFile(hooksPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("UninstallCursorHooks: %v", err)
	}
	got, _ := os.ReadFile(hooksPath)
	if string(got) != string(original) {
		t.Errorf("hooks.json must be byte-identical\nwant: %s\ngot:  %s", original, got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("no backup may be written when nothing is removed; dir has %d entries", len(entries))
	}
}

func TestCursorUninstallStillRemovesConfabEntries(t *testing.T) {
	hooksPath := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCursorHooks(hooksPath); err != nil {
		t.Fatal(err)
	}
	if ok, _ := IsCursorHooksInstalled(hooksPath); ok {
		t.Error("confab entries must be removed")
	}
	f := readCursorHooks(t, hooksPath)
	if hasConfabHookCommand(f, cursorSessionStartEvent) {
		t.Error("sessionStart still carries a confab command")
	}
}
