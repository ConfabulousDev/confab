package cmd

import (
	"testing"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// resetHooksProviderName clears the package-level hooksProviderName between
// tests so a previous test's value can't leak into auto-detect.
func resetHooksProviderName(t *testing.T) {
	t.Helper()
	orig := hooksProviderName
	hooksProviderName = ""
	t.Cleanup(func() { hooksProviderName = orig })
}

// TestHooksProviderFlagDefaultsEmpty confirms the hooks --provider flag no
// longer hardcodes claude-code; it defaults to "" so the command auto-detects
// (matching the setup/skills pattern).
func TestHooksProviderFlagDefaultsEmpty(t *testing.T) {
	f := hooksCmd.PersistentFlags().Lookup("provider")
	if f == nil {
		t.Fatal("hooks has no --provider flag")
	}
	if f.DefValue != "" {
		t.Errorf("hooks --provider default = %q, want empty (auto-detect)", f.DefValue)
	}
}

// TestHooksAddTargetsAutoDetect verifies that with no --provider, hooks add
// resolves to the detected providers (not a hardcoded claude-code).
func TestHooksAddTargetsAutoDetect(t *testing.T) {
	resetHooksProviderName(t)
	stubProviderDetect(t, "codex")
	stubProviderStateDir(t)

	targets, err := hooksAddTargets()
	if err != nil {
		t.Fatalf("hooksAddTargets: %v", err)
	}
	if len(targets) != 1 || targets[0].Name() != provider.NameCodex {
		var got []string
		for _, p := range targets {
			got = append(got, p.Name())
		}
		t.Fatalf("hooksAddTargets = %v, want [codex] (auto-detected, not claude default)", got)
	}
}

// TestHooksAddTargetsExplicit verifies an explicit --provider scopes to that
// provider only.
func TestHooksAddTargetsExplicit(t *testing.T) {
	resetHooksProviderName(t)
	hooksProviderName = "cursor"

	targets, err := hooksAddTargets()
	if err != nil {
		t.Fatalf("hooksAddTargets: %v", err)
	}
	if len(targets) != 1 || targets[0].Name() != provider.NameCursor {
		t.Fatalf("hooksAddTargets with explicit cursor = %v providers, want [cursor]", targets)
	}
}

// TestHooksRemoveTargetsAllProviders verifies that with no --provider, hooks
// remove targets every provider that installs hooks (so a leftover hook in any
// provider is cleaned up).
func TestHooksRemoveTargetsAllProviders(t *testing.T) {
	resetHooksProviderName(t)

	targets, err := hooksRemoveTargets()
	if err != nil {
		t.Fatalf("hooksRemoveTargets: %v", err)
	}
	got := make(map[string]bool)
	for _, p := range targets {
		got[p.Name()] = true
	}
	for _, want := range []string{provider.NameClaudeCode, provider.NameCodex, provider.NameCursor} {
		if !got[want] {
			t.Errorf("hooksRemoveTargets missing %q; got %v", want, got)
		}
	}
}
