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

// TestCodexTrustedHookHashMatchesKnownCodexHashes pins the session-start
// trusted hash for the canonical Confabulous Code install. The expected value is the
// sha256 of the canonical-JSON identity blob, computed outside Go (a Python
// json.dumps(separators=(",",":")) reproduction that also reproduces the
// pre-rename pin d1f33ff… for "Starting Confab sync").
func TestCodexTrustedHookHashMatchesKnownCodexHashes(t *testing.T) {
	startHash := codexTrustedHookHash(
		"session_start",
		"startup|resume|clear",
		"/Users/jackie/.local/bin/confab hook session-start --provider codex",
		"Starting Confabulous Code sync",
	)
	if want := "sha256:c86955493ec0f808ff5373cc5bfb06bf85a1390ab3c9a9d4f3b5772f19a087f8"; startHash != want {
		t.Fatalf("session-start trusted hash = %q, want %q", startHash, want)
	}
}

// TestCodexSessionStartStatusMessageNamesConfabulousCode asserts the
// SessionStart status shown in Codex's UI names the product, and that the
// emitted trusted_hash is the one computed over that same message (so Codex
// does not raise a first-run trust prompt).
func TestCodexSessionStartStatusMessageNamesConfabulousCode(t *testing.T) {
	const bin = "/usr/local/bin/confab"
	got := ensureCodexHooksConfig("", "/Users/test/.codex/config.toml", bin)
	if want := `statusMessage = "Starting Confabulous Code sync"`; !strings.Contains(got, want) {
		t.Fatalf("expected generated config to contain %q\n%s", want, got)
	}
	if strings.Contains(got, "Starting Confab sync") {
		t.Fatalf("generated config still contains the old status message\n%s", got)
	}
	wantHash := codexTrustedHookHash("session_start", "startup|resume|clear",
		bin+" hook session-start --provider codex", "Starting Confabulous Code sync")
	if !strings.Contains(got, `trusted_hash = "`+wantHash+`"`) {
		t.Fatalf("expected session-start trusted_hash %q over the new message\n%s", wantHash, got)
	}
}

// TestCodexEnsureHooksConfigUpgradesPreRenameBlock covers the upgrade path
// for installs written before the rename: re-running setup must replace the
// whole managed block, rewriting the status message and its trusted hash
// together, and leave exactly one managed block.
func TestCodexEnsureHooksConfigUpgradesPreRenameBlock(t *testing.T) {
	const (
		configPath = "/Users/test/.codex/config.toml"
		bin        = "/usr/local/bin/confab"
		cmd        = bin + " hook session-start --provider codex"
		oldMsg     = "Starting Confab sync"
		newMsg     = "Starting Confabulous Code sync"
	)
	oldHash := codexTrustedHookHash("session_start", "startup|resume|clear", cmd, oldMsg)
	newHash := codexTrustedHookHash("session_start", "startup|resume|clear", cmd, newMsg)

	// Build a pre-rename install: the current block with the old message
	// and the old (self-consistent) hash substituted back in.
	current := ensureCodexHooksConfig("[projects.\"/repo\"]\ntrust_level = \"trusted\"\n", configPath, bin)
	preRename := strings.ReplaceAll(current, `statusMessage = "`+newMsg+`"`, `statusMessage = "`+oldMsg+`"`)
	preRename = strings.ReplaceAll(preRename, newHash, oldHash)
	if !strings.Contains(preRename, oldMsg) || !strings.Contains(preRename, oldHash) {
		t.Fatalf("fixture did not produce a pre-rename block\n%s", preRename)
	}

	got := ensureCodexHooksConfig(preRename, configPath, bin)
	if n := strings.Count(got, confabCodexHooksStart); n != 1 {
		t.Fatalf("expected exactly one managed block, got %d\n%s", n, got)
	}
	if strings.Contains(got, oldMsg) || strings.Contains(got, oldHash) {
		t.Fatalf("upgraded config still carries the pre-rename message or hash\n%s", got)
	}
	if !strings.Contains(got, `statusMessage = "`+newMsg+`"`) || !strings.Contains(got, `trusted_hash = "`+newHash+`"`) {
		t.Fatalf("upgraded config missing new message %q with matching hash %q\n%s", newMsg, newHash, got)
	}
	if !strings.Contains(got, "trust_level = \"trusted\"") {
		t.Fatalf("upgrade dropped unmanaged user config\n%s", got)
	}
}

// TestCodexTrustedHookHashMatchesKnownCodexHashes_PreToolUse pins the
// pre-tool-use trusted hash for the canonical Confabulous Code install. Computed by
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
