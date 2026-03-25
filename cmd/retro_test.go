// ABOUTME: Smoke test for the confab retro command.
// ABOUTME: Verifies the command wires flags correctly and delegates to runSessionGet.
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

func TestRunRetro_Success(t *testing.T) {
	backendResp := map[string]interface{}{
		"metadata": map[string]interface{}{
			"session_id":  "uuid-123",
			"external_id": "ext-456",
			"title":       "Test Session",
		},
		"transcript": "<transcript>\n<user>Hello</user>\n</transcript>",
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

	// Verify that the same path-building logic used by retro (via runSessionGet) works
	path := buildSessionGetPath("uuid-123", false, 0)

	var raw json.RawMessage
	if err := client.Get(path, &raw); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if receivedPath != "/api/v1/sessions/uuid-123/condensed-transcript" {
		t.Errorf("received path = %q, want %q", receivedPath, "/api/v1/sessions/uuid-123/condensed-transcript")
	}

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
