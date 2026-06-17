package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// setupCursorTestState installs a Cursor daemon-state record + backend config
// under a temp HOME, mirroring setupCodexTestState.
func setupCursorTestState(t *testing.T, sessionID, confabSessionID string) {
	t.Helper()
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := &config.UploadConfig{BackendURL: testBackendURL, APIKey: "cfb_cursor-test-key-1234567890"}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("SaveUploadConfig: %v", err)
	}

	syncDir := filepath.Join(tempHome, ".confab", "sync", provider.NameCursor)
	if err := os.MkdirAll(syncDir, 0o700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}
	state := daemon.NewStateForProvider(provider.NameCursor, sessionID, "/fake/transcript.jsonl", "/work", 0)
	state.ConfabSessionID = confabSessionID
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}
}

// cursorPreToolUsePayload builds a Cursor preToolUse JSON payload for a Shell
// command.
func cursorPreToolUsePayload(sessionID, command string) []byte {
	in := types.CursorToolUseHookInput{
		SessionID: sessionID,
		ToolName:  "Shell",
		ToolUseID: "tool-1",
		ToolInput: map[string]any{"command": command},
		CWD:       "/work",
	}
	body, _ := json.Marshal(in)
	return body
}

// decodeCursorPreResponse decodes the Cursor preToolUse response shape.
func decodeCursorPreResponse(t *testing.T, b []byte) types.CursorToolUseResponse {
	t.Helper()
	var got types.CursorToolUseResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal cursor response: %v\n%s", err, b)
	}
	return got
}

// TestHandlePreToolUse_CursorGitCommitRewrite verifies a Cursor Shell git
// commit is rewritten in-place (updated_input) to add the Confab-Link trailer,
// with permission "allow".
func TestHandlePreToolUse_CursorGitCommitRewrite(t *testing.T) {
	withHookProvider(t, provider.NameCursor)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000001"
	const confabSessionID = "confab-cursor-001"
	setupCursorTestState(t, sessionID, confabSessionID)

	body := cursorPreToolUsePayload(sessionID, `git commit -m "wip"`)
	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}

	got := decodeCursorPreResponse(t, w.Bytes())
	if got.Permission != "allow" {
		t.Errorf("permission = %q, want allow", got.Permission)
	}
	if got.UpdatedInput == nil {
		t.Fatalf("updated_input missing; want rewritten command\n%s", w.String())
	}
	cmd, _ := got.UpdatedInput["command"].(string)
	wantURL := testBackendURL + "/sessions/" + confabSessionID
	if !strings.Contains(cmd, "Confab-Link: "+wantURL) {
		t.Errorf("rewritten command missing Confab-Link trailer with %q:\n%s", wantURL, cmd)
	}
	if !strings.Contains(cmd, "--trailer") {
		t.Errorf("rewritten commit should inject a --trailer:\n%s", cmd)
	}
}

// TestHandlePreToolUse_CursorPRCreateRewrite verifies a Cursor Shell gh pr
// create is rewritten to include the Confab link line in the body.
func TestHandlePreToolUse_CursorPRCreateRewrite(t *testing.T) {
	withHookProvider(t, provider.NameCursor)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000002"
	const confabSessionID = "confab-cursor-002"
	setupCursorTestState(t, sessionID, confabSessionID)

	body := cursorPreToolUsePayload(sessionID, `gh pr create --title "x" --body "hello"`)
	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}

	got := decodeCursorPreResponse(t, w.Bytes())
	if got.Permission != "allow" || got.UpdatedInput == nil {
		t.Fatalf("permission=%q updated_input=%v; want allow + rewrite\n%s", got.Permission, got.UpdatedInput, w.String())
	}
	cmd, _ := got.UpdatedInput["command"].(string)
	wantURL := testBackendURL + "/sessions/" + confabSessionID
	if !strings.Contains(cmd, wantURL) {
		t.Errorf("rewritten PR command missing session URL %q:\n%s", wantURL, cmd)
	}
	if !strings.Contains(cmd, "📝 [Confab link](") {
		t.Errorf("rewritten PR command missing Confab link line:\n%s", cmd)
	}
}

// TestRewriteCursorPRCommand_BodyForms is a focused unit test over the PR
// body-injection rewrite, covering the supported and declined forms.
func TestRewriteCursorPRCommand_BodyForms(t *testing.T) {
	const url = "https://test.example.com/sessions/sess-1"
	link := formatPRLink(url)

	tests := []struct {
		name       string
		command    string
		wantChange bool   // true if we expect a rewrite
		wantSubstr string // present in the rewritten command (when wantChange)
	}{
		{"double-quoted body", `gh pr create --title "t" --body "hello"`, true, link},
		{"single-quoted body", `gh pr create --body 'hello'`, true, link},
		{"short -b flag", `gh pr create -b "hi"`, true, link},
		{"no body flag", `gh pr create --title "t"`, true, "--body"},
		// Declined forms (return "" → caller allows original): must not append a
		// duplicate --body or corrupt the file reference.
		{"body equals form", `gh pr create --body="hello"`, false, ""},
		{"body-file form", `gh pr create --body-file pr.md`, false, ""},
		{"short -F form", `gh pr create -F pr.md`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteCursorPRCommand(tt.command, url)
			if tt.wantChange {
				if got == "" || got == tt.command {
					t.Fatalf("expected rewrite, got %q", got)
				}
				if !strings.Contains(got, tt.wantSubstr) {
					t.Errorf("rewrite %q missing %q", got, tt.wantSubstr)
				}
				// Never produce two --body flags.
				if strings.Count(got, "--body") > 1 {
					t.Errorf("rewrite produced duplicate --body: %q", got)
				}
			} else if got != "" {
				t.Errorf("expected no rewrite (empty), got %q", got)
			}
		})
	}
}

