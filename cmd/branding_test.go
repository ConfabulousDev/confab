package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The product is named "Confabulous Code" in user-facing prose (kata y7wh).
// The `confab` binary/command names, the `Confab-Link:` commit trailer and
// the `📝 [Confab link](…)` PR body label are deliberately unchanged.

const productName = "Confabulous Code"

// bareConfab matches the old product name as a standalone word.
var bareConfab = regexp.MustCompile(`\bConfab\b`)

// stripKeptLabels removes the label/trailer phrases that intentionally keep
// the "Confab" spelling, so what remains must not name the old product.
func stripKeptLabels(s string) string {
	s = strings.ReplaceAll(s, "Confab-Link", "")
	return strings.ReplaceAll(s, "Confab link", "")
}

func allCommands(c *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{c}
	for _, sub := range c.Commands() {
		out = append(out, allCommands(sub)...)
	}
	return out
}

func TestHelpTextNamesConfabulousCode(t *testing.T) {
	if !strings.HasPrefix(rootCmd.Long, productName+" automatically captures") {
		t.Errorf("root Long must introduce the product as %q, got %q", productName, rootCmd.Long)
	}
	for _, c := range allCommands(rootCmd) {
		for field, text := range map[string]string{"Short": c.Short, "Long": c.Long} {
			if bareConfab.MatchString(stripKeptLabels(text)) {
				t.Errorf("%q %s still names the product \"Confab\": %q", c.CommandPath(), field, text)
			}
			for _, m := range lowercaseProductProse.FindAllStringSubmatch(text, -1) {
				if m[2] == "" { // not a literal `confab hooks add|remove` command
					t.Errorf("%q %s names the product in lowercase (%q): %q", c.CommandPath(), field, m[0], text)
				}
			}
		}
	}
}

// lowercaseProductProse matches prose like "the confab backend" or
// "bundled confab skills". Group 2 is set when the match is actually a
// command invocation ("confab hooks add"), which keeps the binary name.
var lowercaseProductProse = regexp.MustCompile(`\bconfab (backend|hook set|hooks|skills)\b( (?:add|remove))?`)

func TestLoginHelpNamesConfabulousCodeBackend(t *testing.T) {
	if want := "Authenticate with the Confabulous Code backend"; loginCmd.Short != want {
		t.Errorf("login Short = %q, want %q", loginCmd.Short, want)
	}
}

func TestStatusBannerNamesConfabulousCode(t *testing.T) {
	out := runStatusCapture(t, true)
	if !strings.Contains(out, "=== Confabulous Code Status ===") {
		t.Errorf("status output missing the Confabulous Code banner:\n%s", out)
	}
	if strings.Contains(out, "=== Confab Status ===") {
		t.Errorf("status output still prints the old Confab banner:\n%s", out)
	}
}

func TestDefaultKeyNameNamesConfabulousCodeCLI(t *testing.T) {
	got := defaultKeyName()
	if got != "Confabulous Code CLI" && !strings.HasSuffix(got, " (Confabulous Code CLI)") {
		t.Errorf("defaultKeyName() = %q, want \"Confabulous Code CLI\" or \"<host> (Confabulous Code CLI)\"", got)
	}
	if strings.Contains(got, "(Confab CLI)") || got == "Confab CLI" {
		t.Errorf("defaultKeyName() = %q still uses the old Confab CLI name", got)
	}
}

func TestAgentLinkMessagesNameConfabulousCode(t *testing.T) {
	const url = "https://confab.example.com/sessions/abc"
	const marker = "# confab-link-certified"
	tests := []struct {
		name, msg, prefix, keptLabel string
	}{
		{"mcp PR", formatPRDenyReason(url), "✓ Confabulous Code is linking this PR to your session. ", "[Confab link]("},
		{"bash PR", formatBashPRDenyReason(url, marker), "✓ Confabulous Code is linking this PR to your session. ", "if the Confab link is already in the PR body"},
		{"commit", formatCommitDenyReason(url, marker), "✓ Confabulous Code is linking this commit to your session. ", "Confab-Link: " + url},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.msg, tt.prefix) {
				t.Errorf("message must start with %q, got %q", tt.prefix, tt.msg)
			}
			if !strings.Contains(tt.msg, tt.keptLabel) {
				t.Errorf("message must keep the unchanged label %q, got %q", tt.keptLabel, tt.msg)
			}
		})
	}
}

// TestLinkLabelsAndTrailerAreNotRenamed guards the user decision that the
// trailer key and PR body label keep the "Confab" spelling.
func TestLinkLabelsAndTrailerAreNotRenamed(t *testing.T) {
	if commitTrailerPrefix != "Confab-Link: " {
		t.Errorf("commitTrailerPrefix = %q, want %q", commitTrailerPrefix, "Confab-Link: ")
	}
	if !strings.Contains(prLinkPrefix, "[Confab link](") {
		t.Errorf("prLinkPrefix = %q, want it to keep the [Confab link]( label", prLinkPrefix)
	}
}
