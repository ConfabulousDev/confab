package provider_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// The two rollout shapes below are transcribed from real local Codex
// rollouts (see z1jr), not hand-written guesses:
//
//   - legacy: cli_version 0.130.0, event_msg / payload.type "user_message"
//     with a flat payload.message string.
//   - modern: cli_version 0.149.1+, event_msg / payload.type "item_completed"
//     with item.type "UserMessage" and item.content[] typed parts. Only
//     parts with type "text" carry prompt text; a real 0.153.2 rollout
//     carries a {"type":"skill",...} part alongside the text part.
const (
	realLegacyUserMessageLine = `{"timestamp":"2026-05-13T01:07:08.515Z","type":"event_msg","payload":{"type":"user_message","message":"i want to add the linear mcp","images":[],"local_images":[],"text_elements":[]}}`

	realModernUserMessageLine = `{"timestamp":"2026-09-10T17:01:52.711Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"01a08c43-f999-7290-b7f4-b0cb047567f8","turn_id":"01a08c44-b80b-70d2-b4d4-dc246a0b3e42","item":{"type":"UserMessage","id":"01a08c44-ba89-7a12-a49b-0d639174ee89","client_id":"342846d4-a74f-48b3-a426-6f6cd02ff36f","content":[{"type":"text","text":"explore this project","text_elements":[]}]}}}`

	// A real 0.153.2 line: a text part followed by a non-text "skill" part.
	realModernSkillPartLine = `{"timestamp":"2026-09-04T22:19:52.711Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"01a06e82-c419-7e70-9ca3-0b5fcd7bf0e4","content":[{"type":"text","text":"$retro e021e7ea","text_elements":[{"byte_range":{"start":0,"end":6},"placeholder":"$retro"}]},{"type":"skill","name":"retro","path":"/Users/jackie/.claude/skills/retro/SKILL.md"}]}}}`

	// Non-user event_msg lines that precede the user message in real rollouts.
	realTaskStartedLine = `{"timestamp":"2026-09-10T17:01:48.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"01a08c44-b80b-70d2-b4d4-dc246a0b3e42","started_at":1789059708,"model_context_window":258400,"collaboration_mode_kind":"default"}}`

	// A user-role response_item — version-stable but contaminated with
	// injected context. It must NEVER become the extracted message (D2).
	realResponseItemEnvContextLine = `{"timestamp":"2026-09-10T17:01:48.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/Users/jackie/dev/personal-page</cwd>\n</environment_context>"}]}}`

	realResponseItemAgentsMDLine = `{"timestamp":"2026-09-10T17:01:48.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /Users/jackie/dev/personal-page"}]}}`
)

