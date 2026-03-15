// ABOUTME: Manages the /til Claude Code skill — install, uninstall, and ensure up-to-date.
// ABOUTME: The skill file lives at ~/.claude/skills/til/SKILL.md and enables the /til slash command.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// tilSkillTemplate is the SKILL.md content installed for the /til slash command.
const tilSkillTemplate = `---
name: til
description: Capture a TIL (Today I Learned) from this session
disable-model-invocation: true
argument-hint: <what you learned>
allowed-tools: Bash(confab til *)
---

The user wants to capture a TIL — "today I learned" — a note about something
they just figured out or realized during this session. Based on the conversation
context and what the user wrote:

1. Use "$ARGUMENTS" as the TIL title
2. Write a brief summary (2-3 sentences) that captures what was learned and why
   it matters, drawing on the conversation history for context
3. Save it:

` + "```bash" + `
confab til --session "${CLAUDE_SESSION_ID}" --title "<the title>" --summary "<your summary>"
` + "```" + `

4. Briefly confirm to the user that the TIL was saved
`

// tilSkillRelPath is the path to the skill file relative to the Claude state directory.
const tilSkillRelPath = "skills/til/SKILL.md"

// getTilSkillPath returns the absolute path to the /til skill file.
func getTilSkillPath() (string, error) {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(claudeDir, tilSkillRelPath), nil
}

// InstallTilSkill writes the /til skill file to ~/.claude/skills/til/SKILL.md.
// If an existing file differs from the template, it is backed up as SKILL.md.bak.
func InstallTilSkill() error {
	path, err := getTilSkillPath()
	if err != nil {
		return err
	}

	// Back up existing file if it differs from template
	existing, readErr := os.ReadFile(path)
	if readErr == nil && string(existing) != tilSkillTemplate {
		bakPath := path + ".bak"
		if writeErr := os.WriteFile(bakPath, existing, 0644); writeErr != nil {
			logger.Debug("Failed to back up existing skill file: %v", writeErr)
		}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(tilSkillTemplate), 0644)
}

// UninstallTilSkill removes the /til skill directory (~/.claude/skills/til/).
func UninstallTilSkill() error {
	path, err := getTilSkillPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsTilSkillInstalled returns true if the /til skill file exists.
func IsTilSkillInstalled() bool {
	path, err := getTilSkillPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// EnsureTilSkill installs the /til skill if missing, or updates it if outdated.
// Returns true if the skill was newly installed (not present before).
func EnsureTilSkill() (bool, error) {
	path, err := getTilSkillPath()
	if err != nil {
		return false, err
	}

	existing, readErr := os.ReadFile(path)
	if readErr != nil {
		// File doesn't exist — fresh install
		if err := InstallTilSkill(); err != nil {
			return false, err
		}
		return true, nil
	}

	// File exists — update if content differs
	if strings.TrimSpace(string(existing)) != strings.TrimSpace(tilSkillTemplate) {
		if err := InstallTilSkill(); err != nil {
			return false, err
		}
		// Updated, not newly installed
		return false, nil
	}

	// Already up to date
	return false, nil
}
