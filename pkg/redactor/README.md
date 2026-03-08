# pkg/redactor

JSON-aware sensitive data redaction engine. Applies regex patterns to transcript content before upload, preserving JSON structure.

## Files

| File | Role |
|------|------|
| `redactor.go` | Core redaction engine: `Redactor`, `Redact`, `RedactJSONL`, JSON walking |
| `types.go` | `Config`, `Pattern` type definitions |

## Two Pattern Modes

### Value-based patterns
A regex applied to all JSON string values. Used for secrets with distinctive formats (e.g., `sk-ant-api03-...`). No `FieldPattern` set.

### Field-based patterns
A regex on field **names** (e.g., `password|secret|api_key`). When a field name matches, the field's **value** is redacted. Optionally combined with a value `Pattern` for more precise matching.

## Key API

```go
redactor := redactor.NewFromConfig(redactionConfig)  // from pkg/config types
redacted := redactor.RedactJSONL(rawBytes)            // JSON-aware, line-by-line
redacted := redactor.Redact(plainText)                // text-only fallback
```

- **`NewFromConfig(cfg)`** — Creates redactor from config. Includes default patterns if `use_default_patterns` is true. Returns `nil` if no patterns (callers must nil-check).
- **`NewRedactor(cfg)`** — Lower-level constructor from redactor's own config type.
- **`RedactJSONL([]byte)`** — Processes JSONL: parses each line as JSON, recursively walks the structure, redacts string values, re-serializes. Falls back to text-mode `Redact()` for invalid JSON lines.
- **`Redact(input)`** — Plain text redaction. Only applies value-based patterns (field-based patterns need JSON context).

## How to Extend

**Adding a new built-in redaction pattern:** Add to `GetDefaultRedactionPatterns()` in `pkg/config/upload.go` (not here). Patterns are part of the config system; this package is the engine. Order matters — more specific patterns should come before general ones to avoid partial matches.

**Adding a new pattern type:** Add a field to `Pattern` in `types.go`, handle it in `redactStringValue()`. Consider whether the existing value/field modes are truly insufficient.

## Invariants

- **JSON structure must never be corrupted by redaction.** The parse-walk-redact-serialize pipeline ensures this. Never apply regex replacement directly to raw JSON strings.
- **Redaction markers use `[REDACTED:TYPE]` format.** The `TYPE` comes from the pattern's `Type` field. Must be consistent — the backend may parse these markers.
- **Field-based patterns only work in JSON context.** They're skipped in plain text `Redact()` because there's no field name to match against.
- **Must handle lines up to 10MB.** Uses a local `maxLineSize` constant (10MB, same value as `types.MaxJSONLLineSize`). Large tool results in transcripts can approach this limit.
- **Capture group redaction uses submatch byte indices**, not string replacement, to avoid replacing repeated text elsewhere in the match.

## Design Decisions

**JSON-aware redaction.** Naive regex replacement on raw JSON can break structure — e.g., replacing a value containing a quote character corrupts the JSON. The parse-walk-serialize approach is more work but guarantees valid output.

**Patterns defined in config, not redactor.** Default patterns live in `pkg/config/upload.go` because they're part of the user-facing configuration system. The redactor is a pure engine that takes compiled patterns as input. This separation allows custom patterns to be added via config without modifying the engine.

**`NewFromConfig` returns nil when empty.** Callers that receive nil skip redaction entirely, which is the correct behavior when redaction is disabled or has no patterns.

## Testing

```bash
go test ./pkg/redactor/...
```

- `redactor_test.go` — Core engine: JSON walking, value/field patterns, capture groups, edge cases
- `patterns_test.go` — Verifies default patterns match 20+ secret formats (API keys, tokens, credentials)
- `config_test.go` — Pattern compilation, `use_default_patterns` flag, config round-trip

## Dependencies

**Uses:** `pkg/config` (redaction config types, default patterns)

**Used by:** `pkg/sync/` (via `FileTracker.ReadChunk`), `cmd/` (redaction-test command)
