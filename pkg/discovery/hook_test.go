package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadHookInputFrom(t *testing.T) {
	// Set up a temp dir as the Claude projects directory
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_CLAUDE_DIR", tmpDir)

	validPath := filepath.Join(tmpDir, "project", "test.jsonl")

	t.Run("valid input with transcript_path", func(t *testing.T) {
		// Create parent dir so EvalSymlinks works
		os.MkdirAll(filepath.Dir(validPath), 0700)

		input := `{"session_id":"abc-123","transcript_path":"` + validPath + `"}`
		got, err := ReadHookInputFrom(strings.NewReader(input))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SessionID != "abc-123" {
			t.Errorf("SessionID = %q, want %q", got.SessionID, "abc-123")
		}
		if got.TranscriptPath != validPath {
			t.Errorf("TranscriptPath = %q, want %q", got.TranscriptPath, validPath)
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
		input := `{"transcript_path":"` + validPath + `"}`
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

	t.Run("rejects path outside Claude projects dir", func(t *testing.T) {
		input := `{"session_id":"abc-123","transcript_path":"/tmp/evil.jsonl"}`
		_, err := ReadHookInputFrom(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for path outside Claude projects dir")
		}
		if !strings.Contains(err.Error(), "transcript_path") {
			t.Errorf("error should mention transcript_path, got: %v", err)
		}
	})

	t.Run("rejects relative path", func(t *testing.T) {
		input := `{"session_id":"abc-123","transcript_path":"relative/path.jsonl"}`
		_, err := ReadHookInputFrom(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for relative path")
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		input := `{"session_id":"abc-123","transcript_path":"` + tmpDir + `/../../../etc/passwd"}`
		_, err := ReadHookInputFrom(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
	})
}
