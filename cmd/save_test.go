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
}

func (b *saveTestBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/sync/init":
		atomic.AddInt32(&b.initCount, 1)
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
