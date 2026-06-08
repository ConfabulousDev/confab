package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/redactor"
)

func TestNewFileTracker(t *testing.T) {
	ft := NewFileTracker("/path/to/transcript.jsonl")

	if ft.transcriptPath != "/path/to/transcript.jsonl" {
		t.Errorf("expected transcriptPath '/path/to/transcript.jsonl', got %q", ft.transcriptPath)
	}
	expectedSubagentsDir := "/path/to/transcript/subagents"
	if ft.subagentsDir != expectedSubagentsDir {
		t.Errorf("expected subagentsDir %q, got %q", expectedSubagentsDir, ft.subagentsDir)
	}
	if ft.files == nil {
		t.Error("expected files map to be initialized")
	}
	if ft.knownAgentIDs == nil {
		t.Error("expected knownAgentIDs map to be initialized")
	}
}

func TestFileTracker_InitFromBackendState(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	ft := NewFileTracker(transcriptPath)

	state := map[string]FileState{
		"transcript.jsonl":     {LastSyncedLine: 100},
		"agent-abc12345.jsonl": {LastSyncedLine: 50},
		"agent-def67890.jsonl": {LastSyncedLine: 25},
	}

	ft.InitFromBackendState(state)

	files := ft.GetTrackedFiles()
	if len(files) != 3 {
		t.Errorf("expected 3 tracked files, got %d", len(files))
	}

	// Check transcript
	found := false
	for _, f := range files {
		if f.Name == "transcript.jsonl" {
			found = true
			if f.Type != "transcript" {
				t.Errorf("expected transcript type, got %q", f.Type)
			}
			if f.LastSyncedLine != 100 {
				t.Errorf("expected LastSyncedLine 100, got %d", f.LastSyncedLine)
			}
		}
	}
	if !found {
		t.Error("transcript not found in tracked files")
	}

	// Check agent files resolve to subagents directory
	for _, f := range files {
		if f.Type == "agent" {
			expectedPath := filepath.Join(ft.subagentsDir, f.Name)
			if f.Path != expectedPath {
				t.Errorf("expected agent path %q, got %q", expectedPath, f.Path)
			}
		}
	}
}

func TestFileTracker_ReadChunk_AllLines(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create test file with some lines
	content := `{"line": 1}
{"line": 2}
{"line": 3}
{"line": 4}
{"line": 5}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk == nil {
		t.Fatal("expected chunk, got nil")
	}

	if len(chunk.Lines) != 5 {
		t.Errorf("expected 5 lines, got %d", len(chunk.Lines))
	}
	if chunk.FirstLine != 1 {
		t.Errorf("expected FirstLine 1, got %d", chunk.FirstLine)
	}
	if chunk.NewOffset == 0 {
		t.Error("expected NewOffset to be set")
	}
}

func TestFileTracker_ReadChunk_Incremental(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	content := `{"line": 1}
{"line": 2}
{"line": 3}
{"line": 4}
{"line": 5}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 2}, // Backend has first 2 lines
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk == nil {
		t.Fatal("expected chunk, got nil")
	}

	if len(chunk.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(chunk.Lines))
	}
	if chunk.FirstLine != 3 {
		t.Errorf("expected FirstLine 3, got %d", chunk.FirstLine)
	}
	if chunk.Lines[0] != `{"line": 3}` {
		t.Errorf("expected first line to be '{\"line\": 3}', got %q", chunk.Lines[0])
	}
}

func TestFileTracker_ReadChunk_NoNewLines(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	content := `{"line": 1}
{"line": 2}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 2}, // Already synced all lines
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk != nil {
		t.Errorf("expected nil chunk when no new lines, got %+v", chunk)
	}
}

func TestFileTracker_ReadChunk_ExtractsAgentIDs(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Use new-format agent IDs: 17-char hex and compact format
	content := `{"type": "user", "toolUseResult": {"agentId": "a3eaf63159a07953f"}}
{"type": "assistant", "message": "hello"}
{"type": "user", "toolUseResult": {"agentId": "acompact-2aaa241e456ebc94"}}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if len(chunk.AgentIDs) != 2 {
		t.Errorf("expected 2 agent IDs, got %d", len(chunk.AgentIDs))
	}

	// Check that both IDs are present
	found := make(map[string]bool)
	for _, id := range chunk.AgentIDs {
		found[id] = true
	}
	if !found["a3eaf63159a07953f"] || !found["acompact-2aaa241e456ebc94"] {
		t.Errorf("expected agent IDs a3eaf63159a07953f and acompact-2aaa241e456ebc94, got %v", chunk.AgentIDs)
	}
}

