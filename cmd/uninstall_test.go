package cmd

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/hookconfig"
	"github.com/ConfabulousDev/confab/pkg/pathcanon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

// isolatedUninstallEnv points HOME, the confab config and every provider dir
// at fresh, not-yet-created paths under a temp dir (kata 2n1f).
type isolatedUninstallEnv struct {
	home, configPath, claude, codex, cursor, opencode string
}

func newIsolatedUninstallEnv(t *testing.T) isolatedUninstallEnv {
	t.Helper()
	home := t.TempDir()
	e := isolatedUninstallEnv{
		home:       home,
		configPath: filepath.Join(home, ".confab", "config.json"),
		claude:     filepath.Join(home, ".claude"),
		codex:      filepath.Join(home, ".codex"),
		cursor:     filepath.Join(home, ".cursor"),
		opencode:   filepath.Join(home, ".config", "opencode"),
	}
	t.Setenv("HOME", home)
	t.Setenv("CONFAB_CONFIG_PATH", e.configPath)
	t.Setenv("CONFAB_CLAUDE_DIR", e.claude)
	t.Setenv("CONFAB_CODEX_DIR", e.codex)
	t.Setenv("CONFAB_CURSOR_DIR", e.cursor)
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", e.opencode)
	origTimeout := stopAllTimeout
	stopAllTimeout = time.Second
	t.Cleanup(func() { stopAllTimeout = origTimeout })
	return e
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validKey = "cfb_abcdefghijklmnopqrstuvwxyz0123456789ABCD"

// claudeSettingsWithConfab is a settings.json holding every confab hook
// bundle (as a real `confab setup` writes them) plus user-authored content.
const claudeSettingsWithConfab = `{
  "theme": "dark",
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [
      {"type": "command", "command": "echo user-start"},
      {"type": "command", "command": "/usr/local/bin/confab hook session-start --provider claude-code"}]}],
    "SessionEnd": [{"matcher": "*", "hooks": [{"type": "command", "command": "/usr/local/bin/confab hook session-end --provider claude-code"}]}],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/confab hook pre-tool-use"}]},
      {"matcher": "mcp__github__create_pull_request", "hooks": [{"type": "command", "command": "/usr/local/bin/confab hook pre-tool-use"}]}],
    "PostToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/confab hook post-tool-use"}]},
      {"matcher": "mcp__github__create_pull_request", "hooks": [{"type": "command", "command": "/usr/local/bin/confab hook post-tool-use"}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "/usr/local/bin/confab hook user-prompt-submit"}]}]
  }
}`

// claudeSettingsUserOnly is what must remain after uninstall.
const claudeSettingsUserOnly = `{
  "theme": "dark",
  "hooks": {"SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "echo user-start"}]}]}
}`

func assertJSONEqual(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got, exp any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("%s: user content changed\nwant: %s\ngot:  %s", path, want, data)
	}
}

