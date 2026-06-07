package provider

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

func TestOpencodeName(t *testing.T) {
	if got := (Opencode{}).Name(); got != NameOpencode {
		t.Errorf("Name() = %q, want %q", got, NameOpencode)
	}
}

func TestOpencodeSupportsCommitLinking(t *testing.T) {
	if (Opencode{}).SupportsCommitLinking() {
		t.Error("SupportsCommitLinking() = true, want false")
	}
}

func TestOpencodeWalkUpToRoot(t *testing.T) {
	id, path, err := (Opencode{}).WalkUpToRoot("test-session")
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if id != "test-session" {
		t.Errorf("session = %q, want %q", id, "test-session")
	}
	if path != "" {
		t.Errorf("path = %q, want \"\"", path)
	}
}

func TestOpencodeDefaultCWD(t *testing.T) {
	got := (Opencode{}).DefaultCWD("/home/user/.config/opencode/sessions/session-id.jsonl")
	want := "/home/user/.config/opencode/sessions"
	if got != want {
		t.Errorf("DefaultCWD() = %q, want %q", got, want)
	}
}

func TestOpencodeReadSessionHookInput_Valid(t *testing.T) {
	input := `{"session_id":"test-0199","cwd":"/work"}`
	p := Opencode{}
	got, err := p.ReadSessionHookInput(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ReadSessionHookInput: %v", err)
	}
	if got.SessionID != "test-0199" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "test-0199")
	}
	if got.CWD != "/work" {
		t.Errorf("CWD = %q, want %q", got.CWD, "/work")
	}
}

func TestOpencodeReadSessionHookInput_MissingSessionID(t *testing.T) {
	input := `{"cwd":"/work"}`
	_, err := (Opencode{}).ReadSessionHookInput(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for missing session_id")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("error = %q, want session_id error", err)
	}
}

func TestOpencodeReadSessionHookInput_InvalidJSON(t *testing.T) {
	_, err := (Opencode{}).ReadSessionHookInput(strings.NewReader("not-json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestOpencodeReadSessionHookInput_InvalidSessionID(t *testing.T) {
	input := `{"session_id":"../../evil","cwd":"/work"}`
	_, err := (Opencode{}).ReadSessionHookInput(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for invalid session_id (path traversal)")
	}
}

func TestOpencodeReadSessionHookInput_SetsParentPID(t *testing.T) {
	input := `{"session_id":"test-0199","cwd":"/work","parent_pid":4242}`
	got, err := (Opencode{}).ReadSessionHookInput(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ReadSessionHookInput: %v", err)
	}
	if got.ParentPID != 4242 {
		t.Errorf("ParentPID = %d, want 4242", got.ParentPID)
	}
}

func TestOpencodeParseSessionHook(t *testing.T) {
	input := `{"session_id":"test-parse","cwd":"/work"}`
	hookInput, err := (Opencode{}).ParseSessionHook(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSessionHook: %v", err)
	}
	if hookInput.SessionID() != "test-parse" {
		t.Errorf("SessionID() = %q, want %q", hookInput.SessionID(), "test-parse")
	}
	if hookInput.TranscriptPath() != "" {
		t.Errorf("TranscriptPath() = %q, want \"\"", hookInput.TranscriptPath())
	}
	if hookInput.CWD() != "/work" {
		t.Errorf("CWD() = %q, want %q", hookInput.CWD(), "/work")
	}
}

func TestOpencodeStateDir_Default(t *testing.T) {
	// Ensure env var is not set
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	got, err := (Opencode{}).StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join(home, ".config", "opencode")
	if got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
}

func TestOpencodeStateDir_WithEnv(t *testing.T) {
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", "/custom/opencode")
	got, err := (Opencode{}).StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if got != "/custom/opencode" {
		t.Errorf("StateDir() = %q, want %q", got, "/custom/opencode")
	}
}

func TestOpencodePluginDir(t *testing.T) {
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", "/custom/opencode")
	got, err := (Opencode{}).PluginDir()
	if err != nil {
		t.Fatalf("PluginDir: %v", err)
	}
	want := filepath.Join("/custom/opencode", "plugins")
	if got != want {
		t.Errorf("PluginDir() = %q, want %q", got, want)
	}
}

func TestOpencodeIsProcessPattern(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		matches bool
	}{
		{"standalone opencode", "opencode", true},
		{"opencode CLI path", "/usr/local/bin/opencode", true},
		{"opencode with args", "opencode --version", true},
		{"mixed case", "OpenCode", true},
		{"electron opencode", "/opt/OpenCode/opencode", true},
		{"all uppercase", "OPENCODE", true},
		{"not opencode", "claude", false},
		{"not opencode 2", "codex", false},
		{"opencode as substring", "myopencodeapp", false},
		{"prefixed opencode", "preopencode", false},
		{"suffixed opencode", "opencodeextra", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := opencodeProcessPattern.MatchString(tt.cmd)
			if got != tt.matches {
				t.Errorf("opencodeProcessPattern.MatchString(%q) = %v, want %v", tt.cmd, got, tt.matches)
			}
		})
	}
}

func opencodePluginDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	return filepath.Join(filepath.Dir(filename), "plugins")
}

func TestOpencodeInstallHooksWritesPlugin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", tmpDir)

	p := Opencode{}
	gotPath, err := p.InstallHooks()
	if err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	wantPath := filepath.Join(tmpDir, "plugins", "confab-sync.ts")
	if gotPath != wantPath {
		t.Errorf("InstallHooks() returned %q, want %q", gotPath, wantPath)
	}

	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("plugin file not written at %s: %v", wantPath, err)
	}

	source := string(data)
	if !strings.Contains(source, "ConfabSync") {
		t.Error("plugin file missing ConfabSync export")
	}
	if !strings.Contains(source, "session.created") {
		t.Error("plugin file missing session.created handler")
	}
	if strings.Contains(source, "§BT§") {
		t.Error("plugin file still contains §BT§ placeholders; backtick replacement failed")
	}
	if !strings.Contains(source, "session-start --provider opencode") {
		t.Error("plugin file missing session-start command")
	}
	if !strings.Contains(source, "session-end --provider opencode") {
		t.Error("plugin file missing session-end command")
	}
}

func TestOpencodeUninstallHooksRemovesPlugin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", tmpDir)

	p := Opencode{}
	if _, err := p.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() failed: %v", err)
	}

	gotPath, err := p.UninstallHooks()
	if err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	wantPath := filepath.Join(tmpDir, "plugins", "confab-sync.ts")
	if gotPath != wantPath {
		t.Errorf("UninstallHooks() returned %q, want %q", gotPath, wantPath)
	}

	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Errorf("plugin file still exists after UninstallHooks: %v", err)
	}
}

func TestOpencodeIsHooksInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_OPENCODE_CONFIG_DIR", tmpDir)

	p := Opencode{}

	installed, err := p.IsHooksInstalled()
	if err != nil {
		t.Fatalf("IsHooksInstalled() before install: %v", err)
	}
	if installed {
		t.Error("IsHooksInstalled() = true before install, want false")
	}

	if _, err := p.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() failed: %v", err)
	}

	installed, err = p.IsHooksInstalled()
	if err != nil {
		t.Fatalf("IsHooksInstalled() after install: %v", err)
	}
	if !installed {
		t.Error("IsHooksInstalled() = false after install, want true")
	}

	if _, err := p.UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks() failed: %v", err)
	}

	installed, err = p.IsHooksInstalled()
	if err != nil {
		t.Fatalf("IsHooksInstalled() after uninstall: %v", err)
	}
	if installed {
		t.Error("IsHooksInstalled() = true after uninstall, want false")
	}
}

func TestOpencodePluginSourceMatchesFile(t *testing.T) {
	pluginDir := opencodePluginDir(t)
	raw, err := os.ReadFile(filepath.Join(pluginDir, "confab-sync.ts"))
	if err != nil {
		t.Fatalf("read canonical plugin source: %v", err)
	}

	fileContent := string(raw)

	// The file uses real backticks; the constant uses §BT§ as placeholder.
	// Normalize the file to §BT§ form and compare.
	fileAsConstant := strings.ReplaceAll(fileContent, "`", "§BT§")

	if fileAsConstant != opencodePluginSourceRaw {
		t.Fatal("canonical plugin source (pkg/provider/plugins/confab-sync.ts) does not match opencodePluginSourceRaw constant. Regenerate with: go generate ./pkg/provider/")
	}
}

// TestOpencodeHookInputJSON ensures the JSON round-trips correctly.
func TestOpencodeHookInputJSON(t *testing.T) {
	orig := types.OpenCodeHookInput{
		SessionID: "test-session",
		CWD:       "/work",
		ParentPID: 1234,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got types.OpenCodeHookInput
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID != orig.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, orig.SessionID)
	}
	if got.CWD != orig.CWD {
		t.Errorf("CWD = %q, want %q", got.CWD, orig.CWD)
	}
	if got.ParentPID != orig.ParentPID {
		t.Errorf("ParentPID = %d, want %d", got.ParentPID, orig.ParentPID)
	}
}

