# pkg/pathcanon

Canonicalizes filesystem directory paths so two spellings of the same directory
compare equal. Stdlib-only leaf — both `pkg/config` and `pkg/provider` depend on
it without an import cycle.

## Files

| File | Role |
|------|------|
| `pathcanon.go` | `CanonicalDir(dir)` — tilde-expand → `Abs` → `Clean` → `EvalSymlinks` (falling back to the lexical form when the path can't be resolved). Returns `""` unchanged (callers use `""` for "the default dir", which has no filesystem identity). |

## Why

This is the matching contract behind per-`(provider, config dir)` backend
bindings (kata hpec). `confab setup --config-dir <dir>` stores a binding under
`CanonicalDir(configDir)`, and at runtime a Claude hook derives its config dir
from the transcript path and looks the binding up under
`CanonicalDir(derivedDir)`. Because both sides pass through this **same**
function on the **same** logical directory (which exists at both times),
relative form, `.`/`..`/`//`, trailing slash, tilde, and symlinked components
all converge to one string — the lookup matches by construction.

## Invariants / limits

- Strength depends on the path existing at call time (symlink resolution needs
  it); a non-existent path degrades to `Abs`+`Clean` (no symlink resolution).
- Does **not** unify non-symlink aliases (bind mounts, multiple mounts of one
  FS) or case/Unicode-equivalent names on case-insensitive volumes — those are
  filesystem identity, not string identity. Callers handle the residual by
  making a binding miss loud and leak-free rather than silently wrong.
