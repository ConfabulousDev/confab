package provider

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// OpenCodeDBEnv overrides automatic OpenCode SQLite-DB discovery. When set,
// points directly at an opencode.db file (or any SQLite file with the
// expected schema). Used by tests; can also be set by power users debugging
// OpenCode session sync.
const OpenCodeDBEnv = "CONFAB_OPENCODE_DB"

// opencodeReadBusyTimeoutMs is the SQLite busy_timeout pragma applied to
// every reader connection. OpenCode actively writes to the DB in WAL mode;
// 5 seconds covers any in-flight write transaction without blocking the
// poll cycle indefinitely.
const opencodeReadBusyTimeoutMs = 5000

// OpenCodeDBReader reads OpenCode session data from a local SQLite DB
// (~/.local/share/opencode/opencode.db). It is the only producer of the
// materialized {info, parts} JSONL the collector appends; everything
// downstream of that file is provider-agnostic.
//
// Each ReadSession call opens the DB read-only, runs a single LEFT JOIN
// query, and closes — mirroring the Codex state-DB read pattern. The DB is
// concurrently written by OpenCode (WAL mode); the reader never writes,
// uses busy_timeout for transient lock contention, and tolerates seeing
// rows mid-write (downstream completeness gating in ocIsComplete handles
// that).
type OpenCodeDBReader struct {
	path string
}

// NewOpenCodeDBReader builds a reader bound to a specific DB path.
// The path is not validated until ReadSession runs.
func NewOpenCodeDBReader(dbPath string) *OpenCodeDBReader {
	return &OpenCodeDBReader{path: dbPath}
}

// ReadSession returns messages for sessionID strictly greater than
// sinceMessageID (pass "" for a full read), as raw {info, parts} envelopes
// ordered by (message.time_created, message.id) and by part.id within each
// message. The returned envelopes carry id/sessionID injected into info,
// and id/sessionID/messageID injected into each part — these live in DB
// columns, not in the stored JSON, so reconstruction is essential for the
// wire shape the materialized JSONL contract requires.
//
// Returns (nil, nil) when the session has no qualifying rows yet (treated
// as "wait, retry" by the caller). Returns a clear error when the DB file
// is missing or unreadable.
func (r *OpenCodeDBReader) ReadSession(ctx context.Context, sessionID, sinceMessageID string) ([]ocRawEnvelope, error) {
	db, err := r.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Single LEFT JOIN, indexed and incremental.
	//
	// Plan verified against OpenCode v1.15.13:
	//   SEARCH m USING INDEX message_session_time_created_id_idx (session_id=?)
	//   SEARCH p USING INDEX part_message_id_id_idx (message_id=?) LEFT-JOIN
	//
	// LEFT JOIN lets a message with zero parts still surface (parts cols
	// arrive as NULL); the Go loop below tolerates that path. The
	// `(? = '' OR m.id > ?)` HWM clause stays cheap because the index has
	// `id` as a suffix after `session_id, time_created`.
	const query = `
		SELECT m.id, m.session_id, m.data,
		       p.id, p.session_id, p.data
		FROM message m
		LEFT JOIN part p ON p.message_id = m.id
		WHERE m.session_id = ? AND (? = '' OR m.id > ?)
		ORDER BY m.time_created, m.id, p.id`
	rows, err := db.QueryContext(ctx, query, sessionID, sinceMessageID, sinceMessageID)
	if err != nil {
		return nil, fmt.Errorf("query opencode session %s: %w", sessionID, err)
	}
	defer rows.Close()

	return scanEnvelopes(rows)
}

