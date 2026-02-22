package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

// doDeviceLoginFunc is the function used to perform device login.
// It can be overridden in tests to avoid actual authentication.
var doDeviceLoginFunc = doDeviceLoginImpl

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the Confab backend",
	Long: `Authenticates with the confab backend using device code flow.

You'll receive a code to enter at a URL. This works on any machine, including
remote/headless servers - authenticate from any device with a browser.

Use --api-key to provide an API key directly (bypasses device auth flow).`,
	RunE: runLogin,
}

// DeviceCodeResponse is the response from /auth/device/code
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// APIKeyLimitError is returned when the user has too many API keys
type APIKeyLimitError struct {
	BackendURL string
}

func (e *APIKeyLimitError) Error() string {
	return fmt.Sprintf(`API key limit reached (maximum 100 keys).

Please delete some unused API keys before creating a new one.
Manage your keys at: %s/keys`, e.BackendURL)
}

// DeviceTokenResponse is the response from /auth/device/token
type DeviceTokenResponse struct {
	AccessToken string `json:"access_token,omitempty"`
	TokenType   string `json:"token_type,omitempty"`
	Error       string `json:"error,omitempty"`
}

func runLogin(cmd *cobra.Command, args []string) error {
	logger.Info("Starting login flow")

	backendURL, err := cmd.Flags().GetString("backend-url")
	if err != nil {
		return fmt.Errorf("failed to get backend-url flag: %w", err)
	}
	keyName, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("failed to get name flag: %w", err)
	}
	apiKey, err := cmd.Flags().GetString("api-key")
	if err != nil {
		return fmt.Errorf("failed to get api-key flag: %w", err)
	}

	// Default key name to hostname
	if keyName == "" {
		keyName = defaultKeyName()
	}

	fmt.Println("=== Confab Login ===")
	fmt.Println()

	if apiKey != "" {
		if err := loginWithAPIKey(backendURL, apiKey); err != nil {
			return err
		}
	} else {
		// Standard device auth flow
		if err := doDeviceLogin(backendURL, keyName); err != nil {
			return err
		}
	}

	fmt.Println()
	fmt.Println("Next step: Run 'confab hooks add' to install hooks.")
	fmt.Println()
	fmt.Println("Tip: Use 'confab setup' next time to do login + hooks in one step.")

	return nil
}

// loginWithAPIKey validates and saves the provided API key.
// This is the core logic shared between `login --api-key` and `setup --api-key`.
func loginWithAPIKey(backendURL, apiKey string) error {
	logger.Info("API key provided via flag, skipping device auth")

	// Load existing config to preserve other settings (like redaction)
	cfg, err := config.GetUploadConfig()
	if err != nil {
		// If we can't read existing config, start fresh
		cfg = &config.UploadConfig{}
	}

	// Update only the auth-related fields
	cfg.BackendURL = backendURL
	cfg.APIKey = apiKey

	fmt.Println("Validating API key...")
	if err := verifyAPIKey(cfg); err != nil {
		return fmt.Errorf("invalid API key: %w", err)
	}

	if err := config.SaveUploadConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("✓ API key validated and saved")
	return nil
}

// verifyAPIKey checks if the API key works by calling the validate endpoint
func verifyAPIKey(cfg *config.UploadConfig) error {
	client, err := confabhttp.NewClient(cfg, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to create http client: %w", err)
	}

	var result map[string]interface{}
	if err := client.Get("/api/v1/auth/validate", &result); err != nil {
		return err
	}

	if valid, ok := result["valid"].(bool); !ok || !valid {
		return fmt.Errorf("api key is not valid")
	}

	return nil
}

// defaultKeyName returns a descriptive name for the API key
func defaultKeyName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "Confab CLI"
	}
	return fmt.Sprintf("%s (Confab CLI)", hostname)
}

// doDeviceLogin performs the device code login flow and saves credentials
func doDeviceLogin(backendURL, keyName string) error {
	return doDeviceLoginFunc(backendURL, keyName)
}