// modernLine builds an item_completed/UserMessage event_msg line with the
// given content parts, mirroring the real wire shape.
func modernLine(parts ...map[string]any) string {
	if parts == nil {
		parts = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{
		"type": "event_msg",
		"payload": map[string]any{
			"type": "item_completed",
			"item": map[string]any{
				"type":    "UserMessage",
				"id":      "01a08c44-ba89-7a12-a49b-0d639174ee89",
				"content": parts,
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func textPart(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func TestCodex_ExtractFirstUserMessageFromLines_BothEventMsgShapes(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "modern item_completed UserMessage shape",
			lines: []string{realTaskStartedLine, realModernUserMessageLine},
			want:  "explore this project",
		},
		{
			name:  "legacy user_message shape still works",
			lines: []string{realTaskStartedLine, realLegacyUserMessageLine},
			want:  "i want to add the linear mcp",
		},
		{
			name:  "modern shape ignores non-text content parts",
			lines: []string{realModernSkillPartLine},
			want:  "$retro e021e7ea",
		},
		{
			name:  "both shapes present returns the first in line order",
			lines: []string{realModernUserMessageLine, realLegacyUserMessageLine},
			want:  "explore this project",
		},
		{
			name:  "legacy first when it appears first",
			lines: []string{realLegacyUserMessageLine, realModernUserMessageLine},
			want:  "i want to add the linear mcp",
		},
		{
			name:  "neither shape present returns empty",
			lines: []string{realTaskStartedLine},
			want:  "",
		},
		{
			name:  "empty content array is skipped",
			lines: []string{modernLine(), realModernUserMessageLine},
			want:  "explore this project",
		},
		{
			name:  "multiple text parts are concatenated in order",
			lines: []string{modernLine(textPart("first part"), textPart("second part"))},
			want:  "first part\nsecond part",
		},
		{
			name: "non-text parts interleaved between text parts are dropped",
			lines: []string{modernLine(
				textPart("before"),
				map[string]any{"type": "skill", "name": "retro"},
				textPart("after"),
			)},
			want: "before\nafter",
		},
		{
			name:  "whitespace-only modern message is skipped",
			lines: []string{modernLine(textPart("   \n\t ")), realLegacyUserMessageLine},
			want:  "i want to add the linear mcp",
		},
		{
			name:  "whitespace-only legacy message is skipped",
			lines: []string{`{"type":"event_msg","payload":{"type":"user_message","message":"  "}}`, realModernUserMessageLine},
			want:  "explore this project",
		},
		{
			name:  "malformed JSON line mid-file does not abort the scan",
			lines: []string{`{"type":"event_msg","payload":`, realModernUserMessageLine},
			want:  "explore this project",
		},
		{
			name:  "non-UserMessage item types are ignored",
			lines: []string{`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","text":"hi"}}}`, realModernUserMessageLine},
			want:  "explore this project",
		},
		{
			name:  "item_completed without an item is ignored",
			lines: []string{`{"type":"event_msg","payload":{"type":"item_completed"}}`},
			want:  "",
		},
		{
			name:  "response_item environment_context never becomes the message",
			lines: []string{realResponseItemEnvContextLine, realResponseItemAgentsMDLine},
			want:  "",
		},
		{
			name:  "no lines at all",
			lines: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (provider.Codex{}).ExtractFirstUserMessageFromLines(tt.lines)
			if got != tt.want {
				t.Errorf("ExtractFirstUserMessageFromLines() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCodex_ExtractFirstUserMessageFromLines_TruncatesModernShape pins that
// the modern shape gets the same MaxMetadataFieldLength/2 truncation the
// legacy shape has always had.
func TestCodex_ExtractFirstUserMessageFromLines_TruncatesModernShape(t *testing.T) {
	long := strings.Repeat("a", types.MaxMetadataFieldLength)
	got := (provider.Codex{}).ExtractFirstUserMessageFromLines([]string{modernLine(textPart(long))})

	if len(got) != types.MaxMetadataFieldLength/2 {
		t.Errorf("len = %d, want %d", len(got), types.MaxMetadataFieldLength/2)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated message should end with %q, got %q", "...", got)
	}
}

// TestCodex_ScanSessions_ModernRolloutHasTitle covers the offline
// `confab list --provider codex` path end to end: a rollout written in the
// modern shape must produce a real title, with no backend involvement.
func TestCodex_ScanSessions_ModernRolloutHasTitle(t *testing.T) {
	f := codextest.NewFixture(t)
	f.AddRoot("01a08c43-f999-7290-b7f4-b0cb047567f8").
		WithSessionMeta("/work").
		WithUserMessage("refresh the static menu data")

	sessions, err := (provider.Codex{}).ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].FirstUserMessage != "refresh the static menu data" {
		t.Errorf("FirstUserMessage = %q, want %q",
			sessions[0].FirstUserMessage, "refresh the static menu data")
	}
}

// TestCodex_ScanSessions_LegacyRolloutHasTitle pins that rollouts already
// on disk in the retired shape keep working — `confab save` must still
// handle them.
func TestCodex_ScanSessions_LegacyRolloutHasTitle(t *testing.T) {
	f := codextest.NewFixture(t)
	f.AddRoot("01a08c43-f999-7290-b7f4-b0cb047567f9").
		WithSessionMeta("/work").
		WithLegacyUserMessage("i want to add the linear mcp")

	sessions, err := (provider.Codex{}).ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].FirstUserMessage != "i want to add the linear mcp" {
		t.Errorf("FirstUserMessage = %q, want %q",
			sessions[0].FirstUserMessage, "i want to add the linear mcp")
	}
}

// TestCodex_ReadSessionInfo_AgentTypeAliasMarksSubagent covers D7: Codex
// declares #[serde(alias = "agent_type")] on SessionMeta.agent_role, so a
// rollout may carry either key. Reading only agent_role would leave
// AgentRole empty and let IsUserSession misclassify a subagent as a
// user session (stray daemon + polluted `confab list`).
func TestCodex_ReadSessionInfo_AgentTypeAliasMarksSubagent(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("01a08c43-f999-7290-b7f4-b0cb04756800").
		WithRawLine(`{"type":"session_meta","payload":{"id":"01a08c43-f999-7290-b7f4-b0cb04756800","cwd":"/work","source":"cli","thread_source":"agent","agent_type":"reviewer"}}`)

	info, err := (provider.Codex{}).ReadSessionInfo(root.Path())
	if err != nil {
		t.Fatalf("ReadSessionInfo: %v", err)
	}
	if info.AgentRole != "reviewer" {
		t.Errorf("AgentRole = %q, want %q (from the agent_type alias)", info.AgentRole, "reviewer")
	}
	if info.IsUserSession() {
		t.Error("IsUserSession() = true, want false for an agent_type-tagged rollout")
	}
}

// TestCodex_ReadSessionInfo_AgentRoleWinsOverAlias pins precedence when a
// rollout somehow carries both keys: the canonical field wins.
func TestCodex_ReadSessionInfo_AgentRoleWinsOverAlias(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("01a08c43-f999-7290-b7f4-b0cb04756801").
		WithRawLine(`{"type":"session_meta","payload":{"id":"01a08c43-f999-7290-b7f4-b0cb04756801","cwd":"/work","agent_role":"canonical","agent_type":"alias"}}`)

	info, err := (provider.Codex{}).ReadSessionInfo(root.Path())
	if err != nil {
		t.Fatalf("ReadSessionInfo: %v", err)
	}
	if info.AgentRole != "canonical" {
		t.Errorf("AgentRole = %q, want %q", info.AgentRole, "canonical")
	}
}
