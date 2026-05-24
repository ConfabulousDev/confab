# pkg/git

Extracts git repository information from the working directory or from transcript files.

## Files

| File | Role |
|------|------|
| `git.go` | Live git commands (`DetectGitInfo`, `GetHeadSHA`, `GetRepoURL`, `ToGitHubURL`) |

## Key API

- **`DetectGitInfo(cwd)`** — Returns `*GitInfo` with repo URL, branch, commit SHA, message, author, dirty status, **configured remotes, and tracking remote** (CF-493). Returns `nil` (not error) if not in a git repo.
- **`GetHeadSHA(cwd)`** — Returns the full 40-char HEAD commit SHA. Returns empty string and nil if not in a git repo.
- **`GetRepoURL(cwd)`** — Returns `remote.origin.url`.
- **`DetectRemotes(cwd)`** — Returns `[]GitRemote` (merged fetch + push per remote, in `git remote -v` order) for cwd. Returns `(nil, nil)` outside a git repo; silent best-effort.
- **`DetectTrackingRemote(cwd, branch)`** — Returns `branch.<branch>.remote` git config value, or `""` when unset / on any error. `branch == ""` short-circuits without invoking git.
- **`DetectBranch(cwd)`** — Returns `git rev-parse --abbrev-ref HEAD` for cwd, or `""` on error. Detached HEAD returns the literal string `"HEAD"`.
- **`ToGitHubURL(gitURL)`** — Converts git remote URLs (SSH, HTTPS, `git@`) to `https://github.com/owner/repo`. Returns empty string for non-GitHub URLs.
- **`ExtractGitInfoFromTranscript(path)`** — Parses a JSONL transcript to find `gitBranch` and `cwd` fields. Used when the working directory may no longer exist. Best-effort populates remotes + tracking remote from the discovered cwd.

## How to Extend

**Adding a new git field:** Add the field to the `GitInfo` struct, then add the corresponding `git` command in `DetectGitInfo()`. Follow the existing pattern: run `gitCommand()`, check for errors, assign to struct.

## Invariants

- **All git command arguments must be hardcoded string literals or values derived from git's own output / local config.** Never pass arbitrary user input directly to `gitCommand()`. `exec.Command` doesn't invoke a shell, so injection isn't possible per se, but unvalidated args can confuse git or surface unexpected files (`--exec` style flags). Callers that need to inject a value (e.g., `DetectTrackingRemote` substituting a branch name) must source it from a trusted local channel.
- **`DetectGitInfo` returns nil, not error, when not in a git repo.** Callers check for nil, not err. Being outside a git repo is normal (not an error condition).
- **`DetectRemotes` / `DetectTrackingRemote` / `DetectBranch` are silent best-effort.** Return zero values on any error (including not-a-repo); no logs even at debug level. Callers can plug them into chains without nil-checking each individually.
- **Two extraction paths exist intentionally.** Live git commands (`DetectGitInfo`) work when the repo is available. Transcript parsing (`ExtractGitInfoFromTranscript`) works when replaying sessions where the repo may be gone. Don't merge these — they serve different lifecycle phases.
- **`GitRemote` wire format mirrors CF-494's `db.GitRemote` exactly.** JSON tags are `name` / `fetch_url` / `push_url` with no `omitempty` on the URL fields — both URLs are always emitted per the locked CF-493/CF-494 wire-format decision. `parseRemoteVOutput` drops any entry with empty name or both URLs empty (CF-494 strict validation 400s the chunk otherwise).

## Testing

```bash
go test ./pkg/git/...
```

Tests create temporary git repositories with `git init` and verify extraction. `ExtractGitInfoFromTranscript` tests use synthetic JSONL files.

## Dependencies

**Uses:** standard library (os/exec for git commands), `pkg/types` (JSONL scanner)

**Used by:** `cmd/` (post-tool-use hook for commit linking), `pkg/sync/` (metadata extraction)
