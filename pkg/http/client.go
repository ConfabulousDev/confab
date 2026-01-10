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

	// Retry settings for rate limiting
	maxRetries       = 5
	initialBackoff   = 1 * time.Second
	maxBackoff       = 60 * time.Second
	backoffMultiplier = 2.0
)

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

// ErrRateLimited is returned when retries are exhausted on 429 responses.
var ErrRateLimited = errors.New("rate limited")

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

		// Log the JSON payload at debug level (before compression)
		logger.Debug("HTTP %s %s payload: %s", method, path, string(payload))

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

		// Read response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		// Handle rate limiting with retry
		if resp.StatusCode == http.StatusTooManyRequests {
			if attempt == maxRetries {
				return fmt.Errorf("%w: exceeded %d retries", ErrRateLimited, maxRetries)
			}

			// Use Retry-After header if provided, otherwise use exponential backoff
			waitTime := backoff
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil {
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

		// Check other status codes
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status %d: %s", ErrUnauthorized, resp.StatusCode, string(body))
		}
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: status %d: %s", ErrSessionNotFound, resp.StatusCode, string(body))
		}
		if resp.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w: status %d: %s", ErrConflict, resp.StatusCode, string(body))
		}
		// Accept any 2xx status code as success
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("http request failed with status %d: %s", resp.StatusCode, string(body))
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
