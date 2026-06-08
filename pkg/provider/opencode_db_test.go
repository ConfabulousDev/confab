package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/opencodetest"
)

// TestReadSessionInjectsInfoIdentity asserts the reader injects
// id/sessionID into the embedded info JSON. Real OpenCode SQLite never
// stores these fields in message.data — they live in row columns — so a
// reader that forgets to inject them silently corrupts dedup keys + UI
// anchors on the backend.
func TestReadSessionInjectsInfoIdentity(t *testing.T) {
	const sid = "ses_test_inject_info"
	const mid = "msg_0000000000000000000001"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "").
		AddMessage(sid, mid, opencodetest.UserTextMessage("hi"))

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), sid, "")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1", len(envs))
	}
	var info map[string]any
	if err := json.Unmarshal(envs[0].Info, &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	if got, _ := info["id"].(string); got != mid {
		t.Errorf("info.id = %q, want %q", got, mid)
	}
	if got, _ := info["sessionID"].(string); got != sid {
		t.Errorf("info.sessionID = %q, want %q", got, sid)
	}
}

// TestReadSessionInjectsPartIdentity asserts the reader injects
// id/sessionID/messageID into each part's JSON. Real OpenCode SQLite never
// stores these in part.data; the backend's OpenCodePart type expects them.
func TestReadSessionInjectsPartIdentity(t *testing.T) {
	const sid = "ses_test_inject_part"
	const mid = "msg_0000000000000000000001"
	const pid = "prt_0000000000000000000001"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "").
		AddMessage(sid, mid, opencodetest.UserTextMessage("hi")).
		AddPart(mid, pid, opencodetest.TextPart("hi"))

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), sid, "")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(envs) != 1 || len(envs[0].Parts) != 1 {
		t.Fatalf("envs/parts = %d/%d, want 1/1", len(envs), len(envs[0].Parts))
	}
	var part map[string]any
	if err := json.Unmarshal(envs[0].Parts[0], &part); err != nil {
		t.Fatalf("decode part: %v", err)
	}
	if got, _ := part["id"].(string); got != pid {
		t.Errorf("part.id = %q, want %q", got, pid)
	}
	if got, _ := part["sessionID"].(string); got != sid {
		t.Errorf("part.sessionID = %q, want %q", got, sid)
	}
	if got, _ := part["messageID"].(string); got != mid {
		t.Errorf("part.messageID = %q, want %q", got, mid)
	}
}

// TestReadSessionSortsByMessageID asserts envelopes come back in
// ULID lex order (which equals chronological order). The reader must
// produce a monotonic order so the collector's "stop at first incomplete"
// gate works correctly.
func TestReadSessionSortsByMessageID(t *testing.T) {
	const sid = "ses_test_sort"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "")
	// Insert out of order; reader must return sorted.
	b.AddMessage(sid, "msg_00000000000000000000c3", opencodetest.UserTextMessage("third"))
	b.AddMessage(sid, "msg_00000000000000000000a1", opencodetest.UserTextMessage("first"))
	b.AddMessage(sid, "msg_00000000000000000000b2", opencodetest.UserTextMessage("second"))

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), sid, "")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	wantIDs := []string{"msg_00000000000000000000a1", "msg_00000000000000000000b2", "msg_00000000000000000000c3"}
	if len(envs) != len(wantIDs) {
		t.Fatalf("got %d envs, want %d", len(envs), len(wantIDs))
	}
	for i, want := range wantIDs {
		info, err := ocPeekInfo(envs[i].Info)
		if err != nil {
			t.Fatal(err)
		}
		if info.ID != want {
			t.Errorf("envs[%d].id = %q, want %q", i, info.ID, want)
		}
	}
}

// TestReadSessionSortsPartsByID asserts parts within a message are
// returned in part.id (ULID) order.
func TestReadSessionSortsPartsByID(t *testing.T) {
	const sid = "ses_test_part_sort"
	const mid = "msg_test"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "").
		AddMessage(sid, mid, opencodetest.AssistantMessageFinished("stop")).
		// Out of order on insert.
		AddPart(mid, "prt_z", opencodetest.TextPart("last")).
		AddPart(mid, "prt_a", opencodetest.TextPart("first")).
		AddPart(mid, "prt_m", opencodetest.TextPart("middle"))

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), sid, "")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(envs) != 1 || len(envs[0].Parts) != 3 {
		t.Fatalf("envs/parts = %d/%d, want 1/3", len(envs), len(envs[0].Parts))
	}
	wantPartIDs := []string{"prt_a", "prt_m", "prt_z"}
	for i, want := range wantPartIDs {
		var part map[string]any
		if err := json.Unmarshal(envs[0].Parts[i], &part); err != nil {
			t.Fatal(err)
		}
		if got, _ := part["id"].(string); got != want {
			t.Errorf("part[%d].id = %q, want %q", i, got, want)
		}
	}
}

