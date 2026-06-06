package opencodetest

import (
	"database/sql"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

// expectedTables names every table the seeded DB must expose. The OpenCode
// reader only queries session/message/part, but additional tables (events,
// projects, etc.) exist in the real schema; if upstream renames or removes
// any of these, the reader's queries break silently. This self-test pins
// the surface so a schema change in this helper is loudly visible.
var expectedTables = []string{"message", "part", "session"}

// expectedIndices names every index the reader's query plan depends on.
// Without these, the `WHERE m.session_id = ? AND m.id > ? ORDER BY
// m.time_created, m.id, p.id` plan degenerates to a full scan and the
// daemon polls become noticeably slow on large DBs.
var expectedIndices = []string{
	"message_session_time_created_id_idx",
	"part_message_id_id_idx",
	"part_session_idx",
}

// TestSeededSchemaMatchesProduction asserts the fixture DB has the
// production schema. The fixture is no good for testing the reader if its
// table or index shape doesn't match what real OpenCode writes.
func TestSeededSchemaMatchesProduction(t *testing.T) {
	b := NewDB(t)

	db, err := sql.Open("sqlite", "file:"+b.Path()+"?mode=ro")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	t.Run("tables", func(t *testing.T) {
		// `_` is a single-character wildcard in SQL LIKE — escape with `\`
		// so the __drizzle_migrations exclusion doesn't accidentally hide
		// every two-character-or-longer name (i.e. all of them).
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT LIKE '\_\_%' ESCAPE '\'`)
		if err != nil {
			t.Fatalf("query tables: %v", err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			got = append(got, n)
		}
		sort.Strings(got)
		for _, want := range expectedTables {
			if !contains(got, want) {
				t.Errorf("missing table %q (got %v)", want, got)
			}
		}
	})

	t.Run("indices", func(t *testing.T) {
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='index'`)
		if err != nil {
			t.Fatalf("query indices: %v", err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			got = append(got, n)
		}
		for _, want := range expectedIndices {
			if !contains(got, want) {
				t.Errorf("missing index %q (got %v)", want, got)
			}
		}
	})
}

// TestBuilderRoundTrip asserts inserted rows are queryable with the
// reader's column expectations: message.id is the msg ULID, message.data
// is JSON missing the id/sessionID keys (so the reader has work to do),
// part rows likewise omit id/sessionID/messageID from their JSON payload.
func TestBuilderRoundTrip(t *testing.T) {
	const sid, mid, pid = "ses_rt", "msg_rt", "prt_rt"
	b := NewDB(t)
	b.AddSession(sid, "").
		AddMessage(sid, mid, UserTextMessage("hello")).
		AddPart(mid, pid, TextPart("hello"))

	db, err := sql.Open("sqlite", "file:"+b.Path()+"?mode=ro")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var dbMsgID, dbMsgSession, dbMsgData string
	err = db.QueryRow(`SELECT id, session_id, data FROM message WHERE id = ?`, mid).
		Scan(&dbMsgID, &dbMsgSession, &dbMsgData)
	if err != nil {
		t.Fatalf("scan message: %v", err)
	}
	if dbMsgID != mid {
		t.Errorf("message.id = %q, want %q", dbMsgID, mid)
	}
	if dbMsgSession != sid {
		t.Errorf("message.session_id = %q, want %q", dbMsgSession, sid)
	}
	// Real production rows do not store id/sessionID in the data JSON;
	// the fixture must follow suit so the reader's injection is exercised.
	if containsKey(dbMsgData, `"id"`) {
		t.Errorf("message.data unexpectedly contains \"id\" key: %s", dbMsgData)
	}
	if containsKey(dbMsgData, `"sessionID"`) {
		t.Errorf("message.data unexpectedly contains \"sessionID\" key: %s", dbMsgData)
	}

	var dbPartID, dbPartMsg, dbPartSession, dbPartData string
	err = db.QueryRow(`SELECT id, message_id, session_id, data FROM part WHERE id = ?`, pid).
		Scan(&dbPartID, &dbPartMsg, &dbPartSession, &dbPartData)
	if err != nil {
		t.Fatalf("scan part: %v", err)
	}
	if dbPartID != pid || dbPartMsg != mid || dbPartSession != sid {
		t.Errorf("part row identity mismatch: id=%q msg=%q sess=%q", dbPartID, dbPartMsg, dbPartSession)
	}
	for _, k := range []string{`"id"`, `"sessionID"`, `"messageID"`} {
		if containsKey(dbPartData, k) {
			t.Errorf("part.data unexpectedly contains %s key: %s", k, dbPartData)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// containsKey is a deliberately loose check: it looks for the literal
// `"key"` substring with quotes. False positives on a value that happens
// to equal the key are acceptable here — this is a fixture-correctness
// guard, not a parser.
func containsKey(jsonBlob, quotedKey string) bool {
	for i := 0; i+len(quotedKey) <= len(jsonBlob); i++ {
		if jsonBlob[i:i+len(quotedKey)] == quotedKey {
			return true
		}
	}
	return false
}
