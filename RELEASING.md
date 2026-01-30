# Releasing Confab

## Steps

1. **Update version and tag**
   ```bash
   git tag v0.X.Y
   git push origin v0.X.Y
   ```

2. **Create GitHub Release**

   Create a GitHub release for the tag with the following assets:
   - `confab_darwin_amd64` - macOS Intel
   - `confab_darwin_arm64` - macOS Apple Silicon
   - `confab_linux_amd64` - Linux x86_64
   - `confab_linux_arm64` - Linux ARM64

   The CLI auto-update mechanism fetches the latest release from GitHub.

## Version Format

- Use semver with `v` prefix: `v0.3.1`
- The CLI compares versions numerically (major.minor.patch)

## Auto-Update Behavior

- `confab update` checks GitHub releases API for the latest version
- The SessionStart hook auto-updates and re-execs if a new version is available
- User-facing commands (`list`, `save`, `status`) show an update notice but don't auto-install
- Checks are rate-limited to once per hour per machine