func TestUninstallRemovesEverythingFromEveryProviderAndKeepsUserContent(t *testing.T) {
	e := newIsolatedUninstallEnv(t)

	// Claude default + a bound custom config dir.
	writeTestFile(t, filepath.Join(e.claude, "settings.json"), claudeSettingsWithConfab)
	customDir := filepath.Join(e.home, "work-claude")
	writeTestFile(t, filepath.Join(customDir, "settings.json"), claudeSettingsWithConfab)
	if err := config.SaveUploadConfig(&config.UploadConfig{
		BackendURL: "https://default.example",
		APIKey:     validKey,
		LogLevel:   "debug",
		Bindings: map[string]map[string]config.BindingCreds{
			provider.NameClaudeCode: {pathcanon.CanonicalDir(customDir): {BackendURL: "https://work.example", APIKey: validKey}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Codex, Cursor and OpenCode: user content first, then a real install.
	codexUser := "model = \"o3\"\n\n[[hooks.SessionStart]]\n[[hooks.SessionStart.hooks]]\ntype = \"command\"\ncommand = \"echo user-codex\"\n"
	writeTestFile(t, filepath.Join(e.codex, "config.toml"), codexUser)
	cursorUser := `{"version": 1, "hooks": {"sessionStart": [{"command": "echo user-cursor", "type": "command"}]}}`
	writeTestFile(t, filepath.Join(e.cursor, "hooks.json"), cursorUser)
	for _, name := range []string{provider.NameClaudeCode, provider.NameCodex, provider.NameCursor, provider.NameOpencode} {
		p, _ := provider.Get(name)
		if name != provider.NameClaudeCode {
			if _, err := p.InstallHooks(); err != nil {
				t.Fatalf("install %s hooks: %v", name, err)
			}
		}
		if err := p.InstallSkills(); err != nil {
			t.Fatalf("install %s skills: %v", name, err)
		}
	}
	bound, err := provider.GetWithDir(provider.NameClaudeCode, customDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := bound.InstallSkills(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runUninstall(strings.NewReader(""), &out, true); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	output := out.String()

	// Every confab entry, plugin and skill is gone; user content survives.
	assertJSONEqual(t, filepath.Join(e.claude, "settings.json"), claudeSettingsUserOnly)
	assertJSONEqual(t, filepath.Join(customDir, "settings.json"), claudeSettingsUserOnly)
	// Codex: the user's bytes are kept verbatim. The shared `[features] hooks
	// = true` flag confab's install set is deliberately left (kata cd4q): the
	// user's own Codex hooks may depend on it.
	if got, _ := os.ReadFile(filepath.Join(e.codex, "config.toml")); !strings.HasPrefix(string(got), codexUser) || strings.Contains(string(got), "confab") {
		t.Errorf("codex config.toml must keep the user's bytes and lose every confab table\nwant prefix: %q\ngot:  %q", codexUser, got)
	}
	assertJSONEqual(t, filepath.Join(e.cursor, "hooks.json"), cursorUser)
	for _, name := range []string{provider.NameCodex, provider.NameCursor, provider.NameOpencode} {
		p, _ := provider.Get(name)
		if ok, _ := p.IsHooksInstalled(); ok {
			t.Errorf("%s hooks still installed", name)
		}
	}
	for _, p := range []provider.Provider{mustGet(t, provider.NameClaudeCode), mustGet(t, provider.NameCodex), mustGet(t, provider.NameCursor), mustGet(t, provider.NameOpencode), bound} {
		if p.IsSkillInstalled("retro") {
			t.Errorf("%s retro skill still installed", p.Name())
		}
	}
	if !strings.Contains(output, "--config-dir "+pathcanon.CanonicalDir(customDir)) {
		t.Errorf("output must list the bound custom dir:\n%s", output)
	}

	// The flag is set and config.json is otherwise intact.
	cfg, err := config.GetUploadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SyncDisabled {
		t.Error("uninstall must set sync_disabled")
	}
	if cfg.APIKey != validKey || cfg.LogLevel != "debug" || len(cfg.Bindings[provider.NameClaudeCode]) != 1 {
		t.Errorf("credentials/bindings/settings must be kept, got %+v", cfg)
	}

	// Backups written by the Codex/Cursor installs are listed, not deleted.
	for _, path := range []string{filepath.Join(e.codex, "config.toml"), filepath.Join(e.cursor, "hooks.json")} {
		backups := hookconfig.ListBackups(path)
		if len(backups) == 0 {
			t.Errorf("expected install backups next to %s", path)
		}
		for _, b := range backups {
			if !strings.Contains(output, b) {
				t.Errorf("backup %s must be listed in the output", b)
			}
		}
	}
	if !strings.Contains(output, "To remove the binary: rm ") {
		t.Errorf("output must print the binary rm hint:\n%s", output)
	}

	// A second run is a clean no-op.
	before := snapshotTree(t, e.home)
	out.Reset()
	if err := runUninstall(strings.NewReader(""), &out, true); err != nil {
		t.Fatalf("second uninstall: %v\n%s", err, out.String())
	}
	if after := snapshotTree(t, e.home); !reflect.DeepEqual(before, after) {
		t.Errorf("second uninstall must change nothing\nbefore: %v\nafter:  %v", before, after)
	}
}

func mustGet(t *testing.T, name string) provider.Provider {
	t.Helper()
	p, err := provider.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// snapshotTree maps every file under root (except confab's own logs) to its
// contents.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "logs" {
				return filepath.SkipDir
			}
			out[path+"/"] = ""
			return nil
		}
		data, _ := os.ReadFile(path)
		out[path] = string(data)
		return nil
	})
	return out
}

func TestUninstallOnEmptyHomeTouchesNoProviderDir(t *testing.T) {
	e := newIsolatedUninstallEnv(t)
	var out bytes.Buffer
	if err := runUninstall(strings.NewReader(""), &out, true); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	for _, dir := range []string{e.claude, e.codex, e.cursor, e.opencode} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("uninstall must not create %s", dir)
		}
	}
	// The only write is the sync_disabled flag in ~/.confab/config.json.
	if d, _ := config.IsSyncDisabled(); !d {
		t.Error("sync_disabled must be set even on an empty machine")
	}
}

