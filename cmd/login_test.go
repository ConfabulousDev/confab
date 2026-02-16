package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/spf13/cobra"
)

// TestRequestDeviceCode tests the device code request function
func TestRequestDeviceCode(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/auth/device/code" {
			t.Errorf("Expected /auth/device/code, got %s", r.URL.Path)
		}

		// Parse request
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
		}
		if req["key_name"] != "test-key" {
			t.Errorf("Expected key_name 'test-key', got %s", req["key_name"])
		}

		// Return mock response
		resp := DeviceCodeResponse{
			DeviceCode:      "device-code-123",
			UserCode:        "ABCD-1234",
			VerificationURI: "http://localhost/device",
			ExpiresIn:       300,
			Interval:        5,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Test
	deviceCode, err := requestDeviceCode(server.URL, "test-key")
	if err != nil {
		t.Fatalf("requestDeviceCode failed: %v", err)
	}

	if deviceCode.DeviceCode != "device-code-123" {
		t.Errorf("Expected device_code 'device-code-123', got %s", deviceCode.DeviceCode)
	}
	if deviceCode.UserCode != "ABCD-1234" {
		t.Errorf("Expected user_code 'ABCD-1234', got %s", deviceCode.UserCode)
	}
	if deviceCode.ExpiresIn != 300 {
		t.Errorf("Expected expires_in 300, got %d", deviceCode.ExpiresIn)
	}
}

// TestPollDeviceToken_Pending tests polling when authorization is pending
func TestPollDeviceToken_Pending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/auth/device/token" {
			t.Errorf("Expected /auth/device/token, got %s", r.URL.Path)
		}

		// Return pending status
		resp := DeviceTokenResponse{
			Error: "authorization_pending",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	token, err := pollDeviceToken(server.URL, "device-code-123")
	if err != nil {
		t.Fatalf("pollDeviceToken failed: %v", err)
	}

	if token.Error != "authorization_pending" {
		t.Errorf("Expected error 'authorization_pending', got %s", token.Error)
	}
	if token.AccessToken != "" {
		t.Errorf("Expected no access_token, got %s", token.AccessToken)
	}
}

// TestPollDeviceToken_Success tests polling when authorization succeeds
func TestPollDeviceToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := DeviceTokenResponse{
			AccessToken: "cfb_test-api-key-123456",
			TokenType:   "Bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	token, err := pollDeviceToken(server.URL, "device-code-123")
	if err != nil {
		t.Fatalf("pollDeviceToken failed: %v", err)
	}

	if token.Error != "" {
		t.Errorf("Expected no error, got %s", token.Error)
	}
	if token.AccessToken != "cfb_test-api-key-123456" {
		t.Errorf("Expected access_token 'cfb_test-api-key-123456', got %s", token.AccessToken)
	}
}

// TestDeviceCodeFlow_Integration tests the full device code flow
func TestDeviceCodeFlow_Integration(t *testing.T) {
	// Setup: Use temp config file
	tmpDir := t.TempDir()
	testConfigPath := fmt.Sprintf("%s/config.json", tmpDir)
	t.Setenv("CONFAB_CONFIG_PATH", testConfigPath)

	// Track request count to simulate progression
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device/code" {
			resp := DeviceCodeResponse{
				DeviceCode:      "device-code-integration-test",
				UserCode:        "TEST-1234",
				VerificationURI: "http://test/device",
				ExpiresIn:       300,
				Interval:        1, // Fast polling for test
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}

		if r.URL.Path == "/auth/device/token" {
			requestCount++
			w.Header().Set("Content-Type", "application/json")

			// First 2 requests: pending, then success
			if requestCount < 3 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(DeviceTokenResponse{Error: "authorization_pending"})
			} else {
				json.NewEncoder(w).Encode(DeviceTokenResponse{
					AccessToken: "cfb_integration-test-key",
					TokenType:   "Bearer",
				})
			}
			return
		}

		t.Errorf("Unexpected request to %s", r.URL.Path)
	}))
	defer server.Close()

	// Run the device code flow
	done := make(chan error, 1)
	go func() {
		// Request device code
		dc, err := requestDeviceCode(server.URL, "test-key")
		if err != nil {
			done <- err
			return
		}

		// Poll for token
		pollInterval := time.Duration(dc.Interval) * time.Second
		expiresAt := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

		for {
			if time.Now().After(expiresAt) {
				done <- fmt.Errorf("timeout")
				return
			}

			time.Sleep(pollInterval)

			token, err := pollDeviceToken(server.URL, dc.DeviceCode)
			if err != nil {
				done <- err
				return
			}

			if token.Error == "authorization_pending" {
				continue
			}

			if token.Error != "" {
				done <- fmt.Errorf("error: %s", token.Error)
				return
			}

			if token.AccessToken != "" {
				// Save config
				cfg := &config.UploadConfig{
					BackendURL: server.URL,
					APIKey:     token.AccessToken,
				}
				if err := config.SaveUploadConfig(cfg); err != nil {
					done <- err
					return
				}
				done <- nil
				return
			}
		}
	}()

	// Wait for completion
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Device code flow failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for device code flow")
	}

	// Verify config was saved
	cfg, err := config.GetUploadConfig()
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	if cfg.APIKey != "cfb_integration-test-key" {
		t.Errorf("Expected API key 'cfb_integration-test-key', got %s", cfg.APIKey)
	}
	if cfg.BackendURL != server.URL {
		t.Errorf("Expected backend URL %s, got %s", server.URL, cfg.BackendURL)
	}
}

