package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

// verifyHooksInstalled checks that sync hooks were installed in settings.json.
// We check this directly instead of using IsSyncHooksInstalled because that function
// uses isConfabCommand which fails in tests (test binary isn't named "confab").
func verifyHooksInstalled(t *testing.T) {
	t.Helper()

	settingsPath, err := config.GetSettingsPath()
	if err != nil {
		t.Fatalf("failed to get settings path: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings file: %v", err)
	}

	// Check that the file contains our hook commands
	content := string(data)
	if !strings.Contains(content, "hook session-start") {
		t.Error("settings.json should contain 'hook session-start' hook")
	}
	if !strings.Contains(content, "hook session-end") {
		t.Error("settings.json should contain 'hook session-end' hook")
	}
}

// setupTestBackend provides a mock backend for testing setup commands
type setupTestBackend struct {
	validateCalls int32
	validateValid bool

	// Device flow fields (for login tests)
	deviceCodeCalls int32
	tokenCalls      int32
	tokenReady      bool
}

func (b *setupTestBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/api/v1/auth/validate":
		atomic.AddInt32(&b.validateCalls, 1)
		json.NewEncoder(w).Encode(map[string]bool{"valid": b.validateValid})

	case "/auth/device/code":
		atomic.AddInt32(&b.deviceCodeCalls, 1)
		json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:      "test-device-code",
			UserCode:        "TEST-1234",
			VerificationURI: "http://test/device",
			ExpiresIn:       300,
			Interval:        1,
		})

	case "/auth/device/token":
		atomic.AddInt32(&b.tokenCalls, 1)
		if b.tokenReady {
			json.NewEncoder(w).Encode(DeviceTokenResponse{
				AccessToken: "cfb_test-api-key-12345678",
				TokenType:   "Bearer",
			})
		} else {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DeviceTokenResponse{
				Error: "authorization_pending",
			})
		}

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// setupSetupTestEnv creates temp directories and sets env vars for setup tests.
// Returns the temp directory and config path.
func setupSetupTestEnv(t *testing.T, serverURL string) (tmpDir string, configPath string) {
	t.Helper()
	tmpDir = t.TempDir()

	// Override HOME for paths
	t.Setenv("HOME", tmpDir)

	// Create confab config directory
	confabDir := filepath.Join(tmpDir, ".confab")
	if err := os.MkdirAll(confabDir, 0755); err != nil {
		t.Fatalf("failed to create confab dir: %v", err)
	}

	// Create claude directory for settings.json
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("failed to create claude dir: %v", err)
	}
	t.Setenv("CONFAB_CLAUDE_DIR", claudeDir)

	// Set config path
	configPath = filepath.Join(confabDir, "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	// Pre-CF-422 tests assume `confab setup` installs Claude hooks by
	// default. Stub LookPath so Claude is detected on hosts that don't
	// have the real binary installed (matters in CI). Tests that need
	// a different shape (auto-detect both, neither, etc.) call
	// stubProviderDetect after this and override cleanly.
	stubProviderDetect(t, "claude")

	return tmpDir, configPath
}

