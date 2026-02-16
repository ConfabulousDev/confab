# Confabulous.dev CLI: `confab`

Understand your Claude Code sessions. Sync transcripts to a backend (e.g. https://confabulous.dev) for exploration, sharing and analysis.

The `confab` CLI hooks into Claude Code session lifecycle to sync data in real-time.

Works seamlessly - your `claude` workflow stays exactly the same.

## Installation

Supported on macOS and Linux.

```bash
curl -fsSL https://confabulous.dev/install | bash
# Follow the instructions to add confab to your PATH
confab setup --backend-url https://confabulous.dev
```

After setup, your Claude Code sessions will automatically sync to the configured backend.

### Building from Source

```bash
git clone https://github.com/ConfabulousDev/confab.git
cd confab
make build
./confab install
# Follow the instructions to add confab to your PATH
confab setup --backend-url https://confabulous.dev
```

## Usage

### Authentication

```bash
# Login and install hooks
confab setup --backend-url https://confabulous.dev

# Or login separately
confab login --backend-url https://confabulous.dev

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

**Built-in patterns** detect common secrets without any configuration:
- API keys (Anthropic, OpenAI, AWS, GitHub, Google, Stripe, Slack, etc.)
- Private keys (RSA, EC, OpenSSH, PKCS#8)
- JWT tokens
- Database connection string passwords (PostgreSQL, MySQL, MongoDB, Redis)
- Sensitive field names (`password`, `secret`, `token`, `api_key`, etc.)

To add custom patterns, edit `~/.confab/config.json`:

```json
{
  "redaction": {
    "enabled": true,
    "use_default_patterns": true,
    "patterns": [
      {"name": "My Custom Key", "pattern": "mykey-[A-Za-z0-9]+", "type": "api_key"}
    ]
  }
}
```

Custom patterns are added alongside the defaults. Set `use_default_patterns` to `false` to use only your custom patterns.

Pattern options:
- `pattern`: Regex to match in values
- `field_pattern`: Regex to match JSON field names (redacts the field's value)
- `type`: Label for the redaction marker (e.g., `[REDACTED:API_KEY]`)
- `capture_group`: Redact only this capture group (for partial redaction)

Test your patterns against a file:

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