func TestUninstallRefusesWithoutTTYOrYes(t *testing.T) {
	e := newIsolatedUninstallEnv(t)
	writeTestFile(t, filepath.Join(e.claude, "settings.json"), claudeSettingsWithConfab)
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = orig })

	var out bytes.Buffer
	err := runUninstall(strings.NewReader("y\n"), &out, false)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("without a TTY and without --yes uninstall must refuse, got %v", err)
	}
	if d, _ := config.IsSyncDisabled(); d {
		t.Error("a refused uninstall must not disable sync")
	}
	if got, _ := os.ReadFile(filepath.Join(e.claude, "settings.json")); string(got) != claudeSettingsWithConfab {
		t.Error("a refused uninstall must not touch settings.json")
	}
}

func TestUninstallPromptAnswers(t *testing.T) {
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = orig })

	for _, tc := range []struct {
		answer  string
		removes bool
	}{{"y\n", true}, {"YES\n", true}, {"\n", false}, {"n\n", false}, {"", false}} {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			e := newIsolatedUninstallEnv(t)
			settings := filepath.Join(e.claude, "settings.json")
			writeTestFile(t, settings, claudeSettingsWithConfab)
			var out bytes.Buffer
			if err := runUninstall(strings.NewReader(tc.answer), &out, false); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			got, _ := os.ReadFile(settings)
			removed := !strings.Contains(string(got), "confab hook")
			if removed != tc.removes {
				t.Errorf("answer %q: removed=%v, want %v\n%s", tc.answer, removed, tc.removes, out.String())
			}
			if d, _ := config.IsSyncDisabled(); d != tc.removes {
				t.Errorf("answer %q: sync_disabled=%v, want %v", tc.answer, d, tc.removes)
			}
		})
	}
}

