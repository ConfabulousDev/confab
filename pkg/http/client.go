package http

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/klauspost/compress/zstd"
)

const (
	// compressionThreshold is the minimum payload size to compress.
	// Below this, compression overhead isn't worth it.
	compressionThreshold = 1024 // 1KB

	// maxRetryAfterSeconds is the maximum Retry-After value we'll honor.
	// Prevents a malicious server from stalling the client indefinitely.
	maxRetryAfterSeconds = 3600

	// Retry settings for rate limiting
	maxRetries        = 5
	initialBackoff    = 1 * time.Second
	maxBackoff        = 60 * time.Second
	backoffMultiplier = 2.0
)

// maxResponseSize is the maximum size of an HTTP response body we'll read.
// Prevents OOM from malicious or buggy servers sending unbounded responses.
// It is a var (not const) so tests can lower it via
// SetMaxResponseSizeForTest. Do not mutate from production code.
var maxResponseSize int64 = 32 * 1024 * 1024 // 32MB

// SetMaxResponseSizeForTest temporarily lowers the response-size cap so
// tests can exercise the limit without allocating 32MB of memory.
// Returns a restore function that callers should defer.
// Intended for test code only — do not call from production.
func SetMaxResponseSizeForTest(n int64) (restore func()) {
	prev := maxResponseSize
	maxResponseSize = n
	return func() { maxResponseSize = prev }
}

// userAgent is set once at startup via SetUserAgent
var userAgent string

// SetUserAgent sets the User-Agent header for all HTTP requests.
// Should be called once at startup before any requests are made.
func SetUserAgent(ua string) {
	userAgent = ua
}

// BuildUserAgent constructs a User-Agent string in the format: confab/version (os; arch)
func BuildUserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("confab/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH)
}

// ErrUnauthorized is returned when the server returns 401 or 403.
// This typically means the API key is invalid or expired.
var ErrUnauthorized = errors.New("unauthorized")

// errRateLimited is returned when retries are exhausted on 429 responses.
// Internal only — no callers currently check for this sentinel.
var errRateLimited = errors.New("rate limited")

// ErrSessionNotFound is returned when the server returns 404.
// This typically means the session was deleted from the backend.
var ErrSessionNotFound = errors.New("session not found")

// ErrConflict is returned when the server returns 409.
// This typically means the resource already exists (e.g., duplicate link).
var ErrConflict = errors.New("conflict")

// Client is a configured HTTP client for making authenticated requests to the backend
type Client struct {
	cfg        *config.UploadConfig
	httpClient *http.Client
	encoder    *zstd.Encoder
}

// NewClient creates a new authenticated HTTP client
func NewClient(cfg *config.UploadConfig, timeout time.Duration) (*Client, error) {
	// Create zstd encoder with default compression level (good balance of speed/ratio)
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}

	// Configure transport with TLS minimum version for non-localhost URLs.
	//
	// Security note: For localhost URLs (http://localhost, http://127.0.0.1,
	// http://[::1]), we use the default transport without TLS enforcement.
	// This is intentional for local development where developers run a local
	// backend server. Production traffic always goes through HTTPS with TLS 1.2+.
	// Localhost connections stay on the local machine and don't traverse networks.
	var transport http.RoundTripper
	if !isLocalhost(cfg.BackendURL) {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
	} else {
		logger.Debug("Using localhost backend URL - TLS not enforced")
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		encoder: encoder,
	}, nil
}

// isLocalhost checks if the URL points to localhost.
// Used to determine if TLS enforcement should be skipped for local development.
func isLocalhost(url string) bool {
	return strings.HasPrefix(url, "http://localhost") ||
		strings.HasPrefix(url, "http://127.0.0.1") ||
		strings.HasPrefix(url, "http://[::1]")
}

