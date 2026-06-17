package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/opencodetest"
	"github.com/ConfabulousDev/confab/pkg/provider"
)

// setupOpencodeSaveEnv writes a config pointing at backendURL, sets HOME so
// materialized files land in a temp dir, and points CONFAB_OPENCODE_DB at the
// fixture. The returned builder seeds sessions.
func setupOpencodeSaveEnv(t *testing.T, backendURL string, caps capsResponder) *opencodetest.Builder {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	confabDir := filepath.Join(tmpHome, ".confab")
	if err := os.MkdirAll(confabDir, 0o700); err != nil {
		t.Fatalf("mkdir confab dir: %v", err)
	}
	configPath := filepath.Join(confabDir, "config.json")
	cfg := `{"backend_url": "` + backendURL + `", "api_key": "test-key-12345678"}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	b := opencodetest.NewDB(t)
	t.Setenv(provider.OpenCodeDBEnv, b.Path())
	return b
}

// capsResponder is a tiny enum for which capability flags the mock backend
// advertises on /api/v1/capabilities.
type capsResponder struct {
	opencodeSubagentFiles bool
}

// opencodeSaveBackend extends saveTestBackend with a capabilities endpoint so
// the engine's OpenCode child-file gating resolves.
type opencodeSaveBackend struct {
	saveTestBackend
	caps capsResponder
}

func (b *opencodeSaveBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/capabilities" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"opencode_subagent_files":` + boolJSON(b.caps.opencodeSubagentFiles) + `}`))
		return
	}
	b.saveTestBackend.ServeHTTP(w, r)
}

func boolJSON(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// TestSaveOpencode_RootOnly_Uploads asserts a root session with no children
// materializes and uploads as a single backend session.
func TestSaveOpencode_RootOnly_Uploads(t *testing.T) {
	backend := &opencodeSaveBackend{caps: capsResponder{opencodeSubagentFiles: true}}
	server := httptest.NewServer(backend)
	defer server.Close()

	b := setupOpencodeSaveEnv(t, server.URL, backend.caps)
	const root = "ses_save_root_only"
	b.AddSessionWithDir(root, "", "/work")
	b.AddMessage(root, "msg_00000000000000000000a1", opencodetest.UserTextMessage("hello"))
	b.AddPart("msg_00000000000000000000a1", "prt_a", opencodetest.TextPart("hello"))

	if err := saveViaDefault(t, provider.Opencode{}, []string{root}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if backend.initCount != 1 {
		t.Errorf("init = %d, want 1", backend.initCount)
	}
	if backend.chunkCount != 1 {
		t.Errorf("chunks = %d, want 1 (root only)", backend.chunkCount)
	}
	if len(backend.initReqs) != 1 || backend.initReqs[0].Provider != "opencode" {
		t.Fatalf("expected opencode provider in init, got %#v", backend.initReqs)
	}
	if backend.initReqs[0].ExternalID != root {
		t.Errorf("external_id = %q, want %q", backend.initReqs[0].ExternalID, root)
	}
}

// TestSaveOpencode_RootWithChildren_UploadsTree asserts root + descendants are
// materialized and uploaded as agent sidechains (full parity with live capture).
func TestSaveOpencode_RootWithChildren_UploadsTree(t *testing.T) {
	backend := &opencodeSaveBackend{caps: capsResponder{opencodeSubagentFiles: true}}
	server := httptest.NewServer(backend)
	defer server.Close()

	b := setupOpencodeSaveEnv(t, server.URL, backend.caps)
	const root = "ses_save_tree_root"
	const childA = "ses_save_tree_a"
	const childB = "ses_save_tree_b"
	b.AddSessionWithDir(root, "", "/work")
	b.AddSessionWithDir(childA, root, "/work")
	b.AddSessionWithDir(childB, root, "/work")
	for _, sid := range []string{root, childA, childB} {
		b.AddMessage(sid, "msg_"+sid, opencodetest.UserTextMessage("hi "+sid))
		b.AddPart("msg_"+sid, "prt_"+sid, opencodetest.TextPart("hi "+sid))
	}

	if err := saveViaDefault(t, provider.Opencode{}, []string{root}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if backend.initCount != 1 {
		t.Errorf("init = %d, want 1 (one session for the root tree)", backend.initCount)
	}
	if backend.chunkCount != 3 {
		t.Errorf("chunks = %d, want 3 (root + 2 children as agent sidechains)", backend.chunkCount)
	}
	if backend.initReqs[0].ExternalID != root {
		t.Errorf("external_id = %q, want root %q", backend.initReqs[0].ExternalID, root)
	}
}

// TestSaveOpencode_ChildID_ResolvesToRoot asserts saving a descendant id walks
// to the root and uploads the whole tree under the root session.
func TestSaveOpencode_ChildID_ResolvesToRoot(t *testing.T) {
	backend := &opencodeSaveBackend{caps: capsResponder{opencodeSubagentFiles: true}}
	server := httptest.NewServer(backend)
	defer server.Close()

	b := setupOpencodeSaveEnv(t, server.URL, backend.caps)
	const root = "ses_child_resolve_root"
	const child = "ses_child_resolve_child"
	b.AddSessionWithDir(root, "", "/work")
	b.AddSessionWithDir(child, root, "/work")
	for _, sid := range []string{root, child} {
		b.AddMessage(sid, "msg_"+sid, opencodetest.UserTextMessage("hi "+sid))
		b.AddPart("msg_"+sid, "prt_"+sid, opencodetest.TextPart("hi "+sid))
	}

	if err := saveViaDefault(t, provider.Opencode{}, []string{child}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if backend.initReqs[0].ExternalID != root {
		t.Errorf("external_id = %q, want root %q (child must resolve to root)",
			backend.initReqs[0].ExternalID, root)
	}
	if backend.chunkCount != 2 {
		t.Errorf("chunks = %d, want 2 (root + child)", backend.chunkCount)
	}
}

// TestSaveOpencode_CapabilityOff_RootOnly asserts that when the backend does
// NOT advertise opencode_subagent_files, only the root uploads (children are
// gated off — same rule the daemon honors).
func TestSaveOpencode_CapabilityOff_RootOnly(t *testing.T) {
	backend := &opencodeSaveBackend{caps: capsResponder{opencodeSubagentFiles: false}}
	server := httptest.NewServer(backend)
	defer server.Close()

	b := setupOpencodeSaveEnv(t, server.URL, backend.caps)
	const root = "ses_capoff_root"
	const child = "ses_capoff_child"
	b.AddSessionWithDir(root, "", "/work")
	b.AddSessionWithDir(child, root, "/work")
	for _, sid := range []string{root, child} {
		b.AddMessage(sid, "msg_"+sid, opencodetest.UserTextMessage("hi "+sid))
		b.AddPart("msg_"+sid, "prt_"+sid, opencodetest.TextPart("hi "+sid))
	}

	if err := saveViaDefault(t, provider.Opencode{}, []string{root}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if backend.chunkCount != 1 {
		t.Errorf("chunks = %d, want 1 (children gated off by capability)", backend.chunkCount)
	}
}
