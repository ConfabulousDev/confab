package hookconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readCursorHooks parses a hooks.json file into the schema we assert against.
func readCursorHooks(t *testing.T, path string) cursorHooksFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var f cursorHooksFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v\n%s", err, data)
	}
	return f
}

// hasConfabHookCommand reports whether the named event carries a confab command.
func hasConfabHookCommand(f cursorHooksFile, event string) bool {
	for _, h := range f.Hooks[event] {
		if h.Type == "command" && isConfabCursorCommand(h.Command) {
			return true
		}
	}
	return false
}

func TestInstallCursorHooksWritesBothEvents(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")

	got, err := InstallCursorHooks(hooksPath)
	if err != nil {
		t.Fatalf("InstallCursorHooks() error = %v", err)
	}
	if got != hooksPath {
		t.Fatalf("InstallCursorHooks() = %q, want %q", got, hooksPath)
	}

	f := readCursorHooks(t, hooksPath)
	if f.Version != 1 {
		t.Errorf("version = %d, want 1", f.Version)
	}
	if !hasConfabHookCommand(f, "sessionStart") {
		t.Errorf("sessionStart missing confab command\n%+v", f)
	}
	if !hasConfabHookCommand(f, "sessionEnd") {
		t.Errorf("sessionEnd missing confab command\n%+v", f)
	}

	// Commands must invoke the right subcommands with --provider cursor.
	data, _ := os.ReadFile(hooksPath)
	for _, want := range []string{
		"hook session-start --provider cursor",
		"hook session-end --provider cursor",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("hooks.json missing %q\n%s", want, data)
		}
	}
}

func TestIsCursorHooksInstalled(t *testing.T) {
	const confabBoth = `{"version":1,"hooks":{
		"sessionStart":[{"command":"/usr/local/bin/confab hook session-start --provider cursor","type":"command"}],
		"sessionEnd":[{"command":"/usr/local/bin/confab hook session-end --provider cursor","type":"command"}]
	}}`
	const startOnly = `{"version":1,"hooks":{
		"sessionStart":[{"command":"/usr/local/bin/confab hook session-start --provider cursor","type":"command"}]
	}}`
	const otherOnly = `{"version":1,"hooks":{
		"sessionStart":[{"command":"/usr/bin/something-else","type":"command"}],
		"sessionEnd":[{"command":"/usr/bin/something-else","type":"command"}]
	}}`

	tests := []struct {
		name    string
		content string // "" = no file
		want    bool
	}{
		{"missing file", "", false},
		{"empty object", "{}", false},
		{"both events confab", confabBoth, true},
		{"only sessionStart confab", startOnly, false},
		{"only non-confab hooks", otherOnly, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			hooksPath := filepath.Join(tmpDir, "hooks.json")
			if tt.content != "" {
				if err := os.WriteFile(hooksPath, []byte(tt.content), 0600); err != nil {
					t.Fatalf("write hooks.json: %v", err)
				}
			}
			got, err := IsCursorHooksInstalled(hooksPath)
			if err != nil {
				t.Fatalf("IsCursorHooksInstalled() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("IsCursorHooksInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInstallThenIsInstalledThenUninstall(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")

	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	installed, err := IsCursorHooksInstalled(hooksPath)
	if err != nil || !installed {
		t.Fatalf("IsCursorHooksInstalled() = %v, %v; want true, nil", installed, err)
	}

	if _, err := UninstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	installed, err = IsCursorHooksInstalled(hooksPath)
	if err != nil || installed {
		t.Fatalf("after uninstall IsCursorHooksInstalled() = %v, %v; want false, nil", installed, err)
	}
}

func TestInstallCursorHooksIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")

	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("second install: %v", err)
	}

	f := readCursorHooks(t, hooksPath)
	if n := len(f.Hooks["sessionStart"]); n != 1 {
		t.Errorf("sessionStart has %d entries after double install, want 1\n%+v", n, f)
	}
	if n := len(f.Hooks["sessionEnd"]); n != 1 {
		t.Errorf("sessionEnd has %d entries after double install, want 1\n%+v", n, f)
	}
}

func TestInstallCursorHooksPreservesUserHooks(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")

	// A hand-authored user hooks.json with an unmanaged hook on sessionStart
	// plus a whole unmanaged event (stop) we must never touch.
	const userHooks = `{"version":1,"hooks":{
		"sessionStart":[{"command":"/opt/user/notify.sh","type":"command"}],
		"stop":[{"command":"/opt/user/cleanup.sh","type":"command"}]
	}}`
	if err := os.WriteFile(hooksPath, []byte(userHooks), 0600); err != nil {
		t.Fatalf("seed user hooks: %v", err)
	}

	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("install: %v", err)
	}

	f := readCursorHooks(t, hooksPath)
	// User's sessionStart hook preserved, ours appended alongside it.
	if n := len(f.Hooks["sessionStart"]); n != 2 {
		t.Errorf("sessionStart = %d entries, want 2 (user + confab)\n%+v", n, f)
	}
	foundUser := false
	for _, h := range f.Hooks["sessionStart"] {
		if h.Command == "/opt/user/notify.sh" {
			foundUser = true
		}
	}
	if !foundUser {
		t.Errorf("user's sessionStart hook was dropped\n%+v", f)
	}
	// Unmanaged event untouched.
	if n := len(f.Hooks["stop"]); n != 1 || f.Hooks["stop"][0].Command != "/opt/user/cleanup.sh" {
		t.Errorf("user's stop hook was modified\n%+v", f)
	}

	// Uninstall must restore the user file: our hooks gone, theirs intact.
	if _, err := UninstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	f = readCursorHooks(t, hooksPath)
	if hasConfabHookCommand(f, "sessionStart") {
		t.Errorf("confab sessionStart hook survived uninstall\n%+v", f)
	}
	if n := len(f.Hooks["sessionStart"]); n != 1 || f.Hooks["sessionStart"][0].Command != "/opt/user/notify.sh" {
		t.Errorf("user's sessionStart hook not preserved after uninstall\n%+v", f)
	}
	if n := len(f.Hooks["stop"]); n != 1 || f.Hooks["stop"][0].Command != "/opt/user/cleanup.sh" {
		t.Errorf("user's stop hook altered by uninstall\n%+v", f)
	}
}

func TestInstallCursorHooksCreatesBackup(t *testing.T) {
	tmpDir := t.TempDir()
	hooksPath := filepath.Join(tmpDir, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{"version":1,"hooks":{}}`), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("install: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "hooks.json.confab-backup-") {
			found = true
		}
	}
	if !found {
		t.Errorf("no backup file created in %s; entries=%v", tmpDir, entries)
	}
}

func TestInstallCursorHooksCreatesDirWithPerms(t *testing.T) {
	tmpDir := t.TempDir()
	cursorDir := filepath.Join(tmpDir, ".cursor")
	hooksPath := filepath.Join(cursorDir, "hooks.json")

	if _, err := InstallCursorHooks(hooksPath); err != nil {
		t.Fatalf("install: %v", err)
	}

	info, err := os.Stat(cursorDir)
	if err != nil {
		t.Fatalf("stat cursor dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("cursor dir perms = %o, want 700", perm)
	}
}