// TestFileTracker_HasFileChanged_AfterDelete guards the "Can't stat -
// assume changed to be safe" branch at tracker.go:167. A regression
// that swallowed the os.Stat error and returned false would silently
// stop syncing files that were deleted-and-recreated.
//
// Bug-revealing test: if it fails, the SUT has the bug (CF-451 bug
// policy: fix in this PR).
func TestFileTracker_HasFileChanged_AfterDelete(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "doomed.jsonl")
	if err := os.WriteFile(testFile, []byte(`{"line": 1}`+"\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ft := NewFileTracker(filepath.Join(tmpDir, "transcript.jsonl"))
	tracked := &TrackedFile{
		Path:           testFile,
		Name:           "doomed.jsonl",
		Type:           "transcript",
		LastSyncedLine: 0,
	}
	// Sync once so the tracker has cached state.
	ft.UpdateAfterSync(tracked, 1, 12)
	if ft.HasFileChanged(tracked) {
		t.Fatal("setup: HasFileChanged returned true on freshly-synced file")
	}

	// Now delete the file. Per the SUT comment ("Can't stat - assume
	// changed to be safe"), HasFileChanged must return true.
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !ft.HasFileChanged(tracked) {
		t.Error("HasFileChanged(deleted file) = false; want true (safety default)")
	}
}

// TestFileTracker_ReadChunk_DoesNotExtractFromOtherFields verifies the
// agent-ID extractor is field-aware. A previous version of the test
// only exercised the positive case (JSON with toolUseResult.agentId
// present) and would have passed even if extraction matched any
// 17-char hex run.
func TestFileTracker_ReadChunk_DoesNotExtractFromOtherFields(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// agent-shaped IDs that are NOT inside toolUseResult.agentId:
	// - inside a different field name
	// - inside a message body (string content)
	// - inside an unrelated nested object
	content := `{"type":"user","content":"a3eaf63159a07953f"}
{"type":"system","unrelated":{"agentId":"a3eaf63159a07953f"}}
{"type":"user","message":{"text":"acompact-2aaa241e456ebc94 is just text"}}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	chunk, err := ft.ReadChunk(ft.GetTranscriptFile(), nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}
	if len(chunk.AgentIDs) != 0 {
		t.Errorf("expected 0 agent IDs (none inside toolUseResult.agentId), got %d: %v", len(chunk.AgentIDs), chunk.AgentIDs)
	}
}

// TestFileTracker_ReadChunk_AppliesRedactor verifies ReadChunk actually
// applies the redactor it's given. Every other ReadChunk test passes nil
// for the redactor, so a silent regression at this call site would slip
// through. Table-driven across Claude transcript, Codex event_msg, and
// Codex session_meta shapes to pin the provider-agnostic invariant the
// backend's Redactions analytics card relies on (CF-445).
func TestFileTracker_ReadChunk_AppliesRedactor(t *testing.T) {
	const secret = "SECRET-VALUE-123"

	useDefaults := false
	r, err := redactor.NewFromConfig(&config.RedactionConfig{
		UseDefaultPatterns: &useDefaults,
		Patterns: []config.RedactionPattern{
			{Name: "test-secret", Pattern: `SECRET-VALUE-\d+`, Type: "test"},
		},
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	cases := []struct {
		name string
		line string
	}{
		{
			name: "claude_user_message",
			line: `{"type":"user","message":"my key is SECRET-VALUE-123"}`,
		},
		{
			name: "codex_event_msg",
			line: `{"type":"event_msg","payload":{"type":"user_message","message":"my key is SECRET-VALUE-123"}}`,
		},
		{
			name: "codex_session_meta",
			line: `{"type":"session_meta","payload":{"cwd":"/tmp","model":"x","note":"secret embedded: SECRET-VALUE-123"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(transcriptPath, []byte(tc.line+"\n"), 0644); err != nil {
				t.Fatalf("write test file: %v", err)
			}

			ft := NewFileTracker(transcriptPath)
			ft.InitFromBackendState(map[string]FileState{
				"transcript.jsonl": {LastSyncedLine: 0},
			})

			chunk, err := ft.ReadChunk(ft.GetTranscriptFile(), r, DefaultMaxChunkBytes)
			if err != nil {
				t.Fatalf("ReadChunk: %v", err)
			}
			if chunk == nil || len(chunk.Lines) != 1 {
				t.Fatalf("expected 1 line, got chunk=%v", chunk)
			}
			got := chunk.Lines[0]
			if strings.Contains(got, secret) {
				t.Errorf("redactor did not scrub %q: %q", secret, got)
			}
			if !strings.Contains(got, "[REDACTED:TEST]") {
				t.Errorf("missing redaction marker in %q", got)
			}
		})
	}
}

