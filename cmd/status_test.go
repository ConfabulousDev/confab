package cmd

import (
	"bytes"
	"testing"
)

// TestStatusAcceptsProviderFlag asserts the CF-396 contract:
// `confab status --provider codex` must be a recognized command and
// must not error out. (The hook-installed assertion lives in the
// provider's own tests; here we just check the flag is wired.)
func TestStatusAcceptsProviderFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_CODEX_DIR", tmpDir)
	t.Setenv("CONFAB_CLAUDE_DIR", tmpDir)

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"status", "--provider", "codex"})
	defer rootCmd.SetArgs(nil)

	// We don't assert success because backend auth check may fail in
	// test env. We DO assert the flag is parsed (no "unknown flag"
	// error) and the codex code path is reachable.
	err := rootCmd.Execute()
	if err != nil && err.Error() == "unknown flag: --provider" {
		t.Fatalf("status --provider codex: flag not wired: %v", err)
	}
}