func TestUninstallSkipsMissingBoundDir(t *testing.T) {
	e := newIsolatedUninstallEnv(t)
	gone := filepath.Join(e.home, "gone-claude")
	if err := config.SaveUploadConfig(&config.UploadConfig{
		Bindings: map[string]map[string]config.BindingCreds{
			provider.NameClaudeCode: {gone: {BackendURL: "https://x.example", APIKey: validKey}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runUninstall(strings.NewReader(""), &out, true); err != nil {
		t.Fatalf("a missing bound dir is a note, not an error: %v", err)
	}
	if !strings.Contains(out.String(), "skipped, directory no longer exists") {
		t.Errorf("missing bound dir must be noted:\n%s", out.String())
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Error("uninstall must not create a missing bound dir")
	}
}

func TestEnableDisableIdempotent(t *testing.T) {
	newIsolatedUninstallEnv(t)
	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		if err := runDisable(&out, time.Second); err != nil {
			t.Fatalf("disable #%d: %v", i+1, err)
		}
		if d, _ := config.IsSyncDisabled(); !d {
			t.Fatalf("disable #%d must leave sync disabled", i+1)
		}
	}
	if !strings.Contains(out.String(), "Sync was already disabled.") || !strings.Contains(out.String(), "No sync daemons were running.") {
		t.Errorf("unexpected disable output:\n%s", out.String())
	}
	out.Reset()
	for i := 0; i < 2; i++ {
		if err := runEnable(&out); err != nil {
			t.Fatalf("enable #%d: %v", i+1, err)
		}
		if d, _ := config.IsSyncDisabled(); d {
			t.Fatalf("enable #%d must leave sync enabled", i+1)
		}
	}
	if !strings.Contains(out.String(), "Sync is already enabled.") {
		t.Errorf("unexpected enable output:\n%s", out.String())
	}
}

func TestSetupClearsSyncDisabled(t *testing.T) {
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()
	backend := &setupTestBackend{validateValid: true}
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)
	server := srv.URL

	_, configPath := setupSetupTestEnv(t)
	resetSetupProviderName(t)
	seedConfigAt(t, configPath, config.UploadConfig{BackendURL: server, APIKey: "cfb_existing-key-12345678", SyncDisabled: true})

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server, "")
	cmd.Flags().String("api-key", "", "")
	var err error
	output := captureStdout(t, func() { err = runSetup(cmd, nil) })
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if d, _ := config.IsSyncDisabled(); d {
		t.Error("setup must clear sync_disabled")
	}
	if !strings.Contains(output, "Sync re-enabled.") {
		t.Errorf("setup must say it re-enabled sync:\n%s", output)
	}
}

// --- hook gate ---

func TestEveryHookIsSilentNoopWhileDisabled(t *testing.T) {
	newIsolatedUninstallEnv(t)
	if _, err := config.SetSyncDisabled(true); err != nil {
		t.Fatal(err)
	}
	var spawned int
	origSpawn := spawnDaemonFunc
	spawnDaemonFunc = func(*daemonLaunchInput) error { spawned++; return nil }
	t.Cleanup(func() { spawnDaemonFunc = origSpawn })
	origProvider := hookProviderName
	t.Cleanup(func() { hookProviderName = origProvider })

	commit := `{"session_id":"s1","transcript_path":"/tmp/p/projects/x/s1.jsonl","cwd":"/tmp","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git commit -m hi"}}`
	payloads := map[string]string{
		provider.NameClaudeCode: commit,
		provider.NameCodex:      commit,
		provider.NameCursor:     `{"conversation_id":"s1","session_id":"s1","hook_event_name":"preToolUse","tool_name":"Shell","tool_input":{"command":"git commit -m hi"},"workspace_roots":["/tmp"]}`,
		provider.NameOpencode:   `{"session_id":"s1","cwd":"/tmp","parent_pid":1}`,
	}
	if len(hookCmd.Commands()) != 5 {
		t.Fatalf("expected 5 hook subcommands, got %d: add the new one to the gate in hook.go and here", len(hookCmd.Commands()))
	}
	for _, sub := range hookCmd.Commands() {
		for prov, payload := range payloads {
			t.Run(sub.Name()+"/"+prov, func(t *testing.T) {
				hookProviderName = prov
				in := strings.NewReader(payload)
				sub.SetIn(in)
				t.Cleanup(func() { sub.SetIn(nil) })
				var err error
				out := captureStdout(t, func() { err = sub.RunE(sub, nil) })
				if err != nil {
					t.Errorf("must exit 0 while disabled, got %v", err)
				}
				if out != "" {
					t.Errorf("must print nothing while disabled (no link injection), got %q", out)
				}
				if in.Len() != 0 {
					t.Error("stdin must be drained")
				}
			})
		}
	}
	if spawned != 0 {
		t.Errorf("no daemon may be spawned while disabled, got %d", spawned)
	}
}

func TestHookGateTreatsConfigErrorAsEnabled(t *testing.T) {
	e := newIsolatedUninstallEnv(t)
	writeTestFile(t, e.configPath, "{corrupt")
	if skipHookWhenSyncDisabled(strings.NewReader("x")) {
		t.Error("a config read error must not disable hooks")
	}
	writeTestFile(t, e.configPath, `{"sync_disabled":false}`)
	if skipHookWhenSyncDisabled(strings.NewReader("x")) {
		t.Error("enabled sync must not skip hooks")
	}
	writeTestFile(t, e.configPath, `{"sync_disabled":true}`)
	if !skipHookWhenSyncDisabled(strings.NewReader("x")) {
		t.Error("disabled sync must skip hooks")
	}
}

func TestStatusShowsSyncDisabled(t *testing.T) {
	e := newIsolatedUninstallEnv(t)
	writeTestFile(t, e.configPath, `{"sync_disabled":true}`)
	out := captureStdout(t, printBackendSection)
	if !strings.Contains(out, "Sync: disabled") {
		t.Errorf("status must show sync disabled:\n%s", out)
	}
}
