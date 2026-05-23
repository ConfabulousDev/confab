package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNewJSONLScanner(t *testing.T) {
	t.Run("handles normal sized lines", func(t *testing.T) {
		input := `{"type":"user","message":"hello"}`
		scanner := NewJSONLScanner(strings.NewReader(input))

		if !scanner.Scan() {
			t.Fatal("Failed to scan normal line")
		}

		got := scanner.Text()
		if got != input {
			t.Errorf("Got %q, want %q", got, input)
		}
	})

	t.Run("handles lines larger than default 64KB buffer", func(t *testing.T) {
		// Create a line that's 100KB (larger than default 64KB buffer)
		largeContent := strings.Repeat("x", 100*1024)
		input := `{"type":"assistant","content":"` + largeContent + `"}`

		scanner := NewJSONLScanner(strings.NewReader(input))

		if !scanner.Scan() {
			t.Fatalf("Failed to scan large line: %v", scanner.Err())
		}

		got := scanner.Text()
		if len(got) != len(input) {
			t.Errorf("Got %d bytes, want %d bytes", len(got), len(input))
		}
	})

	t.Run("handles lines up to 10MB", func(t *testing.T) {
		// Create a line close to the 10MB limit
		largeContent := strings.Repeat("a", 9*1024*1024) // 9MB
		input := `{"data":"` + largeContent + `"}`

		scanner := NewJSONLScanner(strings.NewReader(input))

		if !scanner.Scan() {
			t.Fatalf("Failed to scan 9MB line: %v", scanner.Err())
		}

		got := scanner.Text()
		if len(got) != len(input) {
			t.Errorf("Got %d bytes, want %d bytes", len(got), len(input))
		}
	})

	t.Run("handles multiple lines", func(t *testing.T) {
		input := "line1\nline2\nline3"
		scanner := NewJSONLScanner(strings.NewReader(input))

		lines := []string{}
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}

		if len(lines) != 3 {
			t.Fatalf("Got %d lines, want 3", len(lines))
		}

		expected := []string{"line1", "line2", "line3"}
		for i, line := range lines {
			if line != expected[i] {
				t.Errorf("Line %d: got %q, want %q", i, line, expected[i])
			}
		}
	})

	t.Run("handles empty input", func(t *testing.T) {
		scanner := NewJSONLScanner(strings.NewReader(""))

		if scanner.Scan() {
			t.Error("Expected no lines from empty input")
		}

		if scanner.Err() != nil {
			t.Errorf("Unexpected error: %v", scanner.Err())
		}
	})

	t.Run("returns error for lines exceeding 10MB", func(t *testing.T) {
		// Create a line that exceeds 10MB
		tooLargeContent := strings.Repeat("x", 11*1024*1024) // 11MB
		input := `{"data":"` + tooLargeContent + `"}`

		scanner := NewJSONLScanner(strings.NewReader(input))

		// Should fail to scan
		if scanner.Scan() {
			t.Error("Expected scan to fail for line > 10MB")
		}

		// Should have an error
		if scanner.Err() == nil {
			t.Error("Expected error for line > 10MB, got nil")
		}
	})
}

func TestMaxJSONLLineSize(t *testing.T) {
	// Verify the constant is set to 10MB
	expected := 10 * 1024 * 1024
	if MaxJSONLLineSize != expected {
		t.Errorf("MaxJSONLLineSize = %d, want %d", MaxJSONLLineSize, expected)
	}
}

