package discovery

import (
	"testing"
)

func TestIsHexString(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abcdef", true},
		{"ABCDEF", true},
		{"123456", true},
		{"abcd1234", true},
		{"ABCD1234", true},
		{"ghijkl", false},
		{"abcdefg", false},
		{"12345z", false},
		{"", true}, // empty string has no non-hex chars
		{"abc def", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isHexString(tt.input)
			if got != tt.want {
				t.Errorf("isHexString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidAgentID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abcd1234", true},
		{"ABCD1234", true},
		{"12345678", true},
		{"abcdefgh", false}, // 'g' and 'h' not hex
		{"abc123", false},   // too short
		{"abcd12345", false}, // too long
		{"", false},
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
			name: "root level agentId",
			message: map[string]interface{}{
				"type": "user",
				"toolUseResult": map[string]interface{}{
					"agentId": "abcd1234",
				},
			},
			want: []string{"abcd1234"},
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
			name: "invalid agentId is filtered",
			message: map[string]interface{}{
				"type": "user",
				"toolUseResult": map[string]interface{}{
					"agentId": "not-valid",
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
