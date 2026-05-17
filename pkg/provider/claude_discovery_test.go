package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockDirEntry implements os.DirEntry for testing parseClaudeSessionFromPath.
type mockDirEntry struct {
	name  string
	isDir bool
	info  os.FileInfo
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                { return m.isDir }
func (m mockDirEntry) Type() os.FileMode          { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error) { return m.info, nil }

func TestParseClaudeSessionFromPath(t *testing.T) {
	tmpDir := t.TempDir()
	projectsDir := filepath.Join(tmpDir, "projects")
	projectDir := filepath.Join(projectsDir, "test-project")
	os.MkdirAll(projectDir, 0755)

	tests := []struct {
		name       string
		filename   string
		wantNil    bool
		wantID     string
		createFile bool
	}{
		{
			name:       "valid session file",
			filename:   "12345678-1234-1234-1234-123456789abc.jsonl",
			wantNil:    false,
			wantID:     "12345678-1234-1234-1234-123456789abc",
			createFile: true,
		},
		{
			name:       "agent file should be skipped",
			filename:   "agent-abcd1234.jsonl",
			wantNil:    true,
			createFile: true,
		},
		{
			name:       "non-jsonl file should be skipped",
			filename:   "readme.txt",
			wantNil:    true,
			createFile: true,
		},
		{
			name:       "short uuid should be skipped",
			filename:   "short-id.jsonl",
			wantNil:    true,
			createFile: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(projectDir, tt.filename)
			if tt.createFile {
				os.WriteFile(filePath, []byte("{}"), 0644)
			}

			info, _ := os.Stat(filePath)
			entry := mockDirEntry{
				name:  tt.filename,
				isDir: false,
				info:  info,
			}

			result := parseClaudeSessionFromPath(filePath, entry, projectsDir)

			if tt.wantNil && result != nil {
				t.Errorf("expected nil, got %+v", result)
			}
			if !tt.wantNil && result == nil {
				t.Error("expected result, got nil")
			}
			if !tt.wantNil && result != nil && result.SessionID != tt.wantID {
				t.Errorf("expected SessionID %q, got %q", tt.wantID, result.SessionID)
			}
		})
	}
}

func TestParseClaudeSessionFromPath_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	entry := mockDirEntry{name: "somedir", isDir: true}

	result := parseClaudeSessionFromPath(tmpDir, entry, tmpDir)
	if result != nil {
		t.Errorf("expected nil for directory, got %+v", result)
	}
}

func TestClaudeCodeScanSessions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	projectsDir := filepath.Join(tmpDir, "projects")
	project1 := filepath.Join(projectsDir, "project1")
	project2 := filepath.Join(projectsDir, "project2")
	os.MkdirAll(project1, 0755)
	os.MkdirAll(project2, 0755)

	session1 := "aaaaaaaa-1111-1111-1111-111111111111.jsonl"
	session2 := "bbbbbbbb-2222-2222-2222-222222222222.jsonl"
	session3 := "cccccccc-3333-3333-3333-333333333333.jsonl"

	os.WriteFile(filepath.Join(project1, session1), []byte("{}"), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(project2, session2), []byte("{}"), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(project1, session3), []byte("{}"), 0644)

	// Files that should be ignored
	os.WriteFile(filepath.Join(project1, "agent-12345678.jsonl"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(project1, "readme.txt"), []byte("{}"), 0644)

	sessions, err := ClaudeCode{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("Expected 3 sessions, got %d", len(sessions))
	}

	foundIDs := make(map[string]bool)
	for _, s := range sessions {
		foundIDs[s.SessionID] = true
	}

	expectedIDs := []string{
		"aaaaaaaa-1111-1111-1111-111111111111",
		"bbbbbbbb-2222-2222-2222-222222222222",
		"cccccccc-3333-3333-3333-333333333333",
	}
	for _, id := range expectedIDs {
		if !foundIDs[id] {
			t.Errorf("Expected to find session %s", id)
		}
	}

	if len(sessions) >= 2 && sessions[0].ModTime.After(sessions[1].ModTime) {
		t.Error("Sessions not sorted by mod time (oldest first)")
	}
}

func TestClaudeCodeScanSessions_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	projectsDir := filepath.Join(tmpDir, "projects")
	os.MkdirAll(projectsDir, 0755)

	sessions, err := ClaudeCode{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("Expected 0 sessions in empty directory, got %d", len(sessions))
	}
}

func TestClaudeCodeScanSessions_NoProjectsDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	sessions, err := ClaudeCode{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}
	if sessions != nil {
		t.Errorf("Expected nil for non-existent directory, got %d sessions", len(sessions))
	}
}