// DoJSON performs an HTTP request with JSON body and parses JSON response
// Automatically sets Content-Type, Authorization, and handles error responses.
// Payloads larger than 1KB are compressed with zstd.
// Retries with exponential backoff on 429 (rate limited) responses.
func (c *Client) DoJSON(method, path string, reqBody, respBody interface{}) error {
	// Marshal and compress request body once (for retries)
	var payload []byte
	var contentEncoding string

	if reqBody != nil {
		var err error
		payload, err = json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}

		// Log request metadata at debug level (never log payload — it contains transcript content)
		logger.Debug("HTTP %s %s payload_bytes=%d", method, path, len(payload))

		// Compress if payload is large enough
		if len(payload) >= compressionThreshold {
			payload = c.encoder.EncodeAll(payload, make([]byte, 0, len(payload)/2))
			contentEncoding = "zstd"
		}
	}

	url := c.cfg.BackendURL + path
	backoff := initialBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Create fresh reader for each attempt
		var bodyReader io.Reader
		if payload != nil {
			bodyReader = bytes.NewReader(payload)
		}

		// Create request
		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Set headers
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
			if contentEncoding != "" {
				req.Header.Set("Content-Encoding", contentEncoding)
			}
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		if userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		}

		// Execute request
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send request: %w", err)
		}

		// Read response body (bounded to prevent OOM from malicious servers)
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		// Handle rate limiting with retry
		if resp.StatusCode == http.StatusTooManyRequests {
			if attempt == maxRetries {
				return fmt.Errorf("%w: exceeded %d retries", errRateLimited, maxRetries)
			}

			// Use Retry-After header if provided, otherwise use exponential backoff
			waitTime := backoff
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 && seconds <= maxRetryAfterSeconds {
					waitTime = time.Duration(seconds) * time.Second
				}
			}

			time.Sleep(waitTime)

			// Exponential backoff for next attempt
			backoff = time.Duration(float64(backoff) * backoffMultiplier)
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Accept any 2xx status code as success
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return mapStatusToError(resp.StatusCode, truncateBody(body, 256))
		}

		// Parse response if requested
		if respBody != nil {
			if err := json.Unmarshal(body, respBody); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
		}

		return nil
	}
	panic("unreachable: retry loop exited without returning")
}

// Get performs a GET request with JSON response parsing
func (c *Client) Get(path string, respBody interface{}) error {
	return c.DoJSON("GET", path, nil, respBody)
}

// Post performs a POST request with JSON body and response
func (c *Client) Post(path string, reqBody, respBody interface{}) error {
	return c.DoJSON("POST", path, reqBody, respBody)
}

// Patch performs a PATCH request with JSON body and response
func (c *Client) Patch(path string, reqBody, respBody interface{}) error {
	return c.DoJSON("PATCH", path, reqBody, respBody)
}

// GetRawToWriter performs a GET request and streams the response body to w.
// Unlike Get/DoJSON, this does not attempt JSON unmarshaling — it copies the
// raw response bytes directly. Handles auth and error status mapping, but does
// not retry on 429 (retrying a stream would produce corrupt output since
// partial data from the first attempt is already written to w).
func (c *Client) GetRawToWriter(path string, w io.Writer) error {
	url := c.cfg.BackendURL + path

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if err := checkStreamResponseStatus(resp); err != nil {
		return err
	}

	if _, err := io.Copy(w, io.LimitReader(resp.Body, maxResponseSize)); err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	return nil
}

// checkStreamResponseStatus maps HTTP error status codes to sentinel errors.
// Used by GetRawToWriter where the response body hasn't been fully read yet.
func checkStreamResponseStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// 429 returns immediately without reading body — the caller may retry elsewhere
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: status %d", errRateLimited, resp.StatusCode)
	}

	// Read a snippet of the body for error context
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))

	return mapStatusToError(resp.StatusCode, string(snippet))
}

func mapStatusToError(status int, body string) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: status %d: %s", ErrUnauthorized, status, body)
	case http.StatusNotFound:
		return fmt.Errorf("%w: status %d: %s", ErrSessionNotFound, status, body)
	case http.StatusConflict:
		return fmt.Errorf("%w: status %d: %s", ErrConflict, status, body)
	default:
		return fmt.Errorf("http request failed with status %d: %s", status, body)
	}
}

// truncateBody truncates a response body for safe inclusion in error messages.
func truncateBody(body []byte, maxLen int) string {
	if len(body) <= maxLen {
		return string(body)
	}
	return string(body[:maxLen]) + "... (truncated)"
}