func TestFileTracker_ReadChunk_ExtractsGitInfo(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	content := `{"type": "user", "message": "hello", "gitBranch": "main", "cwd": "/tmp/test"}
{"type": "assistant", "message": "hi"}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk.Metadata == nil {
		t.Fatal("expected metadata, got nil")
	}

	if chunk.Metadata.GitInfo == nil {
		t.Fatal("expected GitInfo, got nil")
	}

	if chunk.Metadata.GitInfo.Branch != "main" {
		t.Errorf("expected branch 'main', got %q", chunk.Metadata.GitInfo.Branch)
	}
}

// runGitInTracker is a tiny test helper to init/configure a real git repo
// the Codex session_meta path can detect remotes from. Kept local to this
// file rather than depending on pkg/git's test helpers.
func runGitInTracker(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initGitRepoForTracker initialises a temp git repo with an identity and
// one committed file on branch "main", mirroring pkg/git's test helper.
func initGitRepoForTracker(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitInTracker(t, dir, "init")
	runGitInTracker(t, dir, "config", "user.email", "test@example.com")
	runGitInTracker(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGitInTracker(t, dir, "add", "f.txt")
	runGitInTracker(t, dir, "commit", "-m", "init")
	runGitInTracker(t, dir, "branch", "-M", "main")
	return dir
}

func TestFileTracker_ReadChunk_CodexSessionMeta_ExtractsGitInfo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	// Real git repo with two remotes + tracking config — all four CF-494
	// resolver inputs must show up on the resulting chunk.
	repoDir := initGitRepoForTracker(t)
	runGitInTracker(t, repoDir, "remote", "add", "origin", "git@github.com:jackie/repo.git")
	runGitInTracker(t, repoDir, "remote", "add", "upstream", "git@github.com:ConfabulousDev/repo.git")
	runGitInTracker(t, repoDir, "config", "branch.main.remote", "upstream")

	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
	sessionMeta := fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`+"\n",
		repoDir)
	if err := os.WriteFile(transcriptPath, []byte(sessionMeta), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	chunk, err := ft.ReadChunk(ft.GetTranscriptFile(), nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if chunk == nil || chunk.Metadata == nil || chunk.Metadata.GitInfo == nil {
		t.Fatalf("expected chunk with GitInfo, got %+v", chunk)
	}
	gi := chunk.Metadata.GitInfo
	if gi.RepoURL != "git@github.com:jackie/repo.git" {
		t.Errorf("RepoURL = %q, want origin URL", gi.RepoURL)
	}
	if len(gi.Remotes) != 2 {
		t.Fatalf("Remotes = %+v, want 2 entries", gi.Remotes)
	}
	if gi.Remotes[0].Name != "origin" || gi.Remotes[1].Name != "upstream" {
		t.Errorf("Remotes order = [%s %s], want [origin upstream]",
			gi.Remotes[0].Name, gi.Remotes[1].Name)
	}
	if gi.Branch != "main" {
		t.Errorf("Branch = %q, want %q", gi.Branch, "main")
	}
	if gi.TrackingRemote != "upstream" {
		t.Errorf("TrackingRemote = %q, want %q", gi.TrackingRemote, "upstream")
	}
}

func TestFileTracker_ReadChunk_CodexSessionMeta_AgentFile_ExtractsGitInfo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	repoDir := t.TempDir()
	runGitInTracker(t, repoDir, "init")
	runGitInTracker(t, repoDir, "remote", "add", "origin", "git@github.com:owner/repo.git")

	// Build a tracker around a transcript, then register an agent file
	// pointing at a separate JSONL whose first line is session_meta.
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(""), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	agentPath := filepath.Join(tmpDir, "agent-codex-descendant.jsonl")
	sessionMeta, _ := json.Marshal(map[string]any{
		"type":    "session_meta",
		"payload": map[string]any{"cwd": repoDir},
	})
	if err := os.WriteFile(agentPath, append(sessionMeta, '\n'), 0644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})
	// Manually add the agent file as a tracked file — match how Codex
	// descendant rollouts are registered (Type=="agent").
	ft.files[filepath.Base(agentPath)] = &TrackedFile{
		Path:           agentPath,
		Name:           filepath.Base(agentPath),
		Type:           "agent",
		LastSyncedLine: 0,
	}

	chunk, err := ft.ReadChunk(ft.files[filepath.Base(agentPath)], nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if chunk == nil || chunk.Metadata == nil || chunk.Metadata.GitInfo == nil {
		t.Fatalf("expected chunk with GitInfo on agent file, got %+v", chunk)
	}
	if len(chunk.Metadata.GitInfo.Remotes) != 1 || chunk.Metadata.GitInfo.Remotes[0].Name != "origin" {
		t.Errorf("Remotes = %+v, want [origin]", chunk.Metadata.GitInfo.Remotes)
	}
}

