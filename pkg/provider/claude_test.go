package provider

import (
	"os"
	"path/filepath"
	"testing"
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
