package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/types"
)

func TestExtractPRURLFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
		want     string
	}{
		{
			name:     "gh pr create output in stdout",
			response: map[string]any{"stdout": "https://github.com/owner/repo/pull/123\n"},
			want:     "https://github.com/owner/repo/pull/123",
		},
		{
			name:     "pr url in verbose output",
			response: map[string]any{"stdout": "Creating pull request...\nhttps://github.com/myorg/myrepo/pull/456\nDone!"},
			want:     "https://github.com/myorg/myrepo/pull/456",
		},
		{
			name:     "no pr url",
			response: map[string]any{"stdout": "git push origin main"},
			want:     "",
		},
		{
			name:     "nil response",
			response: nil,
			want:     "",
		},
		{
			name:     "empty response",
			response: map[string]any{},
			want:     "",
		},
		{
			name:     "github commit url (not PR)",
			response: map[string]any{"stdout": "https://github.com/owner/repo/commit/abc123"},
			want:     "",
		},
		{
			name:     "pr url with long number",
			response: map[string]any{"stdout": "https://github.com/owner/repo/pull/12345"},
			want:     "https://github.com/owner/repo/pull/12345",
		},
		{
			name:     "multiple pr urls - returns first",
			response: map[string]any{"stdout": "https://github.com/a/b/pull/1\nhttps://github.com/c/d/pull/2"},
			want:     "https://github.com/a/b/pull/1",
		},
		{
			name:     "MCP response with html_url",
			response: map[string]any{"html_url": "https://github.com/owner/repo/pull/789", "number": float64(789)},
			want:     "https://github.com/owner/repo/pull/789",
		},
		{
			name:     "org with hyphen",
			response: map[string]any{"stdout": "https://github.com/my-org/my-repo/pull/123"},
			want:     "https://github.com/my-org/my-repo/pull/123",
		},
		{
			name:     "org with underscore",
			response: map[string]any{"stdout": "https://github.com/my_org/my_repo/pull/123"},
			want:     "https://github.com/my_org/my_repo/pull/123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPRURLFromResponse(tt.response)
			if got != tt.want {
				t.Errorf("extractPRURLFromResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPRURLPattern(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		match bool
	}{
		{"valid pr url", "https://github.com/owner/repo/pull/123", true},
		{"valid pr url with long number", "https://github.com/owner/repo/pull/99999", true},
		{"commit url", "https://github.com/owner/repo/commit/abc123", false},
		{"issues url", "https://github.com/owner/repo/issues/123", false},
		{"http not https", "http://github.com/owner/repo/pull/123", false},
		{"gitlab url", "https://gitlab.com/owner/repo/pull/123", false},
		{"missing pull number", "https://github.com/owner/repo/pull/", false},
		{"non-numeric pull", "https://github.com/owner/repo/pull/abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prURLPattern.MatchString(tt.url)
			if got != tt.match {
				t.Errorf("prURLPattern.MatchString(%q) = %v, want %v", tt.url, got, tt.match)
			}
		})
	}
}

func TestIsSuccessfulBashResponse(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
		want     bool
	}{
		{
			name:     "nil response",
			response: nil,
			want:     false,
		},
		{
			name:     "empty response",
			response: map[string]any{},
			want:     true,
		},
		{
			name:     "stdout only",
			response: map[string]any{"stdout": "success"},
			want:     true,
		},
		{
			name:     "exit code 0",
			response: map[string]any{"exit_code": float64(0), "stdout": "done"},
			want:     true,
		},
		{
			name:     "exit code 1",
			response: map[string]any{"exit_code": float64(1), "stderr": "error"},
			want:     false,
		},
		{
			name:     "exit code 128",
			response: map[string]any{"exit_code": float64(128)},
			want:     false,
		},
		{
			name:     "stderr only (no stdout)",
			response: map[string]any{"stderr": "fatal: not a git repository"},
			want:     false,
		},
		{
			name:     "both stdout and stderr (success with warnings)",
			response: map[string]any{"stdout": "committed", "stderr": "warning: LF will be replaced"},
			want:     true,
		},
		{
			name:     "empty stderr with stdout",
			response: map[string]any{"stdout": "done", "stderr": ""},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSuccessfulBashResponse(tt.response)
			if got != tt.want {
				t.Errorf("isSuccessfulBashResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandlePostToolUse_GitCommit(t *testing.T) {
	// When there's no daemon state, should exit silently
	input := types.HookInput{
		SessionID:     "unknown-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		CWD:           "/some/path",
		ToolInput:     map[string]any{"command": "git commit -m 'test'"},
		ToolResponse:  map[string]any{"stdout": "[main abc1234] test\n 1 file changed"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output when no state exists (can't link)
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no state exists, got %q", w.String())
	}
}

func TestHandlePostToolUse_GitPush(t *testing.T) {
	// When there's no daemon state, should exit silently
	input := types.HookInput{
		SessionID:     "unknown-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		CWD:           "/some/path",
		ToolInput:     map[string]any{"command": "git push origin main"},
		ToolResponse:  map[string]any{"stdout": "To github.com:owner/repo.git\n   abc1234..def5678  main -> main"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output when no state exists (can't link)
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no state exists, got %q", w.String())
	}
}

func TestHandlePostToolUse_GitPushFailed(t *testing.T) {
	// When git push fails, should not attempt to link
	input := types.HookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		CWD:           "/some/path",
		ToolInput:     map[string]any{"command": "git push origin main"},
		ToolResponse:  map[string]any{"exit_code": float64(1), "stderr": "error: failed to push"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output when push fails
	if w.Len() != 0 {
		t.Errorf("Expected empty output when push fails, got %q", w.String())
	}
}

func TestHandlePostToolUse_NonBashTool(t *testing.T) {
	input := types.HookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUse",
		ToolName:      "Read",
		ToolInput:     map[string]any{"file_path": "/test.txt"},
		ToolResponse:  map[string]any{"content": "file contents here"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (silent completion)
	if w.Len() != 0 {
		t.Errorf("Expected empty output for non-Bash tool, got %q", w.String())
	}
}

func TestHandlePostToolUse_NonPRCommand(t *testing.T) {
	input := types.HookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "npm install"},
		ToolResponse:  map[string]any{"stdout": "added 100 packages"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (silent completion)
	if w.Len() != 0 {
		t.Errorf("Expected empty output for non-PR command, got %q", w.String())
	}
}

func TestHandlePostToolUse_PRCreateNoPRURLInOutput(t *testing.T) {
	input := types.HookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "gh pr create --title 'test'"},
		ToolResponse:  map[string]any{"stderr": "Error: could not create PR"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (no URL to link)
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no PR URL in output, got %q", w.String())
	}
}

func TestHandlePostToolUse_InvalidJSON(t *testing.T) {
	r := strings.NewReader("not valid json")
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error (silent failure), got %v", err)
	}

	// Should produce no output on parse error
	if w.Len() != 0 {
		t.Errorf("Expected empty output on parse error, got %q", w.String())
	}
}

func TestHandlePostToolUse_MissingSessionID(t *testing.T) {
	input := types.HookInput{
		SessionID:     "", // Missing
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "gh pr create"},
		ToolResponse:  map[string]any{"stdout": "https://github.com/owner/repo/pull/123"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error (silent failure), got %v", err)
	}

	// Should produce no output on validation error
	if w.Len() != 0 {
		t.Errorf("Expected empty output on validation error, got %q", w.String())
	}
}

func TestHandlePostToolUse_PRCreateNoState(t *testing.T) {
	// When there's no daemon state (sync not set up), should exit silently
	input := types.HookInput{
		SessionID:     "unknown-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "gh pr create --title 'test'"},
		ToolResponse:  map[string]any{"stdout": "https://github.com/owner/repo/pull/123"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output when no state exists (can't link)
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no state exists, got %q", w.String())
	}
}

func TestHandlePostToolUse_MCPGitHubPR(t *testing.T) {
	// When there's no daemon state, should exit silently
	input := types.HookInput{
		SessionID:     "unknown-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameMCPGitHubCreatePR,
		ToolInput: map[string]any{
			"owner": "myorg",
			"repo":  "myrepo",
			"title": "Fix bug",
		},
		ToolResponse: map[string]any{"html_url": "https://github.com/myorg/myrepo/pull/456", "number": float64(456)},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output when no state exists (can't link)
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no state exists, got %q", w.String())
	}
}

func TestHandlePostToolUse_EmptyCommand(t *testing.T) {
	input := types.HookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{}, // No command
		ToolResponse:  map[string]any{"stdout": "https://github.com/owner/repo/pull/123"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePostToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output when no command
	if w.Len() != 0 {
		t.Errorf("Expected empty output for empty command, got %q", w.String())
	}
}

func TestReadPostToolUseInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		wantID    string
	}{
		{
			name:      "valid input",
			input:     `{"session_id": "test-123", "tool_name": "Bash", "tool_response": {"stdout": "success"}}`,
			wantError: false,
			wantID:    "test-123",
		},
		{
			name:      "missing session_id",
			input:     `{"tool_name": "Bash", "tool_response": {"stdout": "success"}}`,
			wantError: true,
			wantID:    "",
		},
		{
			name:      "invalid json",
			input:     "not json",
			wantError: true,
			wantID:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			got, err := types.ReadHookInput(r)

			if tt.wantError {
				if err == nil && (got == nil || got.SessionID == "") {
					// Either error or empty session ID is acceptable for validation failure
					return
				}
				if err != nil {
					return
				}
				t.Errorf("types.ReadHookInput() expected error or empty session, got session_id=%q", got.SessionID)
				return
			}

			if err != nil {
				t.Errorf("types.ReadHookInput() error = %v, want nil", err)
				return
			}
			if got.SessionID != tt.wantID {
				t.Errorf("types.ReadHookInput() session_id = %q, want %q", got.SessionID, tt.wantID)
			}
		})
	}
}
