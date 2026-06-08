// Package opencodetest builds OpenCode-shaped SQLite fixture databases for
// tests that exercise the OpenCode SQLite reader and downstream collector.
//
// The DB this package writes uses the *real* OpenCode schema (session,
// message, part tables + the indices the reader's query plan depends on),
// backed by the same modernc.org/sqlite driver production uses. Tests build
// scenarios by chaining AddSession / AddMessage / AddPart calls or by using
// the higher-level shape helpers (UserTextMessage, ToolPartCompleted, ...).
//
// No fixture DB is checked in. Each test seeds a fresh DB at
// <t.TempDir()>/opencode.db; the helper takes care of teardown via the
// testing.T's cleanup hooks.
//
// Notes on row shape (verified against a real OpenCode v1.15.13 DB):
//   - message.data and part.data JSON do NOT carry id/sessionID/messageID;
//     those live only in row columns. The reader's job is to inject them.
//     The Add* helpers below preserve that property so the reader's
//     injection path is what the tests actually exercise.
//   - time_created is set to 0 across all fixture rows. The reader's
//     ORDER BY (time_created, m.id, p.id) clause falls through to id
//     ordering when time_created is constant, matching real production
//     semantics (where ULIDs and time_created are co-monotonic) and
//     keeping fixture rows order-independent of insertion order.
package opencodetest

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// schemaDDL recreates the subset of the OpenCode v1.15.13 schema the
// reader's queries depend on. Tables and indices are byte-equal to the
// real DDL except for the foreign-key constraints (omitted: project + the
// other reference targets are out of scope; SQLite leaves FKs off by
// default so this only matters for self-documentation).
const schemaDDL = `
CREATE TABLE session (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	parent_id TEXT,
	slug TEXT NOT NULL,
	directory TEXT NOT NULL,
	title TEXT NOT NULL,
	version TEXT NOT NULL,
	share_url TEXT,
	summary_additions INTEGER,
	summary_deletions INTEGER,
	summary_files INTEGER,
	summary_diffs TEXT,
	revert TEXT,
	permission TEXT,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL,
	time_compacting INTEGER,
	time_archived INTEGER,
	workspace_id TEXT,
	path TEXT,
	agent TEXT,
	model TEXT,
	cost REAL DEFAULT 0 NOT NULL,
	tokens_input INTEGER DEFAULT 0 NOT NULL,
	tokens_output INTEGER DEFAULT 0 NOT NULL,
	tokens_reasoning INTEGER DEFAULT 0 NOT NULL,
	tokens_cache_read INTEGER DEFAULT 0 NOT NULL,
	tokens_cache_write INTEGER DEFAULT 0 NOT NULL,
	metadata TEXT
);

CREATE TABLE message (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL,
	data TEXT NOT NULL
);

CREATE TABLE part (
	id TEXT PRIMARY KEY,
	message_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL,
	data TEXT NOT NULL
);

CREATE INDEX message_session_time_created_id_idx ON message (session_id, time_created, id);
CREATE INDEX part_message_id_id_idx ON part (message_id, id);
CREATE INDEX part_session_idx ON part (session_id);
`

// Builder seeds a temporary SQLite DB with the OpenCode schema.
type Builder struct {
	t    *testing.T
	path string
	db   *sql.DB
}

// NewDB creates a temp SQLite DB at <t.TempDir()>/opencode.db with the
// OpenCode schema applied, and returns a Builder for adding rows. The DB
// handle is closed at the end of the test via t.Cleanup.
func NewDB(t *testing.T) *Builder {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	// Plain rollback journal (not WAL). Production OpenCode uses WAL, but
	// the fixture has no concurrent writer to coordinate with, and a plain
	// journal sidesteps a subtle pitfall: a ro reader opened against the
	// same path while the WAL is still hot can intermittently miss the
	// schema, depending on -shm state. The reader code path under test
	// uses ?mode=ro and works identically against either journal mode.
	//
	// busy_timeout(5000) lets the test writer cope with concurrent readers
	// the way production does (CF-538: child collectors poll the same DB
	// while integration tests append late rows). Without it, -race timing
	// surfaces SQLITE_BUSY on the writer side.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opencodetest: open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schemaDDL); err != nil {
		t.Fatalf("opencodetest: apply schema: %v", err)
	}
	return &Builder{t: t, path: path, db: db}
}

// Path returns the absolute path to the fixture DB.
func (b *Builder) Path() string { return b.path }

// AddSession inserts a session row. parentID="" for a root session.
// NOT NULL columns get placeholder values; tests that exercise the
// session.directory or session.parent_id columns should use
// AddSessionWithDir instead.
func (b *Builder) AddSession(id, parentID string) *Builder {
	return b.AddSessionWithDir(id, parentID, "/tmp")
}

