// ABOUTME: Tests for the /til skill install, uninstall, and ensure functions.
// ABOUTME: Validates file creation, backup on update, idempotency, and cleanup.
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func setupSkillTest(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)
}

func TestInstallTilSkill(t *testing.T) {
	setupSkillTest(t)

	if err := InstallTilSkill(); err != nil {
		t.Fatalf("InstallTilSkill() failed: %v", err)
	}

	path, _ := getTilSkillPath()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read installed skill: %v", err)
	}

	if string(content) != tilSkillTemplate {
		t.Error("Installed skill content doesn't match template")
	}
}

func TestInstallTilSkill_CreatesParentDirs(t *testing.T) {
	setupSkillTest(t)

	if err := InstallTilSkill(); err != nil {
		t.Fatalf("InstallTilSkill() failed: %v", err)
	}

	path, _ := getTilSkillPath()
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Parent dir doesn't exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("Parent path is not a directory")
	}
}

func TestUninstallTilSkill(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallTilSkill(); err != nil {
		t.Fatalf("InstallTilSkill() failed: %v", err)
	}

	// Uninstall
	if err := UninstallTilSkill(); err != nil {
		t.Fatalf("UninstallTilSkill() failed: %v", err)
	}

	path, _ := getTilSkillPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Skill file still exists after uninstall")
	}

	// Directory should also be gone
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Skill directory still exists after uninstall")
	}
}

func TestUninstallTilSkill_NotInstalled(t *testing.T) {
	setupSkillTest(t)

	// Uninstall when nothing is installed — should not error
	if err := UninstallTilSkill(); err != nil {
		t.Fatalf("UninstallTilSkill() failed on non-existent skill: %v", err)
	}
}

func TestIsTilSkillInstalled(t *testing.T) {
	setupSkillTest(t)

	if IsTilSkillInstalled() {
		t.Error("IsTilSkillInstalled() = true before install")
	}

	if err := InstallTilSkill(); err != nil {
		t.Fatalf("InstallTilSkill() failed: %v", err)
	}

	if !IsTilSkillInstalled() {
		t.Error("IsTilSkillInstalled() = false after install")
	}
}

func TestEnsureTilSkill_FreshInstall(t *testing.T) {
	setupSkillTest(t)

	installed, err := EnsureTilSkill()
	if err != nil {
		t.Fatalf("EnsureTilSkill() failed: %v", err)
	}
	if !installed {
		t.Error("EnsureTilSkill() returned false for fresh install")
	}

	if !IsTilSkillInstalled() {
		t.Error("Skill not installed after EnsureTilSkill")
	}
}

func TestEnsureTilSkill_AlreadyUpToDate(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallTilSkill(); err != nil {
		t.Fatalf("InstallTilSkill() failed: %v", err)
	}

	// Ensure should return false (not newly installed)
	installed, err := EnsureTilSkill()
	if err != nil {
		t.Fatalf("EnsureTilSkill() failed: %v", err)
	}
	if installed {
		t.Error("EnsureTilSkill() returned true when already up to date")
	}
}

func TestEnsureTilSkill_UpdatesOutdated(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallTilSkill(); err != nil {
		t.Fatalf("InstallTilSkill() failed: %v", err)
	}

	// Modify the file to simulate an outdated version
	path, _ := getTilSkillPath()
	os.WriteFile(path, []byte("old content"), 0644)

	// Ensure should update it
	installed, err := EnsureTilSkill()
	if err != nil {
		t.Fatalf("EnsureTilSkill() failed: %v", err)
	}
	if installed {
		t.Error("EnsureTilSkill() returned true for update (not fresh install)")
	}

	// Content should match template
	content, _ := os.ReadFile(path)
	if string(content) != tilSkillTemplate {
		t.Error("Skill content not updated to template")
	}
}

func TestEnsureTilSkill_BackupOnUpdate(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallTilSkill(); err != nil {
		t.Fatalf("InstallTilSkill() failed: %v", err)
	}

	// Modify the file
	path, _ := getTilSkillPath()
	oldContent := "user customized content"
	os.WriteFile(path, []byte(oldContent), 0644)

	// Ensure should create backup
	if _, err := EnsureTilSkill(); err != nil {
		t.Fatalf("EnsureTilSkill() failed: %v", err)
	}

	bakPath := path + ".bak"
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("Backup file not created: %v", err)
	}
	if string(bakContent) != oldContent {
		t.Errorf("Backup content = %q, want %q", string(bakContent), oldContent)
	}
}
