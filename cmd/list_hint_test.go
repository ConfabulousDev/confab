// ABOUTME: Tests for list's provider-accurate output hints — the "confab save"
// ABOUTME: suggestion and the no-sessions message must name the actual provider,
// ABOUTME: not special-case codex (z0rt).
package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// TestPrintSessionTable_ProviderHint asserts the trailing "confab save" hint
// names the actual provider (cursor/codex) and stays bare for the default
// claude-code provider — i.e. no codex-only special-casing.
func TestPrintSessionTable_ProviderHint(t *testing.T) {
	sessions := []provider.SessionInfo{
		{SessionID: "aaaaaaaa-1111-1111-1111-111111111111", Summary: "x", ModTime: time.Now()},
	}

	cases := []struct {
		name     string
		p        provider.Provider
		wantHint string // the exact provider-flag fragment expected in the save hint
	}{
		{"claude-code is bare", provider.ClaudeCode{}, ""},
		{"codex named", provider.Codex{}, "--provider codex "},
		{"cursor named", provider.Cursor{}, "--provider cursor "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() { printSessionTable(tc.p, sessions) })
			wantLine := "Use 'confab save " + tc.wantHint + "<id>' to upload."
			if !strings.Contains(out, wantLine) {
				t.Errorf("output missing %q\ngot:\n%s", wantLine, out)
			}
		})
	}
}

// TestProviderSaveHint covers the helper directly across all providers.
func TestProviderSaveHint(t *testing.T) {
	cases := []struct {
		p    provider.Provider
		want string
	}{
		{provider.ClaudeCode{}, ""},
		{provider.Codex{}, "--provider codex "},
		{provider.Cursor{}, "--provider cursor "},
		{provider.Opencode{}, "--provider opencode "},
	}
	for _, tc := range cases {
		if got := providerSaveHint(tc.p); got != tc.want {
			t.Errorf("providerSaveHint(%s) = %q, want %q", tc.p.Name(), got, tc.want)
		}
	}
}