// AddSessionWithDir is the full-form session insert: it lets the caller
// set the session.directory column, which the reader's ReadSessionInfo
// surfaces. Used by CF-549 tests that assert on directory + parent_id.
func (b *Builder) AddSessionWithDir(id, parentID, directory string) *Builder {
	b.t.Helper()
	var pid any
	if parentID != "" {
		pid = parentID
	}
	const stmt = `INSERT INTO session
		(id, project_id, parent_id, slug, directory, title, version, time_created, time_updated)
		VALUES (?, 'proj_test', ?, 'slug', ?, 'title', '1.15.13', 0, 0)`
	if _, err := b.db.Exec(stmt, id, pid, directory); err != nil {
		b.t.Fatalf("opencodetest: AddSessionWithDir(%q): %v", id, err)
	}
	return b
}

// AddMessage inserts a message row. data is the JSON envelope minus
// id/sessionID — the reader reconstructs those from row columns. The
// helper marshals the map to JSON before insert.
func (b *Builder) AddMessage(sessionID, msgID string, data map[string]any) *Builder {
	b.t.Helper()
	// Defensive: real OpenCode rows never carry id/sessionID inside data.
	// If a test passes them, strip — the reader's whole job is to inject,
	// so the fixture must not pre-fill them.
	delete(data, "id")
	delete(data, "sessionID")
	raw, err := json.Marshal(data)
	if err != nil {
		b.t.Fatalf("opencodetest: marshal AddMessage data: %v", err)
	}
	const stmt = `INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, 0, 0, ?)`
	if _, err := b.db.Exec(stmt, msgID, sessionID, string(raw)); err != nil {
		b.t.Fatalf("opencodetest: AddMessage(%q): %v", msgID, err)
	}
	return b
}

// AddPart inserts a part row under a message. data is the part envelope
// minus id/sessionID/messageID — the reader reconstructs those from
// columns. The session_id column is denormalized (matches production); it
// is derived from the parent message row.
func (b *Builder) AddPart(messageID, partID string, data map[string]any) *Builder {
	b.t.Helper()
	delete(data, "id")
	delete(data, "sessionID")
	delete(data, "messageID")
	raw, err := json.Marshal(data)
	if err != nil {
		b.t.Fatalf("opencodetest: marshal AddPart data: %v", err)
	}
	const stmt = `INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		SELECT ?, ?, m.session_id, 0, 0, ?
		FROM message m WHERE m.id = ?`
	res, err := b.db.Exec(stmt, partID, messageID, string(raw), messageID)
	if err != nil {
		b.t.Fatalf("opencodetest: AddPart(%q): %v", partID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		b.t.Fatalf("opencodetest: AddPart(%q): parent message %q not found", partID, messageID)
	}
	return b
}

// UserTextMessage returns a user-role message envelope with the minimal
// fields real OpenCode emits for user turns. Caller adds a TextPart
// separately to give it a content part.
func UserTextMessage(text string) map[string]any {
	return map[string]any{
		"role":  "user",
		"time":  map[string]any{"created": 0},
		"agent": "build",
	}
}

// AssistantMessageFinished returns a settled assistant message envelope.
// finish must be one of "stop", "tool-calls", "max_tokens", "length" — the
// finish field's presence is what gates ocIsComplete for assistant rows.
func AssistantMessageFinished(finish string) map[string]any {
	return map[string]any{
		"role":   "assistant",
		"time":   map[string]any{"created": 0, "completed": 0},
		"agent":  "build",
		"mode":   "build",
		"finish": finish,
		"cost":   0,
		"tokens": map[string]any{
			"total": 0, "input": 0, "output": 0, "reasoning": 0,
			"cache": map[string]any{"read": 0, "write": 0},
		},
	}
}

// AssistantMessageStreaming returns an unsettled assistant message
// envelope (no finish, no error). The collector must NOT emit this and
// must stop walking newer messages once it encounters one.
func AssistantMessageStreaming() map[string]any {
	return map[string]any{
		"role":  "assistant",
		"time":  map[string]any{"created": 0},
		"agent": "build",
		"mode":  "build",
	}
}

// TextPart returns a text part envelope.
func TextPart(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

// ReasoningPart returns a reasoning part envelope.
func ReasoningPart(text string) map[string]any {
	return map[string]any{
		"type": "reasoning",
		"text": text,
		"time": map[string]any{"start": 0, "end": 0},
	}
}

// ToolPartCompleted returns a settled tool part envelope.
func ToolPartCompleted(tool string, input map[string]any, output string) map[string]any {
	return map[string]any{
		"type":   "tool",
		"tool":   tool,
		"callID": "call_test",
		"state": map[string]any{
			"status": "completed",
			"input":  input,
			"output": output,
			"time":   map[string]any{"start": 0, "end": 0},
		},
	}
}

// StepFinishPart returns a step-finish part envelope.
func StepFinishPart() map[string]any {
	return map[string]any{
		"type":     "step-finish",
		"reason":   "stop",
		"snapshot": "0000000000000000000000000000000000000000",
		"cost":     0,
		"tokens": map[string]any{
			"total": 0, "input": 0, "output": 0, "reasoning": 0,
			"cache": map[string]any{"read": 0, "write": 0},
		},
	}
}
