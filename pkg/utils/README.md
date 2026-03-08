# pkg/utils

Small shared utility functions and timeout constants.

## Files

| File | Role |
|------|------|
| `strings.go` | String truncation helpers and HTTP timeout constants |

## Functions

- **`TruncateSecret(s, prefixLen, suffixLen)`** — Safely displays secrets as `"sk_live_...3456"`. Returns `"***"` if too short, `"(empty)"` if empty.
- **`TruncateEnd(s, maxLen)`** — Truncates with `"..."` at the end. Minimum `maxLen` is 4.

## Constants

| Constant | Value | Usage |
|----------|-------|-------|
| `DefaultHTTPTimeout` | 30s | API calls (auth validation, sync uploads) |

## When to Add Here vs. Elsewhere

Add a function here only if it's truly generic and reused by multiple packages. Package-specific helpers belong in their package. If in doubt, keep it local — you can always extract later.

## Dependencies

**Uses:** standard library only

**Used by:** `cmd/`, `pkg/sync/`
