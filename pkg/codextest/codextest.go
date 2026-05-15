// Package codextest provides a reusable Codex SQLite + rollout-files
// fixture builder for tests across the codebase (pkg/provider, pkg/sync,
// pkg/daemon, cmd). It writes a minimal version of Codex's state schema
// (just the columns `threads` + `thread_spawn_edges` that the CLI queries)
// to a fresh tmp directory, exposes builder methods for adding root and
// subagent rollouts, and points the CLI's Codex-state lookups at the
// fixture via CONFAB_CODEX_DIR.
//
// The fixture's schema intentionally mirrors only what the CLI reads —
// Codex's real schema is larger and evolves; tracking it column-for-column
// in tests would couple us to upstream changes that don't affect us.
package codextest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

// canonicalUUIDPattern matches Codex's canonical UUID format used inside
// rollout filenames. Thread IDs that match this pattern are reused as the
// filename UUID so FindSessionByID(partialThreadID) can locate the rollout.
var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Fixture is a temporary Codex directory layout with a state DB and a
// sessions tree, scoped to a single test. The CONFAB_CODEX_DIR env var
// points at the fixture for the duration of the test.
type Fixture struct {
	Dir         string
	StateDBPath string
	SessionsDir string

	t  *testing.T
	db *sql.DB

	mu   sync.Mutex
	seen map[string]bool // threadUUID → already added (defensive against duplicates)
}

// schemaDDL is the subset of Codex's schema we depend on. The real
// `threads` table has many more columns; we only model the ones the CLI
// queries via ListSubtree and WalkUpToRoot.
const schemaDDL = `
CREATE TABLE threads (
    id              TEXT PRIMARY KEY,
    rollout_path    TEXT NOT NULL,
    cwd             TEXT,
    model           TEXT,
    source          TEXT,
    thread_source   TEXT,
    agent_path      TEXT,
    agent_role      TEXT,
    agent_nickname  TEXT
);

CREATE TABLE thread_spawn_edges (
    parent_thread_id TEXT NOT NULL,
    child_thread_id  TEXT NOT NULL PRIMARY KEY,
    status           TEXT NOT NULL DEFAULT 'completed'
);
`

// NewFixture creates a tmp Codex directory, initializes the state DB with
// the minimal schema, sets CONFAB_CODEX_DIR for the test, and resets the
// provider's cached state-DB path so the fixture is picked up immediately.
//
// All resources are cleaned up via t.Cleanup.
func NewFixture(t *testing.T) *Fixture {
	t.Helper()
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("codextest: mkdir sessions: %v", err)
	}
	// state_99.sqlite — high suffix so the highest-numeric picker would
	// prefer it even if a stray state_0.sqlite ever appeared in tmp.
	dbPath := filepath.Join(dir, "state_99.sqlite")

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("codextest: open db: %v", err)
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		t.Fatalf("codextest: apply schema: %v", err)
	}

	t.Setenv(provider.CodexStateDirEnv, dir)
	// If a prior test set CONFAB_CODEX_STATE_DB, unset for this fixture so
	// our glob discovery is exercised. Tests that want the env-override
	// path set it explicitly via t.Setenv after NewFixture.
	t.Setenv(provider.CodexStateDBEnv, "")
	provider.ResetStateDBPathCacheForTest()

	f := &Fixture{
		Dir:         dir,
		StateDBPath: dbPath,
		SessionsDir: sessionsDir,
		t:           t,
		db:          db,
		seen:        make(map[string]bool),
	}
	t.Cleanup(func() {
		db.Close()
		provider.ResetStateDBPathCacheForTest()
	})
	return f
}

// DB returns the underlying *sql.DB so tests can insert custom rows that
// don't fit the builder pattern. Use sparingly.
func (f *Fixture) DB() *sql.DB { return f.db }

// SubagentOpts customizes the agent_* metadata written to threads. Empty
// strings are stored as NULL in SQLite.
type SubagentOpts struct {
	AgentPath     string
	AgentRole     string
	AgentNickname string
	ThreadSource  string // defaults to "agent"
}

