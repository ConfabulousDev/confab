package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// withHookProvider temporarily switches the package-level hookProviderName
// so the handler under test sees the desired provider, restoring on cleanup.
func withHookProvider(t *testing.T, name string) {
	t.Helper()
	orig := hookProviderName
	hookProviderName = name
	t.Cleanup(func() { hookProviderName = orig })
}

// setupCodexTestState installs a Codex daemon-state record for sessionID
// and a backend-URL config, returning a cleanup function. Mirrors
// setupTestState (used by the Claude-side handler tests) but writes under
// the Codex provider namespace.
func setupCodexTestState(t *testing.T, sessionID, confabSessionID string) {
	t.Helper()
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := &config.UploadConfig{BackendURL: testBackendURL, APIKey: "cfb_codex-test-key-1234567890"}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("SaveUploadConfig: %v", err)
	}

	syncDir := filepath.Join(tempHome, ".confab", "sync", "codex")
	if err := os.MkdirAll(syncDir, 0o700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}
	state := daemon.NewStateForProvider(provider.NameCodex, sessionID, "/fake/rollout.jsonl", "/fake/cwd", 0)
	state.ConfabSessionID = confabSessionID
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}
}

// TestHandlePreToolUse_CodexBashGitCommitDeny verifies that a Codex Bash
// git-commit invocation is denied with the Confab-Link instruction
// referencing the root session's confab ID.
func TestHandlePreToolUse_CodexBashGitCommitDeny(t *testing.T) {
	withHookProvider(t, provider.NameCodex)

	const codexSessionID = "01234567-89ab-cdef-0123-456789abcdef"
	const confabSessionID = "confab-codex-001"
	setupCodexTestState(t, codexSessionID, confabSessionID)

	input := types.CodexHookInput{
		SessionID:     codexSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "git commit -m 'wip'"},
	}
	body, _ := json.Marshal(input)

	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}

	var got types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, w.String())
	}
	if got.HookSpecificOutput == nil || got.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("permission decision = %+v, want deny", got.HookSpecificOutput)
	}
	if !strings.Contains(got.HookSpecificOutput.PermissionDecisionReason, confabSessionID) {
		t.Errorf("deny reason missing confab session ID %q:\n%s",
			confabSessionID, got.HookSpecificOutput.PermissionDecisionReason)
	}
	if !strings.Contains(got.HookSpecificOutput.PermissionDecisionReason, "Confab-Link:") {
		t.Errorf("deny reason missing 'Confab-Link:' trailer instruction:\n%s",
			got.HookSpecificOutput.PermissionDecisionReason)
	}
}

// TestHandlePreToolUse_CodexSubagentWalksUpToRoot exercises the highest-impact
// correctness bullet from CF-492: a subagent-initiated git commit must
// resolve to the root session's daemon state via the Codex thread tree,
// not silently drop because the subagent UUID has no state of its own.
func TestHandlePreToolUse_CodexSubagentWalksUpToRoot(t *testing.T) {
	withHookProvider(t, provider.NameCodex)

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := &config.UploadConfig{BackendURL: testBackendURL, APIKey: "cfb_codex-test-key-1234567890"}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("SaveUploadConfig: %v", err)
	}

	// Build a real Codex sessions tree + SQLite DB so Codex.WalkUpToRoot
	// runs end-to-end. The subagent has no daemon state; the root does.
	fixture := codextest.NewFixture(t)
	root := fixture.AddRoot("11111111-1111-1111-1111-111111111111")
	child := fixture.AddSubagent(root.ThreadUUID(),
		"22222222-2222-2222-2222-222222222222",
		codextest.SubagentOpts{AgentRole: "tester"})

	const confabRootSession = "confab-codex-root-session"
	rootState := daemon.NewStateForProvider(provider.NameCodex, root.ThreadUUID(), root.Path(), "/work", 0)
	rootState.ConfabSessionID = confabRootSession
	if err := rootState.Save(); err != nil {
		t.Fatalf("save root state: %v", err)
	}

	input := types.CodexHookInput{
		SessionID:     child.ThreadUUID(),
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "git commit -m 'subagent commit'"},
	}
	body, _ := json.Marshal(input)

	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}

	var got types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, w.String())
	}
	if got.HookSpecificOutput == nil || got.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("permission decision = %+v, want deny", got.HookSpecificOutput)
	}
	// The deny reason must reference the ROOT's confab session ID — proves
	// the walk-up resolved correctly. If walk-up was missing or broken, the
	// handler would emit no output (silent allow) because the subagent UUID
	// has no state of its own.
	if !strings.Contains(got.HookSpecificOutput.PermissionDecisionReason, confabRootSession) {
		t.Errorf("deny reason missing root confab session ID %q (walk-up failed?):\n%s",
			confabRootSession, got.HookSpecificOutput.PermissionDecisionReason)
	}
}

// TestHandlePreToolUse_CodexNoState_SilentAllow verifies the no-state path:
// when neither the firing UUID nor its root carry daemon state, the handler
// allows silently rather than emitting a deny.
func TestHandlePreToolUse_CodexNoState_SilentAllow(t *testing.T) {
	withHookProvider(t, provider.NameCodex)

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := &config.UploadConfig{BackendURL: testBackendURL, APIKey: "cfb_codex-test-key-1234567890"}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("SaveUploadConfig: %v", err)
	}

	input := types.CodexHookInput{
		SessionID:     "ffffffff-ffff-ffff-ffff-ffffffffffff",
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "git commit -m 'no state'"},
	}
	body, _ := json.Marshal(input)

	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}
	if w.Len() != 0 {
		t.Errorf("expected silent allow with no output, got: %s", w.String())
	}
}

// TestHandlePreToolUse_CodexNonBashTool exercises the non-bash short-circuit
// for Codex inputs. Codex pre-tool-use fires for every shell invocation and
// for any future MCP tool; the handler should ignore tools other than Bash.
func TestHandlePreToolUse_CodexNonBashTool(t *testing.T) {
	withHookProvider(t, provider.NameCodex)

	input := types.CodexHookInput{
		SessionID:     "33333333-3333-3333-3333-333333333333",
		HookEventName: "PreToolUse",
		ToolName:      "Read",
		ToolInput:     map[string]any{"file_path": "/tmp/x"},
	}
	body, _ := json.Marshal(input)

	var w bytes.Buffer
	if err := handlePreToolUse(bytes.NewReader(body), &w); err != nil {
		t.Fatalf("handlePreToolUse: %v", err)
	}
	if w.Len() != 0 {
		t.Errorf("expected silent allow for non-Bash tool, got: %s", w.String())
	}
}