func TestFileTracker_ByteOffset_Seeking(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create initial content
	content := `{"line": 1}
{"line": 2}
{"line": 3}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// First read - should get all 3 lines
	chunk1, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if len(chunk1.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(chunk1.Lines))
	}

	// Update state after "sync"
	ft.UpdateAfterSync(file, 3, chunk1.NewOffset)

	// Append more content
	f, err := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	f.WriteString(`{"line": 4}` + "\n")
	f.WriteString(`{"line": 5}` + "\n")
	f.Close()

	// Force file change detection
	file.LastModTime = file.LastModTime.Add(-1)

	// Second read - should only get lines 4-5 using byte offset
	chunk2, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}

	if chunk2 == nil {
		t.Fatal("expected chunk2, got nil")
	}

	if len(chunk2.Lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(chunk2.Lines))
	}
	if chunk2.FirstLine != 4 {
		t.Errorf("expected FirstLine 4, got %d", chunk2.FirstLine)
	}
	if chunk2.Lines[0] != `{"line": 4}` {
		t.Errorf("expected first line '{\"line\": 4}', got %q", chunk2.Lines[0])
	}
}

func TestFileTracker_HasFileChanged(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.jsonl")

	// Create initial file
	if err := os.WriteFile(testFile, []byte(`{"line": 1}`), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(filepath.Join(tmpDir, "transcript.jsonl"))
	tracked := &TrackedFile{
		Path:           testFile,
		Name:           "test.jsonl",
		Type:           "transcript",
		LastSyncedLine: 0,
	}

	// First call should return true (no cached state yet)
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true on first call")
	}

	// HasFileChanged does NOT cache values - only UpdateAfterSync does.
	// So calling it again should still return true (file still needs syncing)
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true again (no sync happened)")
	}

	// Simulate a successful sync - this updates the cached state
	ft.UpdateAfterSync(tracked, 1, 12)

	// Now HasFileChanged should return false (file synced, no new changes)
	if ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return false after successful sync")
	}

	// Modify file - should return true
	if err := os.WriteFile(testFile, []byte(`{"line": 1}{"line": 2}`), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true after file modification")
	}

	// Without a sync, should still return true (failed sync shouldn't prevent retry)
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true again (no sync after modification)")
	}

	// Simulate another successful sync
	ft.UpdateAfterSync(tracked, 2, 24)

	// Now should return false
	if ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return false after second successful sync")
	}
}

func TestFileTracker_DiscoverNewFiles(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create transcript (content doesn't matter for this test)
	if err := os.WriteFile(transcriptPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	ft := NewFileTracker(transcriptPath)

	// Create subagents directory and agent file
	os.MkdirAll(ft.subagentsDir, 0755)
	agentPath := filepath.Join(ft.subagentsDir, "agent-abc12345.jsonl")
	if err := os.WriteFile(agentPath, []byte(`{"line": 1}`), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	// Discover new agent
	newFiles := ft.DiscoverNewFiles([]string{"abc12345"})

	if len(newFiles) != 1 {
		t.Errorf("expected 1 new file, got %d", len(newFiles))
	}

	if len(newFiles) > 0 {
		if newFiles[0].Name != "agent-abc12345.jsonl" {
			t.Errorf("expected agent-abc12345.jsonl, got %q", newFiles[0].Name)
		}
		if newFiles[0].Type != "agent" {
			t.Errorf("expected type 'agent', got %q", newFiles[0].Type)
		}
		expectedPath := filepath.Join(ft.subagentsDir, "agent-abc12345.jsonl")
		if newFiles[0].Path != expectedPath {
			t.Errorf("expected path %q, got %q", expectedPath, newFiles[0].Path)
		}
	}

	// Second discovery with same ID should return nothing
	newFiles2 := ft.DiscoverNewFiles([]string{"abc12345"})
	if len(newFiles2) != 0 {
		t.Errorf("expected 0 new files on second call, got %d", len(newFiles2))
	}
}

func TestFileTracker_DiscoverNewFiles_MissingAgent(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	if err := os.WriteFile(transcriptPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	os.MkdirAll(ft.subagentsDir, 0755)

	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	// Try to discover agent that doesn't exist on disk
	newFiles := ft.DiscoverNewFiles([]string{"missing123"})

	if len(newFiles) != 0 {
		t.Errorf("expected 0 new files for missing agent, got %d", len(newFiles))
	}

	// Now create the file in subagents dir
	agentPath := filepath.Join(ft.subagentsDir, "agent-missing123.jsonl")
	if err := os.WriteFile(agentPath, []byte(`{"line": 1}`), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	// Call again - should now find the file since we re-check all known agent IDs
	newFiles2 := ft.DiscoverNewFiles([]string{}) // Empty list - just re-check known IDs
	if len(newFiles2) != 1 {
		t.Errorf("expected 1 new file after creation, got %d", len(newFiles2))
	}
}

func TestFileTracker_ReadChunk_MalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Mix of valid and invalid JSON
	content := `not valid json
{"type": "user", "toolUseResult": {"agentId": "a3eaf63159a07953f"}}
also not valid
{"type": "user", "gitBranch": "develop"}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	// Should still get all 4 lines
	if len(chunk.Lines) != 4 {
		t.Errorf("expected 4 lines, got %d", len(chunk.Lines))
	}

	// Should extract agent IDs from valid lines
	if len(chunk.AgentIDs) != 1 || chunk.AgentIDs[0] != "a3eaf63159a07953f" {
		t.Errorf("expected agent ID a3eaf63159a07953f, got %v", chunk.AgentIDs)
	}

	// Should extract git info into metadata
	if chunk.Metadata == nil || chunk.Metadata.GitInfo == nil || chunk.Metadata.GitInfo.Branch != "develop" {
		t.Errorf("expected branch 'develop', got %v", chunk.Metadata)
	}
}

func TestFileTracker_ReadChunk_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	if err := os.WriteFile(transcriptPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if chunk != nil {
		t.Errorf("expected nil chunk for empty file, got %+v", chunk)
	}
}

