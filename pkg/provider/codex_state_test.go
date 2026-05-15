package provider_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/provider"

	_ "modernc.org/sqlite"
)

// ============================================================================
// StateDBPath
// ============================================================================

func TestStateDBPath_EnvOverride(t *testing.T) {
	codextest.NewFixture(t) // sets CONFAB_CODEX_DIR
	override := filepath.Join(t.TempDir(), "explicit.sqlite")
	t.Setenv(provider.CodexStateDBEnv, override)
	provider.ResetStateDBPathCacheForTest()

	got, err := provider.Codex{}.StateDBPath()
	if err != nil {
		t.Fatalf("StateDBPath: %v", err)
	}
	if got != override {
		t.Fatalf("StateDBPath = %q, want %q", got, override)
	}
}

func TestStateDBPath_PicksHighestNumericSuffix(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{5, 12, 3} {
		path := filepath.Join(dir, "state_"+itoa(n)+".sqlite")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	t.Setenv(provider.CodexStateDirEnv, dir)
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()

	got, err := provider.Codex{}.StateDBPath()
	if err != nil {
		t.Fatalf("StateDBPath: %v", err)
	}
	want := filepath.Join(dir, "state_12.sqlite")
	if got != want {
		t.Fatalf("StateDBPath = %q, want %q", got, want)
	}
}

func TestStateDBPath_MixedNumericAndNonNumeric_NumericWins(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"state_5.sqlite", "state_abc.sqlite"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Setenv(provider.CodexStateDirEnv, dir)
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()

	got, err := provider.Codex{}.StateDBPath()
	if err != nil {
		t.Fatalf("StateDBPath: %v", err)
	}
	if filepath.Base(got) != "state_5.sqlite" {
		t.Fatalf("StateDBPath = %q, want state_5.sqlite (numeric wins over non-numeric)", got)
	}
}

func TestStateDBPath_NoDB_ReturnsErrNotExist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(provider.CodexStateDirEnv, dir)
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()

	_, err := provider.Codex{}.StateDBPath()
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("StateDBPath err = %v, want os.ErrNotExist", err)
	}
}

func TestStateDBPath_OnlyOneDB_ReturnsThatOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state_42.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Setenv(provider.CodexStateDirEnv, dir)
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()

	got, err := provider.Codex{}.StateDBPath()
	if err != nil {
		t.Fatalf("StateDBPath: %v", err)
	}
	if got != path {
		t.Fatalf("StateDBPath = %q, want %q", got, path)
	}
}