// TestPollDeviceToken_ServerError tests handling of server errors
func TestPollDeviceToken_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return valid JSON with error field
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(DeviceTokenResponse{Error: "server_error"})
	}))
	defer server.Close()

	token, err := pollDeviceToken(server.URL, "device-code-123")
	if err != nil {
		t.Fatalf("pollDeviceToken should not return network error: %v", err)
	}

	// Server error results in error field being set
	if token.Error != "server_error" {
		t.Errorf("Expected error 'server_error', got %s", token.Error)
	}
	if token.AccessToken != "" {
		t.Errorf("Expected no access_token on error, got %s", token.AccessToken)
	}
}

// TestRequestDeviceCode_ServerError tests handling of server errors during code request
func TestRequestDeviceCode_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Service Unavailable"))
	}))
	defer server.Close()

	_, err := requestDeviceCode(server.URL, "test-key")
	if err == nil {
		t.Error("Expected error for server error, got nil")
	}
}

// Note: We don't test openBrowser() because it has side effects (opens browser)
// and is platform-specific. It's a simple switch statement that's not worth
// the complexity of mocking exec.Command or the annoyance of actually opening
// browsers during tests.

func TestRunLogin_WithAPIKeyFlag(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Track if device login was called
	var loginCalled bool
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		return nil
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("api-key", "cfb_direct-api-key-12345678", "")

	err := runLogin(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogin failed: %v", err)
	}

	if loginCalled {
		t.Error("device login should not be called when api-key flag is provided")
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
}

func TestRunLogin_WithAPIKeyFlag_InvalidKey(t *testing.T) {
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
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("api-key", "cfb_invalid-api-key-12345678", "")

	err := runLogin(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for invalid API key")
	}

	if !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("expected error message to contain 'invalid API key', got: %v", err)
	}

	if loginCalled {
		t.Error("device login should not be called when api-key flag is provided")
	}
}

func TestRunLogin_WithoutAPIKeyFlag(t *testing.T) {
	// Save and restore the original doDeviceLoginFunc
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true, tokenReady: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	setupSetupTestEnv(t, server.URL)

	var loginCalled bool
	var loginBackendURL string
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		loginCalled = true
		loginBackendURL = backendURL
		newCfg := &config.UploadConfig{
			BackendURL: backendURL,
			APIKey:     "cfb_device-flow-key-12345678",
		}
		return config.SaveUploadConfig(newCfg)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("api-key", "", "") // Empty - should trigger device flow

	err := runLogin(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogin failed: %v", err)
	}

	if !loginCalled {
		t.Error("device login should be called when api-key flag is not provided")
	}

	if loginBackendURL != server.URL {
		t.Errorf("expected backend URL %s, got %s", server.URL, loginBackendURL)
	}
}

