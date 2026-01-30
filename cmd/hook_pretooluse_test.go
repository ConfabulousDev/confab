package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// testBackendURL is the backend URL used in tests
const testBackendURL = "https://test.example.com"

// setupTestState creates a daemon state file for testing and returns a cleanup function.
// It creates the state in a temp directory by overriding HOME.
// Also creates a config file with a test backend URL.
func setupTestState(t *testing.T, claudeSessionID, confabSessionID string) func() {
	t.Helper()

	// Create temp home directory
	tempHome, err := os.MkdirTemp("", "confab-test-home-*")
	if err != nil {
		t.Fatalf("Failed to create temp home: %v", err)
	}

	// Save original HOME and set new one
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)

	// Create state directory
	stateDir := filepath.Join(tempHome, ".confab", "sync")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("Failed to create state dir: %v", err)
	}

	// Create config with backend URL
	cfg := &config.UploadConfig{
		BackendURL: testBackendURL,
		APIKey:     "cfb_test_key_for_testing_purposes_only",
	}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}

	// Create state with ConfabSessionID
	state := daemon.NewState(claudeSessionID, "/fake/transcript.jsonl", "/fake/cwd", 0)
	state.ConfabSessionID = confabSessionID
	if err := state.Save(); err != nil {
		t.Fatalf("Failed to save test state: %v", err)
	}

	// Return cleanup function
	return func() {
		os.Setenv("HOME", origHome)
		os.RemoveAll(tempHome)
	}
}

func TestFindGitCommitPosition(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    int // -1 means not found, >= 0 means found at position
	}{
		{"simple git commit", "git commit -m 'test'", 0},
		{"git commit with flags", "git commit -am 'test'", 0},
		{"chained git add and commit", "git add . && git commit -m 'test'", 13},
		{"git commit with heredoc", "git commit -m \"$(cat <<'EOF'\ntest\nEOF\n)\"", 0},
		{"git status", "git status", -1},
		{"git push", "git push origin main", -1},
		{"npm install", "npm install", -1},
		{"empty command", "", -1},
		{"git log", "git log --oneline", -1},
		{"git diff", "git diff HEAD", -1},
		{"git with -C flag", "git -C /some/path commit -m 'test'", 0},
		{"git with multiple flags", "git --no-pager -C /path commit -m 'test'", 0},
		{"git with config flag", "git -c user.name=test commit -m 'test'", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findGitCommitPosition(tt.command)
			if tt.want < 0 && got >= 0 {
				t.Errorf("findGitCommitPosition(%q) = %d, want not found", tt.command, got)
			} else if tt.want >= 0 && got < 0 {
				t.Errorf("findGitCommitPosition(%q) = not found, want %d", tt.command, tt.want)
			}
		})
	}
}

func TestFindGitPushPosition(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    int // -1 means not found, >= 0 means found at position
	}{
		{"simple git push", "git push", 0},
		{"git push origin main", "git push origin main", 0},
		{"git push with flags", "git push -u origin main", 0},
		{"git push force", "git push --force", 0},
		{"chained commit and push", "git commit -m 'test' && git push", 25},
		{"git status", "git status", -1},
		{"git commit", "git commit -m 'test'", -1},
		{"npm install", "npm install", -1},
		{"empty command", "", -1},
		{"git with -C flag", "git -C /some/path push origin main", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findGitPushPosition(tt.command)
			if tt.want < 0 && got >= 0 {
				t.Errorf("findGitPushPosition(%q) = %d, want not found", tt.command, got)
			} else if tt.want >= 0 && got < 0 {
				t.Errorf("findGitPushPosition(%q) = not found, want %d", tt.command, tt.want)
			}
		})
	}
}