// scanEnvelopes assembles ocRawEnvelopes from a (message, part) LEFT JOIN
// result set ordered by (message.time_created, message.id, part.id). Each row
// carries the message columns plus nullable part columns; the loop groups
// consecutive rows by message id and injects id/sessionID/messageID into the
// raw JSON (these live in DB columns, never the stored JSON). Shared by
// ReadSession (full/incremental materialization) and FirstUserMessageText
// (bounded leading read) so both produce byte-identical envelope shapes.
func scanEnvelopes(rows *sql.Rows) ([]ocRawEnvelope, error) {
	// haveCur acts as a "first row not yet processed" sentinel: it's distinct
	// from the empty-string initial value of curMsgID, so the very first row
	// always triggers the flush-and-start path even if (pathologically) mID
	// is empty — never leaving a part-row trying to attach to a nil curEnv.
	var (
		envs     []ocRawEnvelope
		curMsgID string
		curEnv   *ocRawEnvelope
		haveCur  bool
	)
	flush := func() {
		if curEnv != nil {
			envs = append(envs, *curEnv)
			curEnv = nil
		}
	}
	for rows.Next() {
		var (
			mID, mSession, mData string
			pID, pSession, pData sql.NullString
		)
		if err := rows.Scan(&mID, &mSession, &mData, &pID, &pSession, &pData); err != nil {
			return nil, fmt.Errorf("scan opencode row: %w", err)
		}
		if !haveCur || mID != curMsgID {
			flush()
			info, err := injectInfoIdentity([]byte(mData), mID, mSession)
			if err != nil {
				return nil, fmt.Errorf("inject info identity for %s: %w", mID, err)
			}
			curMsgID = mID
			curEnv = &ocRawEnvelope{Info: info}
			haveCur = true
		}
		if pID.Valid {
			part, err := injectPartIdentity([]byte(pData.String), pID.String, pSession.String, mID)
			if err != nil {
				return nil, fmt.Errorf("inject part identity for %s: %w", pID.String, err)
			}
			curEnv.Parts = append(curEnv.Parts, part)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode rows: %w", err)
	}
	flush()
	return envs, nil
}

// injectInfoIdentity rewrites a message.data JSON blob to carry id +
// sessionID at the top level. The blob in production never has these keys
// (they live in row columns) — the backend's OpenCodeMessageInfo struct
// expects them, so reconstruction is the reader's load-bearing job.
// Existing keys are preserved verbatim; if the JSON unexpectedly already
// holds id/sessionID, they're overwritten with the row-column values
// (which are authoritative).
func injectInfoIdentity(data []byte, id, sessionID string) (json.RawMessage, error) {
	return injectIdentity(data, map[string]string{
		"id":        id,
		"sessionID": sessionID,
	})
}

// injectPartIdentity rewrites a part.data JSON blob to carry id +
// sessionID + messageID at the top level. Same contract as
// injectInfoIdentity; messageID is included because OpenCodePart needs it
// to associate parts with their owning message.
func injectPartIdentity(data []byte, id, sessionID, messageID string) (json.RawMessage, error) {
	return injectIdentity(data, map[string]string{
		"id":        id,
		"sessionID": sessionID,
		"messageID": messageID,
	})
}

// injectIdentity is the shared workhorse. It decodes the JSON into a
// generic map preserving raw bytes for every value, splices in the new
// keys, and re-marshals. Using json.RawMessage values means nested
// structures (tokens, time, cache, state.input, ...) round-trip with byte
// fidelity — only the top-level id/sessionID/messageID keys are added.
func injectIdentity(data []byte, fields map[string]string) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("decode data: %w", err)
	}
	for k, v := range fields {
		quoted, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", k, err)
		}
		obj[k] = quoted
	}
	return json.Marshal(obj)
}

// listDescendantsLimit caps the recursive descent. Realistic OpenCode
// subagent trees observed in production are ~50 wide and 1 deep; 1000
// is well above that ceiling while still defending against pathological
// parent_id cycles.
const listDescendantsLimit = 1000