// TestReadSessionFiltersBySession asserts other sessions' rows are
// invisible. The query's session_id filter must be honored.
func TestReadSessionFiltersBySession(t *testing.T) {
	const want = "ses_wanted"
	const other = "ses_other"
	b := opencodetest.NewDB(t)
	b.AddSession(want, "").AddSession(other, "")
	b.AddMessage(want, "msg_wanted_1", opencodetest.UserTextMessage("mine"))
	b.AddMessage(other, "msg_other_1", opencodetest.UserTextMessage("not mine"))

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), want, "")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("got %d envs, want 1", len(envs))
	}
	info, _ := ocPeekInfo(envs[0].Info)
	if info.ID != "msg_wanted_1" {
		t.Errorf("got message %q, want msg_wanted_1 (other session leaked)", info.ID)
	}
}

// TestReadSessionEmptyWhenNoRows asserts an empty result for a session
// with no message rows (or a session id not in the DB at all) — neither
// case is an error; the collector treats both as "wait, retry".
func TestReadSessionEmptyWhenNoRows(t *testing.T) {
	b := opencodetest.NewDB(t)
	b.AddSession("ses_present_but_empty", "")
	r := NewOpenCodeDBReader(b.Path())

	for _, sid := range []string{"ses_present_but_empty", "ses_does_not_exist"} {
		envs, err := r.ReadSession(context.Background(), sid, "")
		if err != nil {
			t.Errorf("ReadSession(%q) err = %v, want nil", sid, err)
		}
		if len(envs) != 0 {
			t.Errorf("ReadSession(%q) returned %d envs, want 0", sid, len(envs))
		}
	}
}

// TestReadSessionMissingDBReturnsError asserts a clear error (not panic,
// not silent empty) when the DB file is absent. The collector logs Warn
// and retries; a silent empty would never surface the underlying problem.
func TestReadSessionMissingDBReturnsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist", "opencode.db")
	r := NewOpenCodeDBReader(missing)
	_, err := r.ReadSession(context.Background(), "ses_x", "")
	if err == nil {
		t.Fatal("ReadSession returned nil err for missing DB, want error")
	}
}

// TestReadSessionHWMIncremental asserts sinceMessageID filters to messages
// strictly greater. The collector uses this to skip already-emitted
// messages on every poll, which is the efficiency story for the design.
func TestReadSessionHWMIncremental(t *testing.T) {
	const sid = "ses_test_hwm"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "")
	for _, mid := range []string{
		"msg_00000000000000000000a1",
		"msg_00000000000000000000b2",
		"msg_00000000000000000000c3",
	} {
		b.AddMessage(sid, mid, opencodetest.UserTextMessage("m-"+mid))
	}

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), sid, "msg_00000000000000000000a1")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("got %d envs, want 2 (HWM filters strictly >)", len(envs))
	}
	info0, _ := ocPeekInfo(envs[0].Info)
	if info0.ID != "msg_00000000000000000000b2" {
		t.Errorf("first env id = %q, want msg_...b2", info0.ID)
	}
}

