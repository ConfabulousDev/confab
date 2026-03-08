package discovery

import (
	"strings"
	"testing"
)

func TestReadHookInputFrom(t *testing.T) {
	t.Run("valid input with transcript_path", func(t *testing.T) {
		input := `{"session_id":"abc-123","transcript_path":"/tmp/test.jsonl"}`
		got, err := ReadHookInputFrom(strings.NewReader(input))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SessionID != "abc-123" {
			t.Errorf("SessionID = %q, want %q", got.SessionID, "abc-123")
		}
		if got.TranscriptPath != "/tmp/test.jsonl" {
			t.Errorf("TranscriptPath = %q, want %q", got.TranscriptPath, "/tmp/test.jsonl")
		}
	})

	t.Run("missing transcript_path", func(t *testing.T) {
		input := `{"session_id":"abc-123"}`
		_, err := ReadHookInputFrom(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for missing transcript_path")
		}
		if !strings.Contains(err.Error(), "transcript_path") {
			t.Errorf("error should mention transcript_path, got: %v", err)
		}
	})

	t.Run("missing session_id propagates error from types.ReadHookInput", func(t *testing.T) {
		input := `{"transcript_path":"/tmp/test.jsonl"}`
		_, err := ReadHookInputFrom(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for missing session_id")
		}
		if !strings.Contains(err.Error(), "session_id") {
			t.Errorf("error should mention session_id, got: %v", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := ReadHookInputFrom(strings.NewReader("not json"))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}
