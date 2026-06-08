package provider_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// fakeWorkflowRegistrar records RegisterSidechainFile calls and exposes a
// configurable subagents dir, satisfying provider.WorkflowRegistrar.
type fakeWorkflowRegistrar struct {
	subagentsDir string
	registered   []registeredWorkflowFile
	existing     map[string]bool // names treated as already-tracked → Register returns false
}

type registeredWorkflowFile struct {
	path     string
	name     string
	fileType string
}

func (f *fakeWorkflowRegistrar) SubagentsDir() string { return f.subagentsDir }

func (f *fakeWorkflowRegistrar) RegisterSidechainFile(path, name, fileType string) bool {
	f.registered = append(f.registered, registeredWorkflowFile{path, name, fileType})
	return !f.existing[name]
}

func (f *fakeWorkflowRegistrar) names() []string {
	out := make([]string, 0, len(f.registered))
	for _, r := range f.registered {
		out = append(out, r.name)
	}
	sort.Strings(out)
	return out
}

func (f *fakeWorkflowRegistrar) byName(name string) (registeredWorkflowFile, bool) {
	for _, r := range f.registered {
		if r.name == name {
			return r, true
		}
	}
	return registeredWorkflowFile{}, false
}

// writeWorkflowRun lays out a workflow run dir under subagentsDir/workflows/<runID>
// with the given file basenames (each gets a one-line body).
func writeWorkflowRun(t *testing.T, subagentsDir, runID string, files ...string) string {
	t.Helper()
	runDir := filepath.Join(subagentsDir, "workflows", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(runDir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return runDir
}

func allowAllWorkflowTypes(string) bool { return true }

// Spec: when allowed, every agent-<id>.jsonl is registered as file_type=agent
// and journal.jsonl as file_type=workflow_journal, both with forward-slash
// path-encoded backend names; meta.json / wf_*.json are skipped.
func TestClaudeDiscoverWorkflowFiles_RegistersAgentsAndJournalSkipsRest(t *testing.T) {
	subagents := filepath.Join(t.TempDir(), "subagents")
	runDir := writeWorkflowRun(t, subagents, "wf_run1",
		"agent-aaaaaaa11.jsonl",
		"agent-aaaaaaa11.meta.json", // skip
		"agent-bbbbbbb22.jsonl",
		"journal.jsonl",
		"wf_run1.json", // skip
	)
	reg := &fakeWorkflowRegistrar{subagentsDir: subagents}

	n, err := (provider.ClaudeCode{}).DiscoverWorkflowFiles(reg, allowAllWorkflowTypes)
	if err != nil {
		t.Fatalf("DiscoverWorkflowFiles: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3 (2 agents + 1 journal)", n)
	}

	want := []string{
		"subagents/workflows/wf_run1/agent-aaaaaaa11.jsonl",
		"subagents/workflows/wf_run1/agent-bbbbbbb22.jsonl",
		"subagents/workflows/wf_run1/journal.jsonl",
	}
	if got := reg.names(); !equalStrings(got, want) {
		t.Errorf("registered names = %v, want %v", got, want)
	}

	agent, _ := reg.byName("subagents/workflows/wf_run1/agent-aaaaaaa11.jsonl")
	if agent.fileType != "agent" {
		t.Errorf("agent file_type = %q, want \"agent\"", agent.fileType)
	}
	if want := filepath.Join(runDir, "agent-aaaaaaa11.jsonl"); agent.path != want {
		t.Errorf("agent path = %q, want %q", agent.path, want)
	}

	journal, _ := reg.byName("subagents/workflows/wf_run1/journal.jsonl")
	if journal.fileType != provider.FileTypeWorkflowJournal {
		t.Errorf("journal file_type = %q, want %q", journal.fileType, provider.FileTypeWorkflowJournal)
	}
}

// Spec: when no workflows dir exists, return (0, nil) WITHOUT invoking the
// allow predicate — so non-workflow sessions never trigger a backend probe.
func TestClaudeDiscoverWorkflowFiles_NoWorkflowsDir_DoesNotProbe(t *testing.T) {
	subagents := filepath.Join(t.TempDir(), "subagents")
	if err := os.MkdirAll(subagents, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := &fakeWorkflowRegistrar{subagentsDir: subagents}

	allowCalls := 0
	allow := func(string) bool { allowCalls++; return true }

	n, err := (provider.ClaudeCode{}).DiscoverWorkflowFiles(reg, allow)
	if err != nil {
		t.Fatalf("DiscoverWorkflowFiles: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
	if allowCalls != 0 {
		t.Errorf("allow called %d times, want 0 (no probe when no workflows dir)", allowCalls)
	}
	if len(reg.registered) != 0 {
		t.Errorf("registered %d files, want 0", len(reg.registered))
	}
}

// Spec: when the backend disallows a file type, that file is not registered.
func TestClaudeDiscoverWorkflowFiles_NotAllowed_RegistersNothing(t *testing.T) {
	subagents := filepath.Join(t.TempDir(), "subagents")
	writeWorkflowRun(t, subagents, "wf_run1", "agent-aaaaaaa11.jsonl", "journal.jsonl")
	reg := &fakeWorkflowRegistrar{subagentsDir: subagents}

	n, err := (provider.ClaudeCode{}).DiscoverWorkflowFiles(reg, func(string) bool { return false })
	if err != nil {
		t.Fatalf("DiscoverWorkflowFiles: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
	if len(reg.registered) != 0 {
		t.Errorf("registered %d files, want 0 when disallowed", len(reg.registered))
	}
}

// Spec: per-flag gating — agent transcripts upload when workflow_files is on
// even if the journal flag is off (and vice versa).
func TestClaudeDiscoverWorkflowFiles_PerTypeGate(t *testing.T) {
	subagents := filepath.Join(t.TempDir(), "subagents")
	writeWorkflowRun(t, subagents, "wf_run1", "agent-aaaaaaa11.jsonl", "journal.jsonl")
	reg := &fakeWorkflowRegistrar{subagentsDir: subagents}

	// Allow agents, deny journal.
	allow := func(ft string) bool { return ft == "agent" }
	n, err := (provider.ClaudeCode{}).DiscoverWorkflowFiles(reg, allow)
	if err != nil {
		t.Fatalf("DiscoverWorkflowFiles: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1 (agent only)", n)
	}
	if _, ok := reg.byName("subagents/workflows/wf_run1/agent-aaaaaaa11.jsonl"); !ok {
		t.Error("expected agent transcript to be registered")
	}
	if _, ok := reg.byName("subagents/workflows/wf_run1/journal.jsonl"); ok {
		t.Error("journal must be skipped when workflow_journal is disallowed")
	}
}

// Spec: idempotent — an already-tracked file (RegisterSidechainFile returns
// false) is not double-counted, but is still re-registered (path/type fix).
func TestClaudeDiscoverWorkflowFiles_Idempotent(t *testing.T) {
	subagents := filepath.Join(t.TempDir(), "subagents")
	writeWorkflowRun(t, subagents, "wf_run1", "agent-aaaaaaa11.jsonl", "journal.jsonl")
	reg := &fakeWorkflowRegistrar{
		subagentsDir: subagents,
		existing: map[string]bool{
			"subagents/workflows/wf_run1/agent-aaaaaaa11.jsonl": true,
			"subagents/workflows/wf_run1/journal.jsonl":         true,
		},
	}

	n, err := (provider.ClaudeCode{}).DiscoverWorkflowFiles(reg, allowAllWorkflowTypes)
	if err != nil {
		t.Fatalf("DiscoverWorkflowFiles: %v", err)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 (all already tracked)", n)
	}
	if len(reg.registered) != 2 {
		t.Errorf("RegisterSidechainFile called %d times, want 2 (correction still happens)", len(reg.registered))
	}
}

// Spec: Codex has no workflow files; the no-op never touches the predicate.
func TestCodexDiscoverWorkflowFiles_NoOp(t *testing.T) {
	reg := &fakeWorkflowRegistrar{subagentsDir: t.TempDir()}
	allowCalls := 0
	n, err := (provider.Codex{}).DiscoverWorkflowFiles(reg, func(string) bool { allowCalls++; return true })
	if err != nil {
		t.Fatalf("DiscoverWorkflowFiles: %v", err)
	}
	if n != 0 || allowCalls != 0 || len(reg.registered) != 0 {
		t.Errorf("Codex no-op: n=%d allowCalls=%d registered=%d, want 0/0/0", n, allowCalls, len(reg.registered))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
