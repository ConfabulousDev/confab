package provider

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"

	_ "modernc.org/sqlite"
)

// CodexStateDBEnv overrides automatic state DB discovery. When set, points
// directly at a state_N.sqlite file (or any SQLite file with the expected
// schema). Used by tests; can also be set by power users debugging Codex
// state inconsistencies.
const CodexStateDBEnv = "CONFAB_CODEX_STATE_DB"

// walkUpRetryAttempts and walkUpRetryBackoff bound the spawn-vs-edge race
// retry inside WalkUpToRoot. Exported as package-level vars so tests can
// shrink the timing without affecting production. Production defaults
// give a ~250ms ceiling.
var (
	walkUpRetryAttempts = 5
	walkUpRetryBackoff  = 50 * time.Millisecond
)

// maxWalkDepth bounds parent-chain traversal to defend against malformed
// edges. Real Codex trees are shallow (1-3 levels in practice).
const maxWalkDepth = 16

// busyTimeoutMs is the SQLite busy_timeout pragma value, in milliseconds.
// Codex actively writes to its state DB; a 2-second wait covers any
// concurrent write transaction.
const busyTimeoutMs = 2000

// CodexThreadRow describes a single Codex thread as recorded in the local
// state DB's `threads` table. ParentThreadUUID is the immediate parent
// recorded in `thread_spawn_edges`, or "" for a root thread.
type CodexThreadRow struct {
	ThreadUUID       string
	ParentThreadUUID string
	RolloutPath      string
	CWD              string
	Model            string
	Source           string
	ThreadSource     string
	AgentPath        string
	AgentRole        string
	AgentNickname    string
}

var (
	stateDBPathOnce  sync.Once
	stateDBPathCache string
	stateDBPathErr   error
)

// ResetStateDBPathCacheForTest clears the cached state DB path so the next
// StateDBPath call re-evaluates the env var and glob. Production code
// resolves the path once per process; tests need to swap fixtures between
// cases. Lives in non-test code so cross-package tests (sync, daemon, cmd)
// can call it.
func ResetStateDBPathCacheForTest() {
	stateDBPathOnce = sync.Once{}
	stateDBPathCache = ""
	stateDBPathErr = nil
}

// SetWalkUpRetryForTest shrinks WalkUpToRoot's retry budget for tests that
// would otherwise wait for the full production timeout. Pair with
// ResetWalkUpRetryForTest in t.Cleanup.
func SetWalkUpRetryForTest(attempts int, backoff time.Duration) {
	walkUpRetryAttempts = attempts
	walkUpRetryBackoff = backoff
}

// ResetWalkUpRetryForTest restores production retry defaults.
func ResetWalkUpRetryForTest() {
	walkUpRetryAttempts = 5
	walkUpRetryBackoff = 50 * time.Millisecond
}

// StateDBPath resolves the path to Codex's local state SQLite DB. Resolution
// order:
//  1. CONFAB_CODEX_STATE_DB env var (escape hatch for tests/debugging).
//  2. Glob `<StateDir>/state_*.sqlite`, parse the integer suffix between
//     `_` and `.sqlite`, return the entry with the highest numeric suffix.
//     If suffixes don't parse as integers, falls back to alphabetical max.
//  3. Returns os.ErrNotExist if no candidate file exists.
//
// The result is cached for the lifetime of the process via sync.Once.
func (p Codex) StateDBPath() (string, error) {
	stateDBPathOnce.Do(func() {
		stateDBPathCache, stateDBPathErr = p.resolveStateDBPath()
	})
	return stateDBPathCache, stateDBPathErr
}

func (p Codex) resolveStateDBPath() (string, error) {
	if envPath := os.Getenv(CodexStateDBEnv); envPath != "" {
		return envPath, nil
	}
	stateDir, err := p.StateDir()
	if err != nil {
		return "", err
	}
	pattern := filepath.Join(stateDir, "state_*.sqlite")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob %s: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", os.ErrNotExist
	}
	return pickHighestStateDB(matches), nil
}

var statePathSuffix = regexp.MustCompile(`state_([0-9]+)\.sqlite$`)

