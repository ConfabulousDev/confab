// ABOUTME: Tests for the generic skill installer (install, uninstall, backup, idempotency).
// ABOUTME: Exercises the skill type directly via a dummy skill, independent of /til and /retro.
package config

import (
	"os"
	"path/filepath"
	"testing"
)

var dummyTestSkill = skill{
	name:     "test-dummy",
	template: "---\nname: test-dummy\n---\n\ndummy skill for tests\n",
}

func TestSkill_Install(t *testing.T) {
	setupSkillTest(t)

	if err := dummyTestSkill.Install(); err != nil {
		t.Fatalf("Install() failed: %v", err)
	}

	path, _ := dummyTestSkill.path()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read installed skill: %v", err)
	}

	if string(content) != dummyTestSkill.template {
		t.Error("Installed skill content doesn't match template")
	}
}

func TestSkill_Install_CreatesParentDirs(t *testing.T) {
	setupSkillTest(t)

	if err := dummyTestSkill.Install(); err != nil {
		t.Fatalf("Install() failed: %v", err)
	}

	path, _ := dummyTestSkill.path()
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Parent dir doesn't exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("Parent path is not a directory")
	}
}

func TestSkill_Uninstall(t *testing.T) {
	setupSkillTest(t)

	if err := dummyTestSkill.Install(); err != nil {
		t.Fatalf("Install() failed: %v", err)
	}

	if err := dummyTestSkill.Uninstall(); err != nil {
		t.Fatalf("Uninstall() failed: %v", err)
	}

	path, _ := dummyTestSkill.path()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Skill file still exists after uninstall")
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("Skill directory still exists after uninstall")
	}
}

func TestSkill_Uninstall_NotInstalled(t *testing.T) {
	setupSkillTest(t)

	if err := dummyTestSkill.Uninstall(); err != nil {
		t.Fatalf("Uninstall() failed on non-existent skill: %v", err)
	}
}

func TestSkill_Installed(t *testing.T) {
	setupSkillTest(t)

	if dummyTestSkill.Installed() {
		t.Error("Installed() = true before install")
	}

	if err := dummyTestSkill.Install(); err != nil {
		t.Fatalf("Install() failed: %v", err)
	}

	if !dummyTestSkill.Installed() {
		t.Error("Installed() = false after install")
	}
}

func TestSkill_BackupOnUpdate(t *testing.T) {
	setupSkillTest(t)

	if err := dummyTestSkill.Install(); err != nil {
		t.Fatalf("Install() failed: %v", err)
	}

	path, _ := dummyTestSkill.path()
	oldContent := "user customized content"
	if err := os.WriteFile(path, []byte(oldContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := dummyTestSkill.Install(); err != nil {
		t.Fatalf("Install() failed: %v", err)
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

// TestSkill_Install_FailsWhenBackupFails asserts that Install returns an error
// (rather than silently continuing) when the .bak write cannot succeed. This
// locks in the behavior change from CF-461: previously a backup-write failure
// was logged at debug and the install proceeded, which is data-loss-adjacent.
func TestSkill_Install_FailsWhenBackupFails(t *testing.T) {
	setupSkillTest(t)

	// Install once so the skill file exists.
	if err := dummyTestSkill.Install(); err != nil {
		t.Fatalf("Install() failed: %v", err)
	}

	path, _ := dummyTestSkill.path()
	// Modify the installed file so the next Install() will try to back it up.
	customContent := "user customized content"
	if err := os.WriteFile(path, []byte(customContent), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Make the .bak path a pre-existing *directory*; os.WriteFile cannot
	// overwrite a directory, so the backup-write will fail.
	bakPath := path + ".bak"
	if err := os.Mkdir(bakPath, 0755); err != nil {
		t.Fatalf("Mkdir(bakPath) failed: %v", err)
	}

	if err := dummyTestSkill.Install(); err == nil {
		t.Fatal("Install() returned nil, want error when backup write fails")
	}

	// And the user's customized file must NOT have been overwritten — that's
	// the entire reason we abort rather than continue.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(path) failed: %v", err)
	}
	if string(got) != customContent {
		t.Errorf("Install overwrote the customized file even though backup failed: got %q, want %q",
			string(got), customContent)
	}
}
