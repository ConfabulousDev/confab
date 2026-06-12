package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/pathcanon"
)

// TestConfigDirFromTranscript: a well-formed transcript path yields the
// canonical config dir (two levels above the file, anchored on "projects").
func TestConfigDirFromTranscript(t *testing.T) {
	cfgDir := t.TempDir()
	transcript := filepath.Join(cfgDir, "projects", "enc-cwd", "11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := ClaudeCode{}.ConfigDirFromTranscript(transcript)
	if err != nil {
		t.Fatalf("ConfigDirFromTranscript: %v", err)
	}
	if want := pathcanon.CanonicalDir(cfgDir); got != want {
		t.Errorf("ConfigDirFromTranscript = %q, want %q", got, want)
	}
}

// TestConfigDirFromTranscriptAnchorsOnProjects: a config dir whose own path
// contains a "projects" segment must still resolve to the real config dir
// (anchor on the LAST projects/<one-seg>/<file>).
func TestConfigDirFromTranscriptAnchorsOnProjects(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "projects", "claude") // config dir contains "projects"
	transcript := filepath.Join(cfgDir, "projects", "enc", "abc.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := ClaudeCode{}.ConfigDirFromTranscript(transcript)
	if err != nil {
		t.Fatalf("ConfigDirFromTranscript: %v", err)
	}
	if want := pathcanon.CanonicalDir(cfgDir); got != want {
		t.Errorf("ConfigDirFromTranscript = %q, want %q", got, want)
	}
}

// TestConfigDirFromTranscriptMalformed: a path without the projects/<seg>/<file>
// layout must error so the caller can fall back to the default binding.
func TestConfigDirFromTranscriptMalformed(t *testing.T) {
	for _, p := range []string{
		"/no/projects/here.jsonl",
		"relative/projects/enc/x.jsonl",
		"/x/sessions/enc/x.jsonl", // codex-style layout, not claude
	} {
		if _, err := (ClaudeCode{}).ConfigDirFromTranscript(p); err == nil {
			t.Errorf("ConfigDirFromTranscript(%q) = nil error, want error", p)
		}
	}
}

// TestGetWithDirClaudeOverride: GetWithDir("claude-code", dir) returns a
// provider whose StateDir() is the override, beating env/default.
func TestGetWithDirClaudeOverride(t *testing.T) {
	// Point the env at one dir to prove the override wins over it.
	t.Setenv(ClaudeStateDirEnv, t.TempDir())
	want := t.TempDir()

	p, err := GetWithDir(NameClaudeCode, want)
	if err != nil {
		t.Fatalf("GetWithDir: %v", err)
	}
	got, err := p.StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if got != want {
		t.Errorf("StateDir() = %q, want override %q", got, want)
	}
}

// TestGetWithDirUnsupportedProvider: codex/opencode are not wired this ticket
// and must return an error (not silently ignore the dir).
func TestGetWithDirUnsupportedProvider(t *testing.T) {
	for _, name := range []string{NameCodex, NameOpencode} {
		if _, err := GetWithDir(name, t.TempDir()); err == nil {
			t.Errorf("GetWithDir(%q, dir) = nil error, want unsupported error", name)
		}
	}
}
