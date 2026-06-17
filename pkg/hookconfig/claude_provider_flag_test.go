package hookconfig

import (
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
)

// installedClaudeCommand walks the nested hook structure for eventName and
// returns the first confab hook command string found (or "").
func installedClaudeCommand(t *testing.T, settings *config.ClaudeSettings, eventName string) string {
	t.Helper()
	for _, entry := range settings.GetEventHooks(eventName) {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooks, _ := entryMap["hooks"].([]any)
		for _, h := range hooks {
			hookMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := hookMap["command"].(string); ok && strings.Contains(cmd, "hook session-") {
				return cmd
			}
		}
	}
	return ""
}

// TestInstallSyncHooks_AppendsProviderClaudeCode verifies the installed Claude
// session-start/session-end command strings carry an explicit
// `--provider claude-code` (m9mb migration), like codex/cursor already do.
func TestInstallSyncHooks_AppendsProviderClaudeCode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClaudeStateDirEnv, tmpDir)

	if err := InstallSyncHooks(testSettingsPath(t)); err != nil {
		t.Fatalf("InstallSyncHooks: %v", err)
	}
	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}

	start := installedClaudeCommand(t, settings, "SessionStart")
	if !strings.Contains(start, "hook session-start") {
		t.Fatalf("SessionStart command missing 'hook session-start': %q", start)
	}
	if !strings.HasSuffix(start, "hook session-start --provider claude-code") {
		t.Errorf("SessionStart command = %q, want suffix 'hook session-start --provider claude-code'", start)
	}

	end := installedClaudeCommand(t, settings, "SessionEnd")
	if !strings.HasSuffix(end, "hook session-end --provider claude-code") {
		t.Errorf("SessionEnd command = %q, want suffix 'hook session-end --provider claude-code'", end)
	}
}

// TestInstallSyncHooks_IdempotentOverOldNoFlagInstall verifies that
// re-running install upgrades a pre-existing OLD (no-flag) Claude hook string
// in place rather than appending a duplicate. The idempotency matcher uses
// Contains "hook session-start"/"session-end", which matches both shapes.
func TestInstallSyncHooks_IdempotentOverOldNoFlagInstall(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClaudeStateDirEnv, tmpDir)

	// Seed an old-style no-flag confab hook (simulating a pre-m9mb install).
	if err := config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		setTestHook(settings, "SessionStart",
			makeMatcher("*", makeHook("command", "/usr/local/bin/confab hook session-start")),
		)
		setTestHook(settings, "SessionEnd",
			makeMatcher("*", makeHook("command", "/usr/local/bin/confab hook session-end")),
		)
		return nil
	}); err != nil {
		t.Fatalf("seed old hooks: %v", err)
	}

	if err := InstallSyncHooks(testSettingsPath(t)); err != nil {
		t.Fatalf("InstallSyncHooks: %v", err)
	}

	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}

	// Exactly one session-start hook should remain (upgraded in place).
	count := 0
	for _, entry := range settings.GetEventHooks("SessionStart") {
		entryMap := entry.(map[string]any)
		for _, h := range entryMap["hooks"].([]any) {
			cmd, _ := h.(map[string]any)["command"].(string)
			if strings.Contains(cmd, "hook session-start") {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 session-start hook after re-install, got %d", count)
	}
}

// TestUninstallSyncHooks_RemovesNewProviderFlagShape verifies uninstall finds
// and removes the NEW (--provider claude-code) command shape.
func TestUninstallSyncHooks_RemovesNewProviderFlagShape(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.ClaudeStateDirEnv, tmpDir)

	if err := config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		setTestHook(settings, "SessionStart",
			makeMatcher("*", makeHook("command", "/usr/local/bin/confab hook session-start --provider claude-code")),
		)
		setTestHook(settings, "SessionEnd",
			makeMatcher("*", makeHook("command", "/usr/local/bin/confab hook session-end --provider claude-code")),
		)
		return nil
	}); err != nil {
		t.Fatalf("seed new hooks: %v", err)
	}

	if err := UninstallSyncHooks(testSettingsPath(t)); err != nil {
		t.Fatalf("UninstallSyncHooks: %v", err)
	}

	settings, err := config.ReadSettings()
	if err != nil {
		t.Fatalf("ReadSettings: %v", err)
	}
	if installedClaudeCommand(t, settings, "SessionStart") != "" {
		t.Error("SessionStart hook not removed (new --provider shape)")
	}
	if installedClaudeCommand(t, settings, "SessionEnd") != "" {
		t.Error("SessionEnd hook not removed (new --provider shape)")
	}
}
