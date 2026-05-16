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
		"[[hooks.SessionStart]]",
		"command = \"/usr/local/bin/confab hook session-start --provider codex\"",
		`[hooks.state."/Users/test/.codex/config.toml:session_start:0:0"]`,
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
		`[hooks.state."/Users/test/.codex/config.toml:stop:0:0"]`,
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

func TestCodexHooksTOMLEscapesTrustStateKey(t *testing.T) {
	got := codexHooksTOML(`/tmp/codex "quoted"/config.toml`, `/tmp/confab`, 0)
	if !strings.Contains(got, `[hooks.state."/tmp/codex \"quoted\"/config.toml:session_start:0:0"]`) {
		t.Fatalf("expected quoted session-start trust key, got:\n%s", got)
	}
}
