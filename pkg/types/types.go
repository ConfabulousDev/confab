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

// MaxMetadataFieldLength is the maximum byte length for provider session
// metadata fields (first_user_message, summary) sent to the backend.
// Callers should use MaxMetadataFieldLength/2 (4KB) for first_user_message
// to leave headroom for JSON serialization overhead below the backend's
// 8192-character limit.
const MaxMetadataFieldLength = 8 * 1024 // 8KB

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
// settings-file hook system. Instead, the TypeScript plugin constructs
// this struct from the event payload and passes it via stdin to the
// confab hook commands. The daemon then reads OpenCode session data
// directly from the local SQLite DB at ~/.local/share/opencode/opencode.db
// (or CONFAB_OPENCODE_DB), so no per-session URL is required.
type OpenCodeHookInput struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	ParentPID int    `json:"parent_pid,omitempty"`
	// ParentID is the OpenCode session's parent session id, set by the plugin
	// only for subagent (non-root) sessions. Used to suppress daemons for
	// non-root sessions (CF-537); root sessions omit it. Distinct from
	// ParentPID, which is an OS process id.
	ParentID string `json:"parent_id,omitempty"`
}

// CursorHookInput represents hook data from Cursor (cursor-agent CLI and the
// Cursor desktop IDE). Like ClaudeHookInput/CodexHookInput it is a union type
// carrying fields from all observed Cursor hook events (sessionStart,
// sessionEnd); JSON unmarshaling handles missing fields gracefully. The field
// set is ground-truthed from live sessionStart/sessionEnd payloads (kata issue
// 6kys T1 findings). No field has a variant/uncertain shape, so plain Go types
// are used throughout.
//
// transcript_path is NULL at sessionStart (transcripts exist but the path is
// not yet handed to the hook); it is populated from sessionEnd onward. The
// Cursor provider DERIVES the path at sessionStart from workspace_roots[0] +
// session_id, so an empty TranscriptPath here is expected, not an error.
type CursorHookInput struct {
	SessionID         string   `json:"session_id"`
	ConversationID    string   `json:"conversation_id,omitempty"`
	GenerationID      string   `json:"generation_id,omitempty"`
	Model             string   `json:"model,omitempty"`
	ComposerMode      string   `json:"composer_mode,omitempty"`
	IsBackgroundAgent bool     `json:"is_background_agent,omitempty"`
	HookEventName     string   `json:"hook_event_name,omitempty"`
	CursorVersion     string   `json:"cursor_version,omitempty"`
	WorkspaceRoots    []string `json:"workspace_roots,omitempty"`
	UserEmail         string   `json:"user_email,omitempty"`
	TranscriptPath    string   `json:"transcript_path,omitempty"` // null at sessionStart; populated from sessionEnd onward

	// sessionEnd-specific fields.
	Reason       string `json:"reason,omitempty"`        // completed|aborted|error|window_close|user_close
	FinalStatus  string `json:"final_status,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"` // present when reason=error
	DurationMS   int64  `json:"duration_ms,omitempty"`

	ParentPID int `json:"parent_pid,omitempty"` // Cursor process ID (set by confab, not Cursor)
}

// ReadCursorHookInput reads and validates a Cursor hook payload. Validation
// mirrors ReadClaudeHookInput: session_id is required and must pass
// ValidateSessionID (it is used in derived filesystem paths). transcript_path
// is intentionally NOT required — it is null at sessionStart and derived by the
// provider.
func ReadCursorHookInput(r io.Reader) (*CursorHookInput, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxJSONLLineSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	var input CursorHookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("failed to parse cursor hook input: %w", err)
	}

	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if err := ValidateSessionID(input.SessionID); err != nil {
		return nil, err
	}

	return &input, nil
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
