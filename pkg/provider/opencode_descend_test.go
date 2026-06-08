package provider

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/opencodetest"
)

// fakeOpencodeRegistrar implements OpencodeDescendantRegistrar by recording
// every RegisterOpencodeChild call. Embeds nil for DescendantRegistrar's
// IsTracked + RegisterCodexRollout methods; the OpenCode provider should
// not call either of those, but the embed is needed to satisfy the type.
type fakeOpencodeRegistrar struct {
	mu       sync.Mutex
	children []registeredChild

	// trackedNames is consulted by IsTracked — lets tests pre-register a
	// name to verify idempotency (provider must skip already-tracked names).
	trackedNames map[string]bool
}

type registeredChild struct {
	id   string
	path string
}

func (f *fakeOpencodeRegistrar) IsTracked(fileName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trackedNames[fileName]
}

func (f *fakeOpencodeRegistrar) RegisterCodexRollout(string, string, bool, CodexRolloutMetadata) {
	// no-op; OpenCode never calls this on its own registrar
}

func (f *fakeOpencodeRegistrar) RegisterOpencodeChild(childID, localPath string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.children = append(f.children, registeredChild{id: childID, path: localPath})
}

func (f *fakeOpencodeRegistrar) registeredIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.children))
	for i, c := range f.children {
		out[i] = c.id
	}
	return out
}

// TestOpencodeDiscoverDescendantsRegistersChildren asserts that
// DiscoverDescendants enumerates descendants from the SQLite DB and calls
// RegisterOpencodeChild once per descendant.
func TestOpencodeDiscoverDescendantsRegistersChildren(t *testing.T) {
	const root = "ses_test_root_register"
	const childA = "ses_test_a_register"
	const childB = "ses_test_b_register"
	b := opencodetest.NewDB(t)
	b.AddSession(root, "").
		AddSession(childA, root).
		AddSession(childB, root)
	t.Setenv(OpenCodeDBEnv, b.Path())

	reg := &fakeOpencodeRegistrar{trackedNames: map[string]bool{}}
	if err := (Opencode{}).DiscoverDescendants(reg, root); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}

	ids := reg.registeredIDs()
	if len(ids) != 2 {
		t.Fatalf("registered %d children, want 2: %v", len(ids), ids)
	}
	// Children come back in ULID order; we don't care which is first, but
	// both must appear exactly once.
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	if seen[childA] != 1 || seen[childB] != 1 {
		t.Errorf("children counts = %v, want childA=1 childB=1", seen)
	}
}

// TestOpencodeDiscoverDescendantsPathLayoutNested asserts the localPath
// passed to RegisterOpencodeChild is nested under the root's directory:
// ~/.confab/opencode/<root>/children/<child>/messages.jsonl
func TestOpencodeDiscoverDescendantsPathLayoutNested(t *testing.T) {
	const root = "ses_test_path_root"
	const child = "ses_test_path_child"
	b := opencodetest.NewDB(t)
	b.AddSession(root, "").AddSession(child, root)
	t.Setenv(OpenCodeDBEnv, b.Path())

	reg := &fakeOpencodeRegistrar{trackedNames: map[string]bool{}}
	if err := (Opencode{}).DiscoverDescendants(reg, root); err != nil {
		t.Fatalf("DiscoverDescendants: %v", err)
	}

	if len(reg.children) != 1 {
		t.Fatalf("registered %d children, want 1", len(reg.children))
	}
	got := reg.children[0].path
	wantSuffix := filepath.Join("opencode", root, "children", child, "messages.jsonl")
	if filepath.Base(got) != "messages.jsonl" {
		t.Errorf("localPath %q does not end in messages.jsonl", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("localPath %q is not absolute", got)
	}
	// Path must contain the nested "opencode/<root>/children/<child>/messages.jsonl" suffix.
	matched, _ := filepath.Match("*"+string(filepath.Separator)+wantSuffix, got)
	if !matched && !hasSuffixPath(got, wantSuffix) {
		t.Errorf("localPath %q does not have suffix %q", got, wantSuffix)
	}
}

// hasSuffixPath is a tiny helper because filepath.Match doesn't handle
// nested separators in patterns reliably across platforms.
func hasSuffixPath(full, suffix string) bool {
	return len(full) >= len(suffix) && full[len(full)-len(suffix):] == suffix
}

// TestOpencodeDiscoverDescendantsDBMissing asserts a missing DB doesn't
// crash and doesn't propagate the error (so the daemon's sync cycle
// continues uninterrupted).
func TestOpencodeDiscoverDescendantsDBMissing(t *testing.T) {
	t.Setenv(OpenCodeDBEnv, filepath.Join(t.TempDir(), "does", "not", "exist.db"))

	reg := &fakeOpencodeRegistrar{trackedNames: map[string]bool{}}
	if err := (Opencode{}).DiscoverDescendants(reg, "ses_anything"); err != nil {
		t.Errorf("DiscoverDescendants returned %v for missing DB, want nil (graceful)", err)
	}
	if len(reg.children) != 0 {
		t.Errorf("registered %d children with missing DB, want 0", len(reg.children))
	}
}

// TestOpencodeDiscoverDescendantsPlainRegistrarLogsAndNoOps asserts that
// when the registrar is NOT an OpencodeDescendantRegistrar (e.g. a
// forgotten daemon setter), DiscoverDescendants returns nil and does
// nothing observable. The Warn log is the user-facing surface for
// misconfiguration; we verify it doesn't *break*, not that the log fires.
func TestOpencodeDiscoverDescendantsPlainRegistrarLogsAndNoOps(t *testing.T) {
	const root = "ses_test_plain_root"
	const child = "ses_test_plain_child"
	b := opencodetest.NewDB(t)
	b.AddSession(root, "").AddSession(child, root)
	t.Setenv(OpenCodeDBEnv, b.Path())

	// Plain *bareRegistrar satisfies DescendantRegistrar but NOT the OpenCode
	// extension. DiscoverDescendants must type-assert and degrade gracefully.
	plain := &bareRegistrar{}
	if err := (Opencode{}).DiscoverDescendants(plain, root); err != nil {
		t.Errorf("DiscoverDescendants returned %v for plain registrar, want nil", err)
	}
}

// bareRegistrar satisfies only DescendantRegistrar (not the OpenCode
// extension). Used by TestOpencodeDiscoverDescendantsPlainRegistrarLogsAndNoOps.
type bareRegistrar struct{}

func (*bareRegistrar) IsTracked(string) bool                                       { return false }
func (*bareRegistrar) RegisterCodexRollout(string, string, bool, CodexRolloutMetadata) {}
