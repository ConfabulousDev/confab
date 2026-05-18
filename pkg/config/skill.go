// ABOUTME: Generic Claude Code skill installer — shared logic for /til, /retro, and future skills.
// ABOUTME: Each skill is a SKILL.md file in ~/.claude/skills/<name>/.
package config

import (
	"os"
	"path/filepath"
)

// skill represents a Claude Code skill installed as a SKILL.md file under
// ~/.claude/skills/<name>/. Adding a new skill is a matter of declaring a
// template constant and a `var fooSkill = skill{name: "foo", template: ...}`,
// then wiring three thin wrapper funcs into the public API.
type skill struct {
	name     string
	template string
}

// path returns the absolute path to the skill's SKILL.md file.
func (s skill) path() (string, error) {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(claudeDir, "skills", s.name, "SKILL.md"), nil
}

// Install writes the skill's SKILL.md to ~/.claude/skills/<name>/SKILL.md.
// If an existing file differs from the template, it is backed up as
// SKILL.md.bak before being overwritten. A failure to write the backup
// aborts the install and returns the error — we never overwrite a
// user-customized file we couldn't back up.
func (s skill) Install() error {
	path, err := s.path()
	if err != nil {
		return err
	}

	if existing, readErr := os.ReadFile(path); readErr == nil && string(existing) != s.template {
		if err := os.WriteFile(path+".bak", existing, 0644); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(s.template), 0644)
}

// Uninstall removes the skill directory (~/.claude/skills/<name>/).
// A missing directory is not an error (os.RemoveAll returns nil in that case).
func (s skill) Uninstall() error {
	path, err := s.path()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(path))
}

// Installed returns true if the skill's SKILL.md file exists.
func (s skill) Installed() bool {
	path, err := s.path()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}
