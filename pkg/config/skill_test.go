// ABOUTME: Tests for the provider-aware bundled skill installer.
// ABOUTME: Exercises install, uninstall, backup, idempotency, and failure safety.
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallBundledSkill(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, tilSkillName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read installed skill: %v", err)
	}

	if string(content) != tilSkillTemplate {
		t.Error("Installed skill content doesn't match template")
	}
}

func TestInstallBundledSkill_CodexTemplate(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderCodex, tilSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	content, err := os.ReadFile(SkillPath(stateDir, tilSkillName))
	if err != nil {
		t.Fatalf("Failed to read installed skill: %v", err)
	}

	if string(content) != codexTilSkillTemplate {
		t.Error("Installed Codex skill content doesn't match template")
	}
}

func TestInstallBundledSkill_CreatesParentDirs(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	dir := filepath.Dir(SkillPath(stateDir, tilSkillName))
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

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	if err := UninstallBundledSkill(stateDir, tilSkillName); err != nil {
		t.Fatalf("UninstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, tilSkillName)
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

	if err := UninstallBundledSkill(stateDir, tilSkillName); err != nil {
		t.Fatalf("UninstallBundledSkill() failed on non-existent skill: %v", err)
	}
}

func TestIsBundledSkillInstalled(t *testing.T) {
	stateDir := t.TempDir()

	if IsBundledSkillInstalled(stateDir, tilSkillName) {
		t.Error("IsBundledSkillInstalled() = true before install")
	}

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	if !IsBundledSkillInstalled(stateDir, tilSkillName) {
		t.Error("IsBundledSkillInstalled() = false after install")
	}
}

func TestInstallBundledSkill_BackupOnUpdate(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, tilSkillName)
	oldContent := "user customized content"
	if err := os.WriteFile(path, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err != nil {
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

func TestInstallBundledSkill_FailsWhenBackupFails(t *testing.T) {
	stateDir := t.TempDir()

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err != nil {
		t.Fatalf("InstallBundledSkill() failed: %v", err)
	}

	path := SkillPath(stateDir, tilSkillName)
	customContent := "user customized content"
	if err := os.WriteFile(path, []byte(customContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := os.Mkdir(path+".bak", 0o755); err != nil {
		t.Fatalf("Mkdir(bakPath) failed: %v", err)
	}

	if err := InstallBundledSkill(stateDir, SkillProviderClaude, tilSkillName); err == nil {
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