func TestReadHookInput(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		input := `{"session_id":"abc123","transcript_path":"/tmp/test.jsonl","hook_event_name":"SessionStart"}`
		got, err := ReadClaudeHookInput(strings.NewReader(input))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SessionID != "abc123" {
			t.Errorf("SessionID = %q, want %q", got.SessionID, "abc123")
		}
		if got.TranscriptPath != "/tmp/test.jsonl" {
			t.Errorf("TranscriptPath = %q, want %q", got.TranscriptPath, "/tmp/test.jsonl")
		}
		if got.HookEventName != "SessionStart" {
			t.Errorf("HookEventName = %q, want %q", got.HookEventName, "SessionStart")
		}
	})

	t.Run("missing session_id", func(t *testing.T) {
		input := `{"transcript_path":"/tmp/test.jsonl"}`
		_, err := ReadClaudeHookInput(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for missing session_id")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := ReadClaudeHookInput(strings.NewReader("not json"))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})

	t.Run("empty input", func(t *testing.T) {
		_, err := ReadClaudeHookInput(strings.NewReader(""))
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("reader error", func(t *testing.T) {
		_, err := ReadClaudeHookInput(&failingReader{err: errors.New("disk read error")})
		if err == nil {
			t.Fatal("expected error for failing reader")
		}
		if !strings.Contains(err.Error(), "failed to read input") {
			t.Errorf("error should mention 'failed to read input', got: %v", err)
		}
	})

	t.Run("invalid session_id format", func(t *testing.T) {
		input := `{"session_id":"../../tmp/evil","transcript_path":"/tmp/test.jsonl"}`
		_, err := ReadClaudeHookInput(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for invalid session_id format")
		}
		if !strings.Contains(err.Error(), "invalid session_id") {
			t.Errorf("error should mention 'invalid session_id', got: %v", err)
		}
	})

	t.Run("optional fields are zero-valued", func(t *testing.T) {
		input := `{"session_id":"abc123"}`
		got, err := ReadClaudeHookInput(strings.NewReader(input))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Prompt != "" {
			t.Errorf("Prompt should be empty, got %q", got.Prompt)
		}
		if got.ToolName != "" {
			t.Errorf("ToolName should be empty, got %q", got.ToolName)
		}
		if got.ParentPID != 0 {
			t.Errorf("ParentPID should be 0, got %d", got.ParentPID)
		}
	})
}

func TestValidateSessionID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "alphanumeric", id: "abc123", wantErr: false},
		{name: "with hyphens", id: "abc-123", wantErr: false},
		{name: "with underscores", id: "abc_123", wantErr: false},
		{name: "mixed safe chars", id: "a1-B2_c3", wantErr: false},
		{name: "empty string", id: "", wantErr: true},
		{name: "path traversal", id: "../../tmp/evil", wantErr: true},
		{name: "slash", id: "abc/def", wantErr: true},
		{name: "space", id: "abc def", wantErr: true},
		{name: "special chars", id: "abc@123", wantErr: true},
		{name: "dollar sign", id: "abc$123", wantErr: true},
		{name: "newline", id: "abc\n123", wantErr: true},
		{name: "dot", id: "abc.123", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSessionID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSessionID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestNewJSONLScanner_RealWorldScenarios(t *testing.T) {
	t.Run("handles JSONL with thinking blocks", func(t *testing.T) {
		// Simulate a realistic transcript line with a large thinking block
		thinkingBlock := strings.Repeat("This is a long thinking process. ", 10000) // ~330KB
		jsonLine := `{"type":"assistant","message":{"thinking":"` + thinkingBlock + `"}}`

		scanner := NewJSONLScanner(bytes.NewReader([]byte(jsonLine)))

		if !scanner.Scan() {
			t.Fatalf("Failed to scan realistic thinking block: %v", scanner.Err())
		}

		if scanner.Err() != nil {
			t.Errorf("Unexpected error: %v", scanner.Err())
		}
	})

	t.Run("handles JSONL with large tool results", func(t *testing.T) {
		// Simulate a tool result with lots of output
		toolOutput := strings.Repeat("output line\n", 50000) // ~550KB
		jsonLine := `{"type":"tool_result","content":"` + toolOutput + `"}`

		scanner := NewJSONLScanner(bytes.NewReader([]byte(jsonLine)))

		if !scanner.Scan() {
			t.Fatalf("Failed to scan large tool result: %v", scanner.Err())
		}

		if scanner.Err() != nil {
			t.Errorf("Unexpected error: %v", scanner.Err())
		}
	})
}

// failingReader is an io.Reader that always returns an error.
type failingReader struct {
	err error
}

func (r *failingReader) Read(p []byte) (int, error) {
	return 0, r.err
}

// TestCodexHookInputUnmarshalsToolUseFields pins the CodexHookInput union
// shape: it must carry tool_name, tool_input, tool_use_id, and tool_response
// alongside the session-level fields. Codex's pre/post-tool-use payloads
// include these exact field names per its wire schema.
func TestCodexHookInputUnmarshalsToolUseFields(t *testing.T) {
	payload := `{
		"session_id": "01234567-89ab-cdef-0123-456789abcdef",
		"transcript_path": "/home/u/.codex/sessions/2026/05/23/rollout-foo.jsonl",
		"cwd": "/repo",
		"hook_event_name": "PreToolUse",
		"tool_name": "Bash",
		"tool_input": {"command": "git commit -m 'test'"},
		"tool_use_id": "tu_abc",
		"tool_response": {"exit_code": 0, "stdout": "ok"}
	}`

	var got CodexHookInput
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("Unmarshal CodexHookInput: %v", err)
	}

	if got.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want %q", got.ToolName, "Bash")
	}
	if cmd, _ := got.ToolInput["command"].(string); cmd != "git commit -m 'test'" {
		t.Errorf("ToolInput[command] = %q, want %q", cmd, "git commit -m 'test'")
	}
	if got.ToolUseID != "tu_abc" {
		t.Errorf("ToolUseID = %q, want %q", got.ToolUseID, "tu_abc")
	}
	if code, _ := got.ToolResponse["exit_code"].(float64); code != 0 {
		t.Errorf("ToolResponse[exit_code] = %v, want 0", got.ToolResponse["exit_code"])
	}
}
