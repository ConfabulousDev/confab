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
