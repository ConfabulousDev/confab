package types

import (
	"strings"
	"testing"
)

// cursorSessionStartFixture is the real CLI sessionStart payload captured in
// the T1 spike (kata issue 6kys). transcript_path is null at sessionStart.
const cursorSessionStartFixture = `{"session_id":"124c525a-aaaa-bbbb-cccc-000000000001","conversation_id":"124c525a-aaaa-bbbb-cccc-000000000001","generation_id":"124c525a-aaaa-bbbb-cccc-000000000001","model":"composer-2.5-fast","composer_mode":"ask","is_background_agent":false,"hook_event_name":"sessionStart","cursor_version":"2026.06.15-abc1234","workspace_roots":["/Users/jackie/dev/confab"],"user_email":"jackie@example.com","transcript_path":null}`

// cursorSessionEndFixture is the real CLI sessionEnd payload from T1. Here
// transcript_path is populated (matches the derived path) and reason/duration
// fields are present.
const cursorSessionEndFixture = `{"reason":"completed","duration_ms":5082,"final_status":"completed","session_id":"124c525a-aaaa-bbbb-cccc-000000000001","transcript_path":"/Users/jackie/.cursor/projects/Users-jackie-dev-confab/agent-transcripts/124c525a-aaaa-bbbb-cccc-000000000001/124c525a-aaaa-bbbb-cccc-000000000001.jsonl"}`

func TestReadCursorHookInput_SessionStart(t *testing.T) {
	in, err := ReadCursorHookInput(strings.NewReader(cursorSessionStartFixture))
	if err != nil {
		t.Fatalf("ReadCursorHookInput: %v", err)
	}
	if in.SessionID != "124c525a-aaaa-bbbb-cccc-000000000001" {
		t.Errorf("SessionID = %q", in.SessionID)
	}
	if in.ConversationID != in.SessionID {
		t.Errorf("ConversationID = %q, want == SessionID", in.ConversationID)
	}
	if in.Model != "composer-2.5-fast" {
		t.Errorf("Model = %q", in.Model)
	}
	if in.ComposerMode != "ask" {
		t.Errorf("ComposerMode = %q", in.ComposerMode)
	}
	if in.IsBackgroundAgent {
		t.Errorf("IsBackgroundAgent = true, want false")
	}
	if in.HookEventName != "sessionStart" {
		t.Errorf("HookEventName = %q", in.HookEventName)
	}
	if in.CursorVersion != "2026.06.15-abc1234" {
		t.Errorf("CursorVersion = %q", in.CursorVersion)
	}
	if len(in.WorkspaceRoots) != 1 || in.WorkspaceRoots[0] != "/Users/jackie/dev/confab" {
		t.Errorf("WorkspaceRoots = %v", in.WorkspaceRoots)
	}
	if in.UserEmail != "jackie@example.com" {
		t.Errorf("UserEmail = %q", in.UserEmail)
	}
	// transcript_path is null at sessionStart → empty string, not an error.
	if in.TranscriptPath != "" {
		t.Errorf("TranscriptPath = %q, want empty (null in payload)", in.TranscriptPath)
	}
}

func TestReadCursorHookInput_SessionEnd(t *testing.T) {
	in, err := ReadCursorHookInput(strings.NewReader(cursorSessionEndFixture))
	if err != nil {
		t.Fatalf("ReadCursorHookInput: %v", err)
	}
	if in.Reason != "completed" {
		t.Errorf("Reason = %q", in.Reason)
	}
	if in.FinalStatus != "completed" {
		t.Errorf("FinalStatus = %q", in.FinalStatus)
	}
	if in.DurationMS != 5082 {
		t.Errorf("DurationMS = %d, want 5082", in.DurationMS)
	}
	wantPath := "/Users/jackie/.cursor/projects/Users-jackie-dev-confab/agent-transcripts/124c525a-aaaa-bbbb-cccc-000000000001/124c525a-aaaa-bbbb-cccc-000000000001.jsonl"
	if in.TranscriptPath != wantPath {
		t.Errorf("TranscriptPath = %q, want %q", in.TranscriptPath, wantPath)
	}
}

func TestReadCursorHookInput_MissingSessionID(t *testing.T) {
	_, err := ReadCursorHookInput(strings.NewReader(`{"cwd":"/work"}`))
	if err == nil {
		t.Fatal("expected error for missing session_id")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("error = %q, want session_id error", err)
	}
}

func TestReadCursorHookInput_InvalidSessionID(t *testing.T) {
	_, err := ReadCursorHookInput(strings.NewReader(`{"session_id":"../../evil"}`))
	if err == nil {
		t.Fatal("expected error for path-traversal session_id")
	}
}

func TestReadCursorHookInput_InvalidJSON(t *testing.T) {
	_, err := ReadCursorHookInput(strings.NewReader("not-json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
