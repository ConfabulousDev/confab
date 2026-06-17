package provider

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// fakeCursorRegistrar implements the registrar surface Cursor.DiscoverDescendants
// reaches for: DescendantRegistrar (IsTracked + RegisterCodexRollout, unused by
// Cursor), WorkflowRegistrar (SubagentsDir + RegisterSidechainFile), and the
// rootTranscriptProvider accessor that exposes the root transcript path. It
// records every RegisterSidechainFile call so tests can assert backend names,
// file types, and idempotency.
//
// rootTranscript is the absolute path to the root transcript file; the Cursor
// provider derives the subagents dir from filepath.Dir(rootTranscript)/subagents
// (NOT from SubagentsDir(), which is computed for Claude's layout).
type fakeCursorRegistrar struct {
	rootTranscript string
	subagentsDir   string // value returned by SubagentsDir() (Claude-shaped)

	tracked    map[string]bool // pre-tracked backend names (drives RegisterSidechainFile dedup)
	registered []sidechainCall
}

type sidechainCall struct {
	path     string
	name     string
	fileType string
}

func (f *fakeCursorRegistrar) IsTracked(name string) bool { return f.tracked[name] }

func (f *fakeCursorRegistrar) RegisterCodexRollout(string, string, bool, CodexRolloutMetadata) {}

func (f *fakeCursorRegistrar) SubagentsDir() string { return f.subagentsDir }

func (f *fakeCursorRegistrar) RootTranscriptPath() string { return f.rootTranscript }

func (f *fakeCursorRegistrar) RegisterSidechainFile(path, name, fileType string) bool {
	if f.tracked == nil {
		f.tracked = map[string]bool{}
	}
	if f.tracked[name] {
		return false // already tracked: in-place correction, not a new file
	}
	f.tracked[name] = true
	f.registered = append(f.registered, sidechainCall{path: path, name: name, fileType: fileType})
	return true
}

func (f *fakeCursorRegistrar) registeredNames() []string {
	out := make([]string, len(f.registered))
	for i, c := range f.registered {
		out[i] = c.name
	}
	sort.Strings(out)
	return out
}

// cursorTestTree lays out a Cursor session tree under a temp dir and returns the
// root transcript path. Subagents live at <root-dir>/subagents/<sub-id>.jsonl,
// a sibling of the root transcript file — the verified Cursor layout (kata 6kys).
func cursorTestTree(t *testing.T, subIDs ...string) (rootTranscript string) {
	t.Helper()
	root := "8b8e4d09-c7c9-402f-82c0-a61e11e7d0a5"
	sessionDir := filepath.Join(t.TempDir(), "projects", "ws", "agent-transcripts", root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	rootTranscript = filepath.Join(sessionDir, root+".jsonl")
	if err := os.WriteFile(rootTranscript, []byte(cursorUserLine("root")+"\n"), 0o644); err != nil {
		t.Fatalf("write root transcript: %v", err)
	}
	if len(subIDs) > 0 {
		subDir := filepath.Join(sessionDir, "subagents")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("mkdir subagents: %v", err)
		}
		for _, id := range subIDs {
			if err := os.WriteFile(filepath.Join(subDir, id+".jsonl"), []byte(cursorUserLine("child")+"\n"), 0o644); err != nil {
				t.Fatalf("write subagent %s: %v", id, err)
			}
		}
	}
	return rootTranscript
}

// regWithRoot builds a fake registrar whose SubagentsDir() deliberately returns
// the WRONG (Claude-shaped) path, so a test fails loudly if the provider relies
// on SubagentsDir() instead of deriving from the root transcript path.
func regWithRoot(rootTranscript string) *fakeCursorRegistrar {
	claudeShaped := filepath.Join(rootTranscript[:len(rootTranscript)-len(".jsonl")], "subagents")
	return &fakeCursorRegistrar{
		rootTranscript: rootTranscript,
		subagentsDir:   claudeShaped,
		tracked:        map[string]bool{},
	}
}

// TestCursorDiscoverDescendants_RegistersSubagentAsAgent is the core acceptance
// test: a subagent JSONL file under <root-dir>/subagents/ is registered as
// file_type=agent with backend file_name "subagents/<sub-id>.jsonl".
func TestCursorDiscoverDescendants_RegistersSubagentAsAgent(t *testing.T) {
	const subID = "9c4d4938-b532-406a-b0b4-9ad8e765e24c"
	rootTranscript := cursorTestTree(t, subID)
	reg := regWithRoot(rootTranscript)

	if err := (Cursor{}).DiscoverDescendants(reg, "8b8e4d09-c7c9-402f-82c0-a61e11e7d0a5"); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}

	if len(reg.registered) != 1 {
		t.Fatalf("registered %d files, want 1: %+v", len(reg.registered), reg.registered)
	}
	call := reg.registered[0]
	wantName := "subagents/" + subID + ".jsonl"
	if call.name != wantName {
		t.Errorf("backend file_name = %q, want %q", call.name, wantName)
	}
	if call.fileType != FileTypeAgent {
		t.Errorf("file_type = %q, want %q", call.fileType, FileTypeAgent)
	}
	wantPath := filepath.Join(filepath.Dir(rootTranscript), "subagents", subID+".jsonl")
	if call.path != wantPath {
		t.Errorf("local path = %q, want %q", call.path, wantPath)
	}
}