// pickHighestStateDB chooses the candidate file with the highest numeric
// suffix. If no candidate has a numeric suffix, falls back to lexical max.
// Ties on suffix are broken alphabetically.
func pickHighestStateDB(candidates []string) string {
	type scored struct {
		path string
		n    int
		ok   bool
	}
	scoredAll := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		base := filepath.Base(c)
		m := statePathSuffix.FindStringSubmatch(base)
		s := scored{path: c}
		if m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				s.n = n
				s.ok = true
			}
		}
		scoredAll = append(scoredAll, s)
	}
	sort.Slice(scoredAll, func(i, j int) bool {
		// numeric-suffix entries come before non-numeric; among numerics,
		// higher n wins; among non-numerics, lexical max wins.
		a, b := scoredAll[i], scoredAll[j]
		if a.ok != b.ok {
			return a.ok && !b.ok
		}
		if a.ok && b.ok {
			if a.n != b.n {
				return a.n > b.n
			}
			return a.path > b.path
		}
		return a.path > b.path
	})
	return scoredAll[0].path
}

// openStateDB opens the resolved state DB in read-only mode with a busy
// timeout. Returns (nil, nil) if the DB doesn't exist on disk — callers
// should treat that as "no information" rather than an error.
func (p Codex) openStateDB() (*sql.DB, error) {
	path, err := p.StateDBPath()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(%d)", url.PathEscape(path), busyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open codex state db: %w", err)
	}
	return db, nil
}