// AddRoot inserts a row into `threads` representing a user-initiated
// (root) thread with `thread_source='user'`, and creates an empty rollout
// JSONL file under SessionsDir. Returns a RolloutBuilder for chaining.
func (f *Fixture) AddRoot(threadUUID string) *RolloutBuilder {
	f.t.Helper()
	return f.addThread(threadUUID, "", SubagentOpts{ThreadSource: "user"})
}

// AddSubagent inserts a thread row with agent_* metadata and an edge from
// parentUUID → threadUUID in thread_spawn_edges. The rollout JSONL file
// is created as empty; chain WithSessionMeta/WithLines to populate it.
func (f *Fixture) AddSubagent(parentUUID, threadUUID string, opts SubagentOpts) *RolloutBuilder {
	f.t.Helper()
	if opts.ThreadSource == "" {
		opts.ThreadSource = "agent"
	}
	return f.addThread(threadUUID, parentUUID, opts)
}

// AddSubagentNoEdge inserts a subagent-tagged thread row WITHOUT a parent
// edge. Used by edge-race tests to model the window where Codex has
// committed the new subagent's `threads` row but not yet committed the
// matching `thread_spawn_edges` row. Pair with InsertEdgeLater to add the
// edge after a delay (or omit to model "edge never appears").
func (f *Fixture) AddSubagentNoEdge(_ *testing.T, threadUUID string, opts SubagentOpts) *RolloutBuilder {
	f.t.Helper()
	if opts.ThreadSource == "" {
		opts.ThreadSource = "agent"
	}
	return f.addThread(threadUUID, "", opts)
}

func (f *Fixture) addThread(threadUUID, parentUUID string, opts SubagentOpts) *RolloutBuilder {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen[threadUUID] {
		f.t.Fatalf("codextest: thread %q already added", threadUUID)
	}
	f.seen[threadUUID] = true

	rolloutPath := f.rolloutPathFor(threadUUID)
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		f.t.Fatalf("codextest: mkdir rollout dir: %v", err)
	}
	if err := os.WriteFile(rolloutPath, nil, 0o600); err != nil {
		f.t.Fatalf("codextest: create rollout file: %v", err)
	}

	_, err := f.db.Exec(`
        INSERT INTO threads (id, rollout_path, cwd, model, source, thread_source, agent_path, agent_role, agent_nickname)
        VALUES (?, ?, '', '', '', ?, ?, ?, ?)`,
		threadUUID, rolloutPath, opts.ThreadSource,
		nullIfEmpty(opts.AgentPath), nullIfEmpty(opts.AgentRole), nullIfEmpty(opts.AgentNickname))
	if err != nil {
		f.t.Fatalf("codextest: insert thread %s: %v", threadUUID, err)
	}
	if parentUUID != "" {
		_, err := f.db.Exec(
			`INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id, status) VALUES (?, ?, 'completed')`,
			parentUUID, threadUUID,
		)
		if err != nil {
			f.t.Fatalf("codextest: insert edge %s→%s: %v", parentUUID, threadUUID, err)
		}
	}
	return &RolloutBuilder{
		f:           f,
		threadUUID:  threadUUID,
		rolloutPath: rolloutPath,
		opts:        opts,
	}
}

// rolloutPathFor returns the absolute on-disk path the fixture uses for a
// thread's rollout JSONL. Mirrors Codex's real layout
// (sessions/<yyyy>/<MM>/<dd>/rollout-...<uuid>.jsonl) so the CLI's path
// validation (which checks the file is under SessionsDir AND that the
// filename ends in a canonical UUID) is exercised.
//
// When threadUUID is itself a canonical UUID, it is reused as the
// filename's embedded UUID — this lets tests exercise filename-based
// lookups (e.g., FindSessionByID, which scans filenames). When threadUUID
// is a friendly name like "root-1", a fresh UUID is generated so the
// production regex still accepts the filename.
func (f *Fixture) rolloutPathFor(threadUUID string) string {
	now := time.Now().UTC()
	dateDir := filepath.Join(
		f.SessionsDir,
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		fmt.Sprintf("%02d", now.Day()),
	)
	fileUUID := threadUUID
	if !canonicalUUIDPattern.MatchString(fileUUID) {
		fileUUID = uuid.New().String()
	}
	name := fmt.Sprintf("rollout-%s-%s.jsonl", now.Format("2006-01-02T15-04-05"), fileUUID)
	return filepath.Join(dateDir, name)
}