func TestFileTracker_ReadChunk_LargeLines(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create a line with a large message field (500KB)
	largeMessage := make([]byte, 500*1024)
	for i := range largeMessage {
		largeMessage[i] = 'a'
	}

	content := `{"type": "session-start"}
{"type": "assistant", "message": "` + string(largeMessage) + `"}
{"type": "user", "gitBranch": "main"}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(file, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read chunk: %v", err)
	}

	if len(chunk.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(chunk.Lines))
	}
}

func TestFileTracker_ReadChunk_ByteLimit(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create 10 lines of ~100 bytes each (~1KB total)
	var content string
	for i := 0; i < 10; i++ {
		content += `{"line":` + string(rune('0'+i)) + `,"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	}

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Use small limit (~300 bytes) to force chunking - should get ~3 lines per chunk
	maxBytes := 300

	// First read
	chunk1, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}

	if chunk1 == nil {
		t.Fatal("expected chunk1, got nil")
	}

	// Should have limited the chunk
	if len(chunk1.Lines) >= 10 {
		t.Errorf("expected chunk to be limited, but got all %d lines", len(chunk1.Lines))
	}
	if len(chunk1.Lines) < 1 {
		t.Errorf("expected at least 1 line in first chunk, got %d", len(chunk1.Lines))
	}

	t.Logf("First chunk: %d lines", len(chunk1.Lines))

	// Simulate sync
	ft.UpdateAfterSync(file, len(chunk1.Lines), chunk1.NewOffset)

	// Second read
	chunk2, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}

	if chunk2 == nil {
		t.Fatal("expected chunk2, got nil")
	}

	t.Logf("Second chunk: %d lines, FirstLine=%d", len(chunk2.Lines), chunk2.FirstLine)

	// FirstLine should continue from where we left off
	if chunk2.FirstLine != len(chunk1.Lines)+1 {
		t.Errorf("expected FirstLine %d, got %d", len(chunk1.Lines)+1, chunk2.FirstLine)
	}

	// Keep reading until done
	totalLines := len(chunk1.Lines) + len(chunk2.Lines)
	ft.UpdateAfterSync(file, chunk2.FirstLine+len(chunk2.Lines)-1, chunk2.NewOffset)

	for {
		chunk, err := ft.ReadChunk(file, nil, maxBytes)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if chunk == nil {
			break
		}
		totalLines += len(chunk.Lines)
		ft.UpdateAfterSync(file, chunk.FirstLine+len(chunk.Lines)-1, chunk.NewOffset)
	}

	// Total should be 10 lines
	if totalLines != 10 {
		t.Errorf("expected 10 total lines across all chunks, got %d", totalLines)
	}
}

