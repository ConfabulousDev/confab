package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/opencodetest"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// runCodexSessionStart wraps the unified sessionStartFromReader with the
// Codex provider selected and a throwaway response writer. Tests don't
// inspect the hook response.
func runCodexSessionStart(t *testing.T, in []byte) error {
	t.Helper()
	return runCodexSessionStartRaw(t, in)
}

// runCodexSessionStartRaw accepts the raw payload bytes (callers that
// build the JSON ad-hoc).
func runCodexSessionStartRaw(t *testing.T, in []byte) error {
	t.Helper()
	orig := hookProviderName
	hookProviderName = provider.NameCodex
	defer func() { hookProviderName = orig }()
	return sessionStartFromReader(bytes.NewReader(in), io.Discard)
}

// codexHookInputJSON builds a Codex SessionStart hook payload for sessionID
// pointing at rolloutPath. cwd is fixed; tests don't care about it.
func codexHookInputJSON(t *testing.T, sessionID, rolloutPath string) []byte {
	t.Helper()
	b, err := json.Marshal(types.CodexHookInput{
		SessionID:      sessionID,
		TranscriptPath: rolloutPath,
		CWD:            "/work",
		HookEventName:  "SessionStart",
		Source:         "startup",
	})
	if err != nil {
		t.Fatalf("marshal hook input: %v", err)
	}
	return b
}

// setupCodexHookEnv combines the codextest fixture with the shared
// home + state-dir env wiring needed for daemon state files.
// CONFAB_CODEX_DIR is already set by codextest.NewFixture; we still need
// HOME so daemon.NewStateForProvider can write under ~/.confab/sync.
func setupCodexHookEnv(t *testing.T) (*codextest.Fixture, string) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	if err := os.MkdirAll(filepath.Join(tmpHome, ".confab", "sync"), 0o700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}
	return codextest.NewFixture(t), tmpHome
}

func TestCodexHook_FiringSessionIsRoot_SpawnsDaemonForItself(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-aaa")
	rootID := root.ThreadUUID()

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := codexHookInputJSON(t, rootID, root.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called for a root")
	}
	if captured.ExternalID != rootID {
		t.Errorf("session = %q, want root %q", captured.ExternalID, rootID)
	}
	if captured.TranscriptPath != root.Path() {
		t.Errorf("transcript = %q, want %q", captured.TranscriptPath, root.Path())
	}
}

func TestCodexHook_FiringSessionIsDirectChild_WalksUpToRoot_SpawnsDaemonForRoot(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-bbb")
	child := fixture.AddSubagent(root.ThreadUUID(), "child-bbb",
		codextest.SubagentOpts{AgentRole: "worker"})

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := codexHookInputJSON(t, child.ThreadUUID(), child.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called")
	}
	if captured.ExternalID != root.ThreadUUID() {
		t.Errorf("daemon spawned for %q, want root %q", captured.ExternalID, root.ThreadUUID())
	}
	if captured.TranscriptPath != root.Path() {
		t.Errorf("transcript path not rewritten: got %q, want %q",
			captured.TranscriptPath, root.Path())
	}
}

func TestCodexHook_FiringSessionIsGrandchild_WalksUpToTopMostRoot(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-ccc")
	child := fixture.AddSubagent(root.ThreadUUID(), "child-ccc",
		codextest.SubagentOpts{AgentRole: "mid"})
	grand := fixture.AddSubagent(child.ThreadUUID(), "grand-ccc",
		codextest.SubagentOpts{AgentRole: "leaf"})

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := codexHookInputJSON(t, grand.ThreadUUID(), grand.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called")
	}
	if captured.ExternalID != root.ThreadUUID() {
		t.Errorf("daemon spawned for %q, want top-most root %q",
			captured.ExternalID, root.ThreadUUID())
	}
	if captured.TranscriptPath != root.Path() {
		t.Errorf("transcript path = %q, want top-most root %q",
			captured.TranscriptPath, root.Path())
	}
}

func TestCodexHook_RootDaemonAlreadyRunning_HookExitsWithoutSpawning(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-ddd")
	child := fixture.AddSubagent(root.ThreadUUID(), "child-ddd", codextest.SubagentOpts{})

	// Daemon already running for the root.
	state := daemon.NewStateForProvider(provider.NameCodex, root.ThreadUUID(),
		root.Path(), "/work", 0)
	state.PID = os.Getpid()
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	var spawnCalled bool
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		spawnCalled = true
		return nil
	}

	in := codexHookInputJSON(t, child.ThreadUUID(), child.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if spawnCalled {
		t.Error("expected hook to be a no-op when root daemon is already running")
	}
}

