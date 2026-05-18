package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

func setupSkillsCommandEnv(t *testing.T) (tmpDir, claudeDir, codexDir string) {
	t.Helper()
	tmpDir = t.TempDir()
	claudeDir = filepath.Join(tmpDir, ".claude")
	codexDir = filepath.Join(tmpDir, ".codex")
	t.Setenv("HOME", tmpDir)
	t.Setenv(provider.ClaudeStateDirEnv, claudeDir)
	t.Setenv(provider.CodexStateDirEnv, codexDir)
	origProvider := skillsProviderName
	skillsProviderName = ""
	t.Cleanup(func() { skillsProviderName = origProvider })
	return tmpDir, claudeDir, codexDir
}

func TestSkillsAddInstallsForAllDetectedProviders(t *testing.T) {
	_, claudeDir, codexDir := setupSkillsCommandEnv(t)
	stubProviderDetect(t, "claude", "codex")

	if err := skillsAddCmd.RunE(skillsAddCmd, nil); err != nil {
		t.Fatalf("skills add: %v", err)
	}

	for _, base := range []string{claudeDir, codexDir} {
		for _, skill := range []string{"til", "retro"} {
			path := filepath.Join(base, "skills", skill, "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("expected %s after skills add: %v", path, err)
			}
		}
	}
}

func TestSkillsRemoveRemovesFromAllProviderDirs(t *testing.T) {
	_, claudeDir, codexDir := setupSkillsCommandEnv(t)
	stubProviderDetect(t, "claude")

	for _, base := range []string{claudeDir, codexDir} {
		for _, skill := range []string{"til", "retro"} {
			path := filepath.Join(base, "skills", skill, "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", path, err)
			}
			if err := os.WriteFile(path, []byte("custom"), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
		}
	}

	if err := skillsRemoveCmd.RunE(skillsRemoveCmd, nil); err != nil {
		t.Fatalf("skills remove: %v", err)
	}

	for _, base := range []string{claudeDir, codexDir} {
		for _, skill := range []string{"til", "retro"} {
			path := filepath.Join(base, "skills", skill)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("expected %s to be removed, stat err=%v", path, err)
			}
		}
	}
}