func TestVerifyAPIKey(t *testing.T) {
	t.Run("valid API key", func(t *testing.T) {
		backend := &setupTestBackend{validateValid: true}
		server := httptest.NewServer(backend)
		defer server.Close()

		cfg := &config.UploadConfig{
			BackendURL: server.URL,
			APIKey:     "cfb_test-key-12345678",
		}

		err := verifyAPIKey(cfg)
		if err != nil {
			t.Fatalf("verifyAPIKey failed for valid key: %v", err)
		}

		if backend.validateCalls != 1 {
			t.Errorf("expected 1 validate call, got %d", backend.validateCalls)
		}
	})

	t.Run("invalid API key", func(t *testing.T) {
		backend := &setupTestBackend{validateValid: false}
		server := httptest.NewServer(backend)
		defer server.Close()

		cfg := &config.UploadConfig{
			BackendURL: server.URL,
			APIKey:     "cfb_invalid-key-12345678",
		}

		err := verifyAPIKey(cfg)
		if err == nil {
			t.Fatal("expected error for invalid key, got nil")
		}
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := &config.UploadConfig{
			BackendURL: server.URL,
			APIKey:     "cfb_test-key-12345678",
		}

		err := verifyAPIKey(cfg)
		if err == nil {
			t.Fatal("expected error for server error, got nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		cfg := &config.UploadConfig{
			BackendURL: "http://localhost:99999", // Invalid port
			APIKey:     "cfb_test-key-12345678",
		}

		err := verifyAPIKey(cfg)
		if err == nil {
			t.Fatal("expected error for network error, got nil")
		}
	})
}

func TestRunSetup_AlreadyAuthenticated(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Pre-create valid config
	cfg := config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "cfb_existing-key-12345678",
	}
	cfgData, _ := json.Marshal(cfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Track if login was called
	var loginCalled bool
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		return nil
	}

	// Create a test command with the required flags
	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	if loginCalled {
		t.Error("login should not be called when already authenticated")
	}

	// Verify API key was validated
	if backend.validateCalls != 1 {
		t.Errorf("expected 1 validate call, got %d", backend.validateCalls)
	}

	// Verify hooks were installed by checking settings.json directly
	// (IsSyncHooksInstalled uses isConfabCommand which fails in tests since the
	// test binary isn't named "confab")
	verifyHooksInstalled(t)
}

func TestRunSetup_InvalidExistingKey(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{
		validateValid: false, // Existing key is invalid
		tokenReady:    true,  // New login succeeds immediately
	}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Pre-create config with invalid key
	cfg := config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "cfb_invalid-key-12345678",
	}
	cfgData, _ := json.Marshal(cfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Track if login was called
	var loginCalled bool
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		// Simulate successful login by saving new config
		newCfg := &config.UploadConfig{
			BackendURL: backendURL,
			APIKey:     "cfb_new-valid-key-12345678",
		}
		return config.SaveUploadConfig(newCfg)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	if !loginCalled {
		t.Error("login should be called when existing key is invalid")
	}
}

func TestRunSetup_BackendURLChanged(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	// Old backend validates but we're using a new backend
	oldBackend := &setupTestBackend{validateValid: true}
	oldServer := httptest.NewServer(oldBackend)
	defer oldServer.Close()

	newBackend := &setupTestBackend{validateValid: true, tokenReady: true}
	newServer := httptest.NewServer(newBackend)
	defer newServer.Close()

	_, configPath := setupSetupTestEnv(t, newServer.URL)

	// Pre-create config with OLD backend URL
	cfg := config.UploadConfig{
		BackendURL: oldServer.URL, // Different from new server
		APIKey:     "cfb_old-key-12345678",
	}
	cfgData, _ := json.Marshal(cfg)
	os.WriteFile(configPath, cfgData, 0600)

	var loginCalled bool
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		newCfg := &config.UploadConfig{
			BackendURL: backendURL,
			APIKey:     "cfb_new-backend-key-12345678",
		}
		return config.SaveUploadConfig(newCfg)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", newServer.URL, "") // New backend URL
	cmd.Flags().String("api-key", "", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	if !loginCalled {
		t.Error("login should be called when backend URL changed")
	}

	// Old backend should NOT have received any validate calls
	if oldBackend.validateCalls != 0 {
		t.Errorf("old backend should not be called, got %d validate calls", oldBackend.validateCalls)
	}
}

func TestRunSetup_NeedsLogin(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true, tokenReady: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	setupSetupTestEnv(t, server.URL)
	// Don't create any config - simulates fresh install

	var loginCalled bool
	var loginBackendURL string
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		loginBackendURL = backendURL
		newCfg := &config.UploadConfig{
			BackendURL: backendURL,
			APIKey:     "cfb_fresh-install-key-12345678",
		}
		return config.SaveUploadConfig(newCfg)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	if !loginCalled {
		t.Error("login should be called on fresh install")
	}

	if loginBackendURL != server.URL {
		t.Errorf("expected backend URL %s, got %s", server.URL, loginBackendURL)
	}

	// Verify hooks were installed by checking settings.json directly
	// (IsSyncHooksInstalled uses isConfabCommand which fails in tests since the
	// test binary isn't named "confab")
	verifyHooksInstalled(t)
}

func TestSetupCmd_BackendURLRequired(t *testing.T) {
	// Verify that --backend-url is marked as required
	flag := setupCmd.Flags().Lookup("backend-url")
	if flag == nil {
		t.Fatal("expected backend-url flag to exist")
	}
	annotations := flag.Annotations
	if _, ok := annotations[cobra.BashCompOneRequiredFlag]; !ok {
		t.Error("expected backend-url flag to be marked as required")
	}
}

func TestRunSetupCodexProviderOutput(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, _ := setupSetupTestEnv(t, server.URL)
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)

	origProvider := setupProviderName
	setupProviderName = provider.NameCodex
	defer func() { setupProviderName = origProvider }()

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_codex-test-key-12345678", "")

	output := captureStdout(t, func() {
		if err := runSetup(cmd, nil); err != nil {
			t.Fatalf("runSetup failed: %v", err)
		}
	})

	wantSnippets := []string{
		"Backend URL: " + server.URL,
		"✓ API key validated and saved",
		"▶ codex",
		"✓ hooks installed",
		"✅ Setup complete. codex sessions will sync to " + server.URL,
	}
	for _, want := range wantSnippets {
		if !strings.Contains(output, want) {
			t.Fatalf("setup output missing %q\noutput:\n%s", want, output)
		}
	}

	configPath := filepath.Join(codexDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read Codex config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "hooks = true") {
		t.Fatal("expected Codex feature flag to be enabled")
	}
	if strings.Contains(content, "codex_hooks") {
		t.Fatal("expected deprecated Codex hooks feature flag to be absent")
	}
	if !strings.Contains(content, "hook session-start --provider codex") {
		t.Fatal("expected Codex session-start hook command")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	fn()

	w.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read captured stdout: %v", err)
	}
	return string(data)
}

