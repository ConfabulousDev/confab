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
// TestCodexHook_DoesNotInstallClaudeSkills guards against a regression
// where Codex SessionStart routed through the unified handler would
// silently call RunAnnouncements() and write ~/.claude/skills/{til,retro}/
// for Codex-only users.
func TestCodexHook_DoesNotInstallClaudeSkills(t *testing.T) {
	origSpawn := spawnDaemonFunc
	defer func() { spawnDaemonFunc = origSpawn }()
	spawnDaemonFunc = func(launch *daemonLaunchInput) error { return nil }

	fixture, tmpHome := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-no-skills")

	in := codexHookInputJSON(t, root.ThreadUUID(), root.Path())
	if err := runCodexSessionStart(t, in); err != nil {
		t.Fatalf("hook: %v", err)
	}

	for _, skill := range []string{"til", "retro"} {
		path := filepath.Join(tmpHome, ".claude", "skills", skill)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("Codex SessionStart leaked Claude skill into %s", path)
		}
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
