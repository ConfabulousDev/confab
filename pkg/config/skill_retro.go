// ABOUTME: Defines the /retro Claude Code skill — template + thin public wrappers around the generic skill installer.
// ABOUTME: The skill file lives at ~/.claude/skills/retro/SKILL.md and enables the /retro slash command.
package config

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

var retroSkill = skill{name: "retro", template: retroSkillTemplate}

// InstallRetroSkill writes the /retro skill file. See skill.Install for backup semantics.
func InstallRetroSkill() error { return retroSkill.Install() }

// UninstallRetroSkill removes the /retro skill directory.
func UninstallRetroSkill() error { return retroSkill.Uninstall() }

// IsRetroSkillInstalled returns true if the /retro skill file exists.
func IsRetroSkillInstalled() bool { return retroSkill.Installed() }
