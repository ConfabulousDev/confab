package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSessionMetadata(t *testing.T) {
	tests := []struct {
		name                 string
		content              string
		expectedSummary      string
		expectedFirstUserMsg string
	}{
		{
			name:                 "empty file",
			content:              "",
			expectedSummary:      "",
			expectedFirstUserMsg: "",
		},
		{
			name:                 "summary without leafUuid is captured",
			content:              `{"type":"summary","summary":"Fix authentication bug in login flow"}`,
			expectedSummary:      "Fix authentication bug in login flow",
			expectedFirstUserMsg: "",
		},
		{
			name: "summary without leafUuid is captured with user message",
			content: `{"type":"summary","summary":"Session summary"}
{"type":"user","message":{"content":"Help me with a new task"}}`,
			expectedSummary:      "Session summary",
			expectedFirstUserMsg: "Help me with a new task",
		},
		{
			name: "summary after user message is captured",
			content: `{"type":"user","message":{"content":"Help me fix a bug"}}
{"type":"assistant","message":{"content":"Sure!"}}
{"type":"summary","summary":"Bug fix assistance"}`,
			expectedSummary:      "Bug fix assistance",
			expectedFirstUserMsg: "Help me fix a bug",
		},
		{
			name:                 "user message only",
			content:              `{"type":"user","message":{"content":"Can you help me refactor this function?"}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "Can you help me refactor this function?",
		},
		{
			name:                 "long user message NOT truncated",
			content:              `{"type":"user","message":{"content":"This is a very long message that should NOT be truncated because we now send full content up to 10KB limit which is enforced by the backend not the CLI"}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "This is a very long message that should NOT be truncated because we now send full content up to 10KB limit which is enforced by the backend not the CLI",
		},
		{
			name: "HTML tags removed",
			content: `{"type":"user","message":{"content":"help"}}
{"type":"summary","summary":"Fix <code>auth</code> bug"}`,
			expectedSummary:      "Fix auth bug",
			expectedFirstUserMsg: "help",
		},
		{
			name: "HTML entities decoded",
			content: `{"type":"user","message":{"content":"help"}}
{"type":"summary","summary":"Fix &lt;div&gt; rendering"}`,
			expectedSummary:      "Fix <div> rendering",
			expectedFirstUserMsg: "help",
		},
		{
			name:                 "newlines collapsed",
			content:              `{"type":"user","message":{"content":"Line one\nLine two\nLine three"}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "Line one Line two Line three",
		},
		{
			name:                 "no user or summary messages",
			content:              `{"type":"assistant","message":{"content":"Hello!"}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "",
		},
		{
			name:                 "multimodal message with text block",
			content:              `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Help me with this image"}]}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "Help me with this image",
		},
		{
			name:                 "multimodal message with image first then text",
			content:              `{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}},{"type":"text","text":"What is in this screenshot?"}]}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "What is in this screenshot?",
		},
		{
			name:                 "multimodal message with only image",
			content:              `{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}]}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "",
		},
		{
			name: "multimodal first message no text, second message has text",
			content: `{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Now explain this"}]}}`,
			expectedSummary:      "",
			expectedFirstUserMsg: "Now explain this",
		},
		{
			name: "both summary and first user message captured",
			content: `{"type":"user","message":{"content":"First user message here"}}
{"type":"assistant","message":{"content":"Response"}}
{"type":"summary","summary":"This is the summary"}`,
			expectedSummary:      "This is the summary",
			expectedFirstUserMsg: "First user message here",
		},
		{
			name: "multiple summaries - last without leafUuid captured",
			content: `{"type":"summary","summary":"First summary"}
{"type":"user","message":{"content":"User message"}}
{"type":"summary","summary":"Second summary"}
{"type":"summary","summary":"Third summary"}`,
			expectedSummary:      "Third summary",
			expectedFirstUserMsg: "User message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file with test content
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.jsonl")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write temp file: %v", err)
			}

			result := ExtractSessionMetadata(tmpFile)
			if result.Summary != tt.expectedSummary {
				t.Errorf("Summary = %q, want %q", result.Summary, tt.expectedSummary)
			}
			if result.FirstUserMessage != tt.expectedFirstUserMsg {
				t.Errorf("FirstUserMessage = %q, want %q", result.FirstUserMessage, tt.expectedFirstUserMsg)
			}
		})
	}
}

func TestExtractSessionMetadata_NonexistentFile(t *testing.T) {
	result := ExtractSessionMetadata("/nonexistent/path/file.jsonl")
	if result.Summary != "" || result.FirstUserMessage != "" {
		t.Errorf("Expected empty result for nonexistent file, got Summary=%q, FirstUserMessage=%q",
			result.Summary, result.FirstUserMessage)
	}
}

