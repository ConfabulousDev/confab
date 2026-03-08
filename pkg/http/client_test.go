package http

import (
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
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got: %v", err)
	}
	// maxRetries+1 attempts total (0..maxRetries inclusive)
	if attempts != maxRetries+1 {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts)
	}
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
