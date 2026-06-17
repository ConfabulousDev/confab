package provider

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/opencodetest"
)

// withOpenCodeDB points CONFAB_OPENCODE_DB at the fixture and a temp HOME so
// the materialized files (~/.confab/opencode/...) land under t.TempDir().
func withOpenCodeDB(t *testing.T, dbPath string) {
	t.Helper()
	t.Setenv(OpenCodeDBEnv, dbPath)
	home := t.TempDir()
	t.Setenv("HOME", home)
}

// TestOpencodeScanSessionsReturnsOnlyRoots asserts ScanSessions enumerates
// root sessions (parent_id IS NULL) and excludes children — parity with the
// daemon's root-only rule.
func TestOpencodeScanSessionsReturnsOnlyRoots(t *testing.T) {
	const rootA = "ses_0000000000000000000roota"
	const rootB = "ses_0000000000000000000rootb"
	const child = "ses_0000000000000000000child"
	b := opencodetest.NewDB(t)
	b.AddSessionAt(rootA, "", "/work/a", 200).
		AddSessionAt(rootB, "", "/work/b", 100).
		AddSessionWithDir(child, rootA, "/work/a")
	withOpenCodeDB(t, b.Path())

	sessions, err := Opencode{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2 (children excluded): %+v", len(sessions), sessions)
	}
	// Newest first.
	if sessions[0].SessionID != rootA {
		t.Errorf("sessions[0].SessionID = %q, want %q (newest first)", sessions[0].SessionID, rootA)
	}
	if sessions[0].ProjectPath != "/work/a" {
		t.Errorf("sessions[0].ProjectPath = %q, want /work/a", sessions[0].ProjectPath)
	}
	if sessions[0].ModTime != time.Unix(200, 0) {
		t.Errorf("sessions[0].ModTime = %v, want %v", sessions[0].ModTime, time.Unix(200, 0))
	}
	for _, s := range sessions {
		if s.SessionID == child {
			t.Errorf("child session %q leaked into scan", child)
		}
	}
}

// TestOpencodeScanSessionsPopulatesFirstUserMessage asserts the TITLE source
// (FirstUserMessage) is populated per-session from the first user text part.
func TestOpencodeScanSessionsPopulatesFirstUserMessage(t *testing.T) {
	const root = "ses_scan_fum"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(root, "", "/work")
	b.AddMessage(root, "msg_00000000000000000000a1", opencodetest.UserTextMessage("build me a thing"))
	b.AddPart("msg_00000000000000000000a1", "prt_a", opencodetest.TextPart("build me a thing"))
	withOpenCodeDB(t, b.Path())

	sessions, err := Opencode{}.ScanSessions()
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].FirstUserMessage != "build me a thing" {
		t.Errorf("FirstUserMessage = %q, want \"build me a thing\"", sessions[0].FirstUserMessage)
	}
	if sessions[0].Summary != "" {
		t.Errorf("Summary = %q, want \"\" (OpenCode has no summary)", sessions[0].Summary)
	}
}

// TestOpencodeScanSessionsMissingDBErrors asserts a missing DB surfaces a
// clear error to the list command rather than an empty list.
func TestOpencodeScanSessionsMissingDBErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "opencode.db")
	withOpenCodeDB(t, missing)
	_, err := Opencode{}.ScanSessions()
	if err == nil {
		t.Fatal("ScanSessions returned nil err for missing DB, want error")
	}
}

// TestOpencodeFindSessionByIDResolvesPartialRoot asserts a partial id resolves
// to the full root id and materializes the root transcript at the expected
// path, with complete messages written.
func TestOpencodeFindSessionByIDResolvesPartialRoot(t *testing.T) {
	const root = "ses_findbyid_root_aaaa"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(root, "", "/work")
	b.AddMessage(root, "msg_00000000000000000000a1", opencodetest.UserTextMessage("hello"))
	b.AddPart("msg_00000000000000000000a1", "prt_a", opencodetest.TextPart("hello"))
	asst := opencodetest.AssistantMessageFinished("stop")
	b.AddMessage(root, "msg_00000000000000000000b2", asst)
	b.AddPart("msg_00000000000000000000b2", "prt_b", opencodetest.TextPart("hi there"))
	home := t.TempDir()
	t.Setenv(OpenCodeDBEnv, b.Path())
	t.Setenv("HOME", home)

	fullID, transcriptPath, err := Opencode{}.FindSessionByID("ses_findbyid_root")
	if err != nil {
		t.Fatalf("FindSessionByID: %v", err)
	}
	if fullID != root {
		t.Errorf("fullID = %q, want %q", fullID, root)
	}
	wantPath := filepath.Join(home, ".confab", "opencode", root, "messages.jsonl")
	if transcriptPath != wantPath {
		t.Errorf("transcriptPath = %q, want %q", transcriptPath, wantPath)
	}
	lines := readLines(t, transcriptPath)
	if len(lines) != 2 {
		t.Fatalf("materialized %d lines, want 2: %v", len(lines), lines)
	}
}