func TestExtractTextFromMessage(t *testing.T) {
	tests := []struct {
		name     string
		entry    map[string]interface{}
		expected string
	}{
		{
			name:     "nil entry",
			entry:    nil,
			expected: "",
		},
		{
			name:     "no message field",
			entry:    map[string]interface{}{"type": "user"},
			expected: "",
		},
		{
			name: "string content",
			entry: map[string]interface{}{
				"message": map[string]interface{}{
					"content": "Hello world",
				},
			},
			expected: "Hello world",
		},
		{
			name: "array content with text block",
			entry: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "text", "text": "First text"},
					},
				},
			},
			expected: "First text",
		},
		{
			name: "array content with image then text",
			entry: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "image", "source": map[string]interface{}{}},
						map[string]interface{}{"type": "text", "text": "Second block text"},
					},
				},
			},
			expected: "Second block text",
		},
		{
			name: "array content with only image",
			entry: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{
						map[string]interface{}{"type": "image", "source": map[string]interface{}{}},
					},
				},
			},
			expected: "",
		},
		{
			name: "empty array content",
			entry: map[string]interface{}{
				"message": map[string]interface{}{
					"content": []interface{}{},
				},
			},
			expected: "",
		},
		{
			name: "nil content",
			entry: map[string]interface{}{
				"message": map[string]interface{}{
					"content": nil,
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTextFromMessage(tt.entry)
			if result != tt.expected {
				t.Errorf("extractTextFromMessage() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestSanitizeText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text",
			input:    "Hello world",
			expected: "Hello world",
		},
		{
			name:     "HTML tags",
			input:    "<p>Hello</p> <strong>world</strong>",
			expected: "Hello world",
		},
		{
			name:     "HTML entities",
			input:    "&lt;div&gt; &amp; &quot;test&quot;",
			expected: "<div> & \"test\"",
		},
		{
			name:     "whitespace normalization",
			input:    "  multiple   spaces  ",
			expected: "multiple spaces",
		},
		{
			name:     "newlines",
			input:    "line1\nline2\r\nline3",
			expected: "line1 line2 line3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeText(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeText(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		expected string
	}{
		{
			name:     "no truncation needed",
			input:    "hello",
			maxBytes: 10,
			expected: "hello",
		},
		{
			name:     "exact length",
			input:    "hello",
			maxBytes: 5,
			expected: "hello",
		},
		{
			name:     "truncate ASCII",
			input:    "hello world",
			maxBytes: 8,
			expected: "hello...",
		},
		{
			name:     "truncate at UTF-8 boundary",
			input:    "hello 世界 world", // 世 is 3 bytes, 界 is 3 bytes
			maxBytes: 12,                  // 9 bytes content + 3 for "..."
			expected: "hello 世...",
		},
		{
			name:     "truncate mid-UTF8 removes partial char",
			input:    "hello 世界", // "hello " = 6 bytes, 世 = 3 bytes
			maxBytes: 10,           // Would cut in middle of 世, should remove it
			expected: "hello ...",
		},
		{
			name:     "very small limit",
			input:    "hello",
			maxBytes: 3,
			expected: "...",
		},
		{
			name:     "empty string",
			input:    "",
			maxBytes: 10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxBytes)
			if result != tt.expected {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxBytes, result, tt.expected)
			}
		})
	}
}

