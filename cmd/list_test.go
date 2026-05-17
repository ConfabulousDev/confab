package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"seconds", 30 * time.Second, "30s ago"},
		{"minutes", 5 * time.Minute, "5m ago"},
		{"hours", 3 * time.Hour, "3h ago"},
		{"days", 48 * time.Hour, "2d ago"},
		{"mixed hours and minutes shows just hours", 2*time.Hour + 30*time.Minute, "2h ago"},
		{"just under a minute", 59 * time.Second, "59s ago"},
		{"just under an hour", 59 * time.Minute, "59m ago"},
		{"just under a day", 23 * time.Hour, "23h ago"},
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
		session        provider.SessionInfo
		wantContainsID string
		wantTitle      string
	}{
		{
			name: "session with summary",
			session: provider.SessionInfo{
				SessionID: "aaaaaaaa-1111-1111-1111-111111111111",
				Summary:   "Fix authentication bug",
				ModTime:   now.Add(-2 * time.Hour),
			},
			wantContainsID: "aaaaaaaa",
			wantTitle:      "Fix authentication bug",
		},
		{
			name: "session with first user message only",
			session: provider.SessionInfo{
				SessionID:        "bbbbbbbb-2222-2222-2222-222222222222",
				FirstUserMessage: "Help me refactor",
				ModTime:          now.Add(-1 * time.Hour),
			},
			wantContainsID: "bbbbbbbb",
			wantTitle:      "Help me refactor",
		},
		{
			name: "session with both - summary takes precedence",
			session: provider.SessionInfo{
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
			session: provider.SessionInfo{
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

			if len(id) != 8 {
				t.Errorf("Expected ID length 8, got %d (%q)", len(id), id)
			}
			if id != tt.wantContainsID {
				t.Errorf("Expected ID to start with %q, got %q", tt.wantContainsID, id)
			}
			if title != tt.wantTitle {
				t.Errorf("Expected title %q, got %q", tt.wantTitle, title)
			}
			if activity == "" {
				t.Error("Expected activity to be non-empty")
			}
		})
	}
}

func TestListSessions_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_CLAUDE_DIR", tmpDir)

	projectsDir := filepath.Join(tmpDir, "projects")
	project1 := filepath.Join(projectsDir, "test-project")
	os.MkdirAll(project1, 0755)

	session1Content := `{"type":"user","message":{"content":"Fix the auth bug"}}
{"type":"summary","summary":"Fix auth bug"}`
	session2Content := `{"type":"user","message":{"content":"Help me refactor"}}`

	session1Path := filepath.Join(project1, "aaaaaaaa-1111-1111-1111-111111111111.jsonl")
	session2Path := filepath.Join(project1, "bbbbbbbb-2222-2222-2222-222222222222.jsonl")

	os.WriteFile(session1Path, []byte(session1Content), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(session2Path, []byte(session2Content), 0644)

	sessions, err := provider.ClaudeCode{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions() error = %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(sessions))
	}

	sessionMap := make(map[string]provider.SessionInfo)
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
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_CLAUDE_DIR", tmpDir)

	projectsDir := filepath.Join(tmpDir, "projects")
	project := filepath.Join(projectsDir, "test-project")
	os.MkdirAll(project, 0755)

	recentSession := filepath.Join(project, "aaaaaaaa-1111-1111-1111-111111111111.jsonl")
	if err := os.WriteFile(recentSession, []byte(`{"type":"summary","summary":"Recent session"}`), 0644); err != nil {
		t.Fatalf("write recent session: %v", err)
	}

	// Plant an old session that must be filtered OUT. Without this,
	// the test would pass even if scanAndFilterSessions returned every
	// session regardless of duration — the assertion `len == 1` would
	// be satisfied by the recent-only setup.
	oldSession := filepath.Join(project, "bbbbbbbb-2222-2222-2222-222222222222.jsonl")
	if err := os.WriteFile(oldSession, []byte(`{"type":"summary","summary":"Old session"}`), 0644); err != nil {
		t.Fatalf("write old session: %v", err)
	}
	oldTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(oldSession, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old session: %v", err)
	}

	filtered, err := scanAndFilterSessions(provider.ClaudeCode{}, "1h")
	if err != nil {
		t.Fatalf("scanAndFilterSessions error: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("Expected 1 session within last hour, got %d", len(filtered))
	}
	if got := filtered[0].SessionID; got != "aaaaaaaa-1111-1111-1111-111111111111" {
		t.Errorf("expected recent session retained, got %q", got)
	}

	// Sanity-check the inverse: with no filter, both should appear.
	all, err := scanAndFilterSessions(provider.ClaudeCode{}, "")
	if err != nil {
		t.Fatalf("scanAndFilterSessions(unfiltered) error: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("Expected 2 sessions without filter, got %d", len(all))
	}
}