// TestOpencodePluginSourceDropsServerURL pins that the bundled plugin
// (the canonical source string) no longer references serverUrl. The
// canonical .ts file and the bundled Go string are linted against each
// other elsewhere; this guards the bundled copy directly.
func TestOpencodePluginSourceDropsServerURL(t *testing.T) {
	if strings.Contains(opencodePluginSourceRaw, "serverUrl") {
		t.Error("bundled plugin source still references serverUrl")
	}
	if strings.Contains(opencodePluginSourceRaw, "server_url") {
		t.Error("bundled plugin source still emits server_url field")
	}
}

func TestOpencodePluginVitest(t *testing.T) {
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal("npm not found on PATH; install Node.js (https://nodejs.org) to run TypeScript plugin tests")
	}

	pluginDir := opencodePluginDir(t)

	// Run the locally installed vitest via the package.json "test" script.
	// We never fetch packages on the fly — dependencies must be installed
	// beforehand (`npm ci` in pluginDir; CI does this). The test fails loudly
	// if they are missing rather than reaching out to the network.
	cmd := exec.Command(npmPath, "test")
	cmd.Dir = pluginDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vitest failed (run `npm ci` in %s if dependencies are missing):\n%s", pluginDir, string(out))
	}
}

func TestOpencodePluginTypeScript(t *testing.T) {
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal("npm not found on PATH; install Node.js (https://nodejs.org) to validate the TypeScript plugin")
	}

	pluginDir := opencodePluginDir(t)

	// Type-check with the locally installed typescript via the package.json
	// "typecheck" script (tsc --noEmit). tsconfig.json selects the files to
	// check. As with the vitest test, dependencies must be pre-installed; we
	// never install on the fly.
	cmd := exec.Command(npmPath, "run", "typecheck")
	cmd.Dir = pluginDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TypeScript type check failed (run `npm ci` in %s if dependencies are missing):\n%s", pluginDir, string(out))
	}
}


// CF-549 OnAlreadyRunning tests --------------------------------------------

// TestOpencodeOnAlreadyRunningLogsWarn asserts the OpenCode provider logs
// a Warn-level message when invoked. The text mentions multi-process
// resume so operators reading logs can identify the scenario.
func TestOpencodeOnAlreadyRunningLogsWarn(t *testing.T) {
	logDir := logger.SetupForTesting(t)

	Opencode{}.OnAlreadyRunning("ses_resume_collision")

	logBytes, err := os.ReadFile(filepath.Join(logDir, "confab.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logOut := string(logBytes)
	if !strings.Contains(logOut, "ses_resume_collision") {
		t.Errorf("log missing externalID; got: %s", logOut)
	}
	if !strings.Contains(logOut, "WARN") {
		t.Errorf("log missing WARN level; got: %s", logOut)
	}
	if !strings.Contains(logOut, "multi-process resume") {
		t.Errorf("log missing diagnostic phrase \"multi-process resume\"; got: %s", logOut)
	}
}

// TestClaudeOnAlreadyRunningSilent asserts ClaudeCode.OnAlreadyRunning is
// a no-op (does not write to the log). Claude fires SessionStart on every
// turn, so hook deduplication is the normal path — logging would be noise.
func TestClaudeOnAlreadyRunningSilent(t *testing.T) {
	logDir := logger.SetupForTesting(t)

	ClaudeCode{}.OnAlreadyRunning("ses_anything")

	logBytes, _ := os.ReadFile(filepath.Join(logDir, "confab.log"))
	if strings.Contains(string(logBytes), "ses_anything") {
		t.Errorf("ClaudeCode.OnAlreadyRunning should not log; got: %s", string(logBytes))
	}
}

// TestCodexOnAlreadyRunningSilent asserts Codex.OnAlreadyRunning is a
// no-op. Codex fires SessionStart for every subagent; the already-running
// hit is normal hook dedup, not an error.
func TestCodexOnAlreadyRunningSilent(t *testing.T) {
	logDir := logger.SetupForTesting(t)

	Codex{}.OnAlreadyRunning("ses_anything_codex")

	logBytes, _ := os.ReadFile(filepath.Join(logDir, "confab.log"))
	if strings.Contains(string(logBytes), "ses_anything_codex") {
		t.Errorf("Codex.OnAlreadyRunning should not log; got: %s", string(logBytes))
	}
}