// WalkUpToRoot walks the parent_thread_id chain in `thread_spawn_edges`
// starting from threadUUID until it reaches a thread with no parent edge.
// Returns the top-most root's UUID + that root's rollout_path.
//
// If threadUUID is already a root (no parent edge), returns it unchanged.
// If the DB is unavailable or the thread isn't in the DB at all, returns
// (threadUUID, "", nil) — graceful degradation so callers don't need to
// branch on transient errors.
//
// Built-in retry: the FIRST parent lookup retries up to walkUpRetryAttempts
// times with walkUpRetryBackoff between attempts. This absorbs the
// spawn-vs-edge race where Codex fires the SessionStart hook for a fresh
// subagent before the `thread_spawn_edges` row has been committed.
//
// The retry only fires on the first hop; subsequent hops (grandchild → child
// → root walks) assume the chain is already stable in Codex's DB.
//
// Logs the retry-attempt count + wall time at info level so we can later
// tune the retry budget against observed race timing.
func (p Codex) WalkUpToRoot(threadUUID string) (rootUUID, rootRolloutPath string, err error) {
	db, err := p.openStateDB()
	if err != nil || db == nil {
		return threadUUID, "", nil
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	current := threadUUID
	visited := map[string]struct{}{current: {}}

	for depth := 0; depth < maxWalkDepth; depth++ {
		parent, found, attempts, lookupErr := p.lookupParentWithRetry(ctx, db, current, depth == 0)
		if depth == 0 {
			logger.Info("codex walk_up_to_root: thread=%s parent_found=%t attempts=%d elapsed=%s",
				threadUUID, found, attempts, time.Since(start))
		}
		if lookupErr != nil {
			// Schema/IO failure mid-walk: degrade to "current is the root".
			break
		}
		if !found {
			// current has no parent edge → it's a root in Codex's tree.
			break
		}
		if _, seen := visited[parent]; seen {
			return "", "", fmt.Errorf("codex thread cycle detected at %s", parent)
		}
		visited[parent] = struct{}{}
		current = parent
	}

	rolloutPath, _ := p.lookupRolloutPath(ctx, db, current)
	return current, rolloutPath, nil
}

// lookupParentWithRetry queries the immediate parent of childUUID from
// thread_spawn_edges. When withRetry is true, retries on "no edge" up to
// walkUpRetryAttempts times with walkUpRetryBackoff between attempts —
// covering the spawn-vs-edge race window between Codex committing a new
// subagent's thread row and committing the parent edge.
//
// Optimization: before consuming the retry budget, check the thread row's
// `thread_source`. If it's "user", this is a root thread by Codex's own
// classification and no edge will ever exist — return immediately without
// burning the 200ms retry budget. The common case (every fresh Codex
// startup hook) hits this fast path.
//
// Returns (parent_thread_id, true, attempts, nil) on success.
// Returns ("", false, attempts, nil) when no edge exists after retries.
// Returns ("", false, attempts, err) on DB errors.
func (p Codex) lookupParentWithRetry(ctx context.Context, db *sql.DB, childUUID string, withRetry bool) (string, bool, int, error) {
	// First attempt — common case is a fast hit.
	parent, found, err := p.lookupParent(ctx, db, childUUID)
	if err != nil {
		return "", false, 1, err
	}
	if found {
		return parent, true, 1, nil
	}
	// No edge on first attempt. Decide whether to retry.
	if !withRetry {
		return "", false, 1, nil
	}
	// If Codex itself classifies this thread as 'user', no edge will appear
	// no matter how long we wait — skip the retry budget.
	if src, ok := p.lookupThreadSource(ctx, db, childUUID); ok && src == "user" {
		return "", false, 1, nil
	}
	// Real race window — retry.
	for attempts := 2; attempts <= walkUpRetryAttempts; attempts++ {
		select {
		case <-ctx.Done():
			return "", false, attempts - 1, ctx.Err()
		case <-time.After(walkUpRetryBackoff):
		}
		parent, found, err := p.lookupParent(ctx, db, childUUID)
		if err != nil {
			return "", false, attempts, err
		}
		if found {
			return parent, true, attempts, nil
		}
	}
	return "", false, walkUpRetryAttempts, nil
}

func (p Codex) lookupThreadSource(ctx context.Context, db *sql.DB, threadUUID string) (string, bool) {
	const q = `SELECT COALESCE(thread_source, '') FROM threads WHERE id = ? LIMIT 1`
	var src string
	err := db.QueryRowContext(ctx, q, threadUUID).Scan(&src)
	if err != nil {
		return "", false
	}
	return src, true
}

func (p Codex) lookupParent(ctx context.Context, db *sql.DB, childUUID string) (string, bool, error) {
	const q = `SELECT parent_thread_id FROM thread_spawn_edges WHERE child_thread_id = ? LIMIT 1`
	var parent string
	err := db.QueryRowContext(ctx, q, childUUID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return parent, true, nil
}

func (p Codex) lookupRolloutPath(ctx context.Context, db *sql.DB, threadUUID string) (string, error) {
	const q = `SELECT rollout_path FROM threads WHERE id = ? LIMIT 1`
	var path string
	err := db.QueryRowContext(ctx, q, threadUUID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

// ListSubtree returns every descendant of rootThreadUUID, at any depth, via
// a recursive CTE over `thread_spawn_edges` JOIN `threads`. Each returned
// row's ParentThreadUUID is the immediate parent (which may itself be a
// descendant of rootThreadUUID for grandchildren). The root itself is NOT
// returned.
//
// Returns (nil, nil) when the DB doesn't exist or the schema doesn't match
// — degrades gracefully so daemon sync cycles continue uninterrupted.
func (p Codex) ListSubtree(rootThreadUUID string) ([]CodexThreadRow, error) {
	db, err := p.openStateDB()
	if err != nil || db == nil {
		return nil, nil
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const q = `
WITH RECURSIVE descendants(thread_uuid, parent_thread_uuid) AS (
    SELECT child_thread_id, parent_thread_id
    FROM thread_spawn_edges
    WHERE parent_thread_id = ?
  UNION ALL
    SELECT e.child_thread_id, e.parent_thread_id
    FROM thread_spawn_edges e
    JOIN descendants d ON e.parent_thread_id = d.thread_uuid
)
SELECT
    d.thread_uuid,
    d.parent_thread_uuid,
    t.rollout_path,
    COALESCE(t.cwd, ''),
    COALESCE(t.model, ''),
    COALESCE(t.source, ''),
    COALESCE(t.thread_source, ''),
    COALESCE(t.agent_path, ''),
    COALESCE(t.agent_role, ''),
    COALESCE(t.agent_nickname, '')
FROM descendants d
JOIN threads t ON t.id = d.thread_uuid
`
	rows, err := db.QueryContext(ctx, q, rootThreadUUID)
	if err != nil {
		logger.Warn("codex list_subtree: query failed (schema mismatch?): %v", err)
		return nil, nil
	}
	defer rows.Close()

	var out []CodexThreadRow
	for rows.Next() {
		var r CodexThreadRow
		if err := rows.Scan(
			&r.ThreadUUID, &r.ParentThreadUUID, &r.RolloutPath,
			&r.CWD, &r.Model, &r.Source, &r.ThreadSource,
			&r.AgentPath, &r.AgentRole, &r.AgentNickname,
		); err != nil {
			logger.Warn("codex list_subtree: row scan failed: %v", err)
			return nil, nil
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		logger.Warn("codex list_subtree: row iteration failed: %v", err)
		return nil, nil
	}
	return out, nil
}