func TestClaudeCodeFindSessionByID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	projectsDir := filepath.Join(tmpDir, "projects")
	project1 := filepath.Join(projectsDir, "project1")
	os.MkdirAll(project1, 0755)

	sessionID := "aaaaaaaa-1111-1111-1111-111111111111"
	sessionFile := sessionID + ".jsonl"
	sessionPath := filepath.Join(project1, sessionFile)
	os.WriteFile(sessionPath, []byte("{}"), 0644)

	tests := []struct {
		name      string
		searchID  string
		wantFound bool
		wantID    string
	}{
		{"find by full ID", "aaaaaaaa-1111-1111-1111-111111111111", true, "aaaaaaaa-1111-1111-1111-111111111111"},
		{"find by 8-char prefix", "aaaaaaaa", true, "aaaaaaaa-1111-1111-1111-111111111111"},
		{"find by 4-char prefix", "aaaa", true, "aaaaaaaa-1111-1111-1111-111111111111"},
		{"not found", "nonexistent", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fullID, transcriptPath, err := ClaudeCode{}.FindSessionByID(tt.searchID)

			if tt.wantFound {
				if err != nil {
					t.Errorf("Expected to find session, got error: %v", err)
					return
				}
				if fullID != tt.wantID {
					t.Errorf("Expected ID %s, got %s", tt.wantID, fullID)
				}
				if transcriptPath != sessionPath {
					t.Errorf("Expected path %s, got %s", sessionPath, transcriptPath)
				}
			} else {
				if err == nil {
					t.Error("Expected error for non-existent session")
				}
			}
		})
	}
}

func TestClaudeCodeFindSessionByID_AmbiguousID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	projectsDir := filepath.Join(tmpDir, "projects")
	project1 := filepath.Join(projectsDir, "project1")
	os.MkdirAll(project1, 0755)

	session1 := "aaaa1111-1111-1111-1111-111111111111.jsonl"
	session2 := "aaaa2222-2222-2222-2222-222222222222.jsonl"
	os.WriteFile(filepath.Join(project1, session1), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(project1, session2), []byte("{}"), 0644)

	_, _, err := ClaudeCode{}.FindSessionByID("aaaa")
	if err == nil {
		t.Error("Expected error for ambiguous session ID")
	}
	if err != nil && !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("Expected 'ambiguous' error, got: %v", err)
	}
}

func TestExtractClaudeSessionMetadataFromFile(t *testing.T) {
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
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.jsonl")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write temp file: %v", err)
			}

			result := extractClaudeSessionMetadataFromFile(tmpFile)
			if result.Summary != tt.expectedSummary {
				t.Errorf("Summary = %q, want %q", result.Summary, tt.expectedSummary)
			}
			if result.FirstUserMessage != tt.expectedFirstUserMsg {
				t.Errorf("FirstUserMessage = %q, want %q", result.FirstUserMessage, tt.expectedFirstUserMsg)
			}
		})
	}
}

func TestExtractClaudeSessionMetadataFromFile_NonexistentFile(t *testing.T) {
	result := extractClaudeSessionMetadataFromFile("/nonexistent/path/file.jsonl")
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
		{"plain text", "Hello world", "Hello world"},
		{"HTML tags", "<p>Hello</p> <strong>world</strong>", "Hello world"},
		{"HTML entities", "&lt;div&gt; &amp; &quot;test&quot;", "<div> & \"test\""},
		{"whitespace normalization", "  multiple   spaces  ", "multiple spaces"},
		{"newlines", "line1\nline2\r\nline3", "line1 line2 line3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeText(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeText(%q) = %q, want %q", tt.input, result, tt.expected)
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
		{"no truncation needed", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncate ASCII", "hello world", 8, "hello..."},
		{"truncate at UTF-8 boundary", "hello 世界 world", 12, "hello 世..."},
		{"truncate mid-UTF8 removes partial char", "hello 世界", 10, "hello ..."},
		{"very small limit", "hello", 3, "..."},
		{"empty string", "", 10, ""},
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

func TestExtractClaudeSessionMetadataFromFile_LongContent(t *testing.T) {
	longMessage := strings.Repeat("a", 5000) // 5KB message, above 4KB limit
	content := `{"type":"user","message":{"content":"` + longMessage + `"}}`

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.jsonl")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	result := extractClaudeSessionMetadataFromFile(tmpFile)
	expectedLen := maxMetadataFieldSize / 2 // 4KB
	if len(result.FirstUserMessage) != expectedLen {
		t.Errorf("Expected FirstUserMessage length %d, got %d", expectedLen, len(result.FirstUserMessage))
	}
	if !strings.HasSuffix(result.FirstUserMessage, "...") {
		t.Errorf("Expected truncated message to end with '...', got %q", result.FirstUserMessage[len(result.FirstUserMessage)-10:])
	}
}

func TestClaudeCodeExtractMetadata(t *testing.T) {
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
			result := ClaudeCode{}.ExtractMetadata(tt.lines)

			if result.Summary != tt.expectedSummary {
				t.Errorf("Summary = %q, want %q", result.Summary, tt.expectedSummary)
			}
			if result.FirstUserMessage != tt.expectedFirstUserMsg {
				t.Errorf("FirstUserMessage = %q, want %q", result.FirstUserMessage, tt.expectedFirstUserMsg)
			}

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

func TestClaudeCodeDefaultCWD(t *testing.T) {
	got := ClaudeCode{}.DefaultCWD("/path/to/transcript.jsonl")
	want := "/path/to"
	if got != want {
		t.Errorf("DefaultCWD = %q, want %q", got, want)
	}
}