func TestContainsSessionURL(t *testing.T) {
	sessionID := "abc123"

	// Set up temp home with config
	tempHome, err := os.MkdirTemp("", "confab-test-home-*")
	if err != nil {
		t.Fatalf("Failed to create temp home: %v", err)
	}
	defer os.RemoveAll(tempHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	// Create config with backend URL
	cfg := &config.UploadConfig{
		BackendURL: testBackendURL,
		APIKey:     "cfb_test_key_for_testing_purposes_only",
	}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}

	sessionURL, err := formatSessionURL(sessionID)
	if err != nil {
		t.Fatalf("formatSessionURL() error = %v", err)
	}

	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{
			name:    "has session URL in commit",
			command: `git commit -m "Fix bug\n\nConfabulous-Link: ` + sessionURL + `"`,
			want:    true,
		},
		{
			name:    "has session URL in PR",
			command: `gh pr create --title "Fix" --body "📝 [Confabulous link](` + sessionURL + `)"`,
			want:    true,
		},
		{
			name:    "no URL",
			command: `git commit -m "Fix bug"`,
			want:    false,
		},
		{
			name:    "wrong session ID",
			command: `git commit -m "Fix bug\n\nConfabulous-Link: ` + testBackendURL + `/sessions/xyz789"`,
			want:    false,
		},
		{
			name:    "URL in heredoc",
			command: "git commit -m \"$(cat <<'EOF'\nFix bug\n\nConfabulous-Link: " + sessionURL + "\nEOF\n)\"",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsSessionURL(tt.command, sessionID)
			if got != tt.want {
				t.Errorf("containsSessionURL(%q, %q) = %v, want %v", tt.command, sessionID, got, tt.want)
			}
		})
	}
}

func TestFormatSessionURL(t *testing.T) {
	// Set up temp home with config
	tempHome, err := os.MkdirTemp("", "confab-test-home-*")
	if err != nil {
		t.Fatalf("Failed to create temp home: %v", err)
	}
	defer os.RemoveAll(tempHome)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	// Create config with backend URL
	cfg := &config.UploadConfig{
		BackendURL: "https://my-backend.example.com",
		APIKey:     "cfb_test_key_for_testing_purposes_only",
	}
	if err := config.SaveUploadConfig(cfg); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}

	got, err := formatSessionURL("test-session-123")
	if err != nil {
		t.Fatalf("formatSessionURL() error = %v", err)
	}
	want := "https://my-backend.example.com/sessions/test-session-123"
	if got != want {
		t.Errorf("formatSessionURL() = %q, want %q", got, want)
	}
}

func TestFormatTrailerLine(t *testing.T) {
	got := formatTrailerLine("https://confabulous.dev/sessions/abc123")
	want := "Confabulous-Link: https://confabulous.dev/sessions/abc123"
	if got != want {
		t.Errorf("formatTrailerLine() = %q, want %q", got, want)
	}
}

