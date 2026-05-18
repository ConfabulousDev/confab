// ABOUTME: Manages the /til bundled skill — install and uninstall.
// ABOUTME: The skill file lives at <provider>/skills/til/SKILL.md and enables the /til slash command.
package config

import "os"

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

// codexTilSkillTemplate is the SKILL.md content installed for the Codex /til slash command.
const codexTilSkillTemplate = `---
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
confab til --provider codex --session "${CODEX_THREAD_ID}" --title "<the title>" --summary "<your summary>"
` + "```" + `

4. Briefly confirm to the user that the TIL was saved
`

const tilSkillName = "til"

// getTilSkillPath returns the absolute path to the /til skill file.
func getTilSkillPath() (string, error) {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return "", err
	}
	return SkillPath(claudeDir, tilSkillName), nil
}

// InstallTilSkill writes the /til skill file to ~/.claude/skills/til/SKILL.md.
// If an existing file differs from the template, it is backed up as SKILL.md.bak.
func InstallTilSkill() error {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return err
	}
	return InstallBundledSkill(claudeDir, SkillProviderClaude, tilSkillName)
}

// UninstallTilSkill removes the /til skill directory (~/.claude/skills/til/).
func UninstallTilSkill() error {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return err
	}
	return UninstallBundledSkill(claudeDir, tilSkillName)
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