// DeleteRolloutFile removes the rollout JSONL but leaves the threads row
// in place, so tests can exercise the "DB references a missing file"
// branch inside ListSubtree's verification.
func (f *Fixture) DeleteRolloutFile(threadUUID string) {
	f.t.Helper()
	var p string
	if err := f.db.QueryRow(`SELECT rollout_path FROM threads WHERE id = ?`, threadUUID).Scan(&p); err != nil {
		f.t.Fatalf("codextest: lookup rollout_path for %s: %v", threadUUID, err)
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		f.t.Fatalf("codextest: remove rollout file: %v", err)
	}
}

// InsertEdgeLater schedules a parent→child edge insert after the given
// delay. Used by WalkUpToRoot retry tests to simulate the spawn-vs-edge
// race where the hook fires before Codex commits the edge.
//
// The goroutine inherits the fixture's t and will t.Errorf on failure
// rather than crashing the test runner.
func (f *Fixture) InsertEdgeLater(parentUUID, childUUID string, delay time.Duration) {
	f.t.Helper()
	go func() {
		time.Sleep(delay)
		_, err := f.db.Exec(
			`INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id, status) VALUES (?, ?, 'completed')`,
			parentUUID, childUUID,
		)
		if err != nil {
			f.t.Errorf("codextest: delayed-insert edge %s→%s: %v", parentUUID, childUUID, err)
		}
	}()
}

// RolloutBuilder is a small fluent helper for populating a rollout JSONL
// after the row has been registered in the threads table.
type RolloutBuilder struct {
	f           *Fixture
	threadUUID  string
	rolloutPath string
	opts        SubagentOpts
	lines       []string
}

// ThreadUUID returns the rollout's owning thread ID.
func (b *RolloutBuilder) ThreadUUID() string { return b.threadUUID }

// Path returns the absolute rollout file path on disk.
func (b *RolloutBuilder) Path() string { return b.rolloutPath }

// WithSessionMeta writes a `session_meta` JSONL line at the start of the
// rollout. The CLI's IsUserSession / agent-rollout filtering reads this
// line to verify the rollout's role.
func (b *RolloutBuilder) WithSessionMeta(cwd, model string) *RolloutBuilder {
	b.f.t.Helper()
	meta := map[string]any{
		"id":             b.threadUUID,
		"cwd":            cwd,
		"model":          model,
		"source":         "cli",
		"thread_source":  b.opts.ThreadSource,
		"agent_path":     b.opts.AgentPath,
		"agent_role":     b.opts.AgentRole,
		"agent_nickname": b.opts.AgentNickname,
	}
	line := jsonLine("session_meta", meta)
	b.lines = append(b.lines, line)
	b.flush()
	return b
}

// WithUserMessage appends an event_msg/user_message line. Used to set up
// rollouts whose first_user_message extraction is being tested.
func (b *RolloutBuilder) WithUserMessage(msg string) *RolloutBuilder {
	b.f.t.Helper()
	payload := map[string]any{"type": "user_message", "message": msg}
	line := jsonLine("event_msg", payload)
	b.lines = append(b.lines, line)
	b.flush()
	return b
}

// WithRawLine appends a raw JSON line verbatim. Use for testing malformed
// session_meta payloads or unexpected line types.
func (b *RolloutBuilder) WithRawLine(line string) *RolloutBuilder {
	b.f.t.Helper()
	b.lines = append(b.lines, strings.TrimRight(line, "\n"))
	b.flush()
	return b
}

func (b *RolloutBuilder) flush() {
	b.f.t.Helper()
	content := strings.Join(b.lines, "\n") + "\n"
	if err := os.WriteFile(b.rolloutPath, []byte(content), 0o600); err != nil {
		b.f.t.Fatalf("codextest: write rollout: %v", err)
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonLine(typ string, payload map[string]any) string {
	return fmt.Sprintf(`{"type":%q,"payload":%s}`, typ, mustJSON(payload))
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("codextest: marshal: %v", err))
	}
	return string(b)
}
