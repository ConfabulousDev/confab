package types

import (
	"strings"
	"testing"
)

// cursorPreToolUseFixture is the real preToolUse payload captured in the 65aq
// spike (cursor-agent CLI). Shell tool → tool_name="Shell" and tool_input
// carries the command/cwd/timeout.
const cursorPreToolUseFixture = `{"conversation_id":"124c525a-aaaa-bbbb-cccc-000000000001","generation_id":"gen-1","session_id":"124c525a-aaaa-bbbb-cccc-000000000001","cursor_version":"2026.06.15-abc1234","model":"composer-2.5-fast","user_email":"jackie@example.com","workspace_roots":["/Users/jackie/dev/confab"],"transcript_path":"/Users/jackie/.cursor/projects/Users-jackie-dev-confab/agent-transcripts/124c525a-aaaa-bbbb-cccc-000000000001/124c525a-aaaa-bbbb-cccc-000000000001.jsonl","cwd":"/Users/jackie/dev/confab","tool_name":"Shell","tool_use_id":"tool-123","tool_input":{"command":"git commit -m 'wip'","cwd":"/Users/jackie/dev/confab","timeout":120000}}`

// cursorPostToolUseFixture is the real postToolUse payload from the spike.
// tool_output is a JSON object {"output":"<raw stdout>","exitCode":N} carrying
// the raw terminal output (e.g. the commit line with SHA).
const cursorPostToolUseFixture = `{"conversation_id":"124c525a-aaaa-bbbb-cccc-000000000001","generation_id":"gen-1","session_id":"124c525a-aaaa-bbbb-cccc-000000000001","cursor_version":"2026.06.15-abc1234","model":"composer-2.5-fast","user_email":"jackie@example.com","workspace_roots":["/Users/jackie/dev/confab"],"transcript_path":"/Users/jackie/.cursor/projects/Users-jackie-dev-confab/agent-transcripts/124c525a-aaaa-bbbb-cccc-000000000001/124c525a-aaaa-bbbb-cccc-000000000001.jsonl","cwd":"/Users/jackie/dev/confab","tool_name":"Shell","tool_use_id":"tool-123","tool_input":{"command":"git commit -m 'wip'","cwd":"/Users/jackie/dev/confab","timeout":120000},"tool_output":{"output":"[main (root-commit) c5d7fdf] wip\n 1 file changed","exitCode":0},"duration":42}`

func TestReadCursorToolUseHookInput_PreToolUse(t *testing.T) {
	in, err := ReadCursorToolUseHookInput(strings.NewReader(cursorPreToolUseFixture))
	if err != nil {
		t.Fatalf("ReadCursorToolUseHookInput: %v", err)
	}
	if in.SessionID != "124c525a-aaaa-bbbb-cccc-000000000001" {
		t.Errorf("SessionID = %q", in.SessionID)
	}
	if in.ToolName != "Shell" {
		t.Errorf("ToolName = %q, want Shell", in.ToolName)
	}
	if in.ToolUseID != "tool-123" {
		t.Errorf("ToolUseID = %q", in.ToolUseID)
	}
	cmd, _ := in.ToolInput["command"].(string)
	if cmd != "git commit -m 'wip'" {
		t.Errorf("command = %q", cmd)
	}
	if in.CWD != "/Users/jackie/dev/confab" {
		t.Errorf("CWD = %q", in.CWD)
	}
	wantPath := "/Users/jackie/.cursor/projects/Users-jackie-dev-confab/agent-transcripts/124c525a-aaaa-bbbb-cccc-000000000001/124c525a-aaaa-bbbb-cccc-000000000001.jsonl"
	if in.TranscriptPath != wantPath {
		t.Errorf("TranscriptPath = %q", in.TranscriptPath)
	}
	// No tool_output on the preToolUse payload.
	out, ok := in.ToolOutput()
	if ok {
		t.Errorf("ToolOutput present on preToolUse payload: %+v", out)
	}
}

func TestReadCursorToolUseHookInput_PostToolUse(t *testing.T) {
	in, err := ReadCursorToolUseHookInput(strings.NewReader(cursorPostToolUseFixture))
	if err != nil {
		t.Fatalf("ReadCursorToolUseHookInput: %v", err)
	}
	out, ok := in.ToolOutput()
	if !ok {
		t.Fatal("ToolOutput not decoded from postToolUse payload")
	}
	if out.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", out.ExitCode)
	}
	if !strings.Contains(out.Output, "c5d7fdf") {
		t.Errorf("Output missing SHA: %q", out.Output)
	}
}

func TestReadCursorToolUseHookInput_MissingSessionID(t *testing.T) {
	_, err := ReadCursorToolUseHookInput(strings.NewReader(`{"tool_name":"Shell"}`))
	if err == nil {
		t.Fatal("expected error for missing session_id")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("error = %q, want session_id error", err)
	}
}

func TestReadCursorToolUseHookInput_InvalidSessionID(t *testing.T) {
	_, err := ReadCursorToolUseHookInput(strings.NewReader(`{"session_id":"../../evil","tool_name":"Shell"}`))
	if err == nil {
		t.Fatal("expected error for path-traversal session_id")
	}
}

func TestReadCursorToolUseHookInput_InvalidJSON(t *testing.T) {
	_, err := ReadCursorToolUseHookInput(strings.NewReader("not-json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestCursorToolOutput_NonZeroExit confirms a failed command's exit code is
// surfaced so the link-back path can skip on failure.
func TestCursorToolOutput_NonZeroExit(t *testing.T) {
	payload := `{"session_id":"s1","tool_name":"Shell","tool_output":{"output":"fatal: nothing to commit","exitCode":1}}`
	in, err := ReadCursorToolUseHookInput(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("ReadCursorToolUseHookInput: %v", err)
	}
	out, ok := in.ToolOutput()
	if !ok {
		t.Fatal("ToolOutput not decoded")
	}
	if out.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", out.ExitCode)
	}
}