// doDeviceLoginImpl is the actual implementation of doDeviceLogin
func doDeviceLoginImpl(backendURL, keyName string) error {
	logger.Debug("Login parameters: backend=%s, keyName=%s", backendURL, keyName)

	fmt.Printf("Backend: %s\n", backendURL)
	fmt.Println()

	// Request device code
	deviceCode, err := requestDeviceCode(backendURL, keyName)
	if err != nil {
		logger.Error("Failed to get device code: %v", err)
		return fmt.Errorf("failed to initiate login: %w", err)
	}

	// Display instructions
	verificationURL := addQueryParam(deviceCode.VerificationURI, "code", deviceCode.UserCode)
	fmt.Println("To authenticate, visit:")
	fmt.Printf("  %s\n", verificationURL)
	fmt.Println()
	fmt.Printf("Your code: %s\n", deviceCode.UserCode)
	fmt.Println()

	// Try to open browser
	if err := openBrowser(verificationURL); err != nil {
		logger.Debug("Failed to open browser: %v", err)
	}

	fmt.Printf("Waiting for authorization... (expires in %d minutes)\n", deviceCode.ExpiresIn/60)

	// Poll for token
	apiKey, err := pollForToken(backendURL, deviceCode)
	if err != nil {
		return err
	}

	// Load existing config to preserve other settings (like redaction)
	cfg, err := config.GetUploadConfig()
	if err != nil {
		// If we can't read existing config, start fresh
		cfg = &config.UploadConfig{}
	}

	// Update only the auth-related fields
	cfg.BackendURL = backendURL
	cfg.APIKey = apiKey

	if err := config.SaveUploadConfig(cfg); err != nil {
		logger.Error("Failed to save config: %v", err)
		return fmt.Errorf("failed to save config: %w", err)
	}

	logger.Info("Login successful, config saved")
	fmt.Println()
	fmt.Println("Authentication successful!")

	return nil
}

// pollForToken polls the backend until authorization completes or times out
func pollForToken(backendURL string, deviceCode *DeviceCodeResponse) (string, error) {
	pollInterval := time.Duration(deviceCode.Interval) * time.Second
	if pollInterval < 5*time.Second {
		pollInterval = 5 * time.Second
	}

	expiresAt := time.Now().Add(time.Duration(deviceCode.ExpiresIn) * time.Second)

	for {
		if time.Now().After(expiresAt) {
			return "", fmt.Errorf("authorization timed out - please try again")
		}

		time.Sleep(pollInterval)

		token, err := pollDeviceToken(backendURL, deviceCode.DeviceCode)
		if err != nil {
			logger.Error("Error polling for token: %v", err)
			return "", fmt.Errorf("failed to complete authorization: %w", err)
		}

		switch token.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			pollInterval += 5 * time.Second
			continue
		case "api_key_limit_exceeded":
			return "", &APIKeyLimitError{BackendURL: backendURL}
		case "":
			if token.AccessToken != "" {
				return token.AccessToken, nil
			}
		default:
			return "", fmt.Errorf("authorization failed: %s", token.Error)
		}
	}
}

// requestDeviceCode initiates the device code flow
func requestDeviceCode(backendURL, keyName string) (*DeviceCodeResponse, error) {
	reqBody := map[string]string{"key_name": keyName}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(backendURL+"/auth/device/code", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to contact server: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server error: %s", string(body))
	}

	var deviceCode DeviceCodeResponse
	if err := json.Unmarshal(body, &deviceCode); err != nil {
		return nil, err
	}

	return &deviceCode, nil
}

// pollDeviceToken polls the backend for the token
func pollDeviceToken(backendURL, deviceCode string) (*DeviceTokenResponse, error) {
	reqBody := map[string]string{"device_code": deviceCode}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(backendURL+"/auth/device/token", "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to contact server: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var token DeviceTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, err
	}

	return &token, nil
}

// openBrowser opens a URL in the default browser
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("unsupported platform")
	}

	return cmd.Start()
}

// addQueryParam appends a query parameter to a URL, handling both URLs with
// and without existing query strings.
func addQueryParam(baseURL, key, value string) string {
	separator := "?"
	if strings.Contains(baseURL, "?") {
		separator = "&"
	}
	return baseURL + separator + key + "=" + url.QueryEscape(value)
}

func init() {
	rootCmd.AddCommand(loginCmd)

	loginCmd.Flags().String("backend-url", "", "Backend API URL (required)")
	loginCmd.MarkFlagRequired("backend-url")
	loginCmd.Flags().String("name", "", "Name for this API key (default: hostname)")
	loginCmd.Flags().String("api-key", "", "API key (bypasses device auth flow)")
}