// TestReadSessionPreservesOtherFields asserts that fields *other than*
// id/sessionID/messageID round-trip verbatim — the reader injects identity
// without disturbing anything else (role, finish, cost, tokens, tool state).
func TestReadSessionPreservesOtherFields(t *testing.T) {
	const sid = "ses_test_preserve"
	const mid = "msg_test"
	const pid = "prt_test"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "")
	msgEnvelope := opencodetest.AssistantMessageFinished("stop")
	msgEnvelope["cost"] = 0.0123
	msgEnvelope["modelID"] = "test-model"
	msgEnvelope["providerID"] = "test-provider"
	b.AddMessage(sid, mid, msgEnvelope)
	b.AddPart(mid, pid, opencodetest.ToolPartCompleted("bash",
		map[string]any{"command": "ls"}, "file1\nfile2\n"))

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), sid, "")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}
	if len(envs) != 1 || len(envs[0].Parts) != 1 {
		t.Fatalf("envs/parts = %d/%d, want 1/1", len(envs), len(envs[0].Parts))
	}

	var info map[string]any
	_ = json.Unmarshal(envs[0].Info, &info)
	if got, _ := info["modelID"].(string); got != "test-model" {
		t.Errorf("info.modelID = %q, want test-model", got)
	}
	if got, _ := info["providerID"].(string); got != "test-provider" {
		t.Errorf("info.providerID = %q, want test-provider", got)
	}
	if got, _ := info["cost"].(float64); got != 0.0123 {
		t.Errorf("info.cost = %v, want 0.0123", got)
	}

	var part map[string]any
	_ = json.Unmarshal(envs[0].Parts[0], &part)
	if got, _ := part["tool"].(string); got != "bash" {
		t.Errorf("part.tool = %q, want bash", got)
	}
	state, _ := part["state"].(map[string]any)
	if state == nil {
		t.Fatalf("part.state missing")
	}
	if got, _ := state["status"].(string); got != "completed" {
		t.Errorf("part.state.status = %q, want completed", got)
	}
	if got, _ := state["output"].(string); !strings.Contains(got, "file1") {
		t.Errorf("part.state.output = %q, want to contain 'file1'", got)
	}
}

// TestMaterializedEnvelopeBackendCompatible builds a session with shapes
// mirroring real OpenCode rows, materializes via the reader, and asserts
// every envelope round-trips through a minimal local Go struct matching
// the backend's OpenCodeMessage / OpenCodePart field set. Acts as the
// shape-fidelity sentinel: catches reader-vs-backend skew without
// depending on real user data.
func TestMaterializedEnvelopeBackendCompatible(t *testing.T) {
	const sid = "ses_compat"
	b := opencodetest.NewDB(t)
	b.AddSession(sid, "")
	b.AddMessage(sid, "msg_compat_user", opencodetest.UserTextMessage("hello"))
	b.AddPart("msg_compat_user", "prt_compat_user_text", opencodetest.TextPart("hello"))

	asst := opencodetest.AssistantMessageFinished("tool-calls")
	asst["cost"] = 0.001
	asst["modelID"] = "model-x"
	asst["providerID"] = "prov-y"
	b.AddMessage(sid, "msg_compat_asst", asst)
	b.AddPart("msg_compat_asst", "prt_compat_asst_reason", opencodetest.ReasoningPart("think"))
	b.AddPart("msg_compat_asst", "prt_compat_asst_tool",
		opencodetest.ToolPartCompleted("read", map[string]any{"filePath": "/x"}, "data"))
	b.AddPart("msg_compat_asst", "prt_compat_asst_step", opencodetest.StepFinishPart())

	r := NewOpenCodeDBReader(b.Path())
	envs, err := r.ReadSession(context.Background(), sid, "")
	if err != nil {
		t.Fatalf("ReadSession: %v", err)
	}

	// Minimal mirror of confab-web's OpenCodeMessage / OpenCodePart.
	type backendPart struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		MessageID string `json:"messageID"`
		Tool      string `json:"tool,omitempty"`
		Text      string `json:"text,omitempty"`
	}
	type backendInfo struct {
		ID         string  `json:"id"`
		SessionID  string  `json:"sessionID"`
		Role       string  `json:"role"`
		Finish     *string `json:"finish,omitempty"`
		ModelID    string  `json:"modelID,omitempty"`
		ProviderID string  `json:"providerID,omitempty"`
		Cost       float64 `json:"cost"`
	}

	for i, env := range envs {
		var info backendInfo
		if err := json.Unmarshal(env.Info, &info); err != nil {
			t.Errorf("env[%d] info unmarshal: %v", i, err)
		}
		if info.ID == "" || info.SessionID == "" {
			t.Errorf("env[%d] info.id=%q sessionID=%q both must be non-empty", i, info.ID, info.SessionID)
		}
		for j, rawPart := range env.Parts {
			var p backendPart
			if err := json.Unmarshal(rawPart, &p); err != nil {
				t.Errorf("env[%d].parts[%d] unmarshal: %v", i, j, err)
			}
			if p.ID == "" || p.SessionID == "" || p.MessageID == "" {
				t.Errorf("env[%d].parts[%d] id=%q sessionID=%q messageID=%q all must be non-empty",
					i, j, p.ID, p.SessionID, p.MessageID)
			}
		}
	}
}

