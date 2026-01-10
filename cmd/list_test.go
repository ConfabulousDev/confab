package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/discovery"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "seconds",
			duration: 30 * time.Second,
			expected: "30s ago",
		},
		{
			name:     "minutes",
			duration: 5 * time.Minute,
			expected: "5m ago",
		},
		{
			name:     "hours",
			duration: 3 * time.Hour,
			expected: "3h ago",
		},
		{
			name:     "days",
			duration: 48 * time.Hour,
			expected: "2d ago",
		},
		{
			name:     "mixed hours and minutes shows just hours",
			duration: 2*time.Hour + 30*time.Minute,
			expected: "2h ago",
		},
		{
			name:     "just under a minute",
			duration: 59 * time.Second,
			expected: "59s ago",
		},
		{
			name:     "just under an hour",
			duration: 59 * time.Minute,
			expected: "59m ago",
		},
		{
			name:     "just under a day",
			duration: 23 * time.Hour,
			expected: "23h ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, result, tt.expected)
			}
		})
	}
}

func TestFormatSessionRow(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		session        discovery.SessionInfo
		wantContainsID string
		wantTitle      string
	}{
		{
			name: "session with summary",
			session: discovery.SessionInfo{
				SessionID: "aaaaaaaa-1111-1111-1111-111111111111",
				Summary:   "Fix authentication bug",
				ModTime:   now.Add(-2 * time.Hour),
			},
			wantContainsID: "aaaaaaaa",
			wantTitle:      "Fix authentication bug",
		},
		{
			name: "session with first user message only",
			session: discovery.SessionInfo{
				SessionID:        "bbbbbbbb-2222-2222-2222-222222222222",
				FirstUserMessage: "Help me refactor",
				ModTime:          now.Add(-1 * time.Hour),
			},
			wantContainsID: "bbbbbbbb",
			wantTitle:      "Help me refactor",
		},
		{
			name: "session with both - summary takes precedence",
			session: discovery.SessionInfo{
				SessionID:        "cccccccc-3333-3333-3333-333333333333",
				Summary:          "The summary",
				FirstUserMessage: "The user message",
				ModTime:          now.Add(-30 * time.Minute),
			},
			wantContainsID: "cccccccc",
			wantTitle:      "The summary",
		},
		{
			name: "session without title",
			session: discovery.SessionInfo{
				SessionID: "dddddddd-4444-4444-4444-444444444444",
				ModTime:   now.Add(-1 * time.Hour),
			},
			wantContainsID: "dddddddd",
			wantTitle:      "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, title, activity := formatSessionRow(tt.session)

			// Check ID is truncated to 8 chars
			if len(id) != 8 {
				t.Errorf("Expected ID length 8, got %d (%q)", len(id), id)
			}
			if id != tt.wantContainsID {
				t.Errorf("Expected ID to start with %q, got %q", tt.wantContainsID, id)
			}

			// Check title
			if title != tt.wantTitle {
				t.Errorf("Expected title %q, got %q", tt.wantTitle, title)
			}

			// Check activity is formatted
			if activity == "" {
				t.Error("Expected activity to be non-empty")
			}
		})
	}
}

func TestListSessions_Integration(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	oldEnv := os.Getenv("CONFAB_CLAUDE_DIR")
	os.Setenv("CONFAB_CLAUDE_DIR", tmpDir)
	defer os.Setenv("CONFAB_CLAUDE_DIR", oldEnv)

	// Create projects directory with sessions
	projectsDir := filepath.Join(tmpDir, "projects")
	project1 := filepath.Join(projectsDir, "test-project")
	os.MkdirAll(project1, 0755)

	// Create session files with content
	// Session 1: Summary after user message (valid - this session's summary)
	session1Content := `{"type":"user","message":{"content":"Fix the auth bug"}}
{"type":"summary","summary":"Fix auth bug"}`
	// Session 2: Just a user message (no summary yet)
	session2Content := `{"type":"user","message":{"content":"Help me refactor"}}`

	session1Path := filepath.Join(project1, "aaaaaaaa-1111-1111-1111-111111111111.jsonl")
	session2Path := filepath.Join(project1, "bbbbbbbb-2222-2222-2222-222222222222.jsonl")

	os.WriteFile(session1Path, []byte(session1Content), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(session2Path, []byte(session2Content), 0644)

	// Test listing sessions
	sessions, err := discovery.ScanAllSessions()
	if err != nil {
		t.Fatalf("ScanAllSessions() error = %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(sessions))
	}

	// Verify metadata was extracted
	sessionMap := make(map[string]discovery.SessionInfo)
	for _, s := range sessions {
		sessionMap[s.SessionID] = s
	}

	s1 := sessionMap["aaaaaaaa-1111-1111-1111-111111111111"]
	if s1.Summary != "Fix auth bug" {
		t.Errorf("Expected Summary 'Fix auth bug', got %q", s1.Summary)
	}
	if s1.FirstUserMessage != "Fix the auth bug" {
		t.Errorf("Expected FirstUserMessage 'Fix the auth bug', got %q", s1.FirstUserMessage)
	}

	s2 := sessionMap["bbbbbbbb-2222-2222-2222-222222222222"]
	if s2.Summary != "" {
		t.Errorf("Expected empty Summary, got %q", s2.Summary)
	}
	if s2.FirstUserMessage != "Help me refactor" {
		t.Errorf("Expected FirstUserMessage 'Help me refactor', got %q", s2.FirstUserMessage)
	}
}

func TestListSessions_FilterByDuration(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	oldEnv := os.Getenv("CONFAB_CLAUDE_DIR")
	os.Setenv("CONFAB_CLAUDE_DIR", tmpDir)
	defer os.Setenv("CONFAB_CLAUDE_DIR", oldEnv)

	// Create projects directory
	projectsDir := filepath.Join(tmpDir, "projects")
	project := filepath.Join(projectsDir, "test-project")
	os.MkdirAll(project, 0755)

	// Create session files
	recentSession := filepath.Join(project, "aaaaaaaa-1111-1111-1111-111111111111.jsonl")
	os.WriteFile(recentSession, []byte(`{"type":"summary","summary":"Recent session"}`), 0644)

	// Get all sessions
	sessions, err := discovery.ScanAllSessions()
	if err != nil {
		t.Fatalf("ScanAllSessions() error = %v", err)
	}

	// Filter by duration (1 hour)
	cutoff := time.Now().Add(-1 * time.Hour)
	var filtered []discovery.SessionInfo
	for _, s := range sessions {
		if s.ModTime.After(cutoff) {
			filtered = append(filtered, s)
		}
	}

	// The recent session should be included
	if len(filtered) != 1 {
		t.Errorf("Expected 1 session within last hour, got %d", len(filtered))
	}
}

