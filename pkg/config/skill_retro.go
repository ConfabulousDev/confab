// ABOUTME: Manages the /retro bundled skill — install and uninstall.
// ABOUTME: The skill file lives at <provider>/skills/retro/SKILL.md and enables the /retro slash command.
package config

import "os"

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

// codexRetroSkillTemplate is the SKILL.md content installed for the Codex /retro slash command.
const codexRetroSkillTemplate = `---
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
   raw Codex rollout that may contain richer data (full tool outputs,
   reasoning events, and subagent metadata):

` + "```" + `
Glob: ~/.codex/sessions/**/rollout-*<external_id>.jsonl
` + "```" + `

   If found, keep the path for later — you can Read specific sections for
   deeper analysis. If not found, proceed with the condensed transcript only.

3. Present a conversational summary of the session — what it was about, what
   happened, key outcomes — weaving in metadata (duration, cost, model) naturally.

4. If the user provided a question or focus area, answer it. Otherwise, engage
   in open-ended discussion about the session.

For deeper dives into specific moments, Read transcript.xml or the local raw
rollout if available. The condensed transcript is good for overview; the
raw JSONL has the full detail.
`

const retroSkillName = "retro"

// getRetroSkillPath returns the absolute path to the /retro skill file.
func getRetroSkillPath() (string, error) {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return "", err
	}
	return SkillPath(claudeDir, retroSkillName), nil
}

// InstallRetroSkill writes the /retro skill file to ~/.claude/skills/retro/SKILL.md.
// If an existing file differs from the template, it is backed up as SKILL.md.bak.
func InstallRetroSkill() error {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return err
	}
	return InstallBundledSkill(claudeDir, SkillProviderClaude, retroSkillName)
}

// UninstallRetroSkill removes the /retro skill directory (~/.claude/skills/retro/).
func UninstallRetroSkill() error {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return err
	}
	return UninstallBundledSkill(claudeDir, retroSkillName)
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
