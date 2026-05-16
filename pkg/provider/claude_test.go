package provider

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/types"
)

func TestClaudeCodePaths(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	p := ClaudeCode{}

	stateDir, err := p.StateDir()
	if err != nil {
		t.Fatalf("StateDir() error = %v", err)
	}
	if stateDir != tmpDir {
		t.Fatalf("StateDir() = %q, want %q", stateDir, tmpDir)
	}

	projectsDir, err := p.ProjectsDir()
	if err != nil {
		t.Fatalf("ProjectsDir() error = %v", err)
	}
	if projectsDir != filepath.Join(tmpDir, "projects") {
		t.Fatalf("ProjectsDir() = %q", projectsDir)
	}

	settingsPath, err := p.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath() error = %v", err)
	}
	if settingsPath != filepath.Join(tmpDir, "settings.json") {
		t.Fatalf("SettingsPath() = %q", settingsPath)
	}
}

func TestClaudeCodeValidateTranscriptPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	projectsDir := filepath.Join(tmpDir, "projects")
	if err := mkdirAll(projectsDir); err != nil {
		t.Fatalf("failed to create projects dir: %v", err)
	}

	p := ClaudeCode{}
	validPath := filepath.Join(projectsDir, "project", "session.jsonl")
	if err := p.ValidateTranscriptPath(validPath); err != nil {
		t.Fatalf("ValidateTranscriptPath(valid) error = %v", err)
	}

	if err := p.ValidateTranscriptPath("relative/session.jsonl"); err == nil {
		t.Fatal("expected error for relative path")
	}

	traversalPath := filepath.Join(projectsDir, "..", "..", "etc", "passwd")
	if err := p.ValidateTranscriptPath(traversalPath); err == nil {
		t.Fatal("expected error for path traversal")
	}

	// Raw, unnormalized ".." segments — the JSON attack shape. filepath.Join
	// would normalize this away, so build it by string concatenation to ensure
	// ValidateTranscriptPath sees the literal "..".
	rawTraversalPath := projectsDir + "/../../../etc/passwd"
	if err := p.ValidateTranscriptPath(rawTraversalPath); err == nil {
		t.Fatal("expected error for raw '..' traversal segments")
	}

	outsidePath := filepath.Join(filepath.Dir(tmpDir), "other", "session.jsonl")
	if err := p.ValidateTranscriptPath(outsidePath); err == nil {
		t.Fatal("expected error for path outside projects dir")
	}

	nonexistentParentPath := filepath.Join(projectsDir, "new-project", "session.jsonl")
	if err := p.ValidateTranscriptPath(nonexistentParentPath); err != nil {
		t.Fatalf("ValidateTranscriptPath(nonexistent parent) error = %v", err)
	}

	outsideDir := filepath.Join(filepath.Dir(tmpDir), "outside")
	if err := mkdirAll(outsideDir); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	linkPath := filepath.Join(projectsDir, "link-out")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}
	symlinkEscapePath := filepath.Join(linkPath, "session.jsonl")
	if err := p.ValidateTranscriptPath(symlinkEscapePath); err == nil {
		t.Fatal("expected error for symlink escape outside projects dir")
	}
}

func TestClaudeCodeMatchesProcess(t *testing.T) {
	p := ClaudeCode{}
	tests := []struct {
		name    string
		cmd     string
		matches bool
	}{
		{"Claude app", "/Applications/Claude.app/Contents/MacOS/Claude", true},
		{"claude binary", "claude --dangerously-skip-permissions", true},
		{"mixed case", "Claude", true},
		{"word boundary", "/usr/local/bin/claude-code", true},
		{"substring only", "claudette", false},
		{"unrelated", "zsh", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.MatchesProcess(tt.cmd); got != tt.matches {
				t.Fatalf("MatchesProcess(%q) = %v, want %v", tt.cmd, got, tt.matches)
			}
		})
	}
}

func mkdirAll(path string) error {
	return os.MkdirAll(path, 0700)
}

func TestClaudeCodeWriteHookResponse(t *testing.T) {
	var buf bytes.Buffer
	if err := (ClaudeCode{}).WriteHookResponse(&buf, true, "hello"); err != nil {
		t.Fatalf("WriteHookResponse() error = %v", err)
	}
	var got types.ClaudeHookResponse
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if !got.Continue {
		t.Error("Continue = false, want true")
	}
	if !got.SuppressOutput {
		t.Error("SuppressOutput = false, want true")
	}
	if got.SystemMessage != "hello" {
		t.Errorf("SystemMessage = %q, want %q", got.SystemMessage, "hello")
	}
}

