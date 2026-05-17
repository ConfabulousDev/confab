package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ConfabulousDev/confab/pkg/config"
)

func TestClient_CompressionThreshold(t *testing.T) {
	var receivedContentEncoding string
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentEncoding = r.Header.Get("Content-Encoding")
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(&config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "test-key",
	}, 0)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	t.Run("small payload not compressed", func(t *testing.T) {
		// Small payload (< 1KB)
		smallPayload := map[string]string{"msg": "hello"}
		var resp struct{ Ok bool }

		err := client.Post("/test", smallPayload, &resp)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		if receivedContentEncoding != "" {
			t.Errorf("expected no Content-Encoding for small payload, got %q", receivedContentEncoding)
		}

		// Verify it's valid JSON (not compressed)
		var decoded map[string]string
		if err := json.Unmarshal(receivedBody, &decoded); err != nil {
			t.Errorf("small payload should be uncompressed JSON: %v", err)
		}
	})

	t.Run("large payload compressed with zstd", func(t *testing.T) {
		// Large payload (> 1KB)
		largePayload := map[string]string{
			"msg": string(make([]byte, 2000)), // 2KB of data
		}
		var resp struct{ Ok bool }

		err := client.Post("/test", largePayload, &resp)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		if receivedContentEncoding != "zstd" {
			t.Errorf("expected Content-Encoding 'zstd' for large payload, got %q", receivedContentEncoding)
		}

		// Verify it's valid zstd (decompress and check JSON)
		decoder, _ := zstd.NewReader(nil)
		decompressed, err := decoder.DecodeAll(receivedBody, nil)
		if err != nil {
			t.Fatalf("failed to decompress zstd: %v", err)
		}

		var decoded map[string]string
		if err := json.Unmarshal(decompressed, &decoded); err != nil {
			t.Errorf("decompressed payload should be valid JSON: %v", err)
		}

		// Verify compression actually reduced size
		if len(receivedBody) >= len(decompressed) {
			t.Errorf("compression didn't reduce size: compressed=%d, original=%d",
				len(receivedBody), len(decompressed))
		}

		t.Logf("Compression: %d -> %d bytes (%.1f%% reduction)",
			len(decompressed), len(receivedBody),
			100*(1-float64(len(receivedBody))/float64(len(decompressed))))
	})
}

func TestClient_CompressionRatio(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(&config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "test-key",
	}, 0)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Simulate realistic transcript chunk (repetitive JSON structures)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = `{"type":"assistant","message":"This is a typical message with some repeated content and structure"}`
	}
	payload := map[string]interface{}{
		"session_id": "test-session",
		"file_name":  "transcript.jsonl",
		"file_type":  "transcript",
		"first_line": 1,
		"lines":      lines,
	}

	originalJSON, _ := json.Marshal(payload)
	var resp struct{ Ok bool }

	err = client.Post("/test", payload, &resp)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	ratio := float64(len(receivedBody)) / float64(len(originalJSON)) * 100
	t.Logf("Realistic transcript compression: %d -> %d bytes (%.1f%% of original)",
		len(originalJSON), len(receivedBody), ratio)

	// Expect at least 50% reduction for repetitive JSON
	if ratio > 50 {
		t.Errorf("expected at least 50%% compression, got %.1f%%", ratio)
	}
}

func TestBuildUserAgent(t *testing.T) {
	t.Run("with version", func(t *testing.T) {
		ua := BuildUserAgent("1.2.3")
		if ua == "" {
			t.Fatal("expected non-empty user agent")
		}
		if !strings.Contains(ua, "confab/1.2.3") {
			t.Errorf("expected 'confab/1.2.3' in user agent, got %q", ua)
		}
	})

	t.Run("empty version defaults to dev", func(t *testing.T) {
		ua := BuildUserAgent("")
		if !strings.Contains(ua, "confab/dev") {
			t.Errorf("expected 'confab/dev' in user agent, got %q", ua)
		}
	})
}

func TestClient_RetryOn429(t *testing.T) {
	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.Header().Set("Retry-After", "0") // instant retry for test speed
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(&config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "test-key",
	}, 0)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	var resp struct{ Ok bool }
	err = client.Get("/test", &resp)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts (2 retries + 1 success), got %d", attempts)
	}
}

func TestClient_RetryExhausted(t *testing.T) {
	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	client, err := NewClient(&config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "test-key",
	}, 0)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	var resp struct{ Ok bool }
	err = client.Get("/test", &resp)
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if !errors.Is(err, errRateLimited) {
		t.Errorf("expected errRateLimited, got: %v", err)
	}
	// maxRetries+1 attempts total (0..maxRetries inclusive)
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
}