func TestCodexHook_DaemonStateExistsButDaemonDead_CleansStaleStateAndSpawns(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-eee")

	// Stale state pointing at an obviously-dead PID.
	state := daemon.NewStateForProvider(provider.NameCodex, root.ThreadUUID(),
		root.Path(), "/work", 0)
	state.PID = 999999
	if err := state.Save(); err != nil {
		t.Fatalf("save stale state: %v", err)
	}

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := codexHookInputJSON(t, root.ThreadUUID(), root.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn when previous daemon is dead")
	}
	if captured.ExternalID != root.ThreadUUID() {
		t.Errorf("session = %q, want %q", captured.ExternalID, root.ThreadUUID())
	}
}

func TestCodexHook_EdgeRaceWithRetry_EdgeAppearsMidWait_RoutesCorrectly(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	// Keep the delay short and the retry budget generous enough for loaded CI
	// runners. The test still proves that the first no-edge lookup retries.
	provider.SetWalkUpRetryForTest(10, 25*time.Millisecond)
	defer provider.ResetWalkUpRetryForTest()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-fff")
	// Subagent thread row exists, but the parent edge is inserted with delay
	// to simulate the spawn-vs-edge race.
	subOpts := codextest.SubagentOpts{AgentRole: "lagged", ThreadSource: "agent"}
	child := fixture.AddSubagentNoEdge(t, "child-fff", subOpts)
	fixture.InsertEdgeLater(root.ThreadUUID(), child.ThreadUUID(), 10*time.Millisecond)

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := codexHookInputJSON(t, child.ThreadUUID(), child.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn after retry")
	}
	if captured.ExternalID != root.ThreadUUID() {
		t.Errorf("session = %q, want root %q after edge race resolved",
			captured.ExternalID, root.ThreadUUID())
	}
}

func TestCodexHook_EdgeRaceExhausted_TreatsFiringSessionAsRoot(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	provider.SetWalkUpRetryForTest(2, 5*time.Millisecond)
	defer provider.ResetWalkUpRetryForTest()

	fixture, _ := setupCodexHookEnv(t)
	// Thread row exists but no parent edge will ever appear.
	orphan := fixture.AddSubagentNoEdge(t, "orphan-ggg",
		codextest.SubagentOpts{AgentRole: "orphan", ThreadSource: "agent"})

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := codexHookInputJSON(t, orphan.ThreadUUID(), orphan.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn even when retries exhaust")
	}
	if captured.ExternalID != orphan.ThreadUUID() {
		t.Errorf("session = %q, want firing thread %q (treated as root after retry exhaustion)",
			captured.ExternalID, orphan.ThreadUUID())
	}
	if captured.TranscriptPath != orphan.Path() {
		t.Errorf("transcript = %q, want %q", captured.TranscriptPath, orphan.Path())
	}
}

func TestCodexHook_StateDBAbsent_DegradesToFiringSessionAsRoot(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	// No fixture — point the provider at a directory with no state_*.sqlite.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	codexDir := filepath.Join(tmpHome, ".codex")
	if err := os.MkdirAll(filepath.Join(codexDir, "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir codex dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpHome, ".confab", "sync"), 0o700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}
	t.Setenv(provider.CodexStateDirEnv, codexDir)
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()
	defer provider.ResetStateDBPathCacheForTest()

	// Hand-crafted rollout path that satisfies ValidateRolloutPath.
	rolloutPath := codexTestRolloutPath(tmpHome, "11111111-1111-1111-1111-111111111111")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		t.Fatalf("mkdir rollout: %v", err)
	}
	if err := os.WriteFile(rolloutPath, []byte{}, 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	sessionID := "11111111-1111-1111-1111-111111111111"
	// rollout file is empty → ReadSessionInfo returns the default (IsUserSession==true);
	// daemon spawn proceeds. With no state DB present, WalkUpToRoot degrades to
	// "firing session is its own root".
	if err := runCodexSessionStartRaw(t,
		codexHookInputJSON(t, sessionID, rolloutPath)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn even with no state DB")
	}
	if captured.ExternalID != sessionID {
		t.Errorf("session = %q, want firing UUID %q", captured.ExternalID, sessionID)
	}
}

// (sanity) Confirm the hook handler emits a response on stdout even when
// the resolved root's daemon is already running. We don't assert on the
// response body (the Codex hook is fire-and-forget), only that no panic
// or hang occurs.
// TestCodexHook_EnsuresCodexSkills guards the adoption path: when Codex
// hooks are installed, SessionStart should keep bundled Codex skills present
// without leaking Claude skill files for Codex-only users.
func TestCodexHook_EnsuresCodexSkills(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()
	spawnDaemonFunc = func(launch *daemonLaunchInput) error { return nil }

	fixture, tmpHome := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-no-skills")

	in := codexHookInputJSON(t, root.ThreadUUID(), root.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}

	for _, skill := range []string{"retro"} {
		claudePath := filepath.Join(tmpHome, ".claude", "skills", skill)
		if _, err := os.Stat(claudePath); err == nil {
			t.Errorf("Codex SessionStart leaked Claude skill into %s", claudePath)
		}

		codexPath := filepath.Join(fixture.Dir, "skills", skill, "SKILL.md")
		if _, err := os.Stat(codexPath); err != nil {
			t.Errorf("Codex SessionStart did not install Codex skill %s: %v", codexPath, err)
		}
	}
}

// --- OpenCode session-start helpers ---

func runOpencodeSessionStart(t *testing.T, in []byte) error {
	t.Helper()
	orig := hookProviderName
	hookProviderName = provider.NameOpencode
	defer func() { hookProviderName = orig }()
	return sessionStartFromReader(bytes.NewReader(in), io.Discard)
}

func opencodeHookInputJSON(t *testing.T, sessionID string) []byte {
	t.Helper()
	b, err := json.Marshal(types.OpenCodeHookInput{
		SessionID: sessionID,
		CWD:       "/work/opencode",
	})
	if err != nil {
		t.Fatalf("marshal hook input: %v", err)
	}
	return b
}

// --- OpenCode session-start tests ---

func TestOpencodeHook_SessionStart_SpawnsDaemon(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	syncDir := filepath.Join(tmpHome, ".confab", "sync")
	if err := os.MkdirAll(syncDir, 0700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}

	var captured *daemonLaunchInput
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		captured = launch
		return nil
	}

	in := opencodeHookInputJSON(t, "oc-session-0199")
	if err := runOpencodeSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called")
	}
	if captured.ExternalID != "oc-session-0199" {
		t.Errorf("session = %q, want %q", captured.ExternalID, "oc-session-0199")
	}
	if captured.TranscriptPath != "" {
		t.Errorf("TranscriptPath = %q, want \"\"", captured.TranscriptPath)
	}
	if captured.CWD != "/work/opencode" {
		t.Errorf("CWD = %q, want %q", captured.CWD, "/work/opencode")
	}
}

