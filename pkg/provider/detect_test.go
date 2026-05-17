package provider

import (
	"os/exec"
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

func TestDetectInstalled_Both(t *testing.T) {
	stubLookPath(t, "claude", "codex")
	got := DetectInstalled()
	want := []string{NameClaudeCode, NameCodex}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_ClaudeOnly(t *testing.T) {
	stubLookPath(t, "claude")
	got := DetectInstalled()
	want := []string{NameClaudeCode}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_CodexOnly(t *testing.T) {
	stubLookPath(t, "codex")
	got := DetectInstalled()
	want := []string{NameCodex}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v", got, want)
	}
}

func TestDetectInstalled_Neither(t *testing.T) {
	stubLookPath(t)
	got := DetectInstalled()
	if len(got) != 0 {
		t.Fatalf("DetectInstalled() = %v, want empty slice", got)
	}
}

// TestDetectInstalled_OrderIsFixed ensures the returned slice is in the
// canonical registry order regardless of LookPath call order.
func TestDetectInstalled_OrderIsFixed(t *testing.T) {
	stubLookPath(t, "codex", "claude")
	got := DetectInstalled()
	want := []string{NameClaudeCode, NameCodex}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectInstalled() = %v, want %v (fixed claude-code, codex order)", got, want)
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