func TestStateDBPath_Cached_LaterEnvChangeIgnored(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original.sqlite")
	if err := os.WriteFile(original, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(provider.CodexStateDBEnv, original)
	provider.ResetStateDBPathCacheForTest()

	first, err := provider.Codex{}.StateDBPath()
	if err != nil {
		t.Fatalf("first StateDBPath: %v", err)
	}

	// Change env after first resolution; sync.Once should keep the cached value.
	t.Setenv(provider.CodexStateDBEnv, filepath.Join(dir, "different.sqlite"))

	second, err := provider.Codex{}.StateDBPath()
	if err != nil {
		t.Fatalf("second StateDBPath: %v", err)
	}
	if second != first {
		t.Fatalf("second call = %q, want cached %q", second, first)
	}
}

// ============================================================================
// WalkUpToRoot
// ============================================================================

func TestWalkUpToRoot_RootReturnsItself_NoEdge(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root-uuid").WithSessionMeta("/work", "model")

	got, gotPath, err := provider.Codex{}.WalkUpToRoot(root.ThreadUUID())
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != root.ThreadUUID() {
		t.Fatalf("rootUUID = %q, want %q", got, root.ThreadUUID())
	}
	if gotPath != root.Path() {
		t.Fatalf("rootRolloutPath = %q, want %q", gotPath, root.Path())
	}
}

func TestWalkUpToRoot_DirectChild_ReturnsParent(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root").WithSessionMeta("/work", "m")
	child := f.AddSubagent(root.ThreadUUID(), "child", codextest.SubagentOpts{AgentRole: "planner"}).
		WithSessionMeta("/work", "m")

	got, gotPath, err := provider.Codex{}.WalkUpToRoot(child.ThreadUUID())
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != root.ThreadUUID() {
		t.Fatalf("rootUUID = %q, want %q", got, root.ThreadUUID())
	}
	if gotPath != root.Path() {
		t.Fatalf("rootRolloutPath = %q, want %q", gotPath, root.Path())
	}
}

func TestWalkUpToRoot_Grandchild_WalksToRoot(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root").WithSessionMeta("/", "m")
	child := f.AddSubagent(root.ThreadUUID(), "child", codextest.SubagentOpts{AgentRole: "planner"}).
		WithSessionMeta("/", "m")
	grand := f.AddSubagent(child.ThreadUUID(), "grand", codextest.SubagentOpts{AgentRole: "subplanner"}).
		WithSessionMeta("/", "m")

	got, gotPath, err := provider.Codex{}.WalkUpToRoot(grand.ThreadUUID())
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != root.ThreadUUID() {
		t.Fatalf("rootUUID = %q, want %q", got, root.ThreadUUID())
	}
	if gotPath != root.Path() {
		t.Fatalf("rootRolloutPath = %q, want %q", gotPath, root.Path())
	}
}

func TestWalkUpToRoot_DeepTree_5Levels(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("L0").WithSessionMeta("/", "m")
	parent := root.ThreadUUID()
	var leaf string
	for i := 1; i <= 5; i++ {
		id := "L" + itoa(i)
		f.AddSubagent(parent, id, codextest.SubagentOpts{AgentRole: "r"}).WithSessionMeta("/", "m")
		parent = id
		leaf = id
	}

	got, _, err := provider.Codex{}.WalkUpToRoot(leaf)
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != root.ThreadUUID() {
		t.Fatalf("rootUUID = %q, want %q", got, root.ThreadUUID())
	}
}

func TestWalkUpToRoot_Cycle_ReturnsError(t *testing.T) {
	f := codextest.NewFixture(t)
	// Cycle: A → B → A. Manually insert via DB() so the fixture builder
	// doesn't reject the redundant edge.
	f.AddRoot("A").WithSessionMeta("/", "m")
	f.AddSubagent("A", "B", codextest.SubagentOpts{AgentRole: "r"}).WithSessionMeta("/", "m")
	// Add the cycling edge B → A. (B is now both child of A and parent of A.)
	if _, err := f.DB().Exec(
		`INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id, status) VALUES (?, ?, 'completed')`,
		"B", "A",
	); err != nil {
		t.Fatalf("insert cycle edge: %v", err)
	}

	_, _, err := provider.Codex{}.WalkUpToRoot("A")
	if err == nil {
		t.Fatalf("WalkUpToRoot: expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("WalkUpToRoot err = %v, want cycle error", err)
	}
}

func TestWalkUpToRoot_MissingThreadInDB_ReturnsFiringThread(t *testing.T) {
	codextest.NewFixture(t) // empty DB

	got, gotPath, err := provider.Codex{}.WalkUpToRoot("unknown-thread")
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != "unknown-thread" {
		t.Fatalf("rootUUID = %q, want unknown-thread (graceful fallback)", got)
	}
	if gotPath != "" {
		t.Fatalf("rootRolloutPath = %q, want \"\" (thread not in DB)", gotPath)
	}
}

func TestWalkUpToRoot_EdgeAppearsAfterRetry_Succeeds(t *testing.T) {
	tightenRetry(t, 8, 25*time.Millisecond)

	f := codextest.NewFixture(t)
	root := f.AddRoot("root").WithSessionMeta("/", "m")
	// Insert the threads row for the child immediately (Codex normally
	// writes this before firing the SessionStart hook), but delay the
	// thread_spawn_edges insert by ~60ms (covers ~3 retry attempts).
	if _, err := f.DB().Exec(
		`INSERT INTO threads (id, rollout_path, thread_source) VALUES (?, ?, 'agent')`,
		"child", filepath.Join(f.SessionsDir, "child.jsonl"),
	); err != nil {
		t.Fatalf("insert thread: %v", err)
	}
	f.InsertEdgeLater(root.ThreadUUID(), "child", 60*time.Millisecond)

	got, _, err := provider.Codex{}.WalkUpToRoot("child")
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != root.ThreadUUID() {
		t.Fatalf("rootUUID = %q, want %q (edge appeared mid-retry)", got, root.ThreadUUID())
	}
}

func TestWalkUpToRoot_EdgeNeverAppears_FallsBackToFiringThread(t *testing.T) {
	tightenRetry(t, 5, 10*time.Millisecond) // ~40ms total budget for speed

	codextest.NewFixture(t)
	// "child" has no row and no edge — emulates a hook firing for a thread
	// whose Codex SQLite state hasn't been written yet (extreme edge race).

	start := time.Now()
	got, gotPath, err := provider.Codex{}.WalkUpToRoot("orphan-child")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != "orphan-child" {
		t.Fatalf("rootUUID = %q, want orphan-child (fallback to firing thread)", got)
	}
	if gotPath != "" {
		t.Fatalf("rootRolloutPath = %q, want \"\"", gotPath)
	}
	// 4 sleeps × 10ms ≈ 40ms minimum. Cap upper bound generously to
	// avoid flakes on busy CI.
	if elapsed < 30*time.Millisecond {
		t.Fatalf("WalkUpToRoot returned too fast (%s), expected at least ~40ms of retry budget", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("WalkUpToRoot took %s, way over the configured retry budget", elapsed)
	}
}

func TestWalkUpToRoot_DBUnavailable_ReturnsFiringThread(t *testing.T) {
	// Point the env at a path that doesn't exist; no fixture.
	t.Setenv(provider.CodexStateDirEnv, filepath.Join(t.TempDir(), "no-such-codex"))
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()

	got, gotPath, err := provider.Codex{}.WalkUpToRoot("some-uuid")
	if err != nil {
		t.Fatalf("WalkUpToRoot: %v", err)
	}
	if got != "some-uuid" {
		t.Fatalf("rootUUID = %q, want some-uuid", got)
	}
	if gotPath != "" {
		t.Fatalf("rootRolloutPath = %q, want \"\"", gotPath)
	}
}

// ============================================================================
// ListSubtree
// ============================================================================

func TestListSubtree_EmptyDB_ReturnsNil(t *testing.T) {
	codextest.NewFixture(t)

	got, err := provider.Codex{}.ListSubtree("any-root")
	if err != nil {
		t.Fatalf("ListSubtree: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListSubtree = %v, want empty", got)
	}
}

func TestListSubtree_RootWithNoChildren_ReturnsEmpty(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root").WithSessionMeta("/", "m")

	got, err := provider.Codex{}.ListSubtree(root.ThreadUUID())
	if err != nil {
		t.Fatalf("ListSubtree: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListSubtree = %v, want empty (root has no descendants)", got)
	}
}

func TestListSubtree_SingleChild_ReturnsOneRow_WithImmediateParent(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root").WithSessionMeta("/", "m")
	child := f.AddSubagent(root.ThreadUUID(), "child",
		codextest.SubagentOpts{AgentPath: "~/agent.md", AgentRole: "planner", AgentNickname: "Planny"},
	).WithSessionMeta("/work", "gpt-5")

	got, err := provider.Codex{}.ListSubtree(root.ThreadUUID())
	if err != nil {
		t.Fatalf("ListSubtree: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListSubtree returned %d rows, want 1", len(got))
	}
	r := got[0]
	if r.ThreadUUID != child.ThreadUUID() {
		t.Errorf("ThreadUUID = %q, want %q", r.ThreadUUID, child.ThreadUUID())
	}
	if r.ParentThreadUUID != root.ThreadUUID() {
		t.Errorf("ParentThreadUUID = %q, want %q", r.ParentThreadUUID, root.ThreadUUID())
	}
	if r.RolloutPath != child.Path() {
		t.Errorf("RolloutPath = %q, want %q", r.RolloutPath, child.Path())
	}
	if r.AgentRole != "planner" {
		t.Errorf("AgentRole = %q, want planner", r.AgentRole)
	}
	if r.AgentNickname != "Planny" {
		t.Errorf("AgentNickname = %q, want Planny", r.AgentNickname)
	}
	if r.ThreadSource != "agent" {
		t.Errorf("ThreadSource = %q, want agent", r.ThreadSource)
	}
}

func TestListSubtree_TwoSiblings_ReturnsBothWithSameParent(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("root").WithSessionMeta("/", "m")
	f.AddSubagent(root.ThreadUUID(), "child-A", codextest.SubagentOpts{AgentRole: "a"}).WithSessionMeta("/", "m")
	f.AddSubagent(root.ThreadUUID(), "child-B", codextest.SubagentOpts{AgentRole: "b"}).WithSessionMeta("/", "m")

	got, err := provider.Codex{}.ListSubtree(root.ThreadUUID())
	if err != nil {
		t.Fatalf("ListSubtree: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSubtree returned %d rows, want 2", len(got))
	}
	for _, r := range got {
		if r.ParentThreadUUID != root.ThreadUUID() {
			t.Errorf("row %q parent = %q, want %q", r.ThreadUUID, r.ParentThreadUUID, root.ThreadUUID())
		}
	}
}

func TestListSubtree_3LevelTree_PreservesImmediateParents(t *testing.T) {
	f := codextest.NewFixture(t)
	root := f.AddRoot("R").WithSessionMeta("/", "m")
	f.AddSubagent("R", "B", codextest.SubagentOpts{AgentRole: "b"}).WithSessionMeta("/", "m")
	f.AddSubagent("B", "C", codextest.SubagentOpts{AgentRole: "c"}).WithSessionMeta("/", "m")

	got, err := provider.Codex{}.ListSubtree(root.ThreadUUID())
	if err != nil {
		t.Fatalf("ListSubtree: %v", err)
	}
	parents := map[string]string{}
	for _, r := range got {
		parents[r.ThreadUUID] = r.ParentThreadUUID
	}
	if parents["B"] != "R" {
		t.Errorf("B.parent = %q, want R", parents["B"])
	}
	if parents["C"] != "B" {
		t.Errorf("C.parent = %q, want B (immediate parent, not root)", parents["C"])
	}
}

func TestListSubtree_BushyTree_AllDescendants(t *testing.T) {
	f := codextest.NewFixture(t)
	f.AddRoot("R").WithSessionMeta("/", "m")
	f.AddSubagent("R", "A", codextest.SubagentOpts{AgentRole: "a"}).WithSessionMeta("/", "m")
	f.AddSubagent("R", "B", codextest.SubagentOpts{AgentRole: "b"}).WithSessionMeta("/", "m")
	f.AddSubagent("R", "C", codextest.SubagentOpts{AgentRole: "c"}).WithSessionMeta("/", "m")
	f.AddSubagent("A", "A1", codextest.SubagentOpts{AgentRole: "a1"}).WithSessionMeta("/", "m")
	f.AddSubagent("B", "B1", codextest.SubagentOpts{AgentRole: "b1"}).WithSessionMeta("/", "m")

	got, err := provider.Codex{}.ListSubtree("R")
	if err != nil {
		t.Fatalf("ListSubtree: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("ListSubtree returned %d rows, want 5 (A, B, C, A1, B1)", len(got))
	}
	parents := map[string]string{}
	for _, r := range got {
		parents[r.ThreadUUID] = r.ParentThreadUUID
	}
	for child, wantParent := range map[string]string{"A": "R", "B": "R", "C": "R", "A1": "A", "B1": "B"} {
		if got := parents[child]; got != wantParent {
			t.Errorf("%s.parent = %q, want %q", child, got, wantParent)
		}
	}
}

func TestListSubtree_DBOpenFailure_ReturnsNilNoError(t *testing.T) {
	t.Setenv(provider.CodexStateDirEnv, filepath.Join(t.TempDir(), "no-such-codex"))
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()

	got, err := provider.Codex{}.ListSubtree("anything")
	if err != nil {
		t.Fatalf("ListSubtree err = %v, want nil (graceful degradation)", err)
	}
	if got != nil {
		t.Fatalf("ListSubtree = %v, want nil", got)
	}
}

func TestListSubtree_SchemaMismatch_ReturnsNilNoError(t *testing.T) {
	// Point at a SQLite file that exists but lacks our schema.
	dbPath := filepath.Join(t.TempDir(), "wrong.sqlite")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open bad db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated (x INT)`); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	db.Close()
	t.Setenv(provider.CodexStateDBEnv, dbPath)
	provider.ResetStateDBPathCacheForTest()

	got, err := provider.Codex{}.ListSubtree("anything")
	if err != nil {
		t.Fatalf("ListSubtree err = %v, want nil (graceful degradation on schema mismatch)", err)
	}
	if got != nil {
		t.Fatalf("ListSubtree = %v, want nil", got)
	}
}

// ============================================================================
// helpers
// ============================================================================

// tightenRetry shrinks WalkUpToRoot's retry budget for the duration of a
// test so retry-window tests don't take their production-default 250ms.
func tightenRetry(t *testing.T, attempts int, backoff time.Duration) {
	t.Helper()
	provider.SetWalkUpRetryForTest(attempts, backoff)
	t.Cleanup(func() { provider.ResetWalkUpRetryForTest() })
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
