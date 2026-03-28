// ABOUTME: Manages the /retro Claude Code skill — install, uninstall, and ensure up-to-date.
// ABOUTME: The skill file lives at ~/.claude/skills/retro/SKILL.md and enables the /retro slash command.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// retroSkillTemplate is the SKILL.md content installed for the /retro slash command.
const retroSkillTemplate = `---
name: retro
description: Review and discuss a session transcript
disable-model-invocation: true
argument-hint: <session-id> [optional question or focus]
allowed-tools: Bash(confab retro *), Read, Glob
---

The user wants to retrospect on a session — review what happened, extract
learnings, identify patterns, or critique the approach.

Parse "$ARGUMENTS": the first whitespace-delimited token is the session ID,
everything after it is the user's question or focus area (may be empty).

1. Fetch the condensed transcript and write output files. Pick a stable
   output directory with a timestamp so repeated retros don't overwrite
   each other, and reuse it for retries:

` + "```bash" + `
RETRO_DIR="/tmp/retro-$(date +%s)"
confab retro --output-dir "$RETRO_DIR" <session-id>
` + "```" + `

   This writes two files (response.json and transcript.xml) to the output
   directory. Note the file paths printed to stderr — use those for later
   Read calls.

2. From the JSON metadata, note the "external_id" field. Search for a local
   raw transcript that may contain richer data (full tool outputs, thinking
   blocks):

` + "```" + `
Glob: ~/.claude/projects/**/<external_id>.jsonl
` + "```" + `

   If found, keep the path for later — you can Read specific sections for
   deeper analysis. If not found, proceed with the condensed transcript only.

3. Present a conversational summary of the session — what it was about, what
   happened, key outcomes — weaving in metadata (duration, cost, model) naturally.

4. If the user provided a question or focus area, answer it. Otherwise, engage
   in open-ended discussion about the session.

For deeper dives into specific moments, Read transcript.xml or the local raw
transcript if available. The condensed transcript is good for overview; the
raw JSONL has the full detail.
`

// retroSkillRelPath is the path to the skill file relative to the Claude state directory.
const retroSkillRelPath = "skills/retro/SKILL.md"

// getRetroSkillPath returns the absolute path to the /retro skill file.
func getRetroSkillPath() (string, error) {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(claudeDir, retroSkillRelPath), nil
}

// InstallRetroSkill writes the /retro skill file to ~/.claude/skills/retro/SKILL.md.
// If an existing file differs from the template, it is backed up as SKILL.md.bak.
func InstallRetroSkill() error {
	path, err := getRetroSkillPath()
	if err != nil {
		return err
	}

	// Back up existing file if it differs from template
	existing, readErr := os.ReadFile(path)
	if readErr == nil && string(existing) != retroSkillTemplate {
		bakPath := path + ".bak"
		if writeErr := os.WriteFile(bakPath, existing, 0644); writeErr != nil {
			logger.Debug("Failed to back up existing skill file: %v", writeErr)
		}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(retroSkillTemplate), 0644)
}

// UninstallRetroSkill removes the /retro skill directory (~/.claude/skills/retro/).
func UninstallRetroSkill() error {
	path, err := getRetroSkillPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsRetroSkillInstalled returns true if the /retro skill file exists.
func IsRetroSkillInstalled() bool {
	path, err := getRetroSkillPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// EnsureRetroSkill installs the /retro skill if missing, or updates it if outdated.
// Returns true if the skill was newly installed (not present before).
func EnsureRetroSkill() (bool, error) {
	path, err := getRetroSkillPath()
	if err != nil {
		return false, err
	}

	existing, readErr := os.ReadFile(path)
	if readErr != nil {
		// File doesn't exist — fresh install
		if err := InstallRetroSkill(); err != nil {
			return false, err
		}
		return true, nil
	}

	// File exists — update if content differs
	if strings.TrimSpace(string(existing)) != strings.TrimSpace(retroSkillTemplate) {
		if err := InstallRetroSkill(); err != nil {
			return false, err
		}
		// Updated, not newly installed
		return false, nil
	}

	// Already up to date
	return false, nil
}