func TestRunSetup_HookInstallationFails(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create confab config
	confabDir := filepath.Join(tmpDir, ".confab")
	os.MkdirAll(confabDir, 0755)
	configPath := filepath.Join(confabDir, "config.json")
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	// Pre-create valid config
	cfg := config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "cfb_existing-key-12345678",
	}
	cfgData, _ := json.Marshal(cfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Set claude dir to a file (not a directory) to cause hook installation to fail
	claudeFile := filepath.Join(tmpDir, ".claude")
	os.WriteFile(claudeFile, []byte("not a directory"), 0644)
	t.Setenv("CONFAB_CLAUDE_DIR", claudeFile)

	// Force auto-detect to find claude so the install path actually runs
	// (CI hosts don't have the real `claude` binary).
	stubProviderDetect(t, "claude")

	doDeviceLoginFunc = func(backendURL, keyName string) error {
		t.Error("login should not be called")
		return nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "", "")

	err := runSetup(cmd, []string{})
	if err == nil {
		t.Fatal("expected error when hook installation fails")
	}
}

func TestRunSetup_WithAPIKeyFlag(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Track if login was called
	var loginCalled bool
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		return nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_direct-api-key-12345678", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	if loginCalled {
		t.Error("login should not be called when api-key flag is provided")
	}

	// Verify API key was validated
	if backend.validateCalls != 1 {
		t.Errorf("expected 1 validate call, got %d", backend.validateCalls)
	}

	// Verify config was saved with the provided key
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	var cfg config.UploadConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}
	if cfg.APIKey != "cfb_direct-api-key-12345678" {
		t.Errorf("expected api key 'cfb_direct-api-key-12345678', got %s", cfg.APIKey)
	}
	if cfg.BackendURL != server.URL {
		t.Errorf("expected backend URL %s, got %s", server.URL, cfg.BackendURL)
	}

	// Verify hooks were installed
	verifyHooksInstalled(t)
}

