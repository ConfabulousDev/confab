package cmd

import (
	"bytes"
	"encoding/json"
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
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-aaa")
	rootID := root.ThreadUUID()

	var captured *types.CodexHookInput
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		captured = h
		return nil
	}

	in := codexHookInputJSON(t, rootID, root.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called for a root")
	}
	if captured.SessionID != rootID {
		t.Errorf("session = %q, want root %q", captured.SessionID, rootID)
	}
	if captured.TranscriptPath != root.Path() {
		t.Errorf("transcript = %q, want %q", captured.TranscriptPath, root.Path())
	}
}

func TestCodexHook_FiringSessionIsDirectChild_WalksUpToRoot_SpawnsDaemonForRoot(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-bbb")
	child := fixture.AddSubagent(root.ThreadUUID(), "child-bbb",
		codextest.SubagentOpts{AgentRole: "worker"})

	var captured *types.CodexHookInput
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		captured = h
		return nil
	}

	in := codexHookInputJSON(t, child.ThreadUUID(), child.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called")
	}
	if captured.SessionID != root.ThreadUUID() {
		t.Errorf("daemon spawned for %q, want root %q", captured.SessionID, root.ThreadUUID())
	}
	if captured.TranscriptPath != root.Path() {
		t.Errorf("transcript path not rewritten: got %q, want %q",
			captured.TranscriptPath, root.Path())
	}
}

func TestCodexHook_FiringSessionIsGrandchild_WalksUpToTopMostRoot(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-ccc")
	child := fixture.AddSubagent(root.ThreadUUID(), "child-ccc",
		codextest.SubagentOpts{AgentRole: "mid"})
	grand := fixture.AddSubagent(child.ThreadUUID(), "grand-ccc",
		codextest.SubagentOpts{AgentRole: "leaf"})

	var captured *types.CodexHookInput
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		captured = h
		return nil
	}

	in := codexHookInputJSON(t, grand.ThreadUUID(), grand.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn to be called")
	}
	if captured.SessionID != root.ThreadUUID() {
		t.Errorf("daemon spawned for %q, want top-most root %q",
			captured.SessionID, root.ThreadUUID())
	}
	if captured.TranscriptPath != root.Path() {
		t.Errorf("transcript path = %q, want top-most root %q",
			captured.TranscriptPath, root.Path())
	}
}

func TestCodexHook_RootDaemonAlreadyRunning_HookExitsWithoutSpawning(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

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
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		spawnCalled = true
		return nil
	}

	in := codexHookInputJSON(t, child.ThreadUUID(), child.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if spawnCalled {
		t.Error("expected hook to be a no-op when root daemon is already running")
	}
}

func TestCodexHook_DaemonStateExistsButDaemonDead_CleansStaleStateAndSpawns(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-eee")

	// Stale state pointing at an obviously-dead PID.
	state := daemon.NewStateForProvider(provider.NameCodex, root.ThreadUUID(),
		root.Path(), "/work", 0)
	state.PID = 999999
	if err := state.Save(); err != nil {
		t.Fatalf("save stale state: %v", err)
	}

	var captured *types.CodexHookInput
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		captured = h
		return nil
	}

	in := codexHookInputJSON(t, root.ThreadUUID(), root.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn when previous daemon is dead")
	}
	if captured.SessionID != root.ThreadUUID() {
		t.Errorf("session = %q, want %q", captured.SessionID, root.ThreadUUID())
	}
}

func TestCodexHook_EdgeRaceWithRetry_EdgeAppearsMidWait_RoutesCorrectly(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

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

	var captured *types.CodexHookInput
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		captured = h
		return nil
	}

	in := codexHookInputJSON(t, child.ThreadUUID(), child.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn after retry")
	}
	if captured.SessionID != root.ThreadUUID() {
		t.Errorf("session = %q, want root %q after edge race resolved",
			captured.SessionID, root.ThreadUUID())
	}
}

func TestCodexHook_EdgeRaceExhausted_TreatsFiringSessionAsRoot(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

	provider.SetWalkUpRetryForTest(2, 5*time.Millisecond)
	defer provider.ResetWalkUpRetryForTest()

	fixture, _ := setupCodexHookEnv(t)
	// Thread row exists but no parent edge will ever appear.
	orphan := fixture.AddSubagentNoEdge(t, "orphan-ggg",
		codextest.SubagentOpts{AgentRole: "orphan", ThreadSource: "agent"})

	var captured *types.CodexHookInput
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		captured = h
		return nil
	}

	in := codexHookInputJSON(t, orphan.ThreadUUID(), orphan.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn even when retries exhaust")
	}
	if captured.SessionID != orphan.ThreadUUID() {
		t.Errorf("session = %q, want firing thread %q (treated as root after retry exhaustion)",
			captured.SessionID, orphan.ThreadUUID())
	}
	if captured.TranscriptPath != orphan.Path() {
		t.Errorf("transcript = %q, want %q", captured.TranscriptPath, orphan.Path())
	}
}

func TestCodexHook_StateDBAbsent_DegradesToFiringSessionAsRoot(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

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

	var captured *types.CodexHookInput
	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		captured = h
		return nil
	}

	sessionID := "11111111-1111-1111-1111-111111111111"
	// rollout file is empty → ReadSessionInfo returns the default (IsUserSession==true);
	// daemon spawn proceeds. With no state DB present, WalkUpToRoot degrades to
	// "firing session is its own root".
	if err := codexSessionStartFromReader(bytes.NewReader(
		codexHookInputJSON(t, sessionID, rolloutPath))); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if captured == nil {
		t.Fatal("expected spawn even with no state DB")
	}
	if captured.SessionID != sessionID {
		t.Errorf("session = %q, want firing UUID %q", captured.SessionID, sessionID)
	}
}

// (sanity) Confirm the hook handler emits a response on stdout even when
// the resolved root's daemon is already running. We don't assert on the
// response body (the Codex hook is fire-and-forget), only that no panic
// or hang occurs.
func TestCodexHook_RespondsWithoutPanic_WhenNoSpawn(t *testing.T) {
	origSpawn := spawnCodexDaemonFunc
	defer func() { spawnCodexDaemonFunc = origSpawn }()

	fixture, _ := setupCodexHookEnv(t)
	root := fixture.AddRoot("root-hhh")

	state := daemon.NewStateForProvider(provider.NameCodex, root.ThreadUUID(),
		root.Path(), "/work", 0)
	state.PID = os.Getpid()
	if err := state.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	spawnCodexDaemonFunc = func(h *types.CodexHookInput) error {
		t.Fatal("should not spawn when daemon already running")
		return nil
	}

	in := codexHookInputJSON(t, root.ThreadUUID(), root.Path())
	if err := codexSessionStartFromReader(bytes.NewReader(in)); err != nil {
		t.Fatalf("hook: %v", err)
	}
	// Sanity: hook input was valid (no malformed-path errors swallowed).
	if !strings.HasSuffix(root.Path(), ".jsonl") {
		t.Errorf("fixture path looks wrong: %q", root.Path())
	}
}
