package cmd

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// configureCursorLinkTestEnv sets temp HOME, config pointing at the test
// backend, and the --provider=cursor flag.
func configureCursorLinkTestEnv(t *testing.T, serverURL string) {
	t.Helper()
	withHookProvider(t, provider.NameCursor)

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	cfg := &config.UploadConfig{BackendURL: serverURL, APIKey: "cfb_cursor-link-test-key-1234"}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("SaveUploadConfig: %v", err)
	}
	if err := os.MkdirAll(tempHome+"/.confab/sync/cursor", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

// cursorPostToolUsePayload builds a Cursor postToolUse JSON payload with a
// tool_output object {output, exitCode}.
func cursorPostToolUsePayload(t *testing.T, sessionID, command, cwd, output string, exitCode int) []byte {
	t.Helper()
	in := types.CursorToolUseHookInput{
		SessionID: sessionID,
		ToolName:  "Shell",
		ToolUseID: "tool-1",
		ToolInput: map[string]any{"command": command},
		CWD:       cwd,
	}
	raw, _ := json.Marshal(map[string]any{"output": output, "exitCode": exitCode})
	in.ToolOutputRaw = json.RawMessage(raw)
	body, _ := json.Marshal(in)
	return body
}

// initGitRepoWithCommit creates a throwaway git repo with one commit on a
// GitHub remote, returning the dir and the full HEAD SHA.
func initGitRepoWithCommit(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("remote", "add", "origin", "https://github.com/example/repo.git")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "wip")
	shaCmd := exec.Command("git", "rev-parse", "HEAD")
	shaCmd.Dir = dir
	out, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return dir, strings.TrimSpace(string(out))
}

// TestHandlePostToolUse_CursorCommitLinks verifies a successful Cursor Shell
// git commit links the GitHub commit URL (full SHA from the repo) under the
// firing session.
func TestHandlePostToolUse_CursorCommitLinks(t *testing.T) {
	rec := newLinkRecorder()
	server := httptest.NewServer(rec)
	defer server.Close()
	configureCursorLinkTestEnv(t, server.URL)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000010"
	const confabSessionID = "confab-cursor-commit"
	state := daemon.NewStateForProvider(provider.NameCursor, sessionID, "/fake/t.jsonl", "/work", 0)
	state.ConfabSessionID = confabSessionID
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	repoDir, sha := initGitRepoWithCommit(t)
	output := "[main (root-commit) " + sha[:7] + "] wip\n 1 file changed"
	body := cursorPostToolUsePayload(t, sessionID, `git commit -m "wip"`, repoDir, output, 0)

	if err := handlePostToolUse(bytes.NewReader(body), &bytes.Buffer{}); err != nil {
		t.Fatalf("handlePostToolUse: %v", err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.hits != 1 {
		t.Fatalf("expected 1 link POST, got %d", rec.hits)
	}
	if !strings.Contains(rec.path, "/sessions/"+confabSessionID+"/") {
		t.Fatalf("link path = %q, want session %s", rec.path, confabSessionID)
	}
	gotURL, _ := rec.body["url"].(string)
	wantURL := "https://github.com/example/repo/commit/" + sha
	if gotURL != wantURL {
		t.Errorf("link url = %q, want %q", gotURL, wantURL)
	}
}

// TestHandlePostToolUse_CursorCommitFailedSkips verifies a non-zero exitCode
// skips link-back.
func TestHandlePostToolUse_CursorCommitFailedSkips(t *testing.T) {
	rec := newLinkRecorder()
	server := httptest.NewServer(rec)
	defer server.Close()
	configureCursorLinkTestEnv(t, server.URL)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000011"
	state := daemon.NewStateForProvider(provider.NameCursor, sessionID, "/fake/t.jsonl", "/work", 0)
	state.ConfabSessionID = "confab-cursor-fail"
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	repoDir, _ := initGitRepoWithCommit(t)
	body := cursorPostToolUsePayload(t, sessionID, `git commit -m "x"`, repoDir, "nothing to commit", 1)

	if err := handlePostToolUse(bytes.NewReader(body), &bytes.Buffer{}); err != nil {
		t.Fatalf("handlePostToolUse: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.hits != 0 {
		t.Errorf("expected zero POSTs on failed commit, got %d", rec.hits)
	}
}

// TestHandlePostToolUse_CursorPRCreateLinks verifies a Cursor gh pr create
// extracts the PR URL from tool_output.output and links it.
func TestHandlePostToolUse_CursorPRCreateLinks(t *testing.T) {
	rec := newLinkRecorder()
	server := httptest.NewServer(rec)
	defer server.Close()
	configureCursorLinkTestEnv(t, server.URL)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000012"
	const confabSessionID = "confab-cursor-pr"
	state := daemon.NewStateForProvider(provider.NameCursor, sessionID, "/fake/t.jsonl", "/work", 0)
	state.ConfabSessionID = confabSessionID
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	const prURL = "https://github.com/example/repo/pull/42"
	body := cursorPostToolUsePayload(t, sessionID, `gh pr create --title x`, "/work", prURL+"\n", 0)

	if err := handlePostToolUse(bytes.NewReader(body), &bytes.Buffer{}); err != nil {
		t.Fatalf("handlePostToolUse: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.hits != 1 {
		t.Fatalf("expected 1 PR link POST, got %d", rec.hits)
	}
	gotURL, _ := rec.body["url"].(string)
	if gotURL != prURL {
		t.Errorf("link url = %q, want %q", gotURL, prURL)
	}
}

// TestHandlePostToolUse_CursorNonShellTool verifies a non-Shell tool is a
// no-op (no link POST).
func TestHandlePostToolUse_CursorNonShellTool(t *testing.T) {
	rec := newLinkRecorder()
	server := httptest.NewServer(rec)
	defer server.Close()
	configureCursorLinkTestEnv(t, server.URL)

	in := types.CursorToolUseHookInput{
		SessionID: "124c525a-aaaa-bbbb-cccc-000000000013",
		ToolName:  "Read",
		ToolInput: map[string]any{"path": "/tmp/x"},
	}
	body, _ := json.Marshal(in)

	if err := handlePostToolUse(bytes.NewReader(body), &bytes.Buffer{}); err != nil {
		t.Fatalf("handlePostToolUse: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.hits != 0 {
		t.Errorf("expected zero POSTs for non-Shell tool, got %d", rec.hits)
	}
}