func TestRunSetup_WithAPIKeyFlag_InvalidKey(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: false}
	server := httptest.NewServer(backend)
	defer server.Close()

	setupSetupTestEnv(t, server.URL)

	var loginCalled bool
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		return nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_invalid-api-key-12345678", "")

	err := runSetup(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for invalid API key")
	}

	if !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("expected error message to contain 'invalid API key', got: %v", err)
	}

	if loginCalled {
		t.Error("login should not be called when api-key flag is provided")
	}
}

func TestRunSetup_WithAPIKeyFlag_SavesBackendURL(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	// Verify the backend URL from the flag is saved to config
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	doDeviceLoginFunc = func(backendURL, keyName string) error {
		t.Error("login should not be called")
		return nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "") // Use test server
	cmd.Flags().String("api-key", "cfb_test-key-12345678", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	// Verify config was saved with the backend URL
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	var cfg config.UploadConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}
	if cfg.BackendURL != server.URL {
		t.Errorf("expected backend URL %s, got %s", server.URL, cfg.BackendURL)
	}
}

// TestVerifyAPIKeyTimeout tests that verification respects timeout
func TestVerifyAPIKeyTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the 5 second timeout in verifyAPIKey
		time.Sleep(6 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"valid": true})
	}))
	defer server.Close()

	cfg := &config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "cfb_test-key-12345678",
	}

	start := time.Now()
	err := verifyAPIKey(cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Should timeout around 5 seconds, not wait the full 6
	if elapsed > 6*time.Second {
		t.Errorf("verification took too long: %v (should timeout around 5s)", elapsed)
	}
}

// TestSetupWithAPIKey_PreservesCustomRedactionPatterns verifies that setup --api-key
// preserves existing custom redaction patterns while still calling EnsureDefaultRedaction
func TestSetupWithAPIKey_PreservesCustomRedactionPatterns(t *testing.T) {
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Pre-create config with custom redaction patterns
	useDefaults := true
	existingCfg := config.UploadConfig{
		BackendURL: "https://old-backend.com",
		APIKey:     "cfb_old-key-12345678901234",
		LogLevel:   "debug",
		Redaction: &config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "My Custom Pattern", Pattern: `MY_SECRET_[A-Z]+`, Type: "custom"},
				{Name: "Another Pattern", Pattern: `ANOTHER_[0-9]+`, Type: "custom"},
			},
		},
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	doDeviceLoginFunc = func(backendURL, keyName string) error {
		t.Error("device login should not be called when api-key is provided")
		return nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_new-api-key-123456789012", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	// Read back config and verify
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var savedCfg config.UploadConfig
	if err := json.Unmarshal(data, &savedCfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify auth fields were updated
	if savedCfg.APIKey != "cfb_new-api-key-123456789012" {
		t.Errorf("expected new API key, got %s", savedCfg.APIKey)
	}
	if savedCfg.BackendURL != server.URL {
		t.Errorf("expected backend URL %s, got %s", server.URL, savedCfg.BackendURL)
	}

	// Verify redaction config was preserved (EnsureDefaultRedaction should NOT overwrite)
	if savedCfg.Redaction == nil {
		t.Fatal("redaction config was lost")
	}
	if !savedCfg.Redaction.Enabled {
		t.Error("redaction.enabled was changed")
	}
	if len(savedCfg.Redaction.Patterns) != 2 {
		t.Errorf("expected 2 custom patterns, got %d", len(savedCfg.Redaction.Patterns))
	}
	if savedCfg.Redaction.Patterns[0].Name != "My Custom Pattern" {
		t.Errorf("first custom pattern name was changed to %s", savedCfg.Redaction.Patterns[0].Name)
	}

	// Verify log_level was preserved
	if savedCfg.LogLevel != "debug" {
		t.Errorf("log_level was changed from 'debug' to '%s'", savedCfg.LogLevel)
	}
}

// TestSetupDeviceFlow_PreservesCustomRedactionPatterns verifies that setup with device flow
// preserves existing custom redaction patterns
func TestSetupDeviceFlow_PreservesCustomRedactionPatterns(t *testing.T) {
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: false} // Force re-auth
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Pre-create config with custom redaction patterns but invalid API key
	useDefaults := false
	existingCfg := config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "cfb_invalid-key-12345678", // Will fail validation
		LogLevel:   "warn",
		Redaction: &config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "Secret Pattern", Pattern: `SECRET_[A-Z]+`, Type: "secret"},
			},
		},
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Mock device login that preserves config
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		cfg, err := config.GetUploadConfig()
		if err != nil {
			cfg = &config.UploadConfig{}
		}
		cfg.BackendURL = backendURL
		cfg.APIKey = "cfb_device-flow-new-key-1234"
		return config.SaveUploadConfig(cfg)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "", "") // Empty triggers device flow

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	// Read back config and verify
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var savedCfg config.UploadConfig
	if err := json.Unmarshal(data, &savedCfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify auth fields were updated
	if savedCfg.APIKey != "cfb_device-flow-new-key-1234" {
		t.Errorf("expected new API key, got %s", savedCfg.APIKey)
	}

	// Verify redaction config was preserved
	if savedCfg.Redaction == nil {
		t.Fatal("redaction config was lost")
	}
	if !savedCfg.Redaction.Enabled {
		t.Error("redaction.enabled was changed")
	}
	if savedCfg.Redaction.UseDefaultPatterns == nil || *savedCfg.Redaction.UseDefaultPatterns {
		t.Error("use_default_patterns was changed (should remain false)")
	}
	if len(savedCfg.Redaction.Patterns) != 1 {
		t.Errorf("expected 1 custom pattern, got %d", len(savedCfg.Redaction.Patterns))
	}

	// Verify log_level was preserved
	if savedCfg.LogLevel != "warn" {
		t.Errorf("log_level was changed from 'warn' to '%s'", savedCfg.LogLevel)
	}
}

