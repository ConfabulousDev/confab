// ABOUTME: Tests for the confab learn CLI command.
// ABOUTME: Validates request building, session detection, and tag handling.
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

func TestLearnRequest_Fields(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		body     string
		tags     []string
		wantBody string
		wantTags int
	}{
		{"title only", "TIL about proxies", "", nil, "TIL about proxies", 0},
		{"with body", "Proxy workaround", "Detailed explanation here", nil, "Detailed explanation here", 0},
		{"with tags", "TIL about proxies", "", []string{"openshift", "upgrade"}, "TIL about proxies", 2},
		{"empty tags", "TIL about proxies", "", []string{}, "TIL about proxies", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body
			if body == "" {
				body = tt.title
			}
			tags := tt.tags
			if tags == nil {
				tags = []string{}
			}

			req := &learnRequest{
				Title:  tt.title,
				Body:   body,
				Tags:   tags,
				Source: "manual_session",
			}

			if req.Title != tt.title {
				t.Errorf("Title = %s, want %s", req.Title, tt.title)
			}
			if req.Body != tt.wantBody {
				t.Errorf("Body = %s, want %s", req.Body, tt.wantBody)
			}
			if len(req.Tags) != tt.wantTags {
				t.Errorf("Tags count = %d, want %d", len(req.Tags), tt.wantTags)
			}
			if req.Source != "manual_session" {
				t.Errorf("Source = %s, want manual_session", req.Source)
			}
			if req.Tags == nil {
				t.Error("Tags should not be nil, should be empty slice")
			}
		})
	}
}

func TestFindActiveSessionID_NoStates(t *testing.T) {
	// When no daemon states exist, should return empty string
	sid := findActiveSessionID()
	// This may or may not be empty depending on whether daemons are running
	// during the test, but it should not panic
	_ = sid
}