// TestRewriteCursorCommitCommand_QuotingIsShellSafe verifies the injected
// trailer value is single-quoted (no injection via the URL) and carries the
// session URL.
func TestRewriteCursorCommitCommand_QuotingIsShellSafe(t *testing.T) {
	const url = "https://test.example.com/sessions/sess-1"
	got := rewriteCursorCommitCommand(`git commit -m "wip"`, url)
	if !strings.Contains(got, `--trailer 'Confab-Link: `+url+`'`) {
		t.Errorf("commit rewrite missing single-quoted trailer: %q", got)
	}
}

// TestHandlePreToolUse_CursorIdempotent verifies a command that already
// carries the Confab link is allowed without a second rewrite.
func TestHandlePreToolUse_CursorIdempotent(t *testing.T) {
	withHookProvider(t, provider.NameCursor)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000003"
	const confabSessionID = "confab-cursor-003"
	setupCursorTestState(t, sessionID, confabSessionID)

	wantURL := testBackendURL + "/sessions/" + confabSessionID
	command := `git commit -m "wip" --trailer "Confab-Link: ` + wantURL + `"`
	body := cursorPreToolUsePayload(sessionID, command)

	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}
	got := decodeCursorPreResponse(t, w.Bytes())
	if got.Permission != "allow" {
		t.Errorf("permission = %q, want allow", got.Permission)
	}
	if got.UpdatedInput != nil {
		t.Errorf("expected no rewrite for already-linked command, got updated_input=%v", got.UpdatedInput)
	}
}

// TestHandlePreToolUse_CursorNonGitShell verifies a non-git/non-PR Shell
// command is allowed without a rewrite.
func TestHandlePreToolUse_CursorNonGitShell(t *testing.T) {
	withHookProvider(t, provider.NameCursor)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000004"
	const confabSessionID = "confab-cursor-004"
	setupCursorTestState(t, sessionID, confabSessionID)

	body := cursorPreToolUsePayload(sessionID, `ls -la`)
	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}
	got := decodeCursorPreResponse(t, w.Bytes())
	if got.Permission != "allow" {
		t.Errorf("permission = %q, want allow", got.Permission)
	}
	if got.UpdatedInput != nil {
		t.Errorf("non-git command should not be rewritten, got %v", got.UpdatedInput)
	}
}

// TestHandlePreToolUse_CursorNonShellTool verifies a non-Shell tool short
// circuits (allow, no rewrite).
func TestHandlePreToolUse_CursorNonShellTool(t *testing.T) {
	withHookProvider(t, provider.NameCursor)

	const sessionID = "124c525a-aaaa-bbbb-cccc-000000000005"
	const confabSessionID = "confab-cursor-005"
	setupCursorTestState(t, sessionID, confabSessionID)

	in := types.CursorToolUseHookInput{
		SessionID: sessionID,
		ToolName:  "Read",
		ToolInput: map[string]any{"path": "/tmp/x"},
	}
	body, _ := json.Marshal(in)

	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}
	got := decodeCursorPreResponse(t, w.Bytes())
	if got.Permission != "allow" || got.UpdatedInput != nil {
		t.Errorf("non-Shell tool: permission=%q updated_input=%v; want allow + no rewrite", got.Permission, got.UpdatedInput)
	}
}

// TestHandlePreToolUse_CursorNoState verifies a missing daemon state allows
// the command without a rewrite (best-effort linking).
func TestHandlePreToolUse_CursorNoState(t *testing.T) {
	withHookProvider(t, provider.NameCursor)

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	cfg := &config.UploadConfig{BackendURL: testBackendURL, APIKey: "cfb_cursor-test-key-1234567890"}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("SaveUploadConfig: %v", err)
	}

	body := cursorPreToolUsePayload("ffffffff-ffff-ffff-ffff-ffffffffffff", `git commit -m "x"`)
	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}
	got := decodeCursorPreResponse(t, w.Bytes())
	if got.Permission != "allow" {
		t.Errorf("permission = %q, want allow", got.Permission)
	}
	if got.UpdatedInput != nil {
		t.Errorf("no-state should not rewrite, got %v", got.UpdatedInput)
	}
}
