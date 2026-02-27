package discovery

import (
	"testing"
)

func TestIsValidAgentID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// Legacy 8-char hex
		{"abcd1234", true},
		{"ABCD1234", true},
		{"12345678", true},

		// New-format: long hex (17-char)
		{"a3eaf63159a07953f", true},

		// New-format: 7-char hex
		{"a0074ac", true},

		// New-format: compact
		{"acompact-2aaa241e456ebc94", true},

		// New-format: prompt suggestion
		{"aprompt_suggestion-ba74af", true},

		// Edge: exactly 6 chars (minimum)
		{"abcdef", true},

		// Too short (< 6 chars)
		{"abc12", false},
		{"abcd", false},
		{"", false},

		// Invalid characters
		{"abc def12", false},  // space
		{"abc.1234", false},   // dot
		{"abc/1234", false},   // slash
		{"abc!1234", false},   // exclamation
		{"agent@foo", false},  // at sign
		{"abc\t1234", false},  // tab
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := IsValidAgentID(tt.input)
			if got != tt.want {
				t.Errorf("IsValidAgentID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractAgentIDsFromMessage(t *testing.T) {
	tests := []struct {
		name    string
		message map[string]interface{}
		want    []string
	}{
		{
			name:    "non-user message returns nil",
			message: map[string]interface{}{"type": "assistant"},
			want:    nil,
		},
		{
			name: "root level agentId - legacy 8-char hex",
			message: map[string]interface{}{
				"type": "user",
				"toolUseResult": map[string]interface{}{
					"agentId": "abcd1234",
				},
			},
			want: []string{"abcd1234"},
		},
		{
			name: "root level agentId - 17-char hex",
			message: map[string]interface{}{
				"type": "user",
				"toolUseResult": map[string]interface{}{
					"agentId": "a3eaf63159a07953f",
				},
			},
			want: []string{"a3eaf63159a07953f"},
		},
		{
			name: "root level agentId - compact format",
			message: map[string]interface{}{
				"type": "user",
				"toolUseResult": map[string]interface{}{
					"agentId": "acompact-2aaa241e456ebc94",
				},
			},
			want: []string{"acompact-2aaa241e456ebc94"},
		},
		{
			name: "nested agentId in content",
			message: map[string]interface{}{
				"type": "user",
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{
							"type": "tool_result",
							"content": map[string]interface{}{
								"toolUseResult": map[string]interface{}{
									"agentId": "12345678",
								},
							},
						},
					},
				},
			},
			want: []string{"12345678"},
		},
		{
			name: "invalid agentId is filtered - too short",
			message: map[string]interface{}{
				"type": "user",
				"toolUseResult": map[string]interface{}{
					"agentId": "abc",
				},
			},
			want: nil,
		},
		{
			name: "invalid agentId is filtered - bad chars",
			message: map[string]interface{}{
				"type": "user",
				"toolUseResult": map[string]interface{}{
					"agentId": "not valid!",
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractAgentIDsFromMessage(tt.message)
			if !sliceEqual(got, tt.want) {
				t.Errorf("ExtractAgentIDsFromMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

// sliceEqual compares two string slices for equality
func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
