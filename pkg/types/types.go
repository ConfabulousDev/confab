package types

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// MaxJSONLLineSize is the maximum size for a single JSONL line
// Default bufio.Scanner buffer is 64KB, but transcript lines with
// thinking blocks and tool results can exceed 1MB
const MaxJSONLLineSize = 10 * 1024 * 1024 // 10MB

// NewJSONLScanner creates a bufio.Scanner configured for large JSONL files
// with a 10MB buffer to handle long transcript lines
func NewJSONLScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, MaxJSONLLineSize)
	scanner.Buffer(buf, MaxJSONLLineSize)
	return scanner
}

// HookInput represents hook data from Claude Code.
//
// This is a union type containing fields from all hook types (SessionStart,
// UserPromptSubmit, PreToolUse, PostToolUse, etc.). JSON unmarshaling handles
// missing fields gracefully. This approach is pragmatic for a small number of
// hooks with mostly orthogonal fields. Consider splitting into separate types
// if hooks start having conflicting field semantics or the number of hook
// types grows significantly.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	HookEventName  string `json:"hook_event_name"`
	Reason         string `json:"reason"`
	ParentPID      int    `json:"parent_pid,omitempty"` // Claude Code process ID (set by confab, not Claude Code)

	// UserPromptSubmit-specific fields
	Prompt string `json:"prompt,omitempty"`

	// PreToolUse/PostToolUse-specific fields
	ToolName     string         `json:"tool_name,omitempty"`
	ToolInput    map[string]any `json:"tool_input,omitempty"`
	ToolUseID    string         `json:"tool_use_id,omitempty"`
	ToolResponse map[string]any `json:"tool_response,omitempty"` // PostToolUse only
}

// ReadHookInput reads and parses hook input JSON from a reader.
// Used by PreToolUse, PostToolUse, and other hook handlers.
func ReadHookInput(r io.Reader) (*HookInput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	var input HookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("failed to parse hook input: %w", err)
	}

	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	return &input, nil
}

// HookResponse is the JSON response sent back to Claude Code
type HookResponse struct {
	Continue       bool   `json:"continue"`
	StopReason     string `json:"stopReason"`
	SuppressOutput bool   `json:"suppressOutput"`
}

// PreToolUseResponse is the JSON response for PreToolUse hooks
type PreToolUseResponse struct {
	HookSpecificOutput *PreToolUseOutput `json:"hookSpecificOutput,omitempty"`
}

// PreToolUseOutput contains PreToolUse-specific decision fields
type PreToolUseOutput struct {
	HookEventName            string         `json:"hookEventName"`
	PermissionDecision       string         `json:"permissionDecision,omitempty"` // "allow", "deny", or "ask"
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
}

// InboxEvent represents an event written to the daemon's inbox file.
// The inbox is a JSONL file where each line is an event.
type InboxEvent struct {
	Type      string     `json:"type"`                 // Event type: "session_end"
	Timestamp time.Time  `json:"timestamp"`            // When the event was written
	HookInput *HookInput `json:"hook_input,omitempty"` // Full hook payload for session events
}

// SessionFile represents a file discovered for a session
type SessionFile struct {
	Path      string
	Type      string // "transcript" | "agent"
	SizeBytes int64
}

// Session represents a captured session in the database
type Session struct {
	SessionID      string
	TranscriptPath string
	CWD            string
	Reason         string
	Timestamp      time.Time
	FileCount      int
	TotalSizeBytes int64
}