// TestOpenCodeDBPathFollowsEnv asserts CONFAB_OPENCODE_DB overrides
// auto-detection. Tests rely on this hook; power users use it for debugging.
func TestOpenCodeDBPathFollowsEnv(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom.db")
	t.Setenv(OpenCodeDBEnv, custom)
	got, err := OpenCodeDBPath()
	if err != nil {
		t.Fatalf("OpenCodeDBPath: %v", err)
	}
	if got != custom {
		t.Errorf("OpenCodeDBPath = %q, want env override %q", got, custom)
	}
}

// TestOpenCodeDBPathDefaultsToXDG asserts the default path resolves under
// $XDG_DATA_HOME or ~/.local/share, matching where OpenCode actually
// writes the DB.
func TestOpenCodeDBPathDefaultsToXDG(t *testing.T) {
	t.Setenv(OpenCodeDBEnv, "")
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	got, err := OpenCodeDBPath()
	if err != nil {
		t.Fatalf("OpenCodeDBPath: %v", err)
	}
	want := filepath.Join(xdg, "opencode", "opencode.db")
	if got != want {
		t.Errorf("OpenCodeDBPath = %q, want %q", got, want)
	}
}

// TestOpenCodeDBPathFallsBackToHome asserts the ~/.local/share/opencode/
// fallback fires when XDG_DATA_HOME is unset.
func TestOpenCodeDBPathFallsBackToHome(t *testing.T) {
	t.Setenv(OpenCodeDBEnv, "")
	t.Setenv("XDG_DATA_HOME", "")
	got, err := OpenCodeDBPath()
	if err != nil {
		t.Fatalf("OpenCodeDBPath: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if got != want {
		t.Errorf("OpenCodeDBPath = %q, want %q", got, want)
	}
}


// CF-538 ListDescendants tests --------------------------------------------

// TestListDescendantsRecursive asserts ListDescendants walks the full
// session.parent_id tree at any depth, returns descendants in ULID lex
// order, and excludes the root itself.
func TestListDescendantsRecursive(t *testing.T) {
	const root = "ses_00000000000000000000root"
	const childA = "ses_00000000000000000000a000"
	const childB = "ses_00000000000000000000b000"
	const grandchildA1 = "ses_00000000000000000000a100"
	b := opencodetest.NewDB(t)
	b.AddSession(root, "").
		AddSession(childA, root).
		AddSession(childB, root).
		AddSession(grandchildA1, childA)

	r := NewOpenCodeDBReader(b.Path())
	ids, err := r.ListDescendants(context.Background(), root)
	if err != nil {
		t.Fatalf("ListDescendants: %v", err)
	}
	want := []string{childA, grandchildA1, childB}
	if len(ids) != len(want) {
		t.Fatalf("got %d descendants, want %d: %v", len(ids), len(want), ids)
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("ids[%d] = %q, want %q (full: %v)", i, ids[i], w, ids)
		}
	}
}

// TestListDescendantsEmpty asserts a root with no children returns an empty
// slice and nil error (not a sentinel error).
func TestListDescendantsEmpty(t *testing.T) {
	const root = "ses_lonely_root"
	b := opencodetest.NewDB(t)
	b.AddSession(root, "")

	r := NewOpenCodeDBReader(b.Path())
	ids, err := r.ListDescendants(context.Background(), root)
	if err != nil {
		t.Fatalf("ListDescendants: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("got %d descendants for childless root, want 0", len(ids))
	}
}

// TestListDescendantsDBMissing asserts a missing DB file returns
// (nil, nil) — graceful degradation so the daemon's tick loop can
// continue past a transient DB-absence without error noise.
func TestListDescendantsDBMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist", "opencode.db")
	r := NewOpenCodeDBReader(missing)
	ids, err := r.ListDescendants(context.Background(), "ses_x")
	if err == nil {
		t.Fatal("ListDescendants should error on missing DB (callers log Warn; we don't want silent 'no descendants')")
	}
	if len(ids) != 0 {
		t.Errorf("got %d ids, want 0", len(ids))
	}
}

// TestListDescendantsCapAt1000 asserts the recursive CTE is bounded to
// defend against pathological cycles or huge subagent trees.
func TestListDescendantsCapAt1000(t *testing.T) {
	const root = "ses_huge_root"
	b := opencodetest.NewDB(t)
	b.AddSession(root, "")
	for i := 0; i < 1500; i++ {
		b.AddSession(fmt.Sprintf("ses_%04d", i), root)
	}

	r := NewOpenCodeDBReader(b.Path())
	ids, err := r.ListDescendants(context.Background(), root)
	if err != nil {
		t.Fatalf("ListDescendants: %v", err)
	}
	if len(ids) != 1000 {
		t.Errorf("got %d descendants, want 1000 (LIMIT cap)", len(ids))
	}
}

