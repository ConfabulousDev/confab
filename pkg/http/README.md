# pkg/http

HTTP client with zstd compression, TLS enforcement, retry logic, and typed errors.

## Files

| File | Role |
|------|------|
| `client.go` | `Client` struct, `DoJSON` method, compression, retries, error handling |

## Key API

```go
client := http.NewClient(cfg, timeout)
err := client.Post("/api/v1/sync/chunk", reqBody, &respBody)
```

- **`NewClient(cfg, timeout)`** — Creates client with zstd encoder, TLS config, and timeout.
- **`DoJSON(method, path, reqBody, respBody)`** — Core method: marshals JSON, optionally compresses, sends request, handles retries/errors, unmarshals response.
- **`Get` / `Post` / `Patch`** — Convenience wrappers around `DoJSON`.
- **`SetUserAgent(ua)`** — Package-level function, must be called once at startup (from `main.go`).

## Sentinel Errors

| Error | HTTP Status | Meaning |
|-------|-------------|---------|
| `ErrUnauthorized` | 401, 403 | Invalid or expired API key |
| `ErrRateLimited` | 429 | Rate limited after max retries |
| `ErrSessionNotFound` | 404 | Session doesn't exist on backend |
| `ErrConflict` | 409 | Duplicate resource |

Callers use `errors.Is(err, http.ErrUnauthorized)` to handle specific cases.

## Design Decisions

**Zstd over gzip.** Better compression ratio for JSON payloads, which matters for large transcript chunks. The 1KB compression threshold (`compressionThreshold`) avoids compressing tiny payloads where overhead exceeds savings.

**Retry only on 429.** Rate limiting is transient and retryable. Other errors (400, 500) are not retried — they indicate bugs or server issues that won't resolve by waiting. Retries use exponential backoff (1s initial, 2x multiplier, 60s max) and respect `Retry-After` headers.

**Localhost TLS exemption.** Non-localhost URLs enforce TLS 1.2+. Localhost is exempt for local development. This is checked by hostname, not scheme.

**Never log payloads.** `DoJSON` logs payload byte counts but never the content. Payloads contain transcript data which may include sensitive information even after redaction.

## How to Extend

**Adding a new error type:** Define a sentinel error (`var ErrFoo = errors.New("...")`), add a case in `DoJSON`'s status code switch, and document it above.

**Changing retry behavior:** Modify `maxRetries`, `initialBackoff`, `maxBackoff`, or `backoffMultiplier` constants. Consider that retry changes affect all API calls.

## Invariants

- `SetUserAgent()` must be called once at startup before any HTTP requests.
- TLS 1.2+ is enforced for all non-localhost connections — do not weaken this.
- Payloads must never be logged (privacy).
- Retry logic must only apply to 429 responses.

## Testing

```bash
go test ./pkg/http/...
```

Tests use `httptest.NewServer` to verify compression thresholds, error handling, and retry behavior.

## Dependencies

**Uses:** `github.com/klauspost/compress/zstd`, `pkg/config` (UploadConfig for backend URL/API key), `pkg/logger`

**Used by:** `pkg/sync/` (via `Client`), `cmd/` (login, status validation)
