package hookconfig

import (
	"strings"
	"testing"
)

// These tests cover the internal Codex TOML helpers
// (ensureCodexHooksConfig, codexTrustedHookHash, codexHooksTOML). They
// were originally in pkg/provider/codex_test.go before CF-396 moved the
// helpers here.

func TestCodexEnsureHooksConfig(t *testing.T) {
	input := `[projects."/repo"]
trust_level = "trusted"
`

	got := ensureCodexHooksConfig(input, "/Users/test/.codex/config.toml", "/usr/local/bin/confab")
	for _, want := range []string{
		"[features]",
		"hooks = true",
		confabCodexHooksStart,
		// SessionStart event + trust block.
		"[[hooks.SessionStart]]",
		"command = \"/usr/local/bin/confab hook session-start --provider codex\"",
		`[hooks.state."/Users/test/.codex/config.toml:session_start:0:0"]`,
		// PreToolUse event + trust block (CF-492).
		"[[hooks.PreToolUse]]",
		`matcher = "Bash"`,
		"command = \"/usr/local/bin/confab hook pre-tool-use --provider codex\"",
		`[hooks.state."/Users/test/.codex/config.toml:pre_tool_use:0:0"]`,
		// PostToolUse event + trust block (CF-492).
		"[[hooks.PostToolUse]]",
		"command = \"/usr/local/bin/confab hook post-tool-use --provider codex\"",
		`[hooks.state."/Users/test/.codex/config.toml:post_tool_use:0:0"]`,
		`trusted_hash = "sha256:`,
		confabCodexHooksEnd,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated config to contain %q\n%s", want, got)
		}
	}
	for _, notWant := range []string{
		"[[hooks.Stop]]",
		"hook session-end --provider codex",
		"[[hooks.UserPromptSubmit]]",
		"hook user-prompt-submit --provider codex",
		`[hooks.state."/Users/test/.codex/config.toml:stop:0:0"]`,
		`[hooks.state."/Users/test/.codex/config.toml:user_prompt_submit:0:0"]`,
	} {
		if strings.Contains(got, notWant) {
			t.Fatalf("expected managed block to omit %q\n%s", notWant, got)
		}
	}
}

func TestCodexEnsureHooksConfigIsIdempotent(t *testing.T) {
	once := ensureCodexHooksConfig("[features]\ncodex_hooks = false\n", "/Users/test/.codex/config.toml", "/usr/local/bin/confab")
	twice := ensureCodexHooksConfig(once, "/Users/test/.codex/config.toml", "/usr/local/bin/confab")
	if once != twice {
		t.Fatalf("expected idempotent config update\nonce:\n%s\n\ntwice:\n%s", once, twice)
	}
	if strings.Count(twice, confabCodexHooksStart) != 1 {
		t.Fatalf("expected one managed block, got:\n%s", twice)
	}
	if strings.Contains(twice, "codex_hooks") {
		t.Fatalf("expected deprecated feature flag to be removed:\n%s", twice)
	}
	if !strings.Contains(twice, "hooks = true") {
		t.Fatalf("expected hooks feature flag to be enabled:\n%s", twice)
	}
}

func TestCodexEnsureHooksConfigTrustKeysUseExistingHookPositions(t *testing.T) {
	input := `[[hooks.SessionStart]]
matcher = "startup"
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/bin/other start"
`
	got := ensureCodexHooksConfig(input, "/Users/test/.codex/config.toml", "/usr/local/bin/confab")
	if want := `[hooks.state."/Users/test/.codex/config.toml:session_start:1:0"]`; !strings.Contains(got, want) {
		t.Fatalf("expected generated config to contain %q\n%s", want, got)
	}
	if notWant := `[hooks.state."/Users/test/.codex/config.toml:session_start:0:0"]`; strings.Contains(got, notWant) {
		t.Fatalf("generated config contains stale positional trust key %q\n%s", notWant, got)
	}
}

// TestCodexEnsureHooksConfigTrustKeysUseExistingPreToolUsePosition verifies
// the positional-key invariant per event: when the user already has an
// unmanaged [[hooks.PreToolUse]] block, our PreToolUse trust key must be
// :pre_tool_use:1:0 (not :0:0) so Codex's lookup lands on our hook, not
// the user's. See codex-rs/hooks/src/lib.rs:100-110 (positional hook_key).
func TestCodexEnsureHooksConfigTrustKeysUseExistingPreToolUsePosition(t *testing.T) {
	input := `[[hooks.PreToolUse]]
matcher = "Edit"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/bin/other pre"
`
	got := ensureCodexHooksConfig(input, "/Users/test/.codex/config.toml", "/usr/local/bin/confab")
	if want := `[hooks.state."/Users/test/.codex/config.toml:pre_tool_use:1:0"]`; !strings.Contains(got, want) {
		t.Fatalf("expected generated config to contain %q\n%s", want, got)
	}
	if notWant := `[hooks.state."/Users/test/.codex/config.toml:pre_tool_use:0:0"]`; strings.Contains(got, notWant) {
		t.Fatalf("generated config contains stale positional trust key %q\n%s", notWant, got)
	}
}