// TestSetupFreshInstall_AddsDefaultRedaction verifies that setup on fresh install
// adds default redaction config
func TestSetupFreshInstall_AddsDefaultRedaction(t *testing.T) {
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)
	// Don't create any config - simulates fresh install
	os.Remove(configPath)

	doDeviceLoginFunc = func(backendURL, keyName string) error {
		t.Error("device login should not be called when api-key is provided")
		return nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_fresh-install-key-123456", "")

	err := runSetup(cmd, []string{})
	if err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	// Read back config
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var savedCfg config.UploadConfig
	if err := json.Unmarshal(data, &savedCfg); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify auth fields were set
	if savedCfg.APIKey != "cfb_fresh-install-key-123456" {
		t.Errorf("expected API key, got %s", savedCfg.APIKey)
	}

	// Verify default redaction was added by EnsureDefaultRedaction
	if savedCfg.Redaction == nil {
		t.Fatal("redaction config should be added on fresh install")
	}
	if !savedCfg.Redaction.Enabled {
		t.Error("redaction should be enabled by default")
	}
	if savedCfg.Redaction.UseDefaultPatterns == nil || !*savedCfg.Redaction.UseDefaultPatterns {
		t.Error("use_default_patterns should be true")
	}
	// Patterns array should be empty (defaults applied at runtime)
	if len(savedCfg.Redaction.Patterns) != 0 {
		t.Errorf("expected 0 patterns in config (defaults are runtime), got %d", len(savedCfg.Redaction.Patterns))
	}
}

