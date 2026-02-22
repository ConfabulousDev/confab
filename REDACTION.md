# Redaction

Sensitive data is automatically redacted before uploading. Redaction is enabled by default during `confab setup`.

## Built-in Patterns

Built-in patterns detect common secrets without any configuration:

- API keys (Anthropic, OpenAI, AWS, GitHub, Google, Stripe, Slack, etc.)
- Private keys (RSA, EC, OpenSSH, PKCS#8)
- JWT tokens
- Database connection string passwords (PostgreSQL, MySQL, MongoDB, Redis)
- Sensitive field names (`password`, `secret`, `token`, `api_key`, etc.)

## Custom Patterns

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

## Pattern Options

| Option | Description |
|--------|-------------|
| `pattern` | Regex to match in values |
| `field_pattern` | Regex to match JSON field names (redacts the field's value) |
| `type` | Label for the redaction marker (e.g., `[REDACTED:API_KEY]`) |
| `capture_group` | Redact only this capture group (for partial redaction) |

## Testing

Test your patterns against a file:

```bash
confab redaction-test transcript.jsonl
```
