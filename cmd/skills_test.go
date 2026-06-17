package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

func setupSkillsCommandEnv(t *testing.T) (tmpDir, claudeDir, codexDir, cursorDir string) {
	t.Helper()
	tmpDir = t.TempDir()
	claudeDir = filepath.Join(tmpDir, ".claude")
	codexDir = filepath.Join(tmpDir, ".codex")
	cursorDir = filepath.Join(tmpDir, ".cursor")
	t.Setenv("HOME", tmpDir)
	t.Setenv(provider.ClaudeStateDirEnv, claudeDir)
	t.Setenv(provider.CodexStateDirEnv, codexDir)
	t.Setenv(provider.CursorStateDirEnv, cursorDir)
	origProvider := skillsProviderName
	skillsProviderName = ""
	t.Cleanup(func() { skillsProviderName = origProvider })
	return tmpDir, claudeDir, codexDir, cursorDir
}

// TestSkillsRemoveTargetsIncludesOpencode guards the m9mb bug fix:
// skillsRemoveTargets previously omitted opencode even though opencode
// installs the /retro skill, so `skills remove` left opencode skills behind.
func TestSkillsRemoveTargetsIncludesOpencode(t *testing.T) {
	origProvider := skillsProviderName
	skillsProviderName = ""
	t.Cleanup(func() { skillsProviderName = origProvider })

	targets, err := skillsRemoveTargets()
	if err != nil {
		t.Fatalf("skillsRemoveTargets: %v", err)
	}
	got := make(map[string]bool)
	for _, p := range targets {
		got[p.Name()] = true
	}
	for _, want := range []string{
		provider.NameClaudeCode,
		provider.NameCodex,
		provider.NameCursor,
		provider.NameOpencode,
	} {
		if !got[want] {
			t.Errorf("skillsRemoveTargets missing %q; got %v", want, got)
		}
	}
}

func TestSkillsAddInstallsForAllDetectedProviders(t *testing.T) {
	_, claudeDir, codexDir, _ := setupSkillsCommandEnv(t)
	stubProviderDetect(t, "claude", "codex")

	if err := skillsAddCmd.RunE(skillsAddCmd, nil); err != nil {
		t.Fatalf("skills add: %v", err)
	}

	for _, base := range []string{claudeDir, codexDir} {
		for _, skill := range []string{"retro"} {
			path := filepath.Join(base, "skills", skill, "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("expected %s after skills add: %v", path, err)
			}
		}
	}
}

func TestSkillsRemoveRemovesFromAllProviderDirs(t *testing.T) {
	_, claudeDir, codexDir, cursorDir := setupSkillsCommandEnv(t)
	stubProviderDetect(t, "claude")

	for _, base := range []string{claudeDir, codexDir, cursorDir} {
		for _, skill := range []string{"retro"} {
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

	for _, base := range []string{claudeDir, codexDir, cursorDir} {
		for _, skill := range []string{"retro"} {
			path := filepath.Join(base, "skills", skill)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("expected %s to be removed, stat err=%v", path, err)
			}
		}
	}
}