// stubProviderDetect swaps provider.LookPath to simulate which CLIs are
// on PATH for the duration of the test. Shared by setup and status tests.
func stubProviderDetect(t *testing.T, present ...string) {
	t.Helper()
	set := make(map[string]struct{}, len(present))
	for _, p := range present {
		set[p] = struct{}{}
	}
	orig := provider.LookPath
	provider.LookPath = func(name string) (string, error) {
		if _, ok := set[name]; ok {
			return "/usr/local/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { provider.LookPath = orig })
}

// resetSetupProviderName clears the package-level setupProviderName
// between tests so a previous test's value can't leak into auto-detect.
func resetSetupProviderName(t *testing.T) {
	t.Helper()
	orig := setupProviderName
	setupProviderName = ""
	t.Cleanup(func() { setupProviderName = orig })
}

func TestRunSetup_AutoDetect_Both(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, _ := setupSetupTestEnv(t, server.URL)
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)

	resetSetupProviderName(t)
	stubProviderDetect(t, "claude", "codex")

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_autodetect-key-1234567890", "")

	output := captureStdout(t, func() {
		if err := runSetup(cmd, nil); err != nil {
			t.Fatalf("runSetup failed: %v", err)
		}
	})

	wantSnippets := []string{
		"Detected providers: claude-code, codex",
		"▶ claude-code",
		"▶ codex",
		"✓ hooks installed",
		"Summary:",
		"claude-code: installed",
		"codex: installed",
		"✅ Setup complete. claude-code, codex sessions will sync to " + server.URL,
	}
	for _, want := range wantSnippets {
		if !strings.Contains(output, want) {
			t.Fatalf("setup output missing %q\noutput:\n%s", want, output)
		}
	}

	// Claude hooks landed in ~/.claude/settings.json
	verifyHooksInstalled(t)

	// Codex hooks landed in ~/.codex/config.toml
	codexCfg := filepath.Join(codexDir, "config.toml")
	data, err := os.ReadFile(codexCfg)
	if err != nil {
		t.Fatalf("expected Codex config.toml after auto-detect: %v", err)
	}
	if !strings.Contains(string(data), "hook session-start --provider codex") {
		t.Fatal("expected Codex session-start hook in config.toml")
	}
}

func TestRunSetup_AutoDetect_ClaudeOnly(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, _ := setupSetupTestEnv(t, server.URL)
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)

	resetSetupProviderName(t)
	stubProviderDetect(t, "claude")

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_claudeonly-key-1234567890", "")

	output := captureStdout(t, func() {
		if err := runSetup(cmd, nil); err != nil {
			t.Fatalf("runSetup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Detected providers: claude-code") {
		t.Fatalf("expected Claude-only detection line, got:\n%s", output)
	}
	if strings.Contains(output, "▶ codex") {
		t.Fatalf("Codex should not be installed; output:\n%s", output)
	}

	verifyHooksInstalled(t)

	// Codex config must not exist
	codexCfg := filepath.Join(codexDir, "config.toml")
	if _, err := os.Stat(codexCfg); !os.IsNotExist(err) {
		t.Fatalf("expected no Codex config; stat err=%v", err)
	}
}

func TestRunSetup_AutoDetect_CodexOnly(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, _ := setupSetupTestEnv(t, server.URL)
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)

	// Wipe ~/.claude so a stale settings.json from setupSetupTestEnv's
	// MkdirAll doesn't get hooks written into it. (Claude install path
	// won't run because LookPath returns ErrNotFound for "claude".)
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.RemoveAll(claudeDir); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	resetSetupProviderName(t)
	stubProviderDetect(t, "codex")

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_codexonly-key-12345678901", "")

	output := captureStdout(t, func() {
		if err := runSetup(cmd, nil); err != nil {
			t.Fatalf("runSetup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Detected providers: codex") {
		t.Fatalf("expected Codex-only detection line, got:\n%s", output)
	}
	if strings.Contains(output, "▶ claude-code") {
		t.Fatalf("Claude Code should not be installed; output:\n%s", output)
	}

	codexCfg := filepath.Join(codexDir, "config.toml")
	if _, err := os.Stat(codexCfg); err != nil {
		t.Fatalf("expected Codex config.toml: %v", err)
	}

	// Claude settings.json must not exist
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("expected no Claude settings; stat err=%v", err)
	}
}

func TestRunSetup_AutoDetect_None(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, configPath := setupSetupTestEnv(t, server.URL)
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)

	resetSetupProviderName(t)
	stubProviderDetect(t) // neither

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_none-detected-key-1234567", "")

	output := captureStdout(t, func() {
		if err := runSetup(cmd, nil); err != nil {
			t.Fatalf("runSetup must exit 0 when no provider is detected, got: %v", err)
		}
	})

	if !strings.Contains(output, "Detected providers: (none)") {
		t.Fatalf("expected `Detected providers: (none)`, got:\n%s", output)
	}
	if !strings.Contains(output, "No supported CLIs") {
		t.Fatalf("expected terse no-CLI warning, got:\n%s", output)
	}
	if !strings.Contains(output, "Auth saved, but no hooks were installed") {
		t.Fatalf("expected auth-only warning copy, got:\n%s", output)
	}
	if strings.Contains(output, "▶ ") {
		t.Fatalf("no provider sub-headers expected; got:\n%s", output)
	}

	// Auth must still be saved
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config should be saved on auth-only path: %v", err)
	}
	var cfg config.UploadConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid config: %v", err)
	}
	if cfg.APIKey == "" {
		t.Fatal("expected API key persisted even without provider hooks")
	}
}

func TestRunSetup_AutoDetect_PartialFailure(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, _ := setupSetupTestEnv(t, server.URL)

	// Force Codex install to fail by pointing CONFAB_CODEX_DIR at a file
	codexFile := filepath.Join(tmpDir, "codex-blocker")
	if err := os.WriteFile(codexFile, []byte("not a dir"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv(provider.CodexStateDirEnv, codexFile)

	resetSetupProviderName(t)
	stubProviderDetect(t, "claude", "codex")

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_partial-fail-key-12345678", "")

	var runErr error
	output := captureStdout(t, func() {
		runErr = runSetup(cmd, nil)
	})

	if runErr == nil {
		t.Fatalf("expected non-nil error when any provider fails; output:\n%s", output)
	}

	// Claude should still have installed successfully
	verifyHooksInstalled(t)

	// Output must include summary block listing per-provider outcome
	wantSnippets := []string{
		"Summary:",
		"claude-code: installed",
		"codex: failed",
	}
	for _, want := range wantSnippets {
		if !strings.Contains(output, want) {
			t.Fatalf("setup output missing %q\noutput:\n%s", want, output)
		}
	}
}

func TestRunSetup_Idempotent_AlreadyInstalled(t *testing.T) {
	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	tmpDir, _ := setupSetupTestEnv(t, server.URL)
	codexDir := filepath.Join(tmpDir, ".codex")
	t.Setenv(provider.CodexStateDirEnv, codexDir)

	// Pre-populate Claude settings.json with a `confab`-named hook
	// command so IsHooksInstalled returns true. (See
	// TestIsSyncHooksInstalledRoundTrip in pkg/hookconfig.)
	claudeSettings := filepath.Join(tmpDir, ".claude", "settings.json")
	confabClaudeCfg := `{
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-start"}]}],
    "SessionEnd":   [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook session-end"}]}],
    "PreToolUse":   [{"matcher": "Bash", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook pre-tool-use"}]}],
    "PostToolUse":  [{"matcher": "Bash", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook post-tool-use"}]}],
    "UserPromptSubmit": [{"matcher": "*", "hooks": [{"type":"command","command":"/usr/local/bin/confab hook user-prompt-submit"}]}]
  }
}`
	if err := os.WriteFile(claudeSettings, []byte(confabClaudeCfg), 0600); err != nil {
		t.Fatalf("write claude settings: %v", err)
	}

	// Pre-populate Codex config.toml with a confab-named hook.
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	codexCfg := filepath.Join(codexDir, "config.toml")
	confabCodexCfg := `[features]
hooks = true

[[hooks.SessionStart]]
matcher = "startup|resume|clear"
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/local/bin/confab hook session-start --provider codex"
`
	if err := os.WriteFile(codexCfg, []byte(confabCodexCfg), 0600); err != nil {
		t.Fatalf("write codex config: %v", err)
	}

	resetSetupProviderName(t)
	stubProviderDetect(t, "claude", "codex")

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("api-key", "cfb_idempotent-key-1234567890", "")

	output := captureStdout(t, func() {
		if err := runSetup(cmd, nil); err != nil {
			t.Fatalf("runSetup failed: %v", err)
		}
	})

	if !strings.Contains(output, "hooks already installed (no changes)") {
		t.Fatalf("expected already-installed message, got:\n%s", output)
	}
	// Both providers must report already-installed.
	if strings.Count(output, "hooks already installed (no changes)") < 2 {
		t.Fatalf("expected already-installed for BOTH providers, got:\n%s", output)
	}
}
