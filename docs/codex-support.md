---
status: living-plan
linear: CF-342
scope: Add Codex support without disrupting Claude Code users
intent: Track checkpoints, invariants, risks, and decisions for the multi-phase Codex support work.
last_reviewed: 2026-05-14
---

# Codex Support Plan

This document tracks the incremental path to Codex support. It is intentionally broader than any single PR, but each checkpoint must remain small enough to verify without changing existing Claude Code behavior.

## Core Invariant

Phase 2 must not change any installed Claude Code hook command string, settings file location, environment variable, backend request body, daemon state filename, inbox JSON shape, or user-facing default behavior.

In particular, existing Claude Code users must continue to use commands such as:

- `confab hook session-start`
- `confab hook session-end`
- `confab hook pre-tool-use`
- `confab hook post-tool-use`
- `confab hook user-prompt-submit`

## Current Phase: Codex Root Session Rollout

Goal: ship root Codex session upload and transcript viewing while preserving Claude Code behavior. Backend provider support and frontend Codex transcript rendering are already merged in `../confab-web`; the CLI implementation has shipped via the CF-348 PR and the preceding direct-pushed Codex rollout commits.

Completed checkpoints:

- [x] Add `pkg/provider` with concrete `ClaudeCode`.
- [x] Move Claude path/settings/session-root knowledge behind `ClaudeCode` methods.
- [x] Move Claude hook input parsing behind concrete `ClaudeCode` methods.
- [x] Move Claude parent process matching/detection behind concrete `ClaudeCode` methods.
- [x] Rename hook request/response Go types to Claude-specific names while preserving JSON wire shape.
- [x] Keep existing exported Claude-compatible wrappers where callers rely on them.
- [x] Add fixture tests proving installed hook JSON remains unchanged.
- [x] Add response tests proving Claude hook JSON output remains unchanged.
- [x] Keep `CONFAB_CLAUDE_DIR` as the Claude state-dir override.
- [x] Backend provider support: additive `provider` request field, backend default for legacy clients, dedup by `(user_id, provider, external_id)`.
- [x] CLI provider selection: introduce `--provider claude-code|codex` on commands with real provider-specific behavior.
- [x] Codex provider: implement real Codex paths, rollout discovery, hook payload parsing, and hook config writing from current Codex docs/source.
- [x] Codex daemon behavior: run the real daemon lifecycle against Codex rollout files, initially with a local dry-run backend.
- [x] Codex root backend upload: send top-level `provider="codex"` and upload root rollout JSONL as `file_type="transcript"`.
- [x] Codex frontend transcript view v1 in `../confab-web`: route `provider="codex"` sessions through the Codex parser and renderer.
- [x] Populate `first_user_message` metadata on Codex chunk uploads so freshly-uploaded Codex sessions appear in the web session list (CF-348).
- [x] Ship the local CLI Codex commits to `origin/main` (CF-348 PR for `first_user_message`, direct push for the preceding Codex provider/daemon/backend-sync/dry-run/doc commits).

Current rollout TODOs:

- [ ] Run an end-to-end manual QA cycle against a real `confab-web` backend with `confab setup --provider codex`, Codex hooks, daemon sync, and web transcript viewing.
- [ ] Update public/user-facing docs once Codex support is ready to advertise.
- [x] Clean up compatibility shims after provider ownership is stable: `pkg/discovery` has been removed; `pkg/config/paths.go` keeps only the real `ClaudeStateDirEnv` constant + `GetClaudeStateDir`.

## Later Checkpoints

- [ ] Transcript normalization: add backend and frontend normalization keyed by provider before enabling analytics/Smart Recap for Codex.
- [ ] Codex subagents: quick-follow TODO after root Codex backend upload. Model separate rollout files and parent relationships from Codex SQLite relationship state plus rollout `session_meta`.
- [x] Skills: bundled `/til` and `/retro` install into both Claude Code and Codex provider skill dirs. Claude `/til` uses `CLAUDE_SESSION_ID`; Codex `/til` uses `CODEX_THREAD_ID`.
- [ ] Post-rollout backend cleanup in `../confab-web`: backfill legacy `sessions.session_type='Claude Code'` to `claude-code`, then remove temporary dual-value lookup/normalization code.

## Decisions

