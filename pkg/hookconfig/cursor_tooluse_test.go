package hookconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallCursorHooksWritesToolUseEvents verifies that install now also
// writes preToolUse + postToolUse command hooks (matcher "Shell") alongside
// the session lifecycle events (65aq).
func TestInstallCursorHooksWritesToolUseEvents(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")

	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("InstallCursorHooks() error = %v", err)
	}

	f := readCursorHooks(t, hooksPath)
	for _, event := range []string{"sessionStart", "sessionEnd", "preToolUse", "postToolUse"} {
		if !hasConfabHookCommand(f, event) {
			t.Errorf("%s missing confab command\n%+v", event, f)
		}
	}

	data, _ := os.ReadFile(hooksPath)
	for _, want := range []string{
		"hook pre-tool-use --provider cursor",
		"hook post-tool-use --provider cursor",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("hooks.json missing %q\n%s", want, data)
		}
	}

	// The tool-use events carry the Shell matcher so Cursor scopes them to
	// shell invocations.
	for _, event := range []string{"preToolUse", "postToolUse"} {
		var matched bool
		for _, h := range f.Hooks[event] {
			if h.Matcher == "Shell" {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s entry missing matcher \"Shell\"\n%+v", event, f.Hooks[event])
		}
	}
}

// TestIsCursorHooksInstalledRequiresAllFourEvents verifies the install check
// now requires all four managed events (65aq) — a legacy two-event install
// reads as not installed so `confab setup` transparently upgrades it.
func TestIsCursorHooksInstalledRequiresAllFourEvents(t *testing.T) {
	const twoEventLegacy = `{"version":1,"hooks":{
		"sessionStart":[{"command":"/usr/local/bin/confab hook session-start --provider cursor","type":"command"}],
		"sessionEnd":[{"command":"/usr/local/bin/confab hook session-end --provider cursor","type":"command"}]
	}}`
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(twoEventLegacy), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := IsCursorHooksInstalled(hooksPath)
	if err != nil {
		t.Fatalf("IsCursorHooksInstalled: %v", err)
	}
	if got {
		t.Error("legacy two-event install read as installed; want false (upgrade)")
	}

	// A fresh install satisfies the four-event requirement.
	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err = IsCursorHooksInstalled(hooksPath)
	if err != nil || !got {
		t.Fatalf("IsCursorHooksInstalled after fresh install = %v, %v; want true, nil", got, err)
	}
}

// TestUninstallCursorHooksRemovesToolUseEvents confirms uninstall removes the
// preToolUse/postToolUse confab entries too.
func TestUninstallCursorHooksRemovesToolUseEvents(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")
	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := UninstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	f := readCursorHooks(t, hooksPath)
	for _, event := range []string{"preToolUse", "postToolUse"} {
		if hasConfabHookCommand(f, event) {
			t.Errorf("%s confab entry survived uninstall\n%+v", event, f)
		}
	}
}

// TestInstallCursorHooksToolUseIdempotent confirms re-installing does not
// duplicate the preToolUse/postToolUse entries.
func TestInstallCursorHooksToolUseIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")
	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("second install: %v", err)
	}
	f := readCursorHooks(t, hooksPath)
	for _, event := range []string{"preToolUse", "postToolUse"} {
		if n := len(f.Hooks[event]); n != 1 {
			t.Errorf("%s has %d entries after double install, want 1\n%+v", event, n, f)
		}
	}
}