// TestListDescendantsRootSelfExcluded asserts the root's own id never
// appears in the result, even if (pathologically) the DB has parent_id
// pointing at the root from another session AND somehow recurses back.
func TestListDescendantsRootSelfExcluded(t *testing.T) {
	const root = "ses_self_check_root"
	const child = "ses_self_check_child"
	b := opencodetest.NewDB(t)
	b.AddSession(root, "").AddSession(child, root)

	r := NewOpenCodeDBReader(b.Path())
	ids, err := r.ListDescendants(context.Background(), root)
	if err != nil {
		t.Fatalf("ListDescendants: %v", err)
	}
	for _, id := range ids {
		if id == root {
			t.Errorf("descendants list contains the root id %q", root)
		}
	}
}

// CF-549 ReadSessionInfo tests --------------------------------------------

// TestReadSessionInfoReturnsDirAndParent asserts ReadSessionInfo reads
// session.directory and session.parent_id from the OpenCode SQLite DB.
// The resume path in cmd/hook_sessionstart.go relies on this to recover
// cwd + parentID when the plugin only sent {session_id}.
func TestReadSessionInfoReturnsDirAndParent(t *testing.T) {
	const sid = "ses_info_basic"
	const parent = "ses_info_parent"
	const dir = "/home/user/proj"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(parent, "", "/home/other/proj")
	b.AddSessionWithDir(sid, parent, dir)

	r := NewOpenCodeDBReader(b.Path())
	gotDir, gotParent, err := r.ReadSessionInfo(context.Background(), sid)
	if err != nil {
		t.Fatalf("ReadSessionInfo: %v", err)
	}
	if gotDir != dir {
		t.Errorf("directory = %q, want %q", gotDir, dir)
	}
	if gotParent != parent {
		t.Errorf("parentID = %q, want %q", gotParent, parent)
	}
}

// TestReadSessionInfoRootSessionHasEmptyParent asserts a root session
// (NULL parent_id) returns "" for parentID via COALESCE in the query.
// The Opencode.ShouldSpawnForInput gate treats empty parentID as "root".
func TestReadSessionInfoRootSessionHasEmptyParent(t *testing.T) {
	const sid = "ses_info_root"
	const dir = "/work/root"
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir(sid, "", dir)

	r := NewOpenCodeDBReader(b.Path())
	gotDir, gotParent, err := r.ReadSessionInfo(context.Background(), sid)
	if err != nil {
		t.Fatalf("ReadSessionInfo: %v", err)
	}
	if gotDir != dir {
		t.Errorf("directory = %q, want %q", gotDir, dir)
	}
	if gotParent != "" {
		t.Errorf("parentID = %q, want \"\" for root session", gotParent)
	}
}

// TestReadSessionInfoNotFoundIsNotError asserts an unknown session id
// returns ("", "", nil) instead of an error. The hook handler then
// proceeds with best-effort defaults rather than failing the whole spawn.
func TestReadSessionInfoNotFoundIsNotError(t *testing.T) {
	b := opencodetest.NewDB(t)
	b.AddSessionWithDir("ses_other", "", "/somewhere")

	r := NewOpenCodeDBReader(b.Path())
	gotDir, gotParent, err := r.ReadSessionInfo(context.Background(), "ses_does_not_exist")
	if err != nil {
		t.Fatalf("ReadSessionInfo on missing id should not error, got %v", err)
	}
	if gotDir != "" || gotParent != "" {
		t.Errorf("got (%q, %q), want (\"\", \"\") for missing session", gotDir, gotParent)
	}
}

// TestReadSessionInfoMissingDBReturnsError asserts that ReadSessionInfo
// returns an error when the DB file does not exist, distinguishing
// "DB unavailable" (worth logging) from "session not in DB" (silent).
func TestReadSessionInfoMissingDBReturnsError(t *testing.T) {
	r := NewOpenCodeDBReader(filepath.Join(t.TempDir(), "nonexistent.db"))
	_, _, err := r.ReadSessionInfo(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error when DB file is missing, got nil")
	}
}