- Provider work started as concrete Claude extraction, not a premature multi-provider abstraction.
- Hook payload formats are provider-specific. Do not introduce a generic normalized hook input until Codex requirements are confirmed.
- `ClaudeSettings` remains Claude-specific because it wraps `~/.claude/settings.json`.
- Parent PID monitoring remains Claude-specific implementation detail for now.
- `/til` and `/retro` remain Claude-specific for this phase.
- Documentation visible to users should remain Claude-specific until root Codex sync has passed manual QA and the CLI work is pushed.
- Codex support starts CLI-first but includes the full local lifecycle: discovery, `list`, `save`, daemon sync, and hook installation.
- Codex root session backend upload is enabled after backend provider support in CF-347. Codex sync init sends top-level `provider="codex"`.
- Codex session identity is parsed from rollout filenames matching `rollout-<timestamp>-<uuid>.jsonl`.
- Codex rollout `session_meta` is parsed for metadata and top-level filtering. `confab list --provider codex` includes user sessions only: missing/`user` `thread_source`, and no `agent_path`, `agent_role`, or `agent_nickname`.
- Codex local discovery reads rollout JSONL files only. Do not read Codex SQLite state in the first Codex CLI slice.
- Codex backend init should send top-level `provider`. Missing provider on backend requests must default to `claude-code` for old clients.
- Backend session uniqueness should be `(user_id, provider, external_id)`. Session files inherit provider from their parent session.
- Codex root rollout files should continue using `file_type="transcript"` for first backend integration.
- Codex hook install should match Claude's seamless setup posture: preserve existing user config, make backups, install idempotently, enable `features.hooks = true`, remove deprecated `features.codex_hooks`, and clearly surface that feature flag change in CLI output.
- Codex hooks should use existing handler shapes with explicit provider selection, e.g. `confab hook session-start --provider codex`.
- Provider selection flags should be added only where they have real behavior.
- Daemon state should be provider-aware going forward, while preserving legacy Claude state file lookup and cleanup for existing users.

## Codex Subagent Notes

Subagent upload is postponed until after root Codex backend upload works.

Codex subagents differ from Claude Code sidechains. Claude Code stores subagents as files under the parent session directory, so Confab can upload them as `file_type="agent"` on the same backend session. Codex subagents are separate rollout-backed threads with their own session IDs. They should eventually be uploaded as separate backend sessions linked to their parent, not forced into Claude's agent-file shape.

For Codex subagents, SQLite should be treated as the relationship index and rollout JSONL as the transcript source of truth:

- Use Codex SQLite state for parent-child traversal, for example `thread_spawn_edges` when available.
- Use rollout files for uploaded content and provider-owned metadata parsing.
- Resolve parent -> child IDs through SQLite, then resolve child IDs to rollout files, then parse each child rollout before upload.
- Do not infer parent-child relationships from parent conversation text or `spawn_agent` tool output.
- Do not upload guessed relationships. If the SQLite relationship or child rollout cannot be verified, skip the relationship and log locally.

Likely backend shape for subagents:

- Root and child Codex rollouts both create sessions with `provider="codex"`.
- Child sessions carry optional relationship metadata such as `parent_external_id`, `thread_source`, `agent_path`, `agent_role`, `agent_nickname`, and depth if available.
- Backend resolves parent links within the same provider namespace.

## Compatibility Shims (Cleaned Up)

Earlier checkpoints kept a `pkg/discovery` package and several `pkg/config/paths.go` forwarders to `provider.ClaudeCode{}` so the provider-extraction diffs stayed focused. Those shims are gone:

- `pkg/discovery` was removed entirely; hook parsing and session scanning now live on the provider types (`pkg/provider/claude*.go`, `pkg/provider/codex*.go`).
- `pkg/config/paths.go` keeps only the real `ClaudeStateDirEnv` constant (mirrored from `pkg/provider/claude.go`, must stay in sync) and `GetClaudeStateDir` for legacy Claude helper wrappers. Provider clients own skill layout and pass their state directories into `pkg/config`'s bundled-skill installer. The previously-forwarded `GetProjectsDir` / `GetClaudeSettingsPath` are gone — call `provider.ClaudeCode{}` directly.

## Risks

- Mechanical hook type renames can hide JSON wire changes. Protect with exact response and hook settings tests.
- Provider constructor injection can sprawl. Limit command constructor changes to touched hook/status flows.
- Daemon state and inbox files are operationally sensitive. Do not change their filenames or JSON shape in this phase.
- Codex assumptions can drift quickly. Re-check Codex hook config, transcript layout, and subagent metadata before expanding beyond root rollout upload.
