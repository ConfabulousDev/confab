package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	SkillProviderClaude   = "claude-code"
	SkillProviderCodex    = "codex"
	SkillProviderOpencode = "opencode"
)

var bundledSkillNames = []string{retroSkillName}

// retiredSkillNames are skills confab shipped previously but no longer bundles.
// They are removed during ReconcileBundledSkills so a stale SKILL.md left by an
// older confab version stops appearing after upgrade.
//
// TODO(2026-12-01): Remove retiredSkillNames and the prune loop in
// ReconcileBundledSkills. By then users will have upgraded past the TIL-bundling
// version, so the one-time cleanup is no longer needed — and keeping it risks
// deleting a user's own custom skill that happens to be named "til".
var retiredSkillNames = []string{"til"}

// BundledSkillNames returns the shipped skill names in install order.
func BundledSkillNames() []string {
	return append([]string(nil), bundledSkillNames...)
}

// SkillPath returns the provider-local SKILL.md path for name.
func SkillPath(stateDir, name string) string {
	return filepath.Join(stateDir, "skills", name, "SKILL.md")
}

func bundledSkillTemplate(providerName, name string) (string, error) {
	switch name {
	case retroSkillName:
		if providerName == SkillProviderCodex {
			return codexRetroSkillTemplate, nil
		}
		return retroSkillTemplate, nil
	default:
		return "", fmt.Errorf("unknown bundled skill %q", name)
	}
}

// ReconcileBundledSkills installs every shipped skill into stateDir and removes
// any retired skills left over from previous confab versions. It is idempotent:
// pruning a retired skill that is already absent is a no-op.
func ReconcileBundledSkills(stateDir, providerName string) error {
	for _, name := range bundledSkillNames {
		if err := InstallBundledSkill(stateDir, providerName, name); err != nil {
			return err
		}
	}
	for _, name := range retiredSkillNames {
		if err := UninstallBundledSkill(stateDir, name); err != nil {
			return err
		}
	}
	return nil
}

// InstallBundledSkill writes one shipped skill to stateDir, backing up a
// customized existing SKILL.md beside the file before overwriting it. If the
// backup write fails, the install aborts rather than overwriting user content.
func InstallBundledSkill(stateDir, providerName, name string) error {
	content, err := bundledSkillTemplate(providerName, name)
	if err != nil {
		return err
	}
	path := SkillPath(stateDir, name)

	existing, readErr := os.ReadFile(path)
	if readErr == nil && string(existing) != content {
		if writeErr := os.WriteFile(path+".bak", existing, 0o644); writeErr != nil {
			return writeErr
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// UninstallBundledSkills removes every shipped skill directory from stateDir.
func UninstallBundledSkills(stateDir string) error {
	for _, name := range bundledSkillNames {
		if err := UninstallBundledSkill(stateDir, name); err != nil {
			return err
		}
	}
	return nil
}

func UninstallBundledSkill(stateDir, name string) error {
	dir := filepath.Dir(SkillPath(stateDir, name))
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func IsBundledSkillInstalled(stateDir, name string) bool {
	_, err := os.Stat(SkillPath(stateDir, name))
	return err == nil
}