func TestExtractSessionMetadata_LongContent(t *testing.T) {
	// Test that long content is truncated to MaxMetadataFieldSize/2
	longMessage := strings.Repeat("a", 5000) // 5KB message, above 4KB limit
	content := `{"type":"user","message":{"content":"` + longMessage + `"}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.jsonl")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	result := ExtractSessionMetadata(tmpFile)
	expectedLen := MaxMetadataFieldSize / 2 // 4KB
	if len(result.FirstUserMessage) != expectedLen {
		t.Errorf("Expected FirstUserMessage length %d, got %d", expectedLen, len(result.FirstUserMessage))
	}
	if !strings.HasSuffix(result.FirstUserMessage, "...") {
		t.Errorf("Expected truncated message to end with '...', got %q", result.FirstUserMessage[len(result.FirstUserMessage)-10:])
	}
}

func TestExtractMetadataFromLines(t *testing.T) {
	tests := []struct {
		name                 string
		lines                []string
		expectedSummary      string
		expectedFirstUserMsg string
		expectedSummaryLinks []SummaryLink
	}{
		{
			name:                 "empty lines",
			lines:                []string{},
			expectedSummary:      "",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: nil,
		},
		{
			name: "local summary (no leafUuid)",
			lines: []string{
				`{"type":"summary","summary":"Local session summary"}`,
			},
			expectedSummary:      "Local session summary",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: nil,
		},
		{
			name: "summary with leafUuid goes to SummaryLinks",
			lines: []string{
				`{"type":"summary","summary":"Previous session summary","leafUuid":"abc-123"}`,
			},
			expectedSummary:      "",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: []SummaryLink{
				{Summary: "Previous session summary", LeafUUID: "abc-123"},
			},
		},
		{
			name: "both local and linked summaries",
			lines: []string{
				`{"type":"summary","summary":"Linked summary","leafUuid":"uuid-1"}`,
				`{"type":"user","message":{"content":"User message"}}`,
				`{"type":"summary","summary":"Local summary"}`,
			},
			expectedSummary:      "Local summary",
			expectedFirstUserMsg: "User message",
			expectedSummaryLinks: []SummaryLink{
				{Summary: "Linked summary", LeafUUID: "uuid-1"},
			},
		},
		{
			name: "multiple linked summaries",
			lines: []string{
				`{"type":"summary","summary":"First linked","leafUuid":"uuid-1"}`,
				`{"type":"summary","summary":"Second linked","leafUuid":"uuid-2"}`,
			},
			expectedSummary:      "",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: []SummaryLink{
				{Summary: "First linked", LeafUUID: "uuid-1"},
				{Summary: "Second linked", LeafUUID: "uuid-2"},
			},
		},
		{
			name: "first user message captured",
			lines: []string{
				`{"type":"user","message":{"content":"First message"}}`,
				`{"type":"user","message":{"content":"Second message"}}`,
			},
			expectedSummary:      "",
			expectedFirstUserMsg: "First message",
			expectedSummaryLinks: nil,
		},
		{
			name: "last local summary captured",
			lines: []string{
				`{"type":"summary","summary":"First local"}`,
				`{"type":"summary","summary":"Second local"}`,
			},
			expectedSummary:      "Second local",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: nil,
		},
		{
			name: "HTML sanitization applied",
			lines: []string{
				`{"type":"summary","summary":"<b>Bold</b> &amp; text"}`,
				`{"type":"user","message":{"content":"<p>Para</p>"}}`,
			},
			expectedSummary:      "Bold & text",
			expectedFirstUserMsg: "Para",
			expectedSummaryLinks: nil,
		},
		{
			name: "multimodal user message",
			lines: []string{
				`{"type":"user","message":{"content":[{"type":"text","text":"Help with image"}]}}`,
			},
			expectedSummary:      "",
			expectedFirstUserMsg: "Help with image",
			expectedSummaryLinks: nil,
		},
		{
			name: "empty summary ignored",
			lines: []string{
				`{"type":"summary","summary":""}`,
				`{"type":"summary","summary":"Real summary"}`,
			},
			expectedSummary:      "Real summary",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: nil,
		},
		{
			name: "invalid JSON lines skipped",
			lines: []string{
				`not valid json`,
				`{"type":"summary","summary":"Valid summary"}`,
			},
			expectedSummary:      "Valid summary",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: nil,
		},
		{
			name: "blank lines skipped",
			lines: []string{
				``,
				`   `,
				`{"type":"summary","summary":"Summary after blanks"}`,
			},
			expectedSummary:      "Summary after blanks",
			expectedFirstUserMsg: "",
			expectedSummaryLinks: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMetadataFromLines(tt.lines)

			if result.Summary != tt.expectedSummary {
				t.Errorf("Summary = %q, want %q", result.Summary, tt.expectedSummary)
			}
			if result.FirstUserMessage != tt.expectedFirstUserMsg {
				t.Errorf("FirstUserMessage = %q, want %q", result.FirstUserMessage, tt.expectedFirstUserMsg)
			}

			// Check SummaryLinks
			if len(result.SummaryLinks) != len(tt.expectedSummaryLinks) {
				t.Errorf("SummaryLinks length = %d, want %d", len(result.SummaryLinks), len(tt.expectedSummaryLinks))
			} else {
				for i, link := range result.SummaryLinks {
					if link.Summary != tt.expectedSummaryLinks[i].Summary {
						t.Errorf("SummaryLinks[%d].Summary = %q, want %q", i, link.Summary, tt.expectedSummaryLinks[i].Summary)
					}
					if link.LeafUUID != tt.expectedSummaryLinks[i].LeafUUID {
						t.Errorf("SummaryLinks[%d].LeafUUID = %q, want %q", i, link.LeafUUID, tt.expectedSummaryLinks[i].LeafUUID)
					}
				}
			}
		})
	}
}
