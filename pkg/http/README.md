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
- **`GetRawToWriter(path, w)`** — Streaming GET that writes the raw response body to `w`. Used by `confab session download` for large transcript files. Body is streamed through `io.LimitReader(maxResponseSize)`; on write error mid-stream the destination may be left partially populated, so callers should treat the output as incomplete on error.
- **`SetUserAgent(ua)`** — Package-level function, must be called once at startup (from `main.go`).
- **`BuildUserAgent(version)`** — Constructs the canonical user-agent string from a version.

## Sentinel Errors

| Error | HTTP Status | Meaning |
|-------|-------------|---------|
| `ErrUnauthorized` | 401, 403 | Invalid or expired API key |
| `ErrSessionNotFound` | 404 | Session doesn't exist on backend |
| `ErrConflict` | 409 | Duplicate resource |

Note: 429 (rate limited) errors use an internal sentinel (`errRateLimited`) since no callers currently need to distinguish rate limiting from other failures.

Callers use `errors.Is(err, http.ErrUnauthorized)` to handle specific cases.

## Design Decisions

**Zstd over gzip.** Better compression ratio for JSON payloads, which matters for large transcript chunks. The 1KB compression threshold (`compressionThreshold`) avoids compressing tiny payloads where overhead exceeds savings.

**Retry only on 429.** Rate limiting is transient and retryable. Other errors (400, 500) are not retried — they indicate bugs or server issues that won't resolve by waiting. Retries use exponential backoff (1s initial, 2x multiplier, 60s max) and respect `Retry-After` headers (capped at `maxRetryAfterSeconds` = 3600s).

**Bounded response reading.** Response bodies are read with `io.LimitReader` capped at `maxResponseSize` (32MB) to prevent memory exhaustion from malicious or malformed responses. Error messages include response body truncated to 256 bytes via `truncateBody()` to avoid log flooding.

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
- Response bodies must always be read with a size limit (`maxResponseSize`) — never use unbounded `io.ReadAll` on HTTP responses.
- `Retry-After` values must be capped (`maxRetryAfterSeconds`) — a malicious server must not be able to make the client sleep indefinitely.

## Testing

```bash
go test ./pkg/http/...
```

Tests use `httptest.NewServer` to verify compression thresholds, error handling, and retry behavior.

## Dependencies

**Uses:** `github.com/klauspost/compress/zstd`, `pkg/config` (UploadConfig for backend URL/API key), `pkg/logger`

**Used by:** `pkg/sync/` (via `Client`), `cmd/` (login, status validation)
