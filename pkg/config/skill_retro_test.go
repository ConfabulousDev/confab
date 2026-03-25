// ABOUTME: Tests for the /retro skill install, uninstall, and ensure functions.
// ABOUTME: Validates file creation, backup on update, idempotency, and cleanup.
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRetroSkill(t *testing.T) {
	setupSkillTest(t)

	if err := InstallRetroSkill(); err != nil {
		t.Fatalf("InstallRetroSkill() failed: %v", err)
	}

	path, _ := getRetroSkillPath()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read installed skill: %v", err)
	}

	if string(content) != retroSkillTemplate {
		t.Error("Installed skill content doesn't match template")
	}
}

func TestInstallRetroSkill_CreatesParentDirs(t *testing.T) {
	setupSkillTest(t)

	if err := InstallRetroSkill(); err != nil {
		t.Fatalf("InstallRetroSkill() failed: %v", err)
	}

	path, _ := getRetroSkillPath()
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Parent dir doesn't exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("Parent path is not a directory")
	}
}

func TestUninstallRetroSkill(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallRetroSkill(); err != nil {
		t.Fatalf("InstallRetroSkill() failed: %v", err)
	}

	// Uninstall
	if err := UninstallRetroSkill(); err != nil {
		t.Fatalf("UninstallRetroSkill() failed: %v", err)
	}

	path, _ := getRetroSkillPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Skill file still exists after uninstall")
	}

	// Directory should also be gone
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Skill directory still exists after uninstall")
	}
}

func TestUninstallRetroSkill_NotInstalled(t *testing.T) {
	setupSkillTest(t)

	// Uninstall when nothing is installed — should not error
	if err := UninstallRetroSkill(); err != nil {
		t.Fatalf("UninstallRetroSkill() failed on non-existent skill: %v", err)
	}
}

func TestIsRetroSkillInstalled(t *testing.T) {
	setupSkillTest(t)

	if IsRetroSkillInstalled() {
		t.Error("IsRetroSkillInstalled() = true before install")
	}

	if err := InstallRetroSkill(); err != nil {
		t.Fatalf("InstallRetroSkill() failed: %v", err)
	}

	if !IsRetroSkillInstalled() {
		t.Error("IsRetroSkillInstalled() = false after install")
	}
}

func TestEnsureRetroSkill_FreshInstall(t *testing.T) {
	setupSkillTest(t)

	installed, err := EnsureRetroSkill()
	if err != nil {
		t.Fatalf("EnsureRetroSkill() failed: %v", err)
	}
	if !installed {
		t.Error("EnsureRetroSkill() returned false for fresh install")
	}

	if !IsRetroSkillInstalled() {
		t.Error("Skill not installed after EnsureRetroSkill")
	}
}

func TestEnsureRetroSkill_AlreadyUpToDate(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallRetroSkill(); err != nil {
		t.Fatalf("InstallRetroSkill() failed: %v", err)
	}

	// Ensure should return false (not newly installed)
	installed, err := EnsureRetroSkill()
	if err != nil {
		t.Fatalf("EnsureRetroSkill() failed: %v", err)
	}
	if installed {
		t.Error("EnsureRetroSkill() returned true when already up to date")
	}
}

func TestEnsureRetroSkill_UpdatesOutdated(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallRetroSkill(); err != nil {
		t.Fatalf("InstallRetroSkill() failed: %v", err)
	}

	// Modify the file to simulate an outdated version
	path, _ := getRetroSkillPath()
	os.WriteFile(path, []byte("old content"), 0644)

	// Ensure should update it
	installed, err := EnsureRetroSkill()
	if err != nil {
		t.Fatalf("EnsureRetroSkill() failed: %v", err)
	}
	if installed {
		t.Error("EnsureRetroSkill() returned true for update (not fresh install)")
	}

	// Content should match template
	content, _ := os.ReadFile(path)
	if string(content) != retroSkillTemplate {
		t.Error("Skill content not updated to template")
	}
}

func TestEnsureRetroSkill_BackupOnUpdate(t *testing.T) {
	setupSkillTest(t)

	// Install first
	if err := InstallRetroSkill(); err != nil {
		t.Fatalf("InstallRetroSkill() failed: %v", err)
	}

	// Modify the file
	path, _ := getRetroSkillPath()
	oldContent := "user customized content"
	os.WriteFile(path, []byte(oldContent), 0644)

	// Ensure should create backup
	if _, err := EnsureRetroSkill(); err != nil {
		t.Fatalf("EnsureRetroSkill() failed: %v", err)
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
