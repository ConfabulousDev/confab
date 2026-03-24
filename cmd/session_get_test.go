// ABOUTME: Tests for the confab session get command.
// ABOUTME: Validates URL construction, JSON passthrough, and error handling.
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
)

func TestBuildSessionGetPath(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		externalID bool
		maxChars   int
		want       string
	}{
		{
			"uuid",
			"abc-123",
			false,
			0,
			"/api/v1/sessions/abc-123/condensed-transcript",
		},
		{
			"uuid with max-chars",
			"abc-123",
			false,
			5000,
			"/api/v1/sessions/abc-123/condensed-transcript?max_chars=5000",
		},
		{
			"external-id",
			"my-session",
			true,
			0,
			"/api/v1/sessions/condensed-transcript?external_id=my-session",
		},
		{
			"external-id with max-chars",
			"my-session",
			true,
			3000,
			"/api/v1/sessions/condensed-transcript?external_id=my-session&max_chars=3000",
		},
		{
			"uuid with special characters",
			"id/with spaces",
			false,
			0,
			"/api/v1/sessions/id%2Fwith%20spaces/condensed-transcript",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSessionGetPath(tt.id, tt.externalID, tt.maxChars)
			if got != tt.want {
				t.Errorf("buildSessionGetPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunSessionGet_Success(t *testing.T) {
	backendResp := map[string]interface{}{
		"metadata": map[string]interface{}{
			"session_id":  "uuid-123",
			"external_id": "ext-456",
			"title":       "Test Session",
			"total_lines": 100,
		},
		"transcript": "<transcript>\n<user id=\"1\">Hello</user>\n</transcript>",
	}

	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(backendResp)
	}))
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	path := buildSessionGetPath("uuid-123", false, 0)

	var raw json.RawMessage
	if err := client.Get(path, &raw); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if receivedPath != "/api/v1/sessions/uuid-123/condensed-transcript" {
		t.Errorf("received path = %q, want %q", receivedPath, "/api/v1/sessions/uuid-123/condensed-transcript")
	}

	// Verify raw JSON contains expected fields
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("Failed to parse raw JSON: %v", err)
	}
	if _, ok := parsed["metadata"]; !ok {
		t.Error("response missing 'metadata' field")
	}
	if _, ok := parsed["transcript"]; !ok {
		t.Error("response missing 'transcript' field")
	}
}

func TestRunSessionGet_ExternalID(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"metadata":   map[string]interface{}{"session_id": "resolved-uuid"},
			"transcript": "<transcript></transcript>",
		})
	}))
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	path := buildSessionGetPath("my-ext-id", true, 5000)

	var raw json.RawMessage
	if err := client.Get(path, &raw); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	want := "/api/v1/sessions/condensed-transcript?external_id=my-ext-id&max_chars=5000"
	if receivedPath != want {
		t.Errorf("received path = %q, want %q", receivedPath, want)
	}
}

func TestRunSessionGet_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Session not found"}`))
	}))
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "test-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	path := buildSessionGetPath("nonexistent", false, 0)

	var raw json.RawMessage
	err = client.Get(path, &raw)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestRunSessionGet_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"Unauthorized"}`))
	}))
	defer server.Close()

	cfg := &config.UploadConfig{BackendURL: server.URL, APIKey: "bad-key"}
	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	path := buildSessionGetPath("some-id", false, 0)

	var raw json.RawMessage
	err = client.Get(path, &raw)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}
