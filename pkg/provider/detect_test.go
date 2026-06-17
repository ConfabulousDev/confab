package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// stubLookPath swaps the package-level LookPath for the duration of a
// test. Names in present resolve to a fake path; everything else returns
// exec.ErrNotFound.
func stubLookPath(t *testing.T, present ...string) {
	t.Helper()
	set := make(map[string]struct{}, len(present))
	for _, b := range present {
		set[b] = struct{}{}
	}
	orig := LookPath
	LookPath = func(name string) (string, error) {
		if _, ok := set[name]; ok {
			return "/usr/local/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { LookPath = orig })
}

// stubStateDir swaps the package-level StateDirPresent seam for the
// duration of a test. Providers whose canonical Name() is in present
// report a present state dir; everything else reports absent. Mirrors
// stubLookPath so DetectInstalled tests stay filesystem-independent.
func stubStateDir(t *testing.T, present ...string) {
	t.Helper()
	set := make(map[string]struct{}, len(present))
	for _, n := range present {
		set[n] = struct{}{}
	}
	orig := StateDirPresent
	StateDirPresent = func(p Provider) bool {
		_, ok := set[p.Name()]
		return ok
	}
	t.Cleanup(func() { StateDirPresent = orig })
}

// TestDetectInstalled_Permutations is the spec table for CF-572: a
// provider is detected when its CLI is on PATH OR its state/config dir
// exists. Covers the four install permutations per provider, plus
// CLI+desktop dedup and fixed registry order.
func TestDetectInstalled_Permutations(t *testing.T) {
	tests := []struct {
		name     string
		onPath   []string // binary names resolvable via LookPath
		stateDir []string // canonical provider names whose state dir exists
		want     []string
	}{
		// CLI-only — no regression vs. pre-CF-572.
		{"cli only: claude", []string{"claude"}, nil, []string{NameClaudeCode}},
		{"cli only: codex", []string{"codex"}, nil, []string{NameCodex}},
		{"cli only: opencode", []string{"opencode"}, nil, []string{NameOpencode}},
		{"cli only: all three", []string{"claude", "codex", "opencode"}, nil,
			[]string{NameClaudeCode, NameCodex, NameOpencode}},

		// State-dir-only — the new desktop-app / CLI-uninstalled case.
		{"statedir only: claude", nil, []string{NameClaudeCode}, []string{NameClaudeCode}},
		{"statedir only: codex", nil, []string{NameCodex}, []string{NameCodex}},
		{"statedir only: opencode", nil, []string{NameOpencode}, []string{NameOpencode}},
		{"statedir only: all three", nil,
			[]string{NameClaudeCode, NameCodex, NameOpencode},
			[]string{NameClaudeCode, NameCodex, NameOpencode}},

		// Both CLI + state dir — provider must appear exactly once (dedup).
		{"both: claude", []string{"claude"}, []string{NameClaudeCode}, []string{NameClaudeCode}},
		{"both: all three",
			[]string{"claude", "codex", "opencode"},
			[]string{NameClaudeCode, NameCodex, NameOpencode},
			[]string{NameClaudeCode, NameCodex, NameOpencode}},

		// Mixed — CLI for one, state dir for another.
		{"mixed: claude cli + codex statedir",
			[]string{"claude"}, []string{NameCodex},
			[]string{NameClaudeCode, NameCodex}},

		// Neither — absent provider is never detected.
		{"neither: nothing", nil, nil, nil},

		// Order is fixed regardless of input ordering across both seams.
		{"order: codex statedir + claude cli",
			[]string{"claude"}, []string{NameCodex},
			[]string{NameClaudeCode, NameCodex}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubLookPath(t, tc.onPath...)
			stubStateDir(t, tc.stateDir...)

			got := DetectInstalled()
			if len(got) == 0 && len(tc.want) == 0 {
				return // both empty — non-nil-but-empty is fine
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DetectInstalled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Existing pure-PATH behavior must be preserved. These pin the seam to
// "no state dir present" so they exercise the LookPath branch in
// isolation and stay independent of the host filesystem.

func TestDetectInstalled_Both(t *testing.T) {
	stubLookPath(t, "claude", "codex")
	stubStateDir(t)
	got := DetectInstalled()
	want := []string{NameClaudeCode, NameCodex}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_AllThree(t *testing.T) {
	stubLookPath(t, "claude", "codex", "opencode")
	stubStateDir(t)
	got := DetectInstalled()
	want := []string{NameClaudeCode, NameCodex, NameOpencode}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_OpencodeOnly(t *testing.T) {
	stubLookPath(t, "opencode")
	stubStateDir(t)
	got := DetectInstalled()
	want := []string{NameOpencode}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_ClaudeOnly(t *testing.T) {
	stubLookPath(t, "claude")
	stubStateDir(t)
	got := DetectInstalled()
	want := []string{NameClaudeCode}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_CodexOnly(t *testing.T) {
	stubLookPath(t, "codex")
	stubStateDir(t)
	got := DetectInstalled()
	want := []string{NameCodex}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_Neither(t *testing.T) {
	stubLookPath(t)
	stubStateDir(t)
	got := DetectInstalled()
	if len(got) != 0 {
		t.Fatalf("DetectInstalled() = %v, want empty slice", got)
	}
}

// TestDetectInstalled_OrderIsFixed ensures the returned slice is in the
// canonical registry order regardless of LookPath call order.
func TestDetectInstalled_OrderIsFixed(t *testing.T) {
	stubLookPath(t, "codex", "claude")
	stubStateDir(t)
	got := DetectInstalled()
	want := []string{NameClaudeCode, NameCodex}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v (fixed claude-code, codex order)", got, want)
	}
}

// stateDirPresent is the real (un-stubbed) filesystem signal. These tests
// drive it through each provider's StateDir() env override against a temp
// dir, covering present / absent / not-a-directory.

func TestStateDirPresent_Claude(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".claude")
	t.Setenv(ClaudeStateDirEnv, dir)

	if stateDirPresent(ClaudeCode{}) {
		t.Fatal("absent ~/.claude should not be present")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !stateDirPresent(ClaudeCode{}) {
		t.Fatal("existing ~/.claude should be present")
	}
}

func TestStateDirPresent_Codex(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".codex")
	t.Setenv(CodexStateDirEnv, dir)

	if stateDirPresent(Codex{}) {
		t.Fatal("absent ~/.codex should not be present")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !stateDirPresent(Codex{}) {
		t.Fatal("existing ~/.codex should be present")
	}
}

func TestStateDirPresent_Opencode(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "opencode")
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", dir)

	if stateDirPresent(Opencode{}) {
		t.Fatal("absent opencode config dir should not be present")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !stateDirPresent(Opencode{}) {
		t.Fatal("existing opencode config dir should be present")
	}
}

func TestStateDirPresent_Cursor(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".cursor")
	t.Setenv(CursorStateDirEnv, dir)

	if stateDirPresent(Cursor{}) {
		t.Fatal("absent ~/.cursor should not be present")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !stateDirPresent(Cursor{}) {
		t.Fatal("existing ~/.cursor should be present")
	}
}

// TestStateDirPresent_FileNotDir guards the IsDir() check: a regular file
// at the state-dir path is not a present state dir.
func TestStateDirPresent_FileNotDir(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".claude")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(ClaudeStateDirEnv, path)

	if stateDirPresent(ClaudeCode{}) {
		t.Fatal("a regular file at the state-dir path must not count as present")
	}
}

func TestCLIBinaryName_Claude(t *testing.T) {
	if got := (ClaudeCode{}).CLIBinaryName(); got != "claude" {
		t.Fatalf("ClaudeCode.CLIBinaryName() = %q, want %q", got, "claude")
	}
}

func TestCLIBinaryName_Codex(t *testing.T) {
	if got := (Codex{}).CLIBinaryName(); got != "codex" {
		t.Fatalf("Codex.CLIBinaryName() = %q, want %q", got, "codex")
	}
}

func TestCLIBinaryName_Opencode(t *testing.T) {
	if got := (Opencode{}).CLIBinaryName(); got != "opencode" {
		t.Fatalf("Opencode.CLIBinaryName() = %q, want %q", got, "opencode")
	}
}

func TestCLIBinaryName_Cursor(t *testing.T) {
	if got := (Cursor{}).CLIBinaryName(); got != "cursor-agent" {
		t.Fatalf("Cursor.CLIBinaryName() = %q, want %q", got, "cursor-agent")
	}
}