func TestClaudeCodeInstallSkills(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	if err := (ClaudeCode{}).InstallSkills(); err != nil {
		t.Fatalf("InstallSkills() error = %v", err)
	}
	// At least one skill file must have been written under the skills dir.
	entries, err := os.ReadDir(filepath.Join(tmpDir, "skills"))
	if err != nil {
		t.Fatalf("skills dir missing: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("InstallSkills() wrote no skill files")
	}
}

func TestClaudeCodeName(t *testing.T) {
	if got := (ClaudeCode{}).Name(); got != NameClaudeCode {
		t.Fatalf("Name() = %q, want %q", got, NameClaudeCode)
	}
}

func TestClaudeCodeWalkUpToRoot(t *testing.T) {
	rootID, rootPath, err := ClaudeCode{}.WalkUpToRoot("any-session-id")
	if err != nil {
		t.Fatalf("WalkUpToRoot() error = %v", err)
	}
	if rootID != "any-session-id" {
		t.Errorf("rootID = %q, want identity input", rootID)
	}
	if rootPath != "" {
		t.Errorf("rootPath = %q, want empty (Claude has no separate root file)", rootPath)
	}
}

func TestClaudeCodeShouldSpawnForInput(t *testing.T) {
	in := claudeHookInputAdapter{inner: &types.ClaudeHookInput{
		SessionID:      "0199-some-session",
		TranscriptPath: "/tmp/x.jsonl",
	}}
	if !(ClaudeCode{}).ShouldSpawnForInput(in) {
		t.Fatal("ShouldSpawnForInput must always return true for Claude Code")
	}
	// Even with a zero-value HookInput, Claude must allow spawn.
	if !(ClaudeCode{}).ShouldSpawnForInput(claudeHookInputAdapter{inner: &types.ClaudeHookInput{}}) {
		t.Fatal("ShouldSpawnForInput must allow spawn even for empty inputs")
	}
}

func TestClaudeCodeParseSessionHook(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)
	projectsDir := filepath.Join(tmpDir, "projects")
	if err := os.MkdirAll(projectsDir, 0700); err != nil {
		t.Fatalf("failed to set up projects dir: %v", err)
	}

	transcriptPath := filepath.Join(projectsDir, "demo", "session-abc.jsonl")
	payload := `{"session_id":"session-abc","transcript_path":"` + transcriptPath + `","cwd":"/work/here","hook_event_name":"SessionStart"}`

	in, err := (ClaudeCode{}).ParseSessionHook(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("ParseSessionHook() error = %v", err)
	}
	if got := in.SessionID(); got != "session-abc" {
		t.Errorf("SessionID() = %q", got)
	}
	if got := in.TranscriptPath(); got != transcriptPath {
		t.Errorf("TranscriptPath() = %q", got)
	}
	if got := in.CWD(); got != "/work/here" {
		t.Errorf("CWD() = %q", got)
	}
	if got := in.HookEventName(); got != "SessionStart" {
		t.Errorf("HookEventName() = %q", got)
	}

	// Adapter type assertion confirms ParseSessionHook returned the
	// expected wrapper.
	if _, ok := in.(claudeHookInputAdapter); !ok {
		t.Fatalf("ParseSessionHook returned %T, want claudeHookInputAdapter", in)
	}
}

func TestClaudeCodeParseSessionHookRejectsInvalid(t *testing.T) {
	if _, err := (ClaudeCode{}).ParseSessionHook(strings.NewReader("not json")); err == nil {
		t.Fatal("ParseSessionHook must reject malformed input")
	}

	// Missing transcript_path should be rejected (matches existing
	// ReadSessionHookInput contract).
	missing := `{"session_id":"abc"}`
	if _, err := (ClaudeCode{}).ParseSessionHook(strings.NewReader(missing)); err == nil {
		t.Fatal("ParseSessionHook must reject input missing transcript_path")
	}
}

func TestClaudeCodeInstallHooksWritesSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	p := ClaudeCode{}
	path, err := p.InstallHooks()
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	if !strings.HasSuffix(path, "settings.json") {
		t.Errorf("InstallHooks() path = %q, want suffix 'settings.json'", path)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	settings := string(data)
	// All four bundles must leave a fingerprint.
	for _, want := range []string{
		"hook session-start",
		"hook session-end",
		"hook pre-tool-use",
		"hook post-tool-use",
		"hook user-prompt-submit",
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings.json missing %q after InstallHooks()\n%s", want, settings)
		}
	}
}

func TestClaudeCodeUninstallHooksClearsSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(ClaudeStateDirEnv, tmpDir)

	p := ClaudeCode{}
	if _, err := p.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}
	if _, err := p.UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
	settings := string(data)
	// Sync hooks have substring-based uninstall fallbacks so they're
	// removed even when the binary name isn't "confab" (test mode).
	// Pre/post/user-prompt uninstall is binary-path-strict and can't be
	// exercised here without a real "confab" binary; assert the
	// delegation by absence of error above.
	for _, notWant := range []string{"hook session-start", "hook session-end"} {
		if strings.Contains(settings, notWant) {
			t.Errorf("settings.json still contains %q after UninstallHooks()\n%s", notWant, settings)
		}
	}
}

// TestClaudeCodeIsHooksInstalled exercises the AND-aggregation across
// all four hook bundles. We hand-roll settings.json with confab-named
// commands so the underlying isConfabCommand check (which is binary-
// path-sensitive) returns true under test.
func TestClaudeCodeIsHooksInstalled(t *testing.T) {
	const allFour = `{
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-start"}]}],
    "SessionEnd":   [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-end"}]}],
    "PreToolUse":   [{"matcher": "Bash", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook pre-tool-use"}]}],
    "PostToolUse":  [{"matcher": "Bash", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook post-tool-use"}]}],
    "UserPromptSubmit": [{"hooks": [{"type":"command","command":"/usr/local/bin/confab hook user-prompt-submit"}]}]
  }
}`
	const onlyThree = `{
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-start"}]}],
    "SessionEnd":   [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-end"}]}],
    "PreToolUse":   [{"matcher": "Bash", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook pre-tool-use"}]}]
  }
}`

	tests := []struct {
		name     string
		settings string // "" = no settings.json file
		want     bool
	}{
		{"no settings file", "", false},
		{"all four bundles", allFour, true},
		{"missing one bundle", onlyThree, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv(ClaudeStateDirEnv, tmpDir)
			if tt.settings != "" {
				if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), []byte(tt.settings), 0600); err != nil {
					t.Fatalf("failed to write settings: %v", err)
				}
			}
			got, err := (ClaudeCode{}).IsHooksInstalled()
			if err != nil {
				t.Fatalf("IsHooksInstalled() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("IsHooksInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}
