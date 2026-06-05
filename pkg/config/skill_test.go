// ABOUTME: Tests for the provider-aware bundled skill installer.
// ABOUTME: Exercises install, uninstall, backup, idempotency, and failure safety.
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// setupSkillTest points the Claude state dir at a temp dir for skill tests that
// exercise the legacy single-skill install helpers.
func setupSkillTest(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)
}

func TestInstallBundledSkill(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, retroSkillName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read installed skill: %v", err)
	}

	if string(content) != retroSkillTemplate {
		t.Error("Installed skill content doesn't match template")
	}
}

func TestInstallBundledSkill_CodexTemplate(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderCodex, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	content, err := os.ReadFile(SkillPath(stateDir, retroSkillName))
	if err != nil {
		t.Fatalf("Failed to read installed skill: %v", err)
	}

	if string(content) != codexRetroSkillTemplate {
		t.Error("Installed Codex skill content doesn't match template")
	}
}

func TestInstallBundledSkill_CreatesParentDirs(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	dir := filepath.Dir(SkillPath(stateDir, retroSkillName))
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Parent dir doesn't exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("Parent path is not a directory")
	}
}

func TestUninstallBundledSkill(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	if err := UninstallBundledSkill(stateDir, retroSkillName); err != nil {
		t.Fatalf("UninstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, retroSkillName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Skill file still exists after uninstall")
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Skill directory still exists after uninstall")
	}
}

func TestUninstallBundledSkill_NotInstalled(t *testing.T) {
	stateDir := t.TempDir()

	if err := UninstallBundledSkill(stateDir, retroSkillName); err != nil {
		t.Fatalf("UninstallBundledSkill() failed on non-existent skill: %v", err)
	}
}

func TestIsBundledSkillInstalled(t *testing.T) {
	stateDir := t.TempDir()

	if IsBundledSkillInstalled(stateDir, retroSkillName) {
		t.Error("IsBundledSkillInstalled() = true before install")
	}

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	if !IsBundledSkillInstalled(stateDir, retroSkillName) {
		t.Error("IsBundledSkillInstalled() = false after install")
	}
}

func TestInstallBundledSkill_BackupOnUpdate(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, retroSkillName)
	oldContent := "user customized content"
	if err := os.WriteFile(path, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	bakContent, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("Backup file not created: %v", err)
	}
	if string(bakContent) != oldContent {
		t.Errorf("Backup content = %q, want %q", string(bakContent), oldContent)
	}
}

// TestReconcileBundledSkills_PrunesRetiredTilSkill verifies the one-time TIL
// migration (CF-530): a stale /til skill left by an older confab version is
// removed, the current /retro skill is installed, and the user's own custom
// skills are left untouched.
func TestReconcileBundledSkills_PrunesRetiredTilSkill(t *testing.T) {
	stateDir := t.TempDir()

	// Simulate an older confab version having installed the /til skill, with a
	// user-customized backup beside it.
	tilPath := SkillPath(stateDir, "til")
	if err := os.MkdirAll(filepath.Dir(tilPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tilPath, []byte("stale til skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tilPath+".bak", []byte("user customized til"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A user's own custom skill that must survive reconcile.
	customPath := SkillPath(stateDir, "my-custom")
	if err := os.MkdirAll(filepath.Dir(customPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customPath, []byte("user skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ReconcileBundledSkills(stateDir, SkillProviderClaude); err != nil {
		t.Fatalf("ReconcileBundledSkills() failed: %v", err)
	}

	// Retired /til skill dir is fully pruned, including the .bak.
	if _, err := os.Stat(filepath.Dir(tilPath)); !os.IsNotExist(err) {
		t.Error("retired /til skill dir still exists after reconcile")
	}

	// Current bundled skill (/retro) is installed.
	if !IsBundledSkillInstalled(stateDir, retroSkillName) {
		t.Error("/retro skill not installed after reconcile")
	}

	// User's custom skill is left untouched.
	if got, err := os.ReadFile(customPath); err != nil || string(got) != "user skill" {
		t.Errorf("user custom skill altered/removed: got %q err %v", string(got), err)
	}
}

// TestReconcileBundledSkills_IdempotentWithoutRetired verifies reconcile is a
// no-op-safe operation when no retired skill is present and when run twice.
func TestReconcileBundledSkills_IdempotentWithoutRetired(t *testing.T) {
	stateDir := t.TempDir()

	if err := ReconcileBundledSkills(stateDir, SkillProviderClaude); err != nil {
		t.Fatalf("first ReconcileBundledSkills() failed: %v", err)
	}
	if err := ReconcileBundledSkills(stateDir, SkillProviderClaude); err != nil {
		t.Fatalf("second ReconcileBundledSkills() failed: %v", err)
	}

	if !IsBundledSkillInstalled(stateDir, retroSkillName) {
		t.Error("/retro skill not installed after reconcile")
	}
}

func TestInstallBundledSkill_FailsWhenBackupFails(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, retroSkillName)
	customContent := "user customized content"
	if err := os.WriteFile(path, []byte(customContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := os.Mkdir(path+".bak", 0o755); err != nil {
		t.Fatalf("Mkdir(bakPath) failed: %v", err)
	}

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, retroSkillName); err == nil {
		t.Fatal("InstallBundledSkill() returned nil, want error when backup write fails")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(path) failed: %v", err)
	}
	if string(got) != customContent {
		t.Errorf("Install overwrote the customized file even though backup failed: got %q, want %q",
			string(got), customContent)
	}
}