// ListDescendants returns every descendant session ID under rootSessionID
// (at any depth), discovered via recursive walk of session.parent_id. The
// root itself is excluded; results are returned in ULID lex order (which
// equals chronological order for OpenCode session ids) so callers see a
// deterministic enumeration. Capped at listDescendantsLimit rows.
//
// Returns an error only when the DB file is unreadable — callers (the
// provider's DiscoverDescendants) translate that to a Warn log + nil so
// the daemon's sync cycle continues uninterrupted past a transient
// DB-absence.
func (r *OpenCodeDBReader) ListDescendants(ctx context.Context, rootSessionID string) ([]string, error) {
	db, err := r.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	const query = `
		WITH RECURSIVE descendants(id) AS (
		    SELECT id FROM session WHERE parent_id = ?
		  UNION ALL
		    SELECT s.id FROM session s
		    JOIN descendants d ON s.parent_id = d.id
		)
		SELECT id FROM descendants ORDER BY id LIMIT ?`
	rows, err := db.QueryContext(ctx, query, rootSessionID, listDescendantsLimit)
	if err != nil {
		return nil, fmt.Errorf("query opencode descendants of %s: %w", rootSessionID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan opencode descendant row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode descendant rows: %w", err)
	}
	return ids, nil
}

// OpenCodeRootSession is one row returned by ListRootSessions: the minimal
// columns offline session discovery (ScanSessions) needs to build a
// SessionInfo. FirstUserMessage is fetched separately (a bounded secondary
// read) because it lives in the message/part tables, not the session row.
type OpenCodeRootSession struct {
	ID          string
	Directory   string
	TimeCreated int64 // unix epoch seconds (session.time_created)
}

// ListRootSessions enumerates root sessions (parent_id IS NULL), newest first,
// for offline `confab list --provider opencode`. Children are excluded,
// mirroring the daemon's root-only spawn rule. Returns a clear error when the
// DB is missing/unreadable so the list command can report it (manual commands
// surface DB errors rather than the daemon's Warn-and-continue).
func (r *OpenCodeDBReader) ListRootSessions(ctx context.Context) ([]OpenCodeRootSession, error) {
	db, err := r.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	const query = `
		SELECT id, directory, time_created
		FROM session
		WHERE parent_id IS NULL
		ORDER BY time_created DESC`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query opencode root sessions: %w", err)
	}
	defer rows.Close()

	var roots []OpenCodeRootSession
	for rows.Next() {
		var row OpenCodeRootSession
		if err := rows.Scan(&row.ID, &row.Directory, &row.TimeCreated); err != nil {
			return nil, fmt.Errorf("scan opencode root session row: %w", err)
		}
		roots = append(roots, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode root session rows: %w", err)
	}
	return roots, nil
}

// FirstUserMessageText returns the first user message's first text part for a
// session, used to populate the list TITLE column (OpenCode has no summary).
// Bounded: it reads only the small leading slice of the session needed to find
// the first user text, via a single LEFT JOIN over the user message with the
// lowest message id. Returns ("", nil) when there is no usable user text — the
// list TITLE is then blank, not an error.
//
// firstUserMessageScanLimit caps how many leading messages are scanned so the
// secondary per-session read stays cheap even for long sessions.
func (r *OpenCodeDBReader) FirstUserMessageText(ctx context.Context, sessionID string) (string, error) {
	db, err := r.openRO()
	if err != nil {
		return "", err
	}
	defer db.Close()

	// Pull the first few messages with their parts, ordered the same way
	// ReadSession orders them, then reuse the shared extraction helper that
	// AnnotateChunk uses so offline and live first-user-message logic stay
	// identical.
	const query = `
		SELECT m.id, m.session_id, m.data, p.id, p.session_id, p.data
		FROM (
			SELECT id, session_id, data, time_created
			FROM message
			WHERE session_id = ?
			ORDER BY time_created, id
			LIMIT ?
		) m
		LEFT JOIN part p ON p.message_id = m.id
		ORDER BY m.time_created, m.id, p.id`
	rows, err := db.QueryContext(ctx, query, sessionID, firstUserMessageScanLimit)
	if err != nil {
		return "", fmt.Errorf("query opencode first user message for %s: %w", sessionID, err)
	}
	defer rows.Close()

	envs, err := scanEnvelopes(rows)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(envs))
	for _, e := range envs {
		line, err := json.Marshal(e)
		if err != nil {
			return "", fmt.Errorf("marshal envelope: %w", err)
		}
		lines = append(lines, string(line))
	}
	return ocFirstUserMessageText(lines)
}

// firstUserMessageScanLimit bounds the FirstUserMessageText secondary read.
// The first user message is the very first conversation turn, so a small cap
// always covers it while keeping the per-session list cost negligible.
const firstUserMessageScanLimit = 8

