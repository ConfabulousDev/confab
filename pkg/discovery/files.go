package discovery

const (
	// MinAgentIDLength is the minimum length of an agent ID string
	MinAgentIDLength = 6
	// UUIDLength is the expected length of UUID strings (with hyphens)
	UUIDLength = 36
)

// ExtractAgentIDsFromMessage extracts agent IDs from a parsed JSONL message.
// It checks both root-level toolUseResult.agentId and nested content blocks.
func ExtractAgentIDsFromMessage(message map[string]interface{}) []string {
	var agentIDs []string

	// Only process user messages (which contain tool results)
	msgType, ok := message["type"].(string)
	if !ok || msgType != "user" {
		return nil
	}

	// Check for toolUseResult.agentId at ROOT level
	if toolUseResult, ok := message["toolUseResult"].(map[string]interface{}); ok {
		if agentID, ok := toolUseResult["agentId"].(string); ok {
			if IsValidAgentID(agentID) {
				agentIDs = append(agentIDs, agentID)
			}
		}
	}

	// Also check in content blocks inside the nested message object
	if nestedMessage, ok := message["message"].(map[string]interface{}); ok {
		if content, ok := nestedMessage["content"].([]interface{}); ok {
			for _, block := range content {
				if blockMap, ok := block.(map[string]interface{}); ok {
					if blockMap["type"] == "tool_result" {
						if resultContent, ok := blockMap["content"].(map[string]interface{}); ok {
							if toolUseResult, ok := resultContent["toolUseResult"].(map[string]interface{}); ok {
								if agentID, ok := toolUseResult["agentId"].(string); ok {
									if IsValidAgentID(agentID) {
										agentIDs = append(agentIDs, agentID)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return agentIDs
}

// IsValidAgentID checks if a string is a valid agent ID.
// Agent IDs are 6+ characters matching [a-zA-Z0-9_-]+.
// This covers all observed formats:
//   - Pure hex (7-17+ chars): "a0074ac", "a3eaf63159a07953f"
//   - Compact: "acompact-2aaa241e456ebc94"
//   - Prompt suggestion: "aprompt_suggestion-ba74af"
//   - Legacy 8-char hex: "abcd1234"
func IsValidAgentID(s string) bool {
	if len(s) < MinAgentIDLength {
		return false
	}
	for _, c := range s {
		if !isAgentIDChar(c) {
			return false
		}
	}
	return true
}

// isAgentIDChar returns true if the rune is valid in an agent ID: [a-zA-Z0-9_-]
func isAgentIDChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}
