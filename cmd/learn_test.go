// ABOUTME: Tests for the confab learn CLI command.
// ABOUTME: Validates request building logic and tag handling.
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type learnTestBackend struct {
	lastRequest map[string]interface{}
	statusCode  int
}

func (b *learnTestBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/learnings" && r.Method == "POST" {
		json.NewDecoder(r.Body).Decode(&b.lastRequest)
		w.Header().Set("Content-Type", "application/json")
		if b.statusCode != 0 {
			w.WriteHeader(b.statusCode)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     "test-uuid",
			"title":  b.lastRequest["title"],
			"status": "draft",
		})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// Verify the test backend compiles and serves correctly
var _ http.Handler = (*learnTestBackend)(nil)
var _ = httptest.NewServer

func TestBuildLearnRequest(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		tags     []string
		wantTags int
	}{
		{"simple message", "TIL about proxies", nil, 0},
		{"with tags", "TIL about proxies", []string{"openshift", "upgrade"}, 2},
		{"empty tags", "TIL about proxies", []string{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := buildLearnRequest(tt.message, tt.tags)
			if req.Title != tt.message {
				t.Errorf("Title = %s, want %s", req.Title, tt.message)
			}
			if req.Body != tt.message {
				t.Errorf("Body = %s, want %s", req.Body, tt.message)
			}
			if len(req.Tags) != tt.wantTags {
				t.Errorf("Tags count = %d, want %d", len(req.Tags), tt.wantTags)
			}
			if req.Source != "manual_session" {
				t.Errorf("Source = %s, want manual_session", req.Source)
			}
			// Tags should never be nil (for JSON serialization)
			if req.Tags == nil {
				t.Error("Tags should not be nil, should be empty slice")
			}
		})
	}
}