// TestLoginWithAPIKey_PreservesRedactionConfig verifies that login --api-key
// preserves existing redaction settings in config.json
func TestLoginWithAPIKey_PreservesRedactionConfig(t *testing.T) {
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Pre-create config with redaction settings
	useDefaults := true
	existingCfg := config.UploadConfig{
		BackendURL: "https://old-backend.com",
		APIKey:     "cfb_old-key-12345678901234",
		LogLevel:   "debug",
		Redaction: &config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "Custom Pattern", Pattern: `CUSTOM_[A-Z]+`, Type: "custom"},
			},
		},
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Run login with new API key
	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("api-key", "cfb_new-api-key-123456789012", "")

	err := runLogin(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogin failed: %v", err)
	}

	// Read back config and verify redaction was preserved
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

	// Verify redaction config was preserved
	if savedCfg.Redaction == nil {
		t.Fatal("redaction config was lost")
	}
	if !savedCfg.Redaction.Enabled {
		t.Error("redaction.enabled was changed")
	}
	if savedCfg.Redaction.UseDefaultPatterns == nil || !*savedCfg.Redaction.UseDefaultPatterns {
		t.Error("redaction.use_default_patterns was changed")
	}
	if len(savedCfg.Redaction.Patterns) != 1 {
		t.Errorf("expected 1 custom pattern, got %d", len(savedCfg.Redaction.Patterns))
	}
	if savedCfg.Redaction.Patterns[0].Name != "Custom Pattern" {
		t.Errorf("custom pattern name was changed to %s", savedCfg.Redaction.Patterns[0].Name)
	}

	// Verify log_level was preserved
	if savedCfg.LogLevel != "debug" {
		t.Errorf("log_level was changed from 'debug' to '%s'", savedCfg.LogLevel)
	}
}

// TestLoginDeviceFlow_PreservesRedactionConfig verifies that device flow login
// preserves existing redaction settings in config.json
func TestLoginDeviceFlow_PreservesRedactionConfig(t *testing.T) {
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)

	// Pre-create config with redaction settings
	useDefaults := false
	existingCfg := config.UploadConfig{
		BackendURL: server.URL,
		APIKey:     "cfb_old-key-12345678901234",
		LogLevel:   "warn",
		Redaction: &config.RedactionConfig{
			Enabled:            true,
			UseDefaultPatterns: &useDefaults,
			Patterns: []config.RedactionPattern{
				{Name: "Secret Pattern", Pattern: `SECRET_[0-9]+`, Type: "secret"},
				{Name: "Token Pattern", Pattern: `TOKEN_[A-Z]+`, Type: "token"},
			},
		},
	}
	cfgData, _ := json.Marshal(existingCfg)
	os.WriteFile(configPath, cfgData, 0600)

	// Mock device login to simulate the FIXED behavior (preserves config)
	doDeviceLoginFunc = func(backendURL, keyName string) error {
		// Load existing config (this is what the fix does)
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
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("api-key", "", "") // Empty triggers device flow

	err := runLogin(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogin failed: %v", err)
	}

	// Read back config and verify redaction was preserved
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
		t.Error("redaction.use_default_patterns was changed (should be false)")
	}
	if len(savedCfg.Redaction.Patterns) != 2 {
		t.Errorf("expected 2 custom patterns, got %d", len(savedCfg.Redaction.Patterns))
	}

	// Verify log_level was preserved
	if savedCfg.LogLevel != "warn" {
		t.Errorf("log_level was changed from 'warn' to '%s'", savedCfg.LogLevel)
	}
}

// TestLoginWithAPIKey_NoExistingConfig verifies login works with no existing config
func TestLoginWithAPIKey_NoExistingConfig(t *testing.T) {
	origDoDeviceLogin := doDeviceLoginFunc
	defer func() { doDeviceLoginFunc = origDoDeviceLogin }()

	backend := &setupTestBackend{validateValid: true}
	server := httptest.NewServer(backend)
	defer server.Close()

	_, configPath := setupSetupTestEnv(t, server.URL)
	// Don't create any config - simulates fresh install
	os.Remove(configPath)

	cmd := &cobra.Command{}
	cmd.Flags().String("backend-url", server.URL, "")
	cmd.Flags().String("name", "", "")
	cmd.Flags().String("api-key", "cfb_fresh-install-key-123456", "")

	err := runLogin(cmd, []string{})
	if err != nil {
		t.Fatalf("runLogin failed: %v", err)
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
	if savedCfg.BackendURL != server.URL {
		t.Errorf("expected backend URL %s, got %s", server.URL, savedCfg.BackendURL)
	}

	// Redaction should be nil (not set by login, only by setup)
	if savedCfg.Redaction != nil {
		t.Error("redaction should not be set by login alone")
	}
}

func TestLoginCmd_BackendURLRequired(t *testing.T) {
	// Verify that --backend-url is marked as required
	flag := loginCmd.Flags().Lookup("backend-url")
	if flag == nil {
		t.Fatal("expected backend-url flag to exist")
	}
	annotations := flag.Annotations
	if _, ok := annotations[cobra.BashCompOneRequiredFlag]; !ok {
		t.Error("expected backend-url flag to be marked as required")
	}
}
