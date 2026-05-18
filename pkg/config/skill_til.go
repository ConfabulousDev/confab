// ABOUTME: Defines the /til Claude Code skill — template + thin public wrappers around the generic skill installer.
// ABOUTME: The skill file lives at ~/.claude/skills/til/SKILL.md and enables the /til slash command.
package config

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

var tilSkill = skill{name: "til", template: tilSkillTemplate}

// InstallTilSkill writes the /til skill file. See skill.Install for backup semantics.
func InstallTilSkill() error { return tilSkill.Install() }

// UninstallTilSkill removes the /til skill directory.
func UninstallTilSkill() error { return tilSkill.Uninstall() }

// IsTilSkillInstalled returns true if the /til skill file exists.
func IsTilSkillInstalled() bool { return tilSkill.Installed() }