// TestCodexEnsureHooksConfigTrustKeysUseExistingPostToolUsePosition is the
// PostToolUse analog. Same invariant.
func TestCodexEnsureHooksConfigTrustKeysUseExistingPostToolUsePosition(t *testing.T) {
	input := `[[hooks.PostToolUse]]
matcher = "Write"
[[hooks.PostToolUse.hooks]]
type = "command"
command = "/usr/bin/other post"
`
	got := ensureCodexHooksConfig(input, "/Users/test/.codex/config.toml", "/usr/local/bin/confab")
	if want := `[hooks.state."/Users/test/.codex/config.toml:post_tool_use:1:0"]`; !strings.Contains(got, want) {
		t.Fatalf("expected generated config to contain %q\n%s", want, got)
	}
	if notWant := `[hooks.state."/Users/test/.codex/config.toml:post_tool_use:0:0"]`; strings.Contains(got, notWant) {
		t.Fatalf("generated config contains stale positional trust key %q\n%s", notWant, got)
	}
}

func TestCodexTrustedHookHashMatchesKnownCodexHashes(t *testing.T) {
	startHash := codexTrustedHookHash(
		"session_start",
		"startup|resume|clear",
		"/Users/jackie/.local/bin/confab hook session-start --provider codex",
		"Starting Confab sync",
	)
	if want := "sha256:d1f33ff2cf043a857782a0bb0661ae66a4d05446ae116f0774b7b5629af0a987"; startHash != want {
		t.Fatalf("session-start trusted hash = %q, want %q", startHash, want)
	}
}

// TestCodexTrustedHookHashMatchesKnownCodexHashes_PreToolUse pins the
// pre-tool-use trusted hash for the canonical Confab install. Computed by
// the same hash algorithm Codex applies (canonical-JSON sha256 of the
// identity blob — verified against codex-rs/hooks/src/engine/discovery.rs
// + config/src/fingerprint.rs on main 2026-05-23). StatusMessage MUST be
// the empty string in both the rendered TOML and the hashed identity
// blob so the round-trip through Codex produces a matching hash.
func TestCodexTrustedHookHashMatchesKnownCodexHashes_PreToolUse(t *testing.T) {
	got := codexTrustedHookHash(
		"pre_tool_use",
		"Bash",
		"/Users/jackie/.local/bin/confab hook pre-tool-use --provider codex",
		"",
	)
	if want := "sha256:983e0ba3cb03265ced2eabc7bfd2436324a26d54360c1ebd358b4f027cf307a8"; got != want {
		t.Fatalf("pre-tool-use trusted hash = %q, want %q", got, want)
	}
}

// TestCodexTrustedHookHashMatchesKnownCodexHashes_PostToolUse is the
// PostToolUse analog.
func TestCodexTrustedHookHashMatchesKnownCodexHashes_PostToolUse(t *testing.T) {
	got := codexTrustedHookHash(
		"post_tool_use",
		"Bash",
		"/Users/jackie/.local/bin/confab hook post-tool-use --provider codex",
		"",
	)
	if want := "sha256:f52d98fa93210bbcde4824b4d1ff961d299159717d4d74611f592ea482537317"; got != want {
		t.Fatalf("post-tool-use trusted hash = %q, want %q", got, want)
	}
}

func TestCodexHooksTOMLEscapesTrustStateKey(t *testing.T) {
	got := codexHooksTOML(`/tmp/codex "quoted"/config.toml`, `/tmp/confab`, codexHookGroupIndices{})
	for _, want := range []string{
		`[hooks.state."/tmp/codex \"quoted\"/config.toml:session_start:0:0"]`,
		`[hooks.state."/tmp/codex \"quoted\"/config.toml:pre_tool_use:0:0"]`,
		`[hooks.state."/tmp/codex \"quoted\"/config.toml:post_tool_use:0:0"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected quoted trust key %q, got:\n%s", want, got)
		}
	}
}
