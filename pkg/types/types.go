package types

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"
)

// MaxJSONLLineSize is the maximum size for a single JSONL line
// Default bufio.Scanner buffer is 64KB, but transcript lines with
// thinking blocks and tool results can exceed 1MB
const MaxJSONLLineSize = 10 * 1024 * 1024 // 10MB

// MaxFirstUserMessageLength is the maximum size for provider session title
// metadata sent to the backend.
const MaxFirstUserMessageLength = 8 * 1024 // 8KB

// NewJSONLScanner creates a bufio.Scanner configured for large JSONL files
// with a 10MB buffer to handle long transcript lines
func NewJSONLScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, MaxJSONLLineSize)
	scanner.Buffer(buf, MaxJSONLLineSize)
	return scanner
}

// ClaudeHookInput represents hook data from Claude Code.
//
// This is a union type containing fields from all hook types (SessionStart,
// UserPromptSubmit, PreToolUse, PostToolUse, etc.). JSON unmarshaling handles
// missing fields gracefully. This approach is pragmatic for a small number of
// hooks with mostly orthogonal fields. Consider splitting into separate types
// if hooks start having conflicting field semantics or the number of hook
// types grows significantly.
type ClaudeHookInput struct {
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

// CodexHookInput contains the shared fields Confab needs from Codex command
// hooks. The fields follow the current official Codex hook schemas, while
// provider-specific parsing owns validation. Like ClaudeHookInput, this is
// a union type carrying fields from all Codex hook events (SessionStart,
// PreToolUse, PostToolUse); JSON unmarshaling handles missing fields
// gracefully.
type CodexHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Model          string `json:"model,omitempty"`
	Source         string `json:"source,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	ParentPID      int    `json:"parent_pid,omitempty"` // Codex process ID (set by confab, not Codex)

	// PreToolUse/PostToolUse-specific fields. Codex normalizes its shell
	// tool's tool_name to "Bash" in hook stdin (HookToolName::bash() in
	// codex-rs/core/src/tools/hook_names.rs), so our Bash matcher and
	// gitCommit/ghPRCreate regexes work without per-provider tweaks.
	//
	// ToolResponse is json.RawMessage rather than map[string]any because
	// Codex's PostToolUse schema types tool_response as `any` JSON value:
	// the shell tool emits a plain JSON string (the aggregated exec output
	// from format_exec_output_str), while other tools emit objects. Use
	// ToolResponseMap to get a normalized map[string]any for either shape.
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    map[string]any  `json:"tool_input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"` // PostToolUse only
}

// ToolResponseMap normalizes the Codex tool_response value into a
// map[string]any so downstream provider-agnostic code can use a single
// shape. Codex's shell tool sends a plain string; other tools send
// objects. Strings get wrapped under "stdout" because that's how our
// PR-URL extractor and success heuristics read shell output.
func (c *CodexHookInput) ToolResponseMap() map[string]any {
	if len(c.ToolResponse) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(c.ToolResponse, &obj); err == nil {
		return obj
	}
	var s string
	if err := json.Unmarshal(c.ToolResponse, &s); err == nil {
		return map[string]any{"stdout": s}
	}
	return nil
}

// CodexHookResponse is the JSON response sent back to Codex hooks.
type CodexHookResponse struct {
	Continue       bool   `json:"continue"`
	StopReason     string `json:"stopReason,omitempty"`
	SystemMessage  string `json:"systemMessage,omitempty"`
	SuppressOutput bool   `json:"suppressOutput,omitempty"`
}

// OpenCodeHookInput represents hook data from OpenCode.
// Unlike Claude/Codex, OpenCode doesn't pipe hook data via stdin from a
// settings-file hook system. Instead, the TypeScript plugin constructs this
// struct from the event payload and passes it via stdin to the confab hook
// commands. ServerURL is the OpenCode HTTP server address (e.g.
// "http://localhost:4096") — the daemon uses this to connect to the OpenCode
// server for session discovery and SSE event subscription.
type OpenCodeHookInput struct {
	SessionID         string `json:"session_id"`
	OpenCodeServerURL string `json:"server_url"`
	CWD               string `json:"cwd"`
	ParentPID         int    `json:"parent_pid,omitempty"`
}

// sessionIDPattern validates session IDs contain only safe characters.
// This prevents path traversal attacks (e.g., "../../tmp/evil") when
// session IDs are used in file paths.
var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`)

// ValidateSessionID checks that a session ID contains only safe characters.
func ValidateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("invalid session_id: must contain only alphanumeric, hyphen, or underscore characters")
	}
	return nil
}

// ReadClaudeHookInput reads and parses hook input JSON from a reader.
// Used by PreToolUse, PostToolUse, and other hook handlers.
func ReadClaudeHookInput(r io.Reader) (*ClaudeHookInput, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxJSONLLineSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	var input ClaudeHookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("failed to parse hook input: %w", err)
	}

	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	if err := ValidateSessionID(input.SessionID); err != nil {
		return nil, err
	}

	return &input, nil
}

// ClaudeHookResponse is the JSON response sent back to Claude Code
type ClaudeHookResponse struct {
	Continue       bool   `json:"continue"`
	StopReason     string `json:"stopReason"`
	SuppressOutput bool   `json:"suppressOutput"`
	SystemMessage  string `json:"systemMessage,omitempty"`
}

// PreToolUseResponse is the JSON response for PreToolUse hooks. The shape
// is provider-agnostic: Claude Code and Codex both accept the same
// hookSpecificOutput.permissionDecision contract per their respective
// schemas.
type PreToolUseResponse struct {
	HookSpecificOutput *PreToolUseOutput `json:"hookSpecificOutput,omitempty"`
}

// PreToolUseOutput contains PreToolUse-specific decision fields.
type PreToolUseOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"` // "allow", "deny", or "ask"
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

// InboxEvent represents an event written to the daemon's inbox file.
// The inbox is a JSONL file where each line is an event.
type InboxEvent struct {
	Type      string           `json:"type"`                 // Event type: "session_end"
	Timestamp time.Time        `json:"timestamp"`            // When the event was written
	HookInput *ClaudeHookInput `json:"hook_input,omitempty"` // Full hook payload for session events
}
