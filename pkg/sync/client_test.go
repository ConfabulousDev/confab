package sync

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	pkghttp "github.com/ConfabulousDev/confab/pkg/http"
)

func TestClient_LinkGitHub_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/sessions/") || !strings.HasSuffix(r.URL.Path, "/github-links") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Parse request body
		var req GitHubLinkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if req.URL != "https://github.com/owner/repo/pull/123" {
			t.Errorf("expected URL 'https://github.com/owner/repo/pull/123', got %q", req.URL)
		}
		if req.Source != "cli_hook" {
			t.Errorf("expected source 'cli_hook', got %q", req.Source)
		}

		// Return success response
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(GitHubLinkResponse{
			ID:       1,
			LinkType: "pull_request",
			URL:      req.URL,
			Owner:    "owner",
			Repo:     "repo",
			Ref:      "123",
		})
	}))
	defer server.Close()

	client := mustNewTestClient(t, server.URL)
	resp, err := client.LinkGitHub("test-session-id", &GitHubLinkRequest{
		URL:    "https://github.com/owner/repo/pull/123",
		Source: "cli_hook",
	})

	if err != nil {
		t.Fatalf("LinkGitHub failed: %v", err)
	}
	if resp.ID != 1 {
		t.Errorf("expected ID 1, got %d", resp.ID)
	}
	if resp.LinkType != "pull_request" {
		t.Errorf("expected link_type 'pull_request', got %q", resp.LinkType)
	}
	if resp.Owner != "owner" {
		t.Errorf("expected owner 'owner', got %q", resp.Owner)
	}
	if resp.Repo != "repo" {
		t.Errorf("expected repo 'repo', got %q", resp.Repo)
	}
	if resp.Ref != "123" {
		t.Errorf("expected ref '123', got %q", resp.Ref)
	}
}

func TestClient_LinkGitHub_WithTitle(t *testing.T) {
	var receivedTitle string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req GitHubLinkRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedTitle = req.Title

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(GitHubLinkResponse{
			ID:       1,
			LinkType: "pull_request",
			URL:      req.URL,
		})
	}))
	defer server.Close()

	client := mustNewTestClient(t, server.URL)
	_, err := client.LinkGitHub("test-session-id", &GitHubLinkRequest{
		URL:    "https://github.com/owner/repo/pull/456",
		Title:  "Add new feature",
		Source: "cli_hook",
	})

	if err != nil {
		t.Fatalf("LinkGitHub failed: %v", err)
	}
	if receivedTitle != "Add new feature" {
		t.Errorf("expected title 'Add new feature', got %q", receivedTitle)
	}
}

func TestClient_LinkGitHub_Duplicate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 409 Conflict for duplicate
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "link already exists",
		})
	}))
	defer server.Close()

	client := mustNewTestClient(t, server.URL)
	_, err := client.LinkGitHub("test-session-id", &GitHubLinkRequest{
		URL:    "https://github.com/owner/repo/pull/123",
		Source: "cli_hook",
	})

	if err == nil {
		t.Error("expected error for duplicate link")
	}
	if !strings.Contains(err.Error(), "link github failed") {
		t.Errorf("expected 'link github failed' in error, got: %v", err)
	}
	// Verify caller can detect conflict with errors.Is
	if !errors.Is(err, pkghttp.ErrConflict) {
		t.Errorf("expected errors.Is(err, ErrConflict) to be true, got false for: %v", err)
	}
}

func TestClient_LinkGitHub_SessionNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "session not found",
		})
	}))
	defer server.Close()

	client := mustNewTestClient(t, server.URL)
	_, err := client.LinkGitHub("nonexistent-session", &GitHubLinkRequest{
		URL:    "https://github.com/owner/repo/pull/123",
		Source: "cli_hook",
	})

	if err == nil {
		t.Error("expected error for session not found")
	}
}

func TestClient_LinkGitHub_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid API key",
		})
	}))
	defer server.Close()

	client := mustNewTestClient(t, server.URL)
	_, err := client.LinkGitHub("test-session-id", &GitHubLinkRequest{
		URL:    "https://github.com/owner/repo/pull/123",
		Source: "cli_hook",
	})

	if err == nil {
		t.Error("expected error for unauthorized")
	}
}

func mustNewTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	cfg := &config.UploadConfig{
		BackendURL: serverURL,
		APIKey:     "test-api-key-12345678",
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}

// ============================================================================
// ChunkMetadata.CodexRollout wire format (CF-387)
// ============================================================================

func TestChunkMetadata_CodexRollout_JSONMarshal_IncludedWhenSet(t *testing.T) {
	meta := &ChunkMetadata{
		CodexRollout: &CodexRolloutMetadata{
			ThreadUUID:       "abc-uuid",
			ParentThreadUUID: "root-uuid",
			RolloutPath:      "/codex/sessions/2026/01/01/rollout-abc.jsonl",
			CWD:              "/work",
			Model:            "gpt-5",
			Source:           "cli",
			ThreadSource:     "agent",
			AgentPath:        "~/agents/planner.md",
			AgentRole:        "planner",
			AgentNickname:    "Planny",
		},
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`"codex_rollout"`,
		`"thread_uuid":"abc-uuid"`,
		`"parent_thread_uuid":"root-uuid"`,
		`"rollout_path":"/codex/sessions/2026/01/01/rollout-abc.jsonl"`,
		`"cwd":"/work"`,
		`"model":"gpt-5"`,
		`"source":"cli"`,
		`"thread_source":"agent"`,
		`"agent_path":"~/agents/planner.md"`,
		`"agent_role":"planner"`,
		`"agent_nickname":"Planny"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON missing %s; full payload: %s", want, got)
		}
	}
}

func TestChunkMetadata_CodexRollout_JSONMarshal_OmittedWhenNil(t *testing.T) {
	meta := &ChunkMetadata{
		Summary: "x", // ensure the wrapper itself isn't empty
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "codex_rollout") {
		t.Errorf("expected codex_rollout omitted when nil; got %s", b)
	}
}

func TestChunkMetadata_CodexRollout_JSONMarshal_OmitsEmptyOptionalStringFields(t *testing.T) {
	// Only required fields set; optional fields should be elided.
	meta := &ChunkMetadata{
		CodexRollout: &CodexRolloutMetadata{
			ThreadUUID:  "root-uuid",
			RolloutPath: "/codex/sessions/2026/01/01/rollout-root.jsonl",
		},
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)
	for _, wantPresent := range []string{`"thread_uuid":"root-uuid"`, `"rollout_path":"`} {
		if !strings.Contains(got, wantPresent) {
			t.Errorf("expected %s present; got %s", wantPresent, got)
		}
	}
	for _, wantAbsent := range []string{
		"parent_thread_uuid",
		`"cwd":`,
		`"model":`,
		`"source":`,
		`"thread_source":`,
		`"agent_path":`,
		`"agent_role":`,
		`"agent_nickname":`,
	} {
		if strings.Contains(got, wantAbsent) {
			t.Errorf("expected %s omitted (empty optional); got %s", wantAbsent, got)
		}
	}
}

func TestChunkMetadata_CodexRollout_JSONUnmarshal_RoundTrips(t *testing.T) {
	original := &ChunkMetadata{
		CodexRollout: &CodexRolloutMetadata{
			ThreadUUID:       "abc",
			ParentThreadUUID: "root",
			RolloutPath:      "/codex/rollout-abc.jsonl",
			ThreadSource:     "agent",
			AgentRole:        "planner",
		},
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded ChunkMetadata
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.CodexRollout == nil {
		t.Fatal("decoded.CodexRollout = nil; want populated")
	}
	if decoded.CodexRollout.ThreadUUID != "abc" ||
		decoded.CodexRollout.ParentThreadUUID != "root" ||
		decoded.CodexRollout.RolloutPath != "/codex/rollout-abc.jsonl" ||
		decoded.CodexRollout.ThreadSource != "agent" ||
		decoded.CodexRollout.AgentRole != "planner" {
		t.Errorf("decoded payload doesn't match original: %+v", decoded.CodexRollout)
	}
}
