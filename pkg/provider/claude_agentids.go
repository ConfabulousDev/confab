package provider

// Agent-ID extraction is Claude-specific: it parses Claude transcript
// JSONL for embedded toolUseResult.agentId values, which is how Claude's
// sidechain agent files are discovered transitively. Codex agents are
// tracked via the SQLite thread tree instead and never grow agent IDs in
// their rollout JSONL.

// minAgentIDLength is the minimum length of an agent ID string. Agent IDs
// across Claude versions are alphanumeric + [-_], 6 or more characters.
const minAgentIDLength = 6

// ExtractAgentIDsFromMessage extracts agent IDs from a parsed Claude
// transcript message. Checks both root-level toolUseResult.agentId and
// nested content blocks. Empty slice on non-user messages or missing
// fields.
func (ClaudeCode) ExtractAgentIDsFromMessage(message map[string]interface{}) []string {
	if msgType, _ := message["type"].(string); msgType != "user" {
		return nil
	}

	var agentIDs []string
	if id := agentIDFromToolUseResult(message["toolUseResult"]); id != "" {
		agentIDs = append(agentIDs, id)
	}
	for _, block := range nestedContentBlocks(message) {
		blockMap, ok := block.(map[string]interface{})
		if !ok || blockMap["type"] != "tool_result" {
			continue
		}
		resultContent, _ := blockMap["content"].(map[string]interface{})
		if id := agentIDFromToolUseResult(resultContent["toolUseResult"]); id != "" {
			agentIDs = append(agentIDs, id)
		}
	}
	return agentIDs
}

// agentIDFromToolUseResult extracts and validates a toolUseResult.agentId.
// Returns "" when the input isn't a map, agentId is missing/not a string,
// or the ID fails validation.
func agentIDFromToolUseResult(toolUseResult interface{}) string {
	m, ok := toolUseResult.(map[string]interface{})
	if !ok {
		return ""
	}
	id, ok := m["agentId"].(string)
	if !ok || !isValidAgentID(id) {
		return ""
	}
	return id
}

// nestedContentBlocks returns message["message"]["content"] as a slice,
// or nil if either field is missing or has the wrong type.
func nestedContentBlocks(message map[string]interface{}) []interface{} {
	nested, ok := message["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	content, _ := nested["content"].([]interface{})
	return content
}

// IsValidAgentID checks if a string is a valid agent ID. Agent IDs are 6+
// characters matching [a-zA-Z0-9_-]+. Covers all observed formats:
//   - Pure hex: "a0074ac", "a3eaf63159a07953f"
//   - Compact: "acompact-2aaa241e456ebc94"
//   - Prompt suggestion: "aprompt_suggestion-ba74af"
//   - Legacy 8-char hex: "abcd1234"
func (ClaudeCode) IsValidAgentID(s string) bool {
	return isValidAgentID(s)
}

func isValidAgentID(s string) bool {
	if len(s) < minAgentIDLength {
		return false
	}
	for _, c := range s {
		if !isAgentIDChar(c) {
			return false
		}
	}
	return true
}

func isAgentIDChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}
