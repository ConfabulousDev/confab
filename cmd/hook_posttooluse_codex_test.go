package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// linkRecorder is a tiny httptest backend that captures any
// POST /api/v1/sessions/<id>/github-links call so tests can assert the URL
// that the handler emitted.
type linkRecorder struct {
	mu        sync.Mutex
	path      string
	body      map[string]any
	hits      int
	statusOut int
}

func newLinkRecorder() *linkRecorder { return &linkRecorder{statusOut: http.StatusCreated} }

func (lr *linkRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/sessions/") ||
		!strings.HasSuffix(r.URL.Path, "/github-links") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lr.path = r.URL.Path
	lr.hits++
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &lr.body)
	w.WriteHeader(lr.statusOut)
	_, _ = w.Write([]byte(`{"id":"link-1","url":"","created_at":"now"}`))
}

// configureCodexLinkTestEnv sets up the temp HOME, config file pointing at
// the test backend, and the --provider=codex flag.
func configureCodexLinkTestEnv(t *testing.T, serverURL string) {
	t.Helper()
	withHookProvider(t, provider.NameCodex)

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	cfg := &config.UploadConfig{BackendURL: serverURL, APIKey: "cfb_codex-link-test-key-1234"}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("SaveUploadConfig: %v", err)
	}
	if err := os.MkdirAll(tempHome+"/.confab/sync/codex", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

// TestHandlePostToolUse_CodexPRCreateWalksUpAndLinks proves the highest-impact
// CF-492 correctness bullet for PostToolUse: a subagent-initiated `gh pr
// create` must extract the PR URL, walk up to the root's daemon state, and
// POST the link under the ROOT's confab session ID — not the subagent's.
func TestHandlePostToolUse_CodexPRCreateWalksUpAndLinks(t *testing.T) {
	rec := newLinkRecorder()
	server := httptest.NewServer(rec)
	defer server.Close()
	configureCodexLinkTestEnv(t, server.URL)

	fixture := codextest.NewFixture(t)
	root := fixture.AddRoot("11111111-1111-1111-1111-111111111111")
	child := fixture.AddSubagent(root.ThreadUUID(),
		"22222222-2222-2222-2222-222222222222",
		codextest.SubagentOpts{AgentRole: "tester"})

	const confabRoot = "confab-codex-root-pr"
	rootState := daemon.NewStateForProvider(provider.NameCodex, root.ThreadUUID(), root.Path(), "/work", 0)
	rootState.ConfabSessionID = confabRoot
	if err := rootState.Save(); err != nil {
		t.Fatalf("save root state: %v", err)
	}

	const prURL = "https://github.com/example/repo/pull/777"
	input := types.CodexHookInput{
		SessionID:     child.ThreadUUID(),
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "gh pr create --title 'subagent PR'"},
		ToolResponse:  map[string]any{"stdout": prURL + "\n", "exit_code": float64(0)},
	}
	body, _ := json.Marshal(input)

	if err := handlePostToolUse(bytes.NewReader(body), &bytes.Buffer{}); err != nil {
		t.Fatalf("handlePostToolUse: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.hits != 1 {
		t.Fatalf("expected exactly 1 LinkGitHub POST, got %d", rec.hits)
	}
	if !strings.Contains(rec.path, "/sessions/"+confabRoot+"/") {
		t.Fatalf("link POST path = %q, want path containing /sessions/%s/", rec.path, confabRoot)
	}
	gotURL, _ := rec.body["url"].(string)
	if gotURL != prURL {
		t.Errorf("link body url = %q, want %q", gotURL, prURL)
	}
}

// TestHandlePostToolUse_CodexNoState_SilentNoOp covers the case where
// neither the firing UUID nor its root carry daemon state — the handler
// must emit no HTTP request and no error.
func TestHandlePostToolUse_CodexNoState_SilentNoOp(t *testing.T) {
	rec := newLinkRecorder()
	server := httptest.NewServer(rec)
	defer server.Close()
	configureCodexLinkTestEnv(t, server.URL)

	const prURL = "https://github.com/example/repo/pull/8"
	input := types.CodexHookInput{
		SessionID:     "ffffffff-ffff-ffff-ffff-ffffffffffff",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "gh pr create"},
		ToolResponse:  map[string]any{"stdout": prURL, "exit_code": float64(0)},
	}
	body, _ := json.Marshal(input)

	if err := handlePostToolUse(bytes.NewReader(body), &bytes.Buffer{}); err != nil {
		t.Fatalf("handlePostToolUse: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.hits != 0 {
		t.Errorf("expected zero LinkGitHub POSTs when no state, got %d (path=%q)", rec.hits, rec.path)
	}
}

// TestHandlePostToolUse_CodexNonBashTool exercises the early return path
// for tool names other than Bash. Codex doesn't install the GitHub MCP
// matcher, so this is the only non-Bash path that matters for Codex.
func TestHandlePostToolUse_CodexNonBashTool(t *testing.T) {
	rec := newLinkRecorder()
	server := httptest.NewServer(rec)
	defer server.Close()
	configureCodexLinkTestEnv(t, server.URL)

	input := types.CodexHookInput{
		SessionID:     "33333333-3333-3333-3333-333333333333",
		HookEventName: "PostToolUse",
		ToolName:      "Read",
		ToolInput:     map[string]any{"file_path": "/tmp/x"},
		ToolResponse:  map[string]any{"stdout": "ok"},
	}
	body, _ := json.Marshal(input)

	if err := handlePostToolUse(bytes.NewReader(body), &bytes.Buffer{}); err != nil {
		t.Fatalf("handlePostToolUse: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.hits != 0 {
		t.Errorf("expected zero POSTs for non-Bash tool, got %d", rec.hits)
	}
}
