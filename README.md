# Confab CLI

Command-line tool for capturing and uploading Claude Code sessions to cloud storage.

## Installation

Supported on macOS and Linux.

```bash
curl -fsSL https://confabulous.dev/install | bash
# Follow the instructions to add confab to your PATH
confab setup
```

After setup, your Claude Code sessions will automatically sync to https://confabulous.dev.

### Building from Source

```bash
git clone https://github.com/ConfabulousDev/confab.git
cd confab
make build
./confab install
# Follow the instructions to add confab to your PATH
confab setup
```

## Usage

### Authentication

```bash
# Login and install hooks
confab setup

# Or login separately
confab login

# Check status
confab status

# Logout
confab logout
```

### Sync Mode (Default)

Sessions are synced incrementally while you work:

```bash
# Install sync hooks (done automatically by setup)
confab hooks add

# View running sync daemons
confab sync status

# Remove hooks
confab hooks remove
```

The sync daemon uploads transcript data periodically during your session, so data isn't lost if the session ends unexpectedly.

### List Sessions

```bash
# List all local sessions
confab list

# Filter by duration
confab list -d 5d    # Sessions from last 5 days
confab list -d 12h   # Sessions from last 12 hours
```

Copy a session ID from the list to use with `confab save`.

### Manual Upload

```bash
# Upload specific sessions by ID (use IDs from 'confab list')
confab save abc123de

# Upload multiple sessions
confab save abc123de f9e8d7c6
```

### Redaction

Sensitive data is automatically redacted before uploading. Redaction is enabled by default during `confab setup`.

Edit the `redaction` section in `~/.confab/config.json` to customize:

```json
{
  "redaction": {
    "enabled": true,
    "patterns": [
      {"name": "My API Key", "pattern": "mykey-[A-Za-z0-9]+", "type": "api_key"}
    ]
  }
}
```

Each pattern has:
- `name`: Description
- `pattern`: Regex to match (optional if using `field_pattern`)
- `field_pattern`: Regex matching JSON field names (optional)
- `type`: Label for redaction marker (e.g., `[REDACTED:API_KEY]`)
- `capture_group`: Redact only this group number (optional, for partial redaction)

Test your rules against a file:

```bash
confab redaction-test transcript.jsonl
```


## Configuration

| File | Purpose |
|------|---------|
| `~/.confab/config.json` | Backend URL, API key, and redaction settings |
| `~/.confab/logs/confab.log` | Operation logs (auto-rotated, 14 day retention) |

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `CONFAB_CLAUDE_DIR` | `~/.claude` | Claude Code state directory |
| `CONFAB_CONFIG_PATH` | `~/.confab/config.json` | Config file location |
| `CONFAB_LOG_DIR` | `~/.confab/logs` | Log directory |

## Development

```bash
make build
go test ./...
```

## License

MIT
