package cmd

import (
	"strings"
	"testing"
)

// TestListRequiresProviderFlag verifies `confab list` errors when --provider
// is omitted (cobra required flag). m9mb removed the hardcoded claude-code
// default; even a single-provider machine must pass --provider.
func TestListRequiresProviderFlag(t *testing.T) {
	rootCmd.SetArgs([]string{"list"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --provider omitted from list, got nil")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("expected required-flag error mentioning provider, got %q", err.Error())
	}
}

// TestSaveRequiresProviderFlag verifies `confab save` errors when --provider
// is omitted.
func TestSaveRequiresProviderFlag(t *testing.T) {
	rootCmd.SetArgs([]string{"save", "some-session-id"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --provider omitted from save, got nil")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("expected required-flag error mentioning provider, got %q", err.Error())
	}
}

// TestListProviderFlagHasNoDefault confirms the --provider flag on list/save
// carries no hardcoded default value (claude-code is no longer implicit).
func TestListProviderFlagHasNoDefault(t *testing.T) {
	for _, name := range []string{"list", "save"} {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s command: %v", name, err)
		}
		f := cmd.Flags().Lookup("provider")
		if f == nil {
			t.Fatalf("%s has no --provider flag", name)
		}
		if f.DefValue != "" {
			t.Errorf("%s --provider default = %q, want empty (no hardcoded default)", name, f.DefValue)
		}
	}
}
