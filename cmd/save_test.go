package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/codextest"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/sync"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"", 0, false},
		{"5d", 5 * 24 * time.Hour, false},
		{"12h", 12 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1d", 24 * time.Hour, false},
		{"invalid", 0, true},
		{"5x", 0, true},
		{"d", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error for input %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("For input %q: expected %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}

// saveTestBackend provides a mock backend for testing save commands
type saveTestBackend struct {
	initCount  int32
	chunkCount int32
	initReqs   []sync.InitRequest
}

func (b *saveTestBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/sync/init":
		atomic.AddInt32(&b.initCount, 1)
		var req sync.InitRequest
		json.NewDecoder(r.Body).Decode(&req)
		b.initReqs = append(b.initReqs, req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sync.InitResponse{
			SessionID: "internal-123",
			Files:     map[string]sync.FileState{},
		})

	case "/api/v1/sync/chunk":
		atomic.AddInt32(&b.chunkCount, 1)
		var req sync.ChunkRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sync.ChunkResponse{
			LastSyncedLine: req.FirstLine + len(req.Lines) - 1,
		})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func setupSaveTestEnv(t *testing.T, serverURL string) (tmpDir string, sessionID string, sessionPath string) {
	tmpDir = t.TempDir()

	// Set env vars
	t.Setenv("CONFAB_CLAUDE_DIR", tmpDir)

	confabDir := filepath.Join(tmpDir, ".confab")
	os.MkdirAll(confabDir, 0755)
	configPath := filepath.Join(confabDir, "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	configContent := `{"backend_url": "` + serverURL + `", "api_key": "test-key-12345678"}`
	os.WriteFile(configPath, []byte(configContent), 0644)

	projectsDir := filepath.Join(tmpDir, "projects")
	project1 := filepath.Join(projectsDir, "project1")
	os.MkdirAll(project1, 0755)

	sessionID = "aaaaaaaa-1111-1111-1111-111111111111"
	sessionPath = filepath.Join(project1, sessionID+".jsonl")
	os.WriteFile(sessionPath, []byte(`{"type":"test"}`+"\n"), 0644)

	return tmpDir, sessionID, sessionPath
}

func TestSaveSessionsByID(t *testing.T) {
	backend := &saveTestBackend{}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, sessionID, _ := setupSaveTestEnv(t, server.URL)

	t.Run("upload by full ID", func(t *testing.T) {
		atomic.StoreInt32(&backend.initCount, 0)
		atomic.StoreInt32(&backend.chunkCount, 0)
		backend.initReqs = nil

		err := saveSessionsByID([]string{sessionID})
		if err != nil {
			t.Fatalf("saveSessionsByID failed: %v", err)
		}

		if backend.initCount != 1 {
			t.Errorf("Expected 1 init call, got %d", backend.initCount)
		}
		if backend.chunkCount != 1 {
			t.Errorf("Expected 1 chunk call, got %d", backend.chunkCount)
		}
		if len(backend.initReqs) != 1 || backend.initReqs[0].Provider != "claude-code" {
			t.Fatalf("expected explicit claude-code provider in init request, got %#v", backend.initReqs)
		}
	})

	t.Run("upload by partial ID", func(t *testing.T) {
		atomic.StoreInt32(&backend.initCount, 0)
		atomic.StoreInt32(&backend.chunkCount, 0)

		err := saveSessionsByID([]string{"aaaaaaaa"})
		if err != nil {
			t.Fatalf("saveSessionsByID failed: %v", err)
		}

		if backend.initCount != 1 {
			t.Errorf("Expected 1 init call, got %d", backend.initCount)
		}
	})

	t.Run("upload multiple sessions", func(t *testing.T) {
		// Create second session
		tmpDir := t.TempDir()
		t.Setenv("CONFAB_CLAUDE_DIR", tmpDir)

		confabDir := filepath.Join(tmpDir, ".confab")
		os.MkdirAll(confabDir, 0755)
		configPath := filepath.Join(confabDir, "config.json")
		t.Setenv("CONFAB_CONFIG_PATH", configPath)

		configContent := `{"backend_url": "` + server.URL + `", "api_key": "test-key-12345678"}`
		os.WriteFile(configPath, []byte(configContent), 0644)

		projectsDir := filepath.Join(tmpDir, "projects")
		project1 := filepath.Join(projectsDir, "project1")
		os.MkdirAll(project1, 0755)

		sessionID1 := "aaaaaaaa-1111-1111-1111-111111111111"
		sessionID2 := "bbbbbbbb-2222-2222-2222-222222222222"
		os.WriteFile(filepath.Join(project1, sessionID1+".jsonl"), []byte(`{"type":"test"}`+"\n"), 0644)
		os.WriteFile(filepath.Join(project1, sessionID2+".jsonl"), []byte(`{"type":"test2"}`+"\n"), 0644)

		atomic.StoreInt32(&backend.initCount, 0)
		atomic.StoreInt32(&backend.chunkCount, 0)

		err := saveSessionsByID([]string{sessionID1, sessionID2})
		if err != nil {
			t.Fatalf("saveSessionsByID failed: %v", err)
		}

		if backend.initCount != 2 {
			t.Errorf("Expected 2 init calls, got %d", backend.initCount)
		}
	})

	t.Run("non-existent session continues", func(t *testing.T) {
		atomic.StoreInt32(&backend.initCount, 0)

		// Should not return error, just print error message
		err := saveSessionsByID([]string{"nonexistent", sessionID})
		if err != nil {
			t.Fatalf("saveSessionsByID should not fail: %v", err)
		}

		// Should still upload the valid session
		if backend.initCount != 1 {
			t.Errorf("Expected 1 init call (valid session only), got %d", backend.initCount)
		}
	})
}

func TestSaveSessionsByID_UploadError(t *testing.T) {
	// Server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, sessionID, _ := setupSaveTestEnv(t, server.URL)

	// Upload should continue even when individual uploads fail
	err := saveSessionsByID([]string{sessionID})
	if err != nil {
		t.Fatalf("saveSessionsByID should not fail on upload error: %v", err)
	}
}

func TestSaveSessionsByID_NoAuth(t *testing.T) {
	// Create temp directory without config
	tmpDir := t.TempDir()
	t.Setenv("CONFAB_CONFIG_PATH", filepath.Join(tmpDir, "nonexistent", "config.json"))

	err := saveSessionsByID([]string{"some-session"})
	if err == nil {
		t.Fatal("Expected auth error, got nil")
	}
}

func TestSaveCodexSessionsByID_UploadsWithCodexProvider(t *testing.T) {
	backend := &saveTestBackend{}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir := setupCodexSyncTestEnv(t)
	configPath := filepath.Join(tmpDir, ".confab", "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)
	configContent := `{"backend_url": "` + server.URL + `", "api_key": "test-key-12345678"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	sessionID := "cccccccc-3333-3333-3333-333333333333"
	writeCodexTestRollout(t, tmpDir, sessionID, `"thread_source":"user","cwd":"/work/user"`)

	if err := saveSessionsForProvider(provider.Codex{}, []string{"cccccccc"}); err != nil {
		t.Fatalf("saveSessionsByIDForProvider failed: %v", err)
	}

	if backend.initCount != 1 {
		t.Fatalf("expected 1 init call, got %d", backend.initCount)
	}
	if backend.chunkCount != 1 {
		t.Fatalf("expected 1 chunk call, got %d", backend.chunkCount)
	}
	if len(backend.initReqs) != 1 {
		t.Fatalf("expected 1 init request, got %d", len(backend.initReqs))
	}
	if got := backend.initReqs[0].Provider; got != "codex" {
		t.Fatalf("provider = %q, want codex", got)
	}
}

// setupCodexSaveEnv creates a codextest fixture and writes a config file
// pointing at the given backend URL.
func setupCodexSaveEnv(t *testing.T, backendURL string) *codextest.Fixture {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	confabDir := filepath.Join(tmpHome, ".confab")
	if err := os.MkdirAll(filepath.Join(confabDir, "sync"), 0o700); err != nil {
		t.Fatalf("mkdir confab dir: %v", err)
	}
	configPath := filepath.Join(confabDir, "config.json")
	cfg := `{"backend_url": "` + backendURL + `", "api_key": "test-key-12345678"}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFAB_CONFIG_PATH", configPath)
	return codextest.NewFixture(t)
}

// uuidStr returns a canonical UUID v4 string for use as a thread/session ID.
// Generated fresh per call so each test's identifiers are unique.
func uuidStr(t *testing.T, seed byte) string {
	t.Helper()
	// Deterministic UUIDs per seed make assertions easier to read.
	return "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaa" + string("0123456789abcdef"[seed>>4]) + string("0123456789abcdef"[seed&0xf])
}

func TestSaveCodex_RootUUID_UploadsRootAndAllChildren_RecordsChunksOnBackend(t *testing.T) {
	backend := &saveTestBackend{}
	server := httptest.NewServer(backend)
	defer server.Close()

	fixture := setupCodexSaveEnv(t, server.URL)

	rootID := uuidStr(t, 0x10)
	childA := uuidStr(t, 0x20)
	childB := uuidStr(t, 0x30)

	fixture.AddRoot(rootID).
		WithSessionMeta("/work", "gpt-5").
		WithUserMessage("hello")
	fixture.AddSubagent(rootID, childA, codextest.SubagentOpts{AgentRole: "a"}).
		WithSessionMeta("/work", "gpt-5").
		WithUserMessage("plan a")
	fixture.AddSubagent(rootID, childB, codextest.SubagentOpts{AgentRole: "b"}).
		WithSessionMeta("/work", "gpt-5").
		WithUserMessage("plan b")

	if err := saveSessionsForProvider(provider.Codex{}, []string{rootID}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if backend.initCount != 1 {
		t.Errorf("init = %d, want 1 (one session for the root tree)", backend.initCount)
	}
	if backend.chunkCount != 3 {
		t.Errorf("chunks = %d, want 3 (root + 2 children)", backend.chunkCount)
	}
	if backend.initReqs[0].Provider != "codex" {
		t.Errorf("provider = %q, want codex", backend.initReqs[0].Provider)
	}
	if backend.initReqs[0].ExternalID != rootID {
		t.Errorf("external_id = %q, want root %q", backend.initReqs[0].ExternalID, rootID)
	}
}

func TestSaveCodex_SubagentUUID_ResolvesToRoot_StillUploadsWholeTree(t *testing.T) {
	backend := &saveTestBackend{}
	server := httptest.NewServer(backend)
	defer server.Close()

	fixture := setupCodexSaveEnv(t, server.URL)

	rootID := uuidStr(t, 0x11)
	childID := uuidStr(t, 0x22)

	fixture.AddRoot(rootID).WithSessionMeta("/work", "gpt-5").WithUserMessage("hi root")
	fixture.AddSubagent(rootID, childID, codextest.SubagentOpts{AgentRole: "reviewer"}).
		WithSessionMeta("/work", "gpt-5").WithUserMessage("hi child")

	if err := saveSessionsForProvider(provider.Codex{}, []string{childID}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if backend.initReqs[0].ExternalID != rootID {
		t.Errorf("external_id = %q, want root %q (subagent should be transparently rewritten)",
			backend.initReqs[0].ExternalID, rootID)
	}
	if backend.chunkCount != 2 {
		t.Errorf("chunks = %d, want 2 (root + child synced together)", backend.chunkCount)
	}
}

func TestSaveCodex_MultipleSessionsArgs_Independent(t *testing.T) {
	backend := &saveTestBackend{}
	server := httptest.NewServer(backend)
	defer server.Close()

	fixture := setupCodexSaveEnv(t, server.URL)

	root1 := uuidStr(t, 0x40)
	root2 := uuidStr(t, 0x50)
	fixture.AddRoot(root1).WithSessionMeta("/a", "gpt-5").WithUserMessage("a")
	fixture.AddRoot(root2).WithSessionMeta("/b", "gpt-5").WithUserMessage("b")

	if err := saveSessionsForProvider(provider.Codex{}, []string{root1, root2}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if backend.initCount != 2 {
		t.Errorf("init = %d, want 2 (one per session arg)", backend.initCount)
	}
	if len(backend.initReqs) != 2 {
		t.Fatalf("got %d init reqs, want 2", len(backend.initReqs))
	}
	got := map[string]bool{
		backend.initReqs[0].ExternalID: true,
		backend.initReqs[1].ExternalID: true,
	}
	if !got[root1] || !got[root2] {
		t.Errorf("init external_ids = %v, want both %q and %q", got, root1, root2)
	}
}

func TestSaveCodex_OneSessionFails_OthersContinue(t *testing.T) {
	backend := &saveTestBackend{}
	server := httptest.NewServer(backend)
	defer server.Close()

	fixture := setupCodexSaveEnv(t, server.URL)

	// Only one valid session; the other arg is an unknown UUID.
	rootID := uuidStr(t, 0x60)
	fixture.AddRoot(rootID).WithSessionMeta("/work", "gpt-5").WithUserMessage("ok")

	unknown := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if err := saveSessionsForProvider(provider.Codex{}, []string{unknown, rootID}); err != nil {
		t.Fatalf("save should not return error on per-session failure: %v", err)
	}
	if backend.initCount != 1 {
		t.Errorf("init = %d, want 1 (valid root only)", backend.initCount)
	}
}

func TestSaveCodex_StateDBMissing_FallsBackToSingleRolloutSync_NoCrash(t *testing.T) {
	backend := &saveTestBackend{}
	server := httptest.NewServer(backend)
	defer server.Close()

	fixture := setupCodexSaveEnv(t, server.URL)
	rootID := uuidStr(t, 0x70)
	fixture.AddRoot(rootID).WithSessionMeta("/work", "gpt-5").WithUserMessage("lonely root")

	// Remove the state DB to simulate a brand-new install / no Codex DB.
	if err := os.Remove(fixture.StateDBPath); err != nil {
		t.Fatalf("remove state db: %v", err)
	}
	t.Setenv("CONFAB_CODEX_STATE_DB", "")
	// Provider caches the state DB path globally; reset so the missing-DB
	// branch is exercised on this test's first lookup.
	provider.ResetStateDBPathCacheForTest()

	if err := saveSessionsForProvider(provider.Codex{}, []string{rootID}); err != nil {
		t.Fatalf("save should still work with missing state DB: %v", err)
	}
	if backend.initCount != 1 {
		t.Errorf("init = %d, want 1", backend.initCount)
	}
	if backend.chunkCount != 1 {
		t.Errorf("chunks = %d, want 1 (root only — no DB to discover children)",
			backend.chunkCount)
	}
}