func TestFileTracker_ReadChunk_SingleLineExceedsByteLimit(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create a line that's ~200 bytes
	content := `{"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	content += `{"line": 2}` + "\n"

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Use limit smaller than first line - should return an error
	maxBytes := 50

	_, err := ft.ReadChunk(file, nil, maxBytes)
	if err == nil {
		t.Fatal("expected error when line exceeds max chunk size, got nil")
	}

	// Error should mention the line number and sizes
	errStr := err.Error()
	if !strings.Contains(errStr, "line 1") || !strings.Contains(errStr, "exceeds max chunk size") {
		t.Errorf("expected error about line 1 exceeding max chunk size, got: %v", err)
	}
}

func TestFileTracker_ReadChunk_MiddleLineExceedsByteLimit(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create file where second line is too large
	content := `{"line": 1}` + "\n"
	content += `{"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	content += `{"line": 3}` + "\n"

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Limit allows first line but not second
	maxBytes := 50

	// First read should succeed with line 1
	chunk1, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if chunk1 == nil || len(chunk1.Lines) != 1 {
		t.Fatalf("expected 1 line in first chunk, got %v", chunk1)
	}

	ft.UpdateAfterSync(file, 1, chunk1.NewOffset)

	// Second read should fail on line 2
	_, err = ft.ReadChunk(file, nil, maxBytes)
	if err == nil {
		t.Fatal("expected error when line 2 exceeds max chunk size, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "line 2") || !strings.Contains(errStr, "exceeds max chunk size") {
		t.Errorf("expected error about line 2 exceeding max chunk size, got: %v", err)
	}
}

func TestFileTracker_HasFileChanged_ByteOffsetComparison(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.jsonl")

	// Create initial file with 3 lines
	content := `{"line": 1}
{"line": 2}
{"line": 3}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	info, _ := os.Stat(testFile)
	fileSize := info.Size()

	ft := NewFileTracker(filepath.Join(tmpDir, "transcript.jsonl"))

	// Simulate a file that's been partially synced with ByteOffset set
	tracked := &TrackedFile{
		Path:           testFile,
		Name:           "test.jsonl",
		Type:           "transcript",
		LastSyncedLine: 2,
		ByteOffset:     fileSize / 2, // Pretend we've read half the file
		LastModTime:    info.ModTime(),
		LastSize:       fileSize,
	}

	// File hasn't changed and ByteOffset < size, so there's more to read
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true when ByteOffset < file size")
	}

	// Now set ByteOffset to end of file
	tracked.ByteOffset = fileSize
	if ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return false when ByteOffset == file size and file unchanged")
	}

	// Append more data - file size increases, ByteOffset stays same
	f, _ := os.OpenFile(testFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"line": 4}` + "\n")
	f.Close()

	// ByteOffset < new size, so should return true
	if !ft.HasFileChanged(tracked) {
		t.Error("expected HasFileChanged to return true after file was appended")
	}
}

func TestFileTracker_ReadChunk_ByteLimitWithFileAppend(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create initial 5 lines of ~100 bytes each
	var content string
	for i := 0; i < 5; i++ {
		content += `{"line":` + string(rune('0'+i)) + `,"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n"
	}

	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Use limit that fits ~2 lines
	maxBytes := 220

	// First read - should get ~2 lines
	chunk1, err := ft.ReadChunk(file, nil, maxBytes)
	if err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if chunk1 == nil || len(chunk1.Lines) < 1 {
		t.Fatal("expected chunk1 with at least 1 line")
	}

	firstChunkLines := len(chunk1.Lines)
	t.Logf("First chunk: %d lines", firstChunkLines)
	ft.UpdateAfterSync(file, firstChunkLines, chunk1.NewOffset)

	// Append more lines to the file WHILE we have pending data
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	for i := 5; i < 8; i++ {
		f.WriteString(`{"line":` + string(rune('0'+i)) + `,"data":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}` + "\n")
	}
	f.Close()

	// Continue reading - should get remaining original lines plus new lines
	totalLines := firstChunkLines
	for {
		chunk, err := ft.ReadChunk(file, nil, maxBytes)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if chunk == nil {
			break
		}
		totalLines += len(chunk.Lines)
		ft.UpdateAfterSync(file, chunk.FirstLine+len(chunk.Lines)-1, chunk.NewOffset)
	}

	// Should have all 8 lines (5 original + 3 appended)
	if totalLines != 8 {
		t.Errorf("expected 8 total lines, got %d", totalLines)
	}
}

// TestFileTracker_DiscoverNewFiles_DirectoryScan tests that DiscoverNewFiles
// finds agent files in the subagents directory even without matching agent IDs
// from transcript parsing (e.g., after daemon restart).
func TestFileTracker_DiscoverNewFiles_DirectoryScan(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	if err := os.WriteFile(transcriptPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	os.MkdirAll(ft.subagentsDir, 0755)

	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	// Create agent files in subagents dir without providing their IDs
	for _, name := range []string{"agent-a3eaf63159a07953f.jsonl", "agent-acompact-2aaa241e456ebc94.jsonl"} {
		path := filepath.Join(ft.subagentsDir, name)
		if err := os.WriteFile(path, []byte(`{"line": 1}`+"\n"), 0644); err != nil {
			t.Fatalf("failed to write agent file: %v", err)
		}
	}

	// Discover with NO agent IDs — directory scan should find them
	newFiles := ft.DiscoverNewFiles(nil)

	if len(newFiles) != 2 {
		t.Errorf("expected 2 new files from directory scan, got %d", len(newFiles))
	}

	found := make(map[string]bool)
	for _, f := range newFiles {
		found[f.Name] = true
		if f.Type != "agent" {
			t.Errorf("expected type 'agent', got %q", f.Type)
		}
	}
	if !found["agent-a3eaf63159a07953f.jsonl"] {
		t.Error("expected agent-a3eaf63159a07953f.jsonl to be discovered")
	}
	if !found["agent-acompact-2aaa241e456ebc94.jsonl"] {
		t.Error("expected agent-acompact-2aaa241e456ebc94.jsonl to be discovered")
	}
}

// TestFileTracker_NewFormatAgentID_EndToEnd is a regression test that exercises
// a realistic 17-char hex agent ID through the full discover+read path with
// the subagents directory.
func TestFileTracker_NewFormatAgentID_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Transcript references a 17-char hex agent
	content := `{"type":"system","message":"start"}
{"type":"user","toolUseResult":{"agentId":"a3eaf63159a07953f","result":"done"}}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	os.MkdirAll(ft.subagentsDir, 0755)

	// Create agent file in subagents dir
	agentPath := filepath.Join(ft.subagentsDir, "agent-a3eaf63159a07953f.jsonl")
	agentContent := `{"type":"agent","message":"hello from new-format agent"}
`
	if err := os.WriteFile(agentPath, []byte(agentContent), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	// Read transcript chunk — should extract the 17-char agent ID
	transcriptFile := ft.GetTranscriptFile()
	chunk, err := ft.ReadChunk(transcriptFile, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read transcript chunk: %v", err)
	}
	if len(chunk.AgentIDs) != 1 || chunk.AgentIDs[0] != "a3eaf63159a07953f" {
		t.Fatalf("expected agent ID a3eaf63159a07953f, got %v", chunk.AgentIDs)
	}

	// Discover the agent file
	newFiles := ft.DiscoverNewFiles(chunk.AgentIDs)
	if len(newFiles) != 1 {
		t.Fatalf("expected 1 new file, got %d", len(newFiles))
	}
	if newFiles[0].Name != "agent-a3eaf63159a07953f.jsonl" {
		t.Errorf("expected agent-a3eaf63159a07953f.jsonl, got %q", newFiles[0].Name)
	}
	if newFiles[0].Path != agentPath {
		t.Errorf("expected path %q, got %q", agentPath, newFiles[0].Path)
	}

	// Read the agent file
	agentChunk, err := ft.ReadChunk(newFiles[0], nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read agent chunk: %v", err)
	}
	if len(agentChunk.Lines) != 1 {
		t.Errorf("expected 1 agent line, got %d", len(agentChunk.Lines))
	}
}

// TestFileTracker_InitFromBackendState_ReadableAgentFile tests that when
// InitFromBackendState sets up an agent file that exists on disk in the
// subagents directory, it can actually be read via ReadChunk with correct
// incremental state.
func TestFileTracker_InitFromBackendState_ReadableAgentFile(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	if err := os.WriteFile(transcriptPath, []byte(`{"type":"system"}`+"\n"), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	ft := NewFileTracker(transcriptPath)

	// Create subagents directory and agent file with 3 lines
	os.MkdirAll(ft.subagentsDir, 0755)
	agentContent := `{"type":"agent","line":1}
{"type":"agent","line":2}
{"type":"agent","line":3}
`
	agentPath := filepath.Join(ft.subagentsDir, "agent-a3eaf63159a07953f.jsonl")
	if err := os.WriteFile(agentPath, []byte(agentContent), 0644); err != nil {
		t.Fatalf("failed to write agent file: %v", err)
	}

	// Backend says it already has the first line of the agent file
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl":              {LastSyncedLine: 1},
		"agent-a3eaf63159a07953f.jsonl": {LastSyncedLine: 1},
	})

	// Find the agent file in tracked files
	var agentFile *TrackedFile
	for _, f := range ft.GetTrackedFiles() {
		if f.Name == "agent-a3eaf63159a07953f.jsonl" {
			agentFile = f
			break
		}
	}
	if agentFile == nil {
		t.Fatal("agent file not found in tracked files")
	}

	// Verify path points to subagentsDir
	if agentFile.Path != agentPath {
		t.Errorf("expected path %q, got %q", agentPath, agentFile.Path)
	}

	// Read chunk — should get only lines 2-3 (line 1 already synced)
	chunk, err := ft.ReadChunk(agentFile, nil, DefaultMaxChunkBytes)
	if err != nil {
		t.Fatalf("failed to read agent chunk: %v", err)
	}
	if chunk == nil {
		t.Fatal("expected chunk, got nil")
	}
	if chunk.FirstLine != 2 {
		t.Errorf("expected FirstLine 2, got %d", chunk.FirstLine)
	}
	if len(chunk.Lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(chunk.Lines))
	}
}

func TestFileTracker_ReadChunk_ByteLimitRespectsLineNumber(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	// Create 6 lines with varying sizes
	content := `{"line": 1, "short": true}
{"line": 2, "data": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}
{"line": 3, "short": true}
{"line": 4, "data": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}
{"line": 5, "short": true}
{"line": 6, "short": true}
`
	if err := os.WriteFile(transcriptPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ft := NewFileTracker(transcriptPath)
	ft.InitFromBackendState(map[string]FileState{
		"transcript.jsonl": {LastSyncedLine: 0},
	})

	file := ft.GetTranscriptFile()

	// Read all chunks and verify line numbers are correct
	maxBytes := 150

	var allChunks []*Chunk
	for {
		chunk, err := ft.ReadChunk(file, nil, maxBytes)
		if err != nil {
			t.Fatalf("read failed: %v", err)
		}
		if chunk == nil {
			break
		}
		allChunks = append(allChunks, chunk)
		ft.UpdateAfterSync(file, chunk.FirstLine+len(chunk.Lines)-1, chunk.NewOffset)
	}

	// Verify FirstLine values are consecutive
	expectedLine := 1
	for i, chunk := range allChunks {
		if chunk.FirstLine != expectedLine {
			t.Errorf("chunk %d: expected FirstLine %d, got %d", i, expectedLine, chunk.FirstLine)
		}
		expectedLine += len(chunk.Lines)
	}

	// Verify we got all 6 lines
	if expectedLine != 7 {
		t.Errorf("expected to end at line 7, ended at %d", expectedLine)
	}
}

// ============================================================================
// Codex rollout tracking (CF-387)
// ============================================================================

func TestFileTracker_AddCodexRollout_RootAsTranscriptType(t *testing.T) {
	tr := NewFileTracker("/irrelevant.jsonl")
	meta := CodexRolloutMetadata{
		ThreadUUID:  "root-uuid",
		RolloutPath: "/codex/sessions/.../rollout-root.jsonl",
	}
	got := tr.AddCodexRollout("/codex/sessions/.../rollout-root.jsonl", "rollout-root.jsonl", true, meta)
	if got.Type != "transcript" {
		t.Errorf("Type = %q, want transcript (isRoot=true)", got.Type)
	}
	if got.Name != "rollout-root.jsonl" {
		t.Errorf("Name = %q, want rollout-root.jsonl", got.Name)
	}
	if got.CodexRollout == nil || got.CodexRollout.ThreadUUID != "root-uuid" {
		t.Errorf("CodexRollout not preserved on tracked file: %+v", got.CodexRollout)
	}
}

func TestFileTracker_AddCodexRollout_ChildAsAgentType(t *testing.T) {
	tr := NewFileTracker("/irrelevant.jsonl")
	meta := CodexRolloutMetadata{
		ThreadUUID:       "child-uuid",
		ParentThreadUUID: "root-uuid",
		RolloutPath:      "/codex/sessions/.../rollout-child.jsonl",
		AgentRole:        "planner",
	}
	got := tr.AddCodexRollout("/codex/sessions/.../rollout-child.jsonl", "rollout-child.jsonl", false, meta)
	if got.Type != "agent" {
		t.Errorf("Type = %q, want agent (isRoot=false)", got.Type)
	}
	if got.CodexRollout.ParentThreadUUID != "root-uuid" {
		t.Errorf("ParentThreadUUID = %q, want root-uuid", got.CodexRollout.ParentThreadUUID)
	}
	if got.CodexRollout.AgentRole != "planner" {
		t.Errorf("AgentRole = %q, want planner", got.CodexRollout.AgentRole)
	}
}

func TestFileTracker_AddCodexRollout_IdempotentOnRepeatedAdd_SamePath(t *testing.T) {
	tr := NewFileTracker("/irrelevant.jsonl")
	meta := CodexRolloutMetadata{ThreadUUID: "x", RolloutPath: "/path.jsonl"}
	first := tr.AddCodexRollout("/path.jsonl", "path.jsonl", true, meta)
	// Pretend the second call has different (wrong) metadata — first call should win.
	wrongMeta := CodexRolloutMetadata{ThreadUUID: "y", RolloutPath: "/path.jsonl"}
	second := tr.AddCodexRollout("/path.jsonl", "path.jsonl", false, wrongMeta)
	if first != second {
		t.Errorf("second AddCodexRollout returned a different *TrackedFile; expected idempotent")
	}
	if second.CodexRollout.ThreadUUID != "x" {
		t.Errorf("metadata mutated on second call: got %q, want x", second.CodexRollout.ThreadUUID)
	}
	if got := len(tr.GetTrackedFiles()); got != 1 {
		t.Errorf("tracked files = %d, want 1", got)
	}
}

func TestFileTracker_AddCodexRollout_DistinctPaths_AddsBoth(t *testing.T) {
	tr := NewFileTracker("/irrelevant.jsonl")
	tr.AddCodexRollout("/a.jsonl", "a.jsonl", true, CodexRolloutMetadata{ThreadUUID: "a", RolloutPath: "/a.jsonl"})
	tr.AddCodexRollout("/b.jsonl", "b.jsonl", false, CodexRolloutMetadata{ThreadUUID: "b", RolloutPath: "/b.jsonl", ParentThreadUUID: "a"})
	if got := len(tr.GetTrackedFiles()); got != 2 {
		t.Errorf("tracked files = %d, want 2", got)
	}
}

// Codex descendant-discovery tests migrated to pkg/provider/codex_dispatch_test.go
// as part of CF-397 (engine became provider-agnostic; the discovery method
// moved from FileTracker to provider.Codex). FileTracker now exposes only
// generic primitives (IsTracked, AddCodexRollout, RegisterCodexRollout);
// the dispatch-through-stub tests live with their implementation.

// ---- Workflow file registration (CF-533) ----

// Spec: SubagentsDir exposes <session>/subagents for the provider to scan.
func TestFileTracker_SubagentsDir(t *testing.T) {
	tr := NewFileTracker("/projects/p/session-abc.jsonl")
	want := filepath.Join("/projects/p/session-abc", "subagents")
	if got := tr.SubagentsDir(); got != want {
		t.Errorf("SubagentsDir() = %q, want %q", got, want)
	}
}

// Spec: registering a brand-new workflow file returns true and tracks it with
// the path-encoded name, on-disk path, and given file type.
func TestFileTracker_RegisterSidechainFile_New(t *testing.T) {
	tr := NewFileTracker("/irrelevant.jsonl")
	name := "subagents/workflows/wf_run1/journal.jsonl"
	path := "/abs/subagents/workflows/wf_run1/journal.jsonl"

	if isNew := tr.RegisterSidechainFile(path, name, "workflow_journal"); !isNew {
		t.Error("RegisterSidechainFile returned false for a new file, want true")
	}
	if !tr.IsTracked(name) {
		t.Fatalf("file %q not tracked after register", name)
	}
	f := tr.files[name]
	if f.Path != path || f.Name != name || f.Type != "workflow_journal" {
		t.Errorf("tracked file = {Path:%q Name:%q Type:%q}, want {%q %q workflow_journal}",
			f.Path, f.Name, f.Type, path, name)
	}
}

// Spec: re-registering an already-tracked name returns false and overwrites
// Path+Type IN PLACE while preserving sync position (LastSyncedLine/ByteOffset).
func TestFileTracker_RegisterSidechainFile_OverwritesPreservingPosition(t *testing.T) {
	tr := NewFileTracker("/irrelevant.jsonl")
	name := "subagents/workflows/wf_run1/journal.jsonl"

	// First register (e.g. from a prior cycle), then advance sync position.
	tr.RegisterSidechainFile("/wrong/path/journal.jsonl", name, "agent")
	tr.files[name].LastSyncedLine = 7
	tr.files[name].ByteOffset = 123

	correctPath := "/abs/subagents/workflows/wf_run1/journal.jsonl"
	if isNew := tr.RegisterSidechainFile(correctPath, name, "workflow_journal"); isNew {
		t.Error("RegisterSidechainFile returned true for an existing file, want false")
	}
	f := tr.files[name]
	if f.Path != correctPath {
		t.Errorf("Path = %q, want %q (overwritten)", f.Path, correctPath)
	}
	if f.Type != "workflow_journal" {
		t.Errorf("Type = %q, want workflow_journal (corrected)", f.Type)
	}
	if f.LastSyncedLine != 7 || f.ByteOffset != 123 {
		t.Errorf("sync position = {line:%d off:%d}, want {7 123} (preserved)", f.LastSyncedLine, f.ByteOffset)
	}
	if got := len(tr.GetTrackedFiles()); got != 1 {
		t.Errorf("tracked files = %d, want 1 (in-place correction, no dup)", got)
	}
}
