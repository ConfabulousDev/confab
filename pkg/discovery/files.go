package discovery

const (
	// AgentIDLength is the expected length of agent ID hex strings
	AgentIDLength = 8
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

// IsValidAgentID checks if a string is a valid 8-character hex agent ID
func IsValidAgentID(s string) bool {
	return len(s) == AgentIDLength && isHexString(s)
}

// isHexString checks if a string contains only hexadecimal characters
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