func TestOpencodeHook_SessionStart_NoDuplicateSpawn(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	syncDir := filepath.Join(tmpHome, ".confab", "sync", provider.NameOpencode)
	if err := os.MkdirAll(syncDir, 0700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}

	sessionID := "oc-session-duplicate"
	state := daemon.NewStateForProvider(provider.NameOpencode, sessionID, "", "/work/opencode", 0)
	state.PID = os.Getpid()
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	var spawnCalled bool
	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		spawnCalled = true
		return nil
	}

	in := opencodeHookInputJSON(t, sessionID)
	if err := runOpencodeSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if spawnCalled {
		t.Error("expected hook to be a no-op when daemon is already running")
	}
}

func TestBuildOpencodeLaunchArgs(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantID    string
		wantCWD   string
		wantError bool
	}{
		{
			name:    "valid input",
			input:   `{"session_id":"test-0199","cwd":"/work"}`,
			wantID:  "test-0199",
			wantCWD: "/work",
		},
		{
			name:      "missing session_id",
			input:     `{"cwd":"/work"}`,
			wantError: true,
		},
		{
			name:      "invalid JSON",
			input:     `not-json`,
			wantError: true,
		},
		{
			name:      "empty input",
			input:     ``,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launch, err := buildOpencodeLaunchArgs(strings.NewReader(tt.input))
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if launch == nil {
				t.Fatal("expected launch, got nil")
			}
			if launch.Provider != provider.NameOpencode {
				t.Errorf("Provider = %q, want %q", launch.Provider, provider.NameOpencode)
			}
			if launch.ExternalID != tt.wantID {
				t.Errorf("ExternalID = %q, want %q", launch.ExternalID, tt.wantID)
			}
			if launch.CWD != tt.wantCWD {
				t.Errorf("CWD = %q, want %q", launch.CWD, tt.wantCWD)
			}
			if launch.TranscriptPath != "" {
				t.Errorf("TranscriptPath = %q, want \"\"", launch.TranscriptPath)
			}
		})
	}
}

func TestCodexHook_RespondsWithoutPanic_WhenNoSpawn(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-hhh")

	state := daemon.NewStateForProvider(provider.NameCodex, root.ThreadUUID(),
		root.Path(), "/work", 0)
	state.PID = os.Getpid()
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	spawnDaemonFunc = func(launch *daemonLaunchInput) error {
		t.Fatal("should not spawn when daemon already running")
		return nil
	}

	in := codexHookInputJSON(t, root.ThreadUUID(), root.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}
	// Sanity: hook input was valid (no malformed-path errors swallowed).
	if !strings.HasSuffix(root.Path(), ".jsonl") {
		t.Errorf("fixture path looks wrong: %q", root.Path())
	}
}


