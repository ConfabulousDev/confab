# pkg/provider

Provider-specific local behavior for tools that Confab can integrate with.

Phase 2 starts with concrete Claude Code extraction only. This package does not define a generic Codex provider, a normalized hook model, backend tool identity, or skill abstraction yet.

## Files

| File | Role |
|------|------|
| `claude.go` | Claude Code paths, hook parsing, transcript path validation, and parent process detection |

## Invariants

- `ClaudeCode` preserves existing Claude Code behavior, including `CONFAB_CLAUDE_DIR`.
- Claude hook parsing returns `types.ClaudeHookInput`; there is no generic normalized hook payload.
- Transcript validation preserves legacy behavior where `CONFAB_CLAUDE_DIR` may be treated as an allowed transcript root.
- Parent PID detection is Claude-specific and is not part of a generic provider interface.

## Used By

`cmd/`, `pkg/config/`, and `pkg/discovery/`.
