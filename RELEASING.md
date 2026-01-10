# Releasing Confab

## Steps

1. **Update version and tag**
   ```bash
   git tag v0.X.Y
   git push origin v0.X.Y
   ```

2. **Update latest_version file in confab-web**

   Edit `frontend/public/cli/latest_version` to contain the new version:
   ```
   v0.X.Y
   ```

   Commit and deploy confab-web so the CLI auto-update check sees the new version.

3. **Build and upload release binary**

   The install script at `confabulous.dev/install` downloads from GitHub releases or a CDN. Ensure the binary is available there.

## Version Format

- Use semver with `v` prefix: `v0.3.1`
- The CLI compares versions numerically (major.minor.patch)

## Auto-Update Behavior

- `confab update` checks `confabulous.dev/cli/latest_version` for the latest version
- The SessionStart hook auto-updates and re-execs if a new version is available
- User-facing commands (`list`, `save`, `status`) show an update notice but don't auto-install
- Checks are rate-limited to once per hour per machine