// CF-549 buildOpencodeLaunchArgs tests --------------------------------------

// TestBuildOpencodeLaunchArgsUsesInlineCWD asserts the fast path: when the
// plugin sends cwd inline (session.created flow), no SQLite lookup runs
// and the launch input carries that cwd verbatim.
func TestBuildOpencodeLaunchArgsUsesInlineCWD(t *testing.T) {
	// Sentinel DB path that does not exist; if we accidentally do the
	// lookup, it would surface the path in a log/error. The fast path
	// must not touch the DB at all.
	t.Setenv(provider.OpenCodeDBEnv, filepath.Join(t.TempDir(), "should-not-be-read.db"))

	in := []byte(`{"session_id":"ses_inline","cwd":"/work/inline","parent_pid":42}`)
	launch, err := buildOpencodeLaunchArgs(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("buildOpencodeLaunchArgs: %v", err)
	}
	if launch.CWD != "/work/inline" {
		t.Errorf("CWD = %q, want %q", launch.CWD, "/work/inline")
	}
	if launch.ParentPID != 42 {
		t.Errorf("ParentPID = %d, want 42", launch.ParentPID)
	}
}

// TestBuildOpencodeLaunchArgsResolvesFromDB asserts the resume path: with
// an empty cwd, the handler reads directory + parent_id from the
// OpenCode SQLite DB and stamps them into the launch input.
func TestBuildOpencodeLaunchArgsResolvesFromDB(t *testing.T) {
	const sid = "ses_resume_lookup"
	const dir = "/work/resumed"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(sid, "", dir)
	t.Setenv(provider.OpenCodeDBEnv, b.Path())

	in := []byte(`{"session_id":"` + sid + `","cwd":"","parent_pid":99}`)
	launch, err := buildOpencodeLaunchArgs(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("buildOpencodeLaunchArgs: %v", err)
	}
	if launch.CWD != dir {
		t.Errorf("CWD = %q, want %q (resolved from DB)", launch.CWD, dir)
	}
	if launch.SessionParentID != "" {
		t.Errorf("SessionParentID = %q, want \"\" for root session", launch.SessionParentID)
	}
	if launch.ParentPID != 99 {
		t.Errorf("ParentPID = %d, want 99 (from input)", launch.ParentPID)
	}
}

// TestBuildOpencodeLaunchArgsResolvesParentIDForSubagent asserts the
// resume path surfaces the SessionParentID so ShouldSpawnForInput can
// later refuse the spawn for a subagent.
func TestBuildOpencodeLaunchArgsResolvesParentIDForSubagent(t *testing.T) {
	const root = "ses_root_for_resume"
	const child = "ses_child_for_resume"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(root, "", "/work/parent")
	b.AddSessionWithDir(child, root, "/work/child")
	t.Setenv(provider.OpenCodeDBEnv, b.Path())

	in := []byte(`{"session_id":"` + child + `","cwd":""}`)
	launch, err := buildOpencodeLaunchArgs(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("buildOpencodeLaunchArgs: %v", err)
	}
	if launch.SessionParentID != root {
		t.Errorf("SessionParentID = %q, want %q (resolved from DB)", launch.SessionParentID, root)
	}
}

// TestBuildOpencodeLaunchArgsDBErrorGraceful asserts that when the
// SQLite DB is unreachable (path points to a missing file), the launch
// still returns successfully with empty cwd/parent_id. Failure to
// resolve must NOT block the spawn.
func TestBuildOpencodeLaunchArgsDBErrorGraceful(t *testing.T) {
	t.Setenv(provider.OpenCodeDBEnv, filepath.Join(t.TempDir(), "nonexistent.db"))

	in := []byte(`{"session_id":"ses_nodb","cwd":""}`)
	launch, err := buildOpencodeLaunchArgs(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("buildOpencodeLaunchArgs returned error; resume must degrade gracefully: %v", err)
	}
	if launch.CWD != "" {
		t.Errorf("CWD = %q, want \"\" (DB missing)", launch.CWD)
	}
}

// TestBuildOpencodeLaunchArgsDBNotFoundGraceful asserts that when the DB
// exists but the session_id is absent, launch still returns successfully
// with empty fields (no error).
func TestBuildOpencodeLaunchArgsDBNotFoundGraceful(t *testing.T) {
	b := opencodetest.NewDB(t) // empty DB, no rows
	t.Setenv(provider.OpenCodeDBEnv, b.Path())

	in := []byte(`{"session_id":"ses_absent","cwd":""}`)
	launch, err := buildOpencodeLaunchArgs(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("buildOpencodeLaunchArgs: %v", err)
	}
	if launch.CWD != "" {
		t.Errorf("CWD = %q, want \"\" (session not in DB)", launch.CWD)
	}
}
