# pkg/git

Extracts git repository information from the working directory or from transcript files.

## Files

| File | Role |
|------|------|
| `git.go` | Live git commands (`DetectGitInfo`, `GetHeadSHA`, `GetRepoURL`, `ToGitHubURL`) |

## Key API

- **`DetectGitInfo(cwd)`** — Returns `*GitInfo` with repo URL, branch, commit SHA, message, author, and dirty status. Returns `nil` (not error) if not in a git repo.
- **`GetHeadSHA(cwd)`** — Returns the full 40-char HEAD commit SHA.
- **`GetRepoURL(cwd)`** — Returns `remote.origin.url`.
- **`ToGitHubURL(gitURL)`** — Converts git remote URLs (SSH, HTTPS, `git@`) to `https://github.com/owner/repo`. Returns empty string for non-GitHub URLs.
- **`ExtractGitInfoFromTranscript(path)`** — Parses a JSONL transcript to find `gitBranch` and `cwd` fields. Used when the working directory may no longer exist.

## How to Extend

**Adding a new git field:** Add the field to the `GitInfo` struct, then add the corresponding `git` command in `DetectGitInfo()`. Follow the existing pattern: run `gitCommand()`, check for errors, assign to struct.

## Invariants

- **All git command arguments must be hardcoded string literals.** Never pass user input to `gitCommand()` / `exec.Command`. This prevents command injection.
- **`DetectGitInfo` returns nil, not error, when not in a git repo.** Callers check for nil, not err. Being outside a git repo is normal (not an error condition).
- **Two extraction paths exist intentionally.** Live git commands (`DetectGitInfo`) work when the repo is available. Transcript parsing (`ExtractGitInfoFromTranscript`) works when replaying sessions where the repo may be gone. Don't merge these — they serve different lifecycle phases.

## Testing

```bash
go test ./pkg/git/...
```

Tests create temporary git repositories with `git init` and verify extraction. `ExtractGitInfoFromTranscript` tests use synthetic JSONL files.

## Dependencies

**Uses:** standard library only (os/exec for git commands)

**Used by:** `cmd/` (post-tool-use hook for commit linking), `pkg/sync/` (metadata extraction)