func TestHandlePreToolUse_NonBashTool(t *testing.T) {
	input := types.HookInput{
		SessionID:     "test-session",
		HookEventName: "PreToolUse",
		ToolName:      "Read",
		ToolInput:     map[string]any{"file_path": "/test.txt"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (silent allow)
	if w.Len() != 0 {
		t.Errorf("Expected empty output for non-Bash tool, got %q", w.String())
	}
}

func TestHandlePreToolUse_NonGitCommand(t *testing.T) {
	input := types.HookInput{
		SessionID:     "test-session",
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "npm install"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (silent allow)
	if w.Len() != 0 {
		t.Errorf("Expected empty output for non-git command, got %q", w.String())
	}
}

func TestHandlePreToolUse_GitCommitWithoutTrailer(t *testing.T) {
	claudeSessionID := "claude-session-123"
	confabSessionID := "confab-session-456"

	// Set up test state with Confab session ID
	cleanup := setupTestState(t, claudeSessionID, confabSessionID)
	defer cleanup()

	input := types.HookInput{
		SessionID:     claudeSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "git commit -m 'Fix bug'"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should output deny response
	var response types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.HookSpecificOutput == nil {
		t.Fatal("Expected hookSpecificOutput, got nil")
	}
	if response.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("Expected permissionDecision 'deny', got %q", response.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(response.HookSpecificOutput.PermissionDecisionReason, "Confabulous-Link:") {
		t.Errorf("Expected reason to contain trailer instruction, got %q", response.HookSpecificOutput.PermissionDecisionReason)
	}
	// Verify the URL uses the Confab session ID, not Claude session ID
	if !strings.Contains(response.HookSpecificOutput.PermissionDecisionReason, confabSessionID) {
		t.Errorf("Expected reason to contain Confab session ID %q, got %q", confabSessionID, response.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestHandlePreToolUse_GitCommitWithTrailer(t *testing.T) {
	claudeSessionID := "claude-session-123"
	confabSessionID := "confab-session-456"

	// Set up test state with Confab session ID (must be before formatSessionURL)
	cleanup := setupTestState(t, claudeSessionID, confabSessionID)
	defer cleanup()

	sessionURL, err := formatSessionURL(confabSessionID)
	if err != nil {
		t.Fatalf("formatSessionURL() error = %v", err)
	}

	input := types.HookInput{
		SessionID:     claudeSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput: map[string]any{
			"command": "git commit -m \"Fix bug\n\nConfabulous-Link: " + sessionURL + "\"",
		},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err = handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should output allow response
	var response types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.HookSpecificOutput == nil {
		t.Fatal("Expected hookSpecificOutput, got nil")
	}
	if response.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("Expected permissionDecision 'allow', got %q", response.HookSpecificOutput.PermissionDecision)
	}
}

func TestHandlePreToolUse_GitCommitNoState(t *testing.T) {
	// When there's no daemon state (sync not set up), commits should be allowed silently
	input := types.HookInput{
		SessionID:     "unknown-session",
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "git commit -m 'Fix bug'"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (silent allow) when no state exists
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no state exists, got %q", w.String())
	}
}

func TestHandlePreToolUse_InvalidJSON(t *testing.T) {
	r := strings.NewReader("not valid json")
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error (silent failure), got %v", err)
	}

	// Should produce no output on parse error (silent allow)
	if w.Len() != 0 {
		t.Errorf("Expected empty output on parse error, got %q", w.String())
	}
}

func TestHandlePreToolUse_MissingSessionID(t *testing.T) {
	input := types.HookInput{
		SessionID:     "", // Missing
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": "git commit -m 'test'"},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error (silent failure), got %v", err)
	}

	// Should produce no output on validation error (silent allow)
	if w.Len() != 0 {
		t.Errorf("Expected empty output on validation error, got %q", w.String())
	}
}

// --- PR Creation Tests ---

func TestFindGHPRCreatePosition(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    int // -1 means not found, >= 0 means found at position
	}{
		{"simple gh pr create", "gh pr create", 0},
		{"gh pr create with title", "gh pr create --title 'Fix bug'", 0},
		{"gh pr create with body", `gh pr create --title "test" --body "description"`, 0},
		{"gh pr create with heredoc", "gh pr create --title \"test\" --body \"$(cat <<'EOF'\ndescription\nEOF\n)\"", 0},
		{"gh pr list", "gh pr list", -1},
		{"gh pr view", "gh pr view 123", -1},
		{"git push", "git push origin main", -1},
		{"npm install", "npm install", -1},
		{"empty command", "", -1},
		{"gh with -R flag", "gh -R owner/repo pr create --title 'test'", 0},
		{"gh with multiple flags", "gh --no-pager -R owner/repo pr create", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findGHPRCreatePosition(tt.command)
			if tt.want < 0 && got >= 0 {
				t.Errorf("findGHPRCreatePosition(%q) = %d, want not found", tt.command, got)
			} else if tt.want >= 0 && got < 0 {
				t.Errorf("findGHPRCreatePosition(%q) = not found, want %d", tt.command, tt.want)
			}
		})
	}
}

// TestPositionBasedPrecedence tests that the earlier matching command takes precedence.
// This fixes false positives where "gh pr create" text in a commit message would
// incorrectly trigger PR handling.
func TestPositionBasedPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		wantCommit   bool
		wantPRCreate bool
	}{
		{
			name:         "git commit with PR text in message",
			command:      `git commit -m "Add detection for gh pr create"`,
			wantCommit:   true,
			wantPRCreate: false,
		},
		{
			name:         "git commit mentioning PR in heredoc",
			command:      "git commit -m \"$(cat <<'EOF'\nAdd gh pr create handling\nEOF\n)\"",
			wantCommit:   true,
			wantPRCreate: false,
		},
		{
			name:         "simple git commit",
			command:      `git commit -m "Fix bug"`,
			wantCommit:   true,
			wantPRCreate: false,
		},
		{
			name:         "simple gh pr create",
			command:      `gh pr create --title "test"`,
			wantCommit:   false,
			wantPRCreate: true,
		},
		{
			name:         "gh pr create mentioning commit",
			command:      `gh pr create --title "git commit changes"`,
			wantCommit:   false,
			wantPRCreate: true,
		},
		{
			name:         "neither command",
			command:      `npm install`,
			wantCommit:   false,
			wantPRCreate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commitPos := findGitCommitPosition(tt.command)
			prCreatePos := findGHPRCreatePosition(tt.command)

			isCommit := commitPos >= 0 && (prCreatePos < 0 || commitPos < prCreatePos)
			isPRCreate := prCreatePos >= 0 && (commitPos < 0 || prCreatePos < commitPos)

			if isCommit != tt.wantCommit {
				t.Errorf("isCommit = %v, want %v (commitPos=%d, prCreatePos=%d)",
					isCommit, tt.wantCommit, commitPos, prCreatePos)
			}
			if isPRCreate != tt.wantPRCreate {
				t.Errorf("isPRCreate = %v, want %v (commitPos=%d, prCreatePos=%d)",
					isPRCreate, tt.wantPRCreate, commitPos, prCreatePos)
			}
		})
	}
}

func TestFormatPRLink(t *testing.T) {
	got := formatPRLink("https://confabulous.dev/sessions/abc123")
	want := "📝 [Confabulous link](https://confabulous.dev/sessions/abc123)"
	if got != want {
		t.Errorf("formatPRLink() = %q, want %q", got, want)
	}
}

func TestHandlePreToolUse_PRCreateWithoutLink(t *testing.T) {
	claudeSessionID := "claude-session-123"
	confabSessionID := "confab-session-456"

	// Set up test state with Confab session ID
	cleanup := setupTestState(t, claudeSessionID, confabSessionID)
	defer cleanup()

	input := types.HookInput{
		SessionID:     claudeSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": `gh pr create --title "Fix bug" --body "Just a description"`},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should output deny response
	var response types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.HookSpecificOutput == nil {
		t.Fatal("Expected hookSpecificOutput, got nil")
	}
	if response.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("Expected permissionDecision 'deny', got %q", response.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(response.HookSpecificOutput.PermissionDecisionReason, "Confabulous link") {
		t.Errorf("Expected reason to contain PR link instruction, got %q", response.HookSpecificOutput.PermissionDecisionReason)
	}
	// Verify the URL uses the Confab session ID
	if !strings.Contains(response.HookSpecificOutput.PermissionDecisionReason, confabSessionID) {
		t.Errorf("Expected reason to contain Confab session ID %q, got %q", confabSessionID, response.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestHandlePreToolUse_PRCreateWithLink(t *testing.T) {
	claudeSessionID := "claude-session-123"
	confabSessionID := "confab-session-456"

	// Set up test state with Confab session ID (must be before formatSessionURL)
	cleanup := setupTestState(t, claudeSessionID, confabSessionID)
	defer cleanup()

	sessionURL, err := formatSessionURL(confabSessionID)
	if err != nil {
		t.Fatalf("formatSessionURL() error = %v", err)
	}

	input := types.HookInput{
		SessionID:     claudeSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput: map[string]any{
			"command": `gh pr create --title "Fix bug" --body "Summary\n\n📝 [Confabulous link](` + sessionURL + `)"`,
		},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err = handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should output allow response
	var response types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.HookSpecificOutput == nil {
		t.Fatal("Expected hookSpecificOutput, got nil")
	}
	if response.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("Expected permissionDecision 'allow', got %q", response.HookSpecificOutput.PermissionDecision)
	}
}

func TestHandlePreToolUse_PRCreateNoState(t *testing.T) {
	// When there's no daemon state (sync not set up), PR creation should be allowed silently
	input := types.HookInput{
		SessionID:     "unknown-session",
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameBash,
		ToolInput:     map[string]any{"command": `gh pr create --title "Fix bug"`},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (silent allow) when no state exists
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no state exists, got %q", w.String())
	}
}

// --- MCP GitHub Tool Tests ---

func TestHandlePreToolUse_MCPGitHubPRWithoutLink(t *testing.T) {
	claudeSessionID := "claude-session-123"
	confabSessionID := "confab-session-456"

	// Set up test state with Confab session ID
	cleanup := setupTestState(t, claudeSessionID, confabSessionID)
	defer cleanup()

	input := types.HookInput{
		SessionID:     claudeSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameMCPGitHubCreatePR,
		ToolInput: map[string]any{
			"owner": "myorg",
			"repo":  "myrepo",
			"title": "Fix bug",
			"body":  "This PR fixes a bug",
			"head":  "feature-branch",
			"base":  "main",
		},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should output deny response
	var response types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.HookSpecificOutput == nil {
		t.Fatal("Expected hookSpecificOutput, got nil")
	}
	if response.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("Expected permissionDecision 'deny', got %q", response.HookSpecificOutput.PermissionDecision)
	}
	if !strings.Contains(response.HookSpecificOutput.PermissionDecisionReason, "Confabulous link") {
		t.Errorf("Expected reason to contain PR link instruction, got %q", response.HookSpecificOutput.PermissionDecisionReason)
	}
	if !strings.Contains(response.HookSpecificOutput.PermissionDecisionReason, confabSessionID) {
		t.Errorf("Expected reason to contain Confab session ID %q, got %q", confabSessionID, response.HookSpecificOutput.PermissionDecisionReason)
	}
}

func TestHandlePreToolUse_MCPGitHubPRWithLink(t *testing.T) {
	claudeSessionID := "claude-session-123"
	confabSessionID := "confab-session-456"

	// Set up test state with Confab session ID (must be before formatSessionURL)
	cleanup := setupTestState(t, claudeSessionID, confabSessionID)
	defer cleanup()

	sessionURL, err := formatSessionURL(confabSessionID)
	if err != nil {
		t.Fatalf("formatSessionURL() error = %v", err)
	}

	input := types.HookInput{
		SessionID:     claudeSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameMCPGitHubCreatePR,
		ToolInput: map[string]any{
			"owner": "myorg",
			"repo":  "myrepo",
			"title": "Fix bug",
			"body":  "This PR fixes a bug\n\n📝 [Confabulous link](" + sessionURL + ")",
			"head":  "feature-branch",
			"base":  "main",
		},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err = handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should output allow response
	var response types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.HookSpecificOutput == nil {
		t.Fatal("Expected hookSpecificOutput, got nil")
	}
	if response.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("Expected permissionDecision 'allow', got %q", response.HookSpecificOutput.PermissionDecision)
	}
}

func TestHandlePreToolUse_MCPGitHubPRNoState(t *testing.T) {
	// When there's no daemon state, MCP PR creation should be allowed silently
	input := types.HookInput{
		SessionID:     "unknown-session",
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameMCPGitHubCreatePR,
		ToolInput: map[string]any{
			"owner": "myorg",
			"repo":  "myrepo",
			"title": "Fix bug",
			"body":  "Description",
		},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should produce no output (silent allow) when no state exists
	if w.Len() != 0 {
		t.Errorf("Expected empty output when no state exists, got %q", w.String())
	}
}

func TestHandlePreToolUse_MCPGitHubPRNoBody(t *testing.T) {
	claudeSessionID := "claude-session-123"
	confabSessionID := "confab-session-456"

	// Set up test state with Confab session ID
	cleanup := setupTestState(t, claudeSessionID, confabSessionID)
	defer cleanup()

	// MCP tool call without body field - should still request link
	input := types.HookInput{
		SessionID:     claudeSessionID,
		HookEventName: "PreToolUse",
		ToolName:      config.ToolNameMCPGitHubCreatePR,
		ToolInput: map[string]any{
			"owner": "myorg",
			"repo":  "myrepo",
			"title": "Fix bug",
			"head":  "feature-branch",
			"base":  "main",
		},
	}

	inputJSON, _ := json.Marshal(input)
	r := strings.NewReader(string(inputJSON))
	var w bytes.Buffer

	err := handlePreToolUse(r, &w)
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	// Should output deny response requesting the link be added
	var response types.PreToolUseResponse
	if err := json.Unmarshal(w.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response.HookSpecificOutput == nil {
		t.Fatal("Expected hookSpecificOutput, got nil")
	}
	if response.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("Expected permissionDecision 'deny', got %q", response.HookSpecificOutput.PermissionDecision)
	}
}