// TestOpencodeFindSessionByIDAmbiguous asserts an ambiguous prefix errors
// rather than silently picking one.
func TestOpencodeFindSessionByIDAmbiguous(t *testing.T) {
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir("ses_dup_aaa", "", "/work").
		AddSessionWithDir("ses_dup_bbb", "", "/work")
	withOpenCodeDB(t, b.Path())

	_, _, err := Opencode{}.FindSessionByID("ses_dup_")
	if err == nil {
		t.Fatal("FindSessionByID returned nil err for ambiguous prefix, want error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %v, want it to mention 'ambiguous'", err)
	}
}

// TestOpencodeFindSessionByIDNotFound asserts an unknown id errors.
func TestOpencodeFindSessionByIDNotFound(t *testing.T) {
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir("ses_present", "", "/work")
	withOpenCodeDB(t, b.Path())

	_, _, err := Opencode{}.FindSessionByID("ses_absent")
	if err == nil {
		t.Fatal("FindSessionByID returned nil err for unknown id, want error")
	}
}

// TestOpencodeFindSessionByIDResolvesDescendantToRoot asserts that passing a
// descendant id resolves up to its root (consistent with "root + descendants"
// save scope) and materializes the root transcript.
func TestOpencodeFindSessionByIDResolvesDescendantToRoot(t *testing.T) {
	const root = "ses_tree_root"
	const child = "ses_tree_child"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(root, "", "/work")
	b.AddSessionWithDir(child, root, "/work")
	b.AddMessage(root, "msg_00000000000000000000a1", opencodetest.UserTextMessage("root msg"))
	b.AddPart("msg_00000000000000000000a1", "prt_a", opencodetest.TextPart("root msg"))
	home := t.TempDir()
	t.Setenv(OpenCodeDBEnv, b.Path())
	t.Setenv("HOME", home)

	fullID, transcriptPath, err := Opencode{}.FindSessionByID(child)
	if err != nil {
		t.Fatalf("FindSessionByID: %v", err)
	}
	if fullID != root {
		t.Errorf("fullID = %q, want root %q (descendant must resolve to root)", fullID, root)
	}
	wantPath := filepath.Join(home, ".confab", "opencode", root, "messages.jsonl")
	if transcriptPath != wantPath {
		t.Errorf("transcriptPath = %q, want %q", transcriptPath, wantPath)
	}
}

// TestOpencodeFindSessionByIDEmptySessionResolves asserts a session with no
// complete messages still resolves to its id without panicking. Materialize
// produces no file (nothing to write); the save path then surfaces a per-
// session "file not found" error and continues — graceful, not a crash.
func TestOpencodeFindSessionByIDEmptySessionResolves(t *testing.T) {
	const root = "ses_empty_session"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(root, "", "/work")
	// Only an unsettled assistant message → nothing complete to materialize.
	b.AddMessage(root, "msg_00000000000000000000a1", opencodetest.AssistantMessageStreaming())
	home := t.TempDir()
	t.Setenv(OpenCodeDBEnv, b.Path())
	t.Setenv("HOME", home)

	fullID, _, err := Opencode{}.FindSessionByID(root)
	if err != nil {
		t.Fatalf("FindSessionByID should resolve even an empty session: %v", err)
	}
	if fullID != root {
		t.Errorf("fullID = %q, want %q", fullID, root)
	}
}

// TestOpencodeMaterializeWritesCompleteMessages asserts the exported one-shot
// Materialize reuses the collector's completeness gating: only settled
// messages are written, stopping at the first incomplete one.
func TestOpencodeMaterializeWritesCompleteMessages(t *testing.T) {
	const sid = "ses_mat"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "")
	b.AddMessage(sid, "msg_00000000000000000000a1", opencodetest.UserTextMessage("u1"))
	b.AddPart("msg_00000000000000000000a1", "prt_a", opencodetest.TextPart("u1"))
	b.AddMessage(sid, "msg_00000000000000000000b2", opencodetest.AssistantMessageFinished("stop"))
	b.AddPart("msg_00000000000000000000b2", "prt_b", opencodetest.TextPart("done"))
	// Streaming (incomplete) — must NOT be emitted, and must stop the walk.
	b.AddMessage(sid, "msg_00000000000000000000c3", opencodetest.AssistantMessageStreaming())

	out := filepath.Join(t.TempDir(), "messages.jsonl")
	reader := NewOpenCodeDBReader(b.Path())
	n, err := MaterializeOpenCodeSession(context.Background(), reader, sid, out, 0)
	if err != nil {
		t.Fatalf("MaterializeOpenCodeSession: %v", err)
	}
	if n != 2 {
		t.Errorf("materialized %d messages, want 2 (incomplete trailing message excluded)", n)
	}
	lines := readLines(t, out)
	if len(lines) != 2 {
		t.Fatalf("file has %d lines, want 2", len(lines))
	}
}