// MatchSessionIDs returns every session id (root or descendant) whose id has
// partialID as a prefix, ordered for determinism. Used by FindSessionByID to
// resolve a partial id the way the file-based providers do. An empty partialID
// matches everything (the caller then reports ambiguity unless there is
// exactly one session).
func (r *OpenCodeDBReader) MatchSessionIDs(ctx context.Context, partialID string) ([]string, error) {
	db, err := r.openRO()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// LIKE with an escaped prefix; partialID is a session id, not free text,
	// but escape LIKE metacharacters defensively so a stray %/_ can't widen
	// the match.
	const query = `SELECT id FROM session WHERE id LIKE ? ESCAPE '\' ORDER BY id`
	rows, err := db.QueryContext(ctx, query, likePrefix(partialID))
	if err != nil {
		return nil, fmt.Errorf("query opencode session ids for %q: %w", partialID, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan opencode session id row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode session id rows: %w", err)
	}
	return ids, nil
}

// ResolveOpencodeRoot walks session.parent_id up from sessionID to the topmost
// root (parent_id IS NULL) and returns its id. A session that is already a root
// returns itself. Bounded by opencodeRootWalkLimit as a cycle defense. Used by
// FindSessionByID so passing any descendant id resolves to the user-facing
// root (consistent with the root+descendants save scope).
func (r *OpenCodeDBReader) ResolveOpencodeRoot(ctx context.Context, sessionID string) (string, error) {
	db, err := r.openRO()
	if err != nil {
		return "", err
	}
	defer db.Close()

	cur := sessionID
	for depth := 0; depth < opencodeRootWalkLimit; depth++ {
		const query = `SELECT COALESCE(parent_id, '') FROM session WHERE id = ?`
		var parent string
		err := db.QueryRowContext(ctx, query, cur).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			// cur is not in the session table. If it's the original id, the
			// caller already matched it via MatchSessionIDs, so this only
			// happens if a parent_id dangles — treat cur as the root.
			return cur, nil
		}
		if err != nil {
			return "", fmt.Errorf("walk opencode parent of %s: %w", cur, err)
		}
		if parent == "" {
			return cur, nil
		}
		cur = parent
	}
	return "", fmt.Errorf("opencode parent walk for %s exceeded depth %d (possible cycle)", sessionID, opencodeRootWalkLimit)
}

// opencodeRootWalkLimit caps the parent_id walk in ResolveOpencodeRoot.
// Realistic OpenCode subagent trees are 1 deep; the cap defends against a
// pathological parent_id cycle.
const opencodeRootWalkLimit = 100

// likePrefix builds a SQL LIKE pattern that prefix-matches s, escaping the
// LIKE metacharacters % _ \ so they are treated literally (ESCAPE '\').
func likePrefix(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s) + "%"
}

// ReadSessionInfo fetches a session row's directory and parent_id from the
// OpenCode SQLite DB. Returns empty strings (not an error) when the row is
// absent so the caller can proceed with best-effort defaults. Errors are
// returned only when the DB itself is unreadable.
//
// Used by the resume path in cmd/hook_sessionstart.go to resolve the cwd +
// parent session id from a session_id-only payload (CF-549).
func (r *OpenCodeDBReader) ReadSessionInfo(ctx context.Context, sessionID string) (directory, parentID string, err error) {
	db, err := r.openRO()
	if err != nil {
		return "", "", err
	}
	defer db.Close()

	// COALESCE collapses NULL parent_id (root sessions) to the empty
	// string. The Opencode.ShouldSpawnForInput gate treats "" as root.
	const query = `SELECT directory, COALESCE(parent_id, '') FROM session WHERE id = ?`
	var dir, pid string
	err = db.QueryRowContext(ctx, query, sessionID).Scan(&dir, &pid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("query opencode session %s: %w", sessionID, err)
	}
	return dir, pid, nil
}

// openRO opens the OpenCode SQLite DB read-only with the standard
// busy_timeout pragma. Verifies the file exists first so the caller gets a
// clear "db not found" error rather than a driver-internal one. Shared by
// ReadSession (collector path) and ReadSessionInfo (resume path) so the
// DSN flags stay in lockstep.
func (r *OpenCodeDBReader) openRO() (*sql.DB, error) {
	if _, err := os.Stat(r.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("opencode db not found at %s", r.path)
		}
		return nil, fmt.Errorf("stat opencode db: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(%d)",
		url.PathEscape(r.path), opencodeReadBusyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open opencode db: %w", err)
	}
	return db, nil
}

// OpenCodeDBPath resolves the OpenCode SQLite DB path in this order:
//  1. CONFAB_OPENCODE_DB env override
//  2. $XDG_DATA_HOME/opencode/opencode.db (when XDG_DATA_HOME is set)
//  3. ~/.local/share/opencode/opencode.db
//
// The returned path is not guaranteed to exist on disk; callers handle
// that via the reader's normal retry path.
func OpenCodeDBPath() (string, error) {
	if env := os.Getenv(OpenCodeDBEnv); env != "" {
		return env, nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "opencode.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}
