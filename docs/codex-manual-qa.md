---
status: living-checklist
scope: Manual QA cycle for Codex-support changes
intent: Provide a repeatable sanity check that Claude Code behavior remains unchanged while Confab moves toward Codex support.
last_reviewed: 2026-05-12
---

# Codex Support Manual QA

Run this checklist after every significant Codex-support change. The main goal is to catch regressions in the existing Claude Code experience before adding or enabling Codex behavior.

## Preflight

- [ ] Build the CLI from the current branch:
  ```sh
  go build ./...
  ```
- [ ] Run automated checks:
  ```sh
  go test ./...
  go vet ./...
  git diff --check
  ```
- [ ] Confirm no unexpected config files changed:
  ```sh
  git status --short
  ```

## Hook Installation

- [ ] Back up current Claude settings:
  ```sh
  cp ~/.claude/settings.json ~/.claude/settings.json.confab-qa-backup
  ```
- [ ] Run hook installation:
  ```sh
  ./confab hooks add
  ```
- [ ] Verify `~/.claude/settings.json` still uses the existing command names:
  - `confab hook session-start`
  - `confab hook session-end`
  - `confab hook pre-tool-use`
  - `confab hook post-tool-use`
  - `confab hook user-prompt-submit`
- [ ] Verify `PreToolUse` and `PostToolUse` still include both matchers:
  - `Bash`
  - `mcp__github__create_pull_request`
- [ ] Diff the settings file against the backup:
  ```sh
  diff -u ~/.claude/settings.json.confab-qa-backup ~/.claude/settings.json
  ```
  Confirm the only differences are the expected Confab hook additions or updates from this QA run. Existing non-Confab settings and unrelated hooks should be byte-for-byte unchanged.
- [ ] Run:
  ```sh
  ./confab status
  ```
  Confirm hooks are shown as installed.

## Basic Claude Session Sync

- [ ] Start a new Claude Code session in a test repo.
- [ ] Send one simple prompt.
- [ ] Confirm the SessionStart hook prints the Confab daemon banner and does not block Claude.
- [ ] Confirm a sync daemon state file appears under `~/.confab/sync/`.
- [ ] Wait for one sync interval or trigger enough activity for sync.
- [ ] Confirm the session appears in Confab.
- [ ] End the Claude session.
- [ ] Confirm the daemon exits and removes its state file.

## Resume / Teleport Path

- [ ] Resume an existing Claude session.
- [ ] Send a prompt.
- [ ] Confirm `UserPromptSubmit` can start or reuse the daemon without errors.
- [ ] Confirm there is still only one daemon for the session.
- [ ] Confirm the resumed session continues syncing.

## Tool Hooks

- [ ] In a test git repo, make a commit through Claude Code.
- [ ] Confirm the `PreToolUse` hook asks Claude to include the Confab link when needed.
- [ ] Confirm the commit trailer format is unchanged:
  ```text
  Confab-Link: <session-url>
  ```
- [ ] Create a test PR through `gh pr create` or a dry-run equivalent.
- [ ] Confirm PR linking behavior is unchanged.
- [ ] If GitHub MCP is available, create or simulate a GitHub MCP PR creation call and confirm the matcher still fires.

## Skills

- [ ] Confirm bundled skills install for detected providers:
  ```sh
  ./confab skills add
  ```
- [ ] Confirm Claude has `~/.claude/skills/til/SKILL.md` and `~/.claude/skills/retro/SKILL.md`.
- [ ] Confirm Codex has `~/.codex/skills/til/SKILL.md` and `~/.codex/skills/retro/SKILL.md` when Codex is detected or set up.
- [ ] In Claude Code, run a small `/til` flow and confirm the TIL posts to the backend.
- [ ] In Codex, run a small `/til` flow and confirm the TIL posts to the backend for the root thread.
- [ ] Run a small `/retro <session-id>` flow and confirm output is unchanged.

## Path Override Smoke Test

- [ ] Create a temporary Claude directory:
  ```sh
  tmpdir="$(mktemp -d)"
  export CONFAB_CLAUDE_DIR="$tmpdir"
  ```
- [ ] Run:
  ```sh
  ./confab hooks add
  ./confab list
  ```
- [ ] Confirm Confab reads and writes under the temp directory.
- [ ] Unset the override:
  ```sh
  unset CONFAB_CLAUDE_DIR
  ```

## Negative Checks

- [ ] Confirm a transcript path outside the Claude root is rejected by hook parsing.
- [ ] Confirm a transcript path through a symlink that resolves outside the Claude root is rejected.
- [ ] Confirm a fresh transcript path whose parent does not exist yet is still accepted when it is lexically under the Claude root.

## Cleanup

- [ ] Restore Claude settings if needed:
  ```sh
  mv ~/.claude/settings.json.confab-qa-backup ~/.claude/settings.json
  ```
- [ ] Remove any test daemon state files under `~/.confab/sync/`.
- [ ] Remove temporary Claude directories.
- [ ] Close or delete test PRs and test commits if they were pushed.

## Pass Criteria

- Claude Code sessions start, resume, sync, and end without user-visible behavior changes.
- Installed hook command strings are unchanged.
- `CONFAB_CLAUDE_DIR` still works.
- Daemon lifecycle is unchanged.
- Git commit and PR linking still work.
- `/til` and `/retro` still work.
- No Codex-specific behavior is exposed unless the current checkpoint explicitly intends it.