func TestClient_GetRawToWriter(t *testing.T) {
	t.Run("streams response to writer", func(t *testing.T) {
		want := "line1\nline2\nline3\n"
		var receivedAuth string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(want))
		}))
		defer server.Close()

		client, err := NewClient(&config.UploadConfig{
			BackendURL: server.URL,
			APIKey:     "test-key",
		}, 0)
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}

		var buf bytes.Buffer
		if err := client.GetRawToWriter("/download", &buf); err != nil {
			t.Fatalf("GetRawToWriter() error = %v", err)
		}

		if buf.String() != want {
			t.Errorf("got %q, want %q", buf.String(), want)
		}
		if receivedAuth != "Bearer test-key" {
			t.Errorf("auth header = %q, want %q", receivedAuth, "Bearer test-key")
		}
	})

	t.Run("returns ErrSessionNotFound on 404", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`))
		}))
		defer server.Close()

		client, err := NewClient(&config.UploadConfig{
			BackendURL: server.URL,
			APIKey:     "test-key",
		}, 0)
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}

		var buf bytes.Buffer
		err = client.GetRawToWriter("/download", &buf)
		if err == nil {
			t.Fatal("expected error for 404")
		}
		if !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("expected ErrSessionNotFound, got: %v", err)
		}
	})

	t.Run("returns ErrUnauthorized on 401", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
		}))
		defer server.Close()

		client, err := NewClient(&config.UploadConfig{
			BackendURL: server.URL,
			APIKey:     "bad-key",
		}, 0)
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}

		var buf bytes.Buffer
		err = client.GetRawToWriter("/download", &buf)
		if err == nil {
			t.Fatal("expected error for 401")
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("expected ErrUnauthorized, got: %v", err)
		}
	})

	t.Run("returns errRateLimited on 429 without retry", func(t *testing.T) {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
		}))
		defer server.Close()

		client, err := NewClient(&config.UploadConfig{
			BackendURL: server.URL,
			APIKey:     "test-key",
		}, 0)
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}

		var buf bytes.Buffer
		err = client.GetRawToWriter("/download", &buf)
		if err == nil {
			t.Fatal("expected error for 429")
		}
		if !errors.Is(err, errRateLimited) {
			t.Errorf("expected errRateLimited, got: %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected exactly 1 attempt (no retry), got %d", attempts)
		}
	})
}

func TestClient_ErrUnauthorized(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
		wantUnauth bool
	}{
		{"401 returns ErrUnauthorized", http.StatusUnauthorized, true, true},
		{"403 returns ErrUnauthorized", http.StatusForbidden, true, true},
		{"404 returns error but not ErrUnauthorized", http.StatusNotFound, true, false},
		{"500 returns error but not ErrUnauthorized", http.StatusInternalServerError, true, false},
		{"200 returns no error", http.StatusOK, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(`{"error":"test error"}`))
			}))
			defer server.Close()

			client, err := NewClient(&config.UploadConfig{
				BackendURL: server.URL,
				APIKey:     "test-key",
			}, 0)
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			var resp map[string]interface{}
			err = client.Get("/test", &resp)

			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}

			if tt.wantUnauth {
				if !errors.Is(err, ErrUnauthorized) {
					t.Errorf("expected ErrUnauthorized, got %v", err)
				}
			} else if err != nil && errors.Is(err, ErrUnauthorized) {
				t.Errorf("did not expect ErrUnauthorized for status %d", tt.statusCode)
			}
		})
	}
}

// ============================================================================
// S14 additions — Patch, SetUserAgent, network error, maxResponseSize
// ============================================================================

func TestClient_Patch(t *testing.T) {
	var receivedMethod, receivedPath string
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	client, err := NewClient(&config.UploadConfig{BackendURL: server.URL, APIKey: "k"}, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var resp struct{ Status string }
	if err := client.Patch("/api/v1/things/abc", map[string]string{"k": "v"}, &resp); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if receivedMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", receivedMethod)
	}
	if receivedPath != "/api/v1/things/abc" {
		t.Errorf("path = %q, want /api/v1/things/abc", receivedPath)
	}
	if receivedBody["k"] != "v" {
		t.Errorf("body[k] = %q, want v", receivedBody["k"])
	}
	if resp.Status != "ok" {
		t.Errorf("resp.Status = %q, want ok", resp.Status)
	}
}

func TestClient_SetUserAgent(t *testing.T) {
	var receivedUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer server.Close()

	prevUA := userAgent
	SetUserAgent("test-agent/9.9 (audit)")
	t.Cleanup(func() { SetUserAgent(prevUA) })

	client, err := NewClient(&config.UploadConfig{BackendURL: server.URL, APIKey: "k"}, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var resp struct{ Ok bool }
	if err := client.Post("/x", map[string]string{"a": "b"}, &resp); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if receivedUA != "test-agent/9.9 (audit)" {
		t.Errorf("server saw User-Agent %q, want %q", receivedUA, "test-agent/9.9 (audit)")
	}
}

// TestClient_NetworkError covers the connection-refused path (port 1
// is reserved and unbound, producing a deterministic refusal on any
// platform). Prior tests only exercised HTTP-status errors.
func TestClient_NetworkError(t *testing.T) {
	client, err := NewClient(&config.UploadConfig{BackendURL: "http://127.0.0.1:1", APIKey: "k"}, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var resp struct{}
	err = client.Post("/anything", map[string]string{}, &resp)
	if err == nil {
		t.Fatal("expected error from connection-refused, got nil")
	}
	// Don't pin on specific error type — net errors are platform-flavored.
	// But it must NOT be one of our HTTP-status sentinels.
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrSessionNotFound) {
		t.Errorf("network error mis-categorized as HTTP-status sentinel: %v", err)
	}
}

// TestClient_MaxResponseSize covers the maxResponseSize cap. Uses
// SetMaxResponseSizeForTest to keep memory usage trivial; without
// the var seam we'd have to allocate 32MB.
func TestClient_MaxResponseSize(t *testing.T) {
	// Cap at 100 bytes for this test.
	restore := SetMaxResponseSizeForTest(100)
	defer restore()

	// Server sends a 500-byte JSON-shaped response. With a 100-byte cap
	// the client will read at most 100 bytes — that truncates inside
	// the JSON, so the decode must fail (the partial body is invalid
	// JSON). We're not asserting on the specific error type, only that
	// the client doesn't silently swallow the truncation.
	bigJSON := `{"data":"` + strings.Repeat("A", 500) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(bigJSON))
	}))
	defer server.Close()

	client, err := NewClient(&config.UploadConfig{BackendURL: server.URL, APIKey: "k"}, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var resp struct {
		Data string `json:"data"`
	}
	err = client.Post("/x", map[string]string{}, &resp)
	if err == nil {
		t.Errorf("expected error decoding truncated response, got nil; resp = %+v", resp)
	}
}