// TestCursorDiscoverDescendants_MultipleSubagents asserts every subagent file is
// registered, each as file_type=agent under subagents/<id>.jsonl.
func TestCursorDiscoverDescendants_MultipleSubagents(t *testing.T) {
	subA := "11111111-1111-1111-1111-111111111111"
	subB := "22222222-2222-2222-2222-222222222222"
	rootTranscript := cursorTestTree(t, subA, subB)
	reg := regWithRoot(rootTranscript)

	if err := (Cursor{}).DiscoverDescendants(reg, "root"); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}

	got := reg.registeredNames()
	want := []string{"subagents/" + subA + ".jsonl", "subagents/" + subB + ".jsonl"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("registered names = %v, want %v", got, want)
	}
	for _, c := range reg.registered {
		if c.fileType != FileTypeAgent {
			t.Errorf("file %q file_type = %q, want %q", c.name, c.fileType, FileTypeAgent)
		}
	}
}

// TestCursorDiscoverDescendants_Idempotent asserts a second discovery pass over
// an already-tracked subagent does not re-register it (RegisterSidechainFile
// returns false), so SyncAll re-scanning every cycle is a no-op once tracked.
func TestCursorDiscoverDescendants_Idempotent(t *testing.T) {
	const subID = "9c4d4938-b532-406a-b0b4-9ad8e765e24c"
	rootTranscript := cursorTestTree(t, subID)
	reg := regWithRoot(rootTranscript)

	for i := 0; i < 3; i++ {
		if err := (Cursor{}).DiscoverDescendants(reg, "root"); err != nil {
			t.Fatalf("DiscoverDescendants pass %d: %v", i, err)
		}
	}
	if len(reg.registered) != 1 {
		t.Fatalf("registered %d times across 3 passes, want 1", len(reg.registered))
	}
}

// TestCursorDiscoverDescendants_NoSubagentsDir asserts a session with no
// subagents/ directory (the common case) registers nothing and returns nil.
func TestCursorDiscoverDescendants_NoSubagentsDir(t *testing.T) {
	rootTranscript := cursorTestTree(t) // no subagents
	reg := regWithRoot(rootTranscript)

	if err := (Cursor{}).DiscoverDescendants(reg, "root"); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}
	if len(reg.registered) != 0 {
		t.Fatalf("registered %d files for a session with no subagents, want 0", len(reg.registered))
	}
}

// TestCursorDiscoverDescendants_IgnoresNonJSONL asserts only *.jsonl files in
// subagents/ are registered; meta/sidecar files and nested dirs are skipped.
func TestCursorDiscoverDescendants_IgnoresNonJSONL(t *testing.T) {
	const subID = "9c4d4938-b532-406a-b0b4-9ad8e765e24c"
	rootTranscript := cursorTestTree(t, subID)
	subDir := filepath.Join(filepath.Dir(rootTranscript), "subagents")
	if err := os.WriteFile(filepath.Join(subDir, subID+".meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(subDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	reg := regWithRoot(rootTranscript)

	if err := (Cursor{}).DiscoverDescendants(reg, "root"); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}
	if len(reg.registered) != 1 || reg.registered[0].name != "subagents/"+subID+".jsonl" {
		t.Fatalf("registered = %+v, want only subagents/%s.jsonl", reg.registered, subID)
	}
}

// TestCursorDiscoverDescendants_PlainRegistrarNoOps asserts that a registrar
// lacking the WorkflowRegistrar/rootTranscriptProvider surface is handled
// gracefully (no panic, no registration) — the type assertions guard the call.
func TestCursorDiscoverDescendants_PlainRegistrarNoOps(t *testing.T) {
	// plainReg satisfies only DescendantRegistrar.
	plain := plainDescendantRegistrar{}
	if err := (Cursor{}).DiscoverDescendants(plain, "root"); err != nil {
		t.Fatalf("DiscoverDescendants with plain registrar: %v", err)
	}
}

// plainDescendantRegistrar implements only DescendantRegistrar (no sidechain or
// root-path surface), modeling a hypothetical caller that can't capture
// sidechains. Cursor must no-op rather than panic.
type plainDescendantRegistrar struct{}

func (plainDescendantRegistrar) IsTracked(string) bool { return false }
func (plainDescendantRegistrar) RegisterCodexRollout(string, string, bool, CodexRolloutMetadata) {
}
