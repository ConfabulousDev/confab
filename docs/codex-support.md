---
status: living-plan
linear: CF-342
scope: Add Codex support without disrupting Claude Code users
intent: Track checkpoints, invariants, risks, and decisions for the multi-phase Codex support work.
last_reviewed: 2026-05-12
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

## Current Phase: Claude Provider Extraction

Goal: extract Claude-specific local behavior into a concrete provider package without implementing Codex.

Non-goals for this phase:

- No Codex provider stub.
- No `--tool` CLI flag.
- No backend `tool_name` payload.
- No daemon state or inbox schema change.
- No transcript normalization.
- No Codex hook config writer.
- No skill abstraction for `/til` or `/retro`.
- No generic normalized hook input model.

Checklist:

- [ ] Add `pkg/provider` with concrete `ClaudeCode`.
- [ ] Move Claude path/settings/session-root knowledge behind `ClaudeCode` methods.
- [ ] Move Claude hook input parsing behind concrete `ClaudeCode` methods.
- [ ] Move Claude parent process matching/detection behind concrete `ClaudeCode` methods.
- [ ] Rename hook request/response Go types to Claude-specific names while preserving JSON wire shape.
- [ ] Keep existing exported Claude-compatible wrappers where callers rely on them.
- [ ] Add fixture tests proving installed hook JSON remains unchanged.
- [ ] Add response tests proving Claude hook JSON output remains unchanged.
- [ ] Keep `CONFAB_CLAUDE_DIR` as the only state-dir override.
- [ ] Keep temporary compatibility shims (`pkg/config/paths.go`, `pkg/discovery/hook.go`) until provider call sites settle.

## Later Checkpoints

- [ ] Backend provider support: additive request field, backend default for legacy clients, dedup by `(user_id, provider, external_id)`.
- [ ] Cleanup compatibility shims after provider ownership is stable: move remaining path and hook parsing callers directly to provider APIs, then remove wrappers that no runtime code needs.
- [ ] CLI provider selection: introduce `--provider claude-code|codex` surgically on commands with real provider-specific behavior.
- [ ] Codex provider: implement real Codex paths, rollout discovery, hook payload parsing, and hook config writing from current Codex docs/source.
- [x] Codex daemon behavior: run the real daemon lifecycle against Codex rollout files, initially with a local dry-run backend.
- [ ] Transcript normalization: add backend and frontend normalization keyed by tool name before enabling analytics/Smart Recap for Codex.
- [ ] Codex subagents: quick-follow TODO after root Codex backend upload. Model separate rollout files and parent relationships from Codex SQLite relationship state plus rollout `session_meta`.
- [ ] Skills: revisit `/til` and `/retro` separately; Claude slash-command skills should remain Claude-specific until Codex has a well-defined surface.

## Decisions

- Provider work starts as concrete Claude extraction, not a premature multi-provider abstraction.
- Hook payload formats are provider-specific. Do not introduce a generic normalized hook input until Codex requirements are confirmed.
- `ClaudeSettings` remains Claude-specific because it wraps `~/.claude/settings.json`.
- Parent PID monitoring remains Claude-specific implementation detail for now.
- `/til` and `/retro` remain Claude-specific for this phase.
- Documentation visible to users should remain Claude-specific until Codex support is real.
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

## Compatibility Shims (Future Cleanup)

These exist only to keep this checkpoint's diff focused. They should be removed in a later checkpoint, once provider usage settles and Claude behavior has not regressed:

- `pkg/discovery/hook.go` — `ReadHookInputFrom` now forwards to `provider.ClaudeCode{}.ReadSessionHookInput`. Runtime callers have all moved to the provider directly; only `pkg/discovery/hook_test.go` still exercises this wrapper. Remove after one checkpoint of bake time; the `..`-traversal assertion is already covered in `pkg/provider/claude_test.go`.
- `pkg/config/paths.go` — `GetClaudeStateDir`, `GetProjectsDir`, `GetClaudeSettingsPath`, and the `ClaudeStateDirEnv` constant all forward to `provider.ClaudeCode{}`. Real callers (`cmd/skills.go`, `pkg/config/skill_til.go`, `pkg/config/skill_retro.go`, `pkg/discovery/sessions.go`) should call `provider` directly once the skill and discovery surfaces are moved.

## Risks

- Mechanical hook type renames can hide JSON wire changes. Protect with exact response and hook settings tests.
- Provider constructor injection can sprawl. Limit command constructor changes to touched hook/status flows.
- Daemon state and inbox files are operationally sensitive. Do not change their filenames or JSON shape in this phase.
- Codex assumptions can drift quickly. Confirm Codex hook config, transcript layout, and subagent metadata before implementing the Codex provider.
