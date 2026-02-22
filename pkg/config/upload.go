package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// UploadConfig holds backend upload configuration
type UploadConfig struct {
	BackendURL string           `json:"backend_url"`
	APIKey     string           `json:"api_key"`
	LogLevel   string           `json:"log_level,omitempty"`  // debug, info, warn, error (default: info)
	AutoUpdate *bool            `json:"auto_update,omitempty"` // nil = enabled (default), false = disabled
	Redaction  *RedactionConfig `json:"redaction,omitempty"`
}

// IsAutoUpdateEnabled returns whether auto-update is enabled.
// Defaults to true when AutoUpdate is nil (not set in config).
func (c *UploadConfig) IsAutoUpdateEnabled() bool {
	return c.AutoUpdate == nil || *c.AutoUpdate
}

// RedactionConfig holds redaction settings
type RedactionConfig struct {
	Enabled            bool               `json:"enabled"`
	UseDefaultPatterns *bool              `json:"use_default_patterns,omitempty"` // defaults to true if nil
	Patterns           []RedactionPattern `json:"patterns,omitempty"`
}

// ShouldUseDefaultPatterns returns true if default patterns should be used.
// Defaults to true if UseDefaultPatterns is nil.
func (c *RedactionConfig) ShouldUseDefaultPatterns() bool {
	return c.UseDefaultPatterns == nil || *c.UseDefaultPatterns
}

// RedactionPattern represents a single redaction pattern
type RedactionPattern struct {
	Name         string `json:"name"`
	Pattern      string `json:"pattern,omitempty"`
	Type         string `json:"type"`
	CaptureGroup int    `json:"capture_group,omitempty"`
	FieldPattern string `json:"field_pattern,omitempty"`
}

// GetUploadConfig reads upload configuration from ~/.confab/config.json
func GetUploadConfig() (*UploadConfig, error) {
	configPath, err := getConfigPath()
	if err != nil {
		return nil, err
	}

	// Return default config if file doesn't exist
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &UploadConfig{
			BackendURL: "",
			APIKey:     "",
		}, nil
	}

	// Read and parse config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read confab config (%s): %w", configPath, err)
	}

	var config UploadConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("confab config has invalid JSON (%s): %w", configPath, err)
	}

	return &config, nil
}

// SaveUploadConfig writes upload configuration to ~/.confab/config.json
func SaveUploadConfig(config *UploadConfig) error {
	// Validate before saving
	if err := config.Validate(); err != nil {
		return err
	}

	configPath, err := getConfigPath()
	if err != nil {
		return err
	}

	// Ensure directory exists
	confabDir := filepath.Dir(configPath)
	if err := os.MkdirAll(confabDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config to JSON
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func getConfigPath() (string, error) {
	// Allow overriding config path for testing
	if testConfigPath := os.Getenv("CONFAB_CONFIG_PATH"); testConfigPath != "" {
		return testConfigPath, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(home, ".confab", "config.json"), nil
}

// ValidateBackendURL checks if the backend URL is valid
func ValidateBackendURL(backendURL string) error {
	if backendURL == "" {
		return nil // Empty is allowed (not configured)
	}

	parsed, err := url.Parse(backendURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Must have a scheme
	if parsed.Scheme == "" {
		return fmt.Errorf("url must include scheme (http:// or https://)")
	}

	// Only allow http and https
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https, got %q", parsed.Scheme)
	}

	// Must have a host
	if parsed.Host == "" {
		return fmt.Errorf("url must include a host")
	}

	return nil
}

// ValidateAPIKey checks if the API key format is valid.
// Confab API keys have the format: cfb_<40 alphanumeric chars>
// Returns nil for empty string (not configured), but empty is not a valid key.
func ValidateAPIKey(apiKey string) error {
	// Empty means not configured - skip validation but callers should
	// check separately if authentication is required
	if apiKey == "" {
		return nil
	}

	// Require cfb_ prefix
	const keyPrefix = "cfb_"
	if !strings.HasPrefix(apiKey, keyPrefix) {
		return fmt.Errorf("api key must start with %q", keyPrefix)
	}

	// Minimum length check to catch truncated/corrupted keys
	// Production keys are 44 chars (cfb_ + 40), but allow shorter for testing
	const minKeyLength = 20
	if len(apiKey) < minKeyLength {
		return fmt.Errorf("api key too short (minimum %d characters)", minKeyLength)
	}

	// Check for obviously invalid characters (whitespace, control chars)
	if strings.ContainsAny(apiKey, " \t\n\r") {
		return fmt.Errorf("api key contains invalid whitespace characters")
	}

	return nil
}

// Validate checks if the upload config is valid
func (c *UploadConfig) Validate() error {
	if err := ValidateBackendURL(c.BackendURL); err != nil {
		return fmt.Errorf("invalid backend URL: %w", err)
	}

	if err := ValidateAPIKey(c.APIKey); err != nil {
		return fmt.Errorf("invalid API key: %w", err)
	}

	return nil
}

// EnsureAuthenticated reads the config and verifies it has valid credentials
// Returns the config if authenticated, or an error if not configured
func EnsureAuthenticated() (*UploadConfig, error) {
	cfg, err := GetUploadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.BackendURL == "" || cfg.APIKey == "" {
		return nil, fmt.Errorf("not authenticated. Run 'confab login' first")
	}

	return cfg, nil
}

// ParseLogLevel parses a log level string and returns the corresponding logger.Level
func ParseLogLevel(level string) (logger.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return logger.DEBUG, nil
	case "info", "":
		return logger.INFO, nil
	case "warn", "warning":
		return logger.WARN, nil
	case "error":
		return logger.ERROR, nil
	default:
		return logger.INFO, fmt.Errorf("invalid log level %q: must be debug, info, warn, or error", level)
	}
}

// ApplyLogLevel reads the log level from config and applies it to the logger
func ApplyLogLevel() {
	cfg, err := GetUploadConfig()
	if err != nil {
		return // Use default level if config can't be read
	}

	level, err := ParseLogLevel(cfg.LogLevel)
	if err != nil {
		logger.Warn("Invalid log_level in config: %v", err)
		return
	}

	logger.Get().SetLevel(level)
}

// GetDefaultRedactionPatterns returns the default high-precision redaction patterns
func GetDefaultRedactionPatterns() []RedactionPattern {
	return []RedactionPattern{
		// Field-based patterns - match sensitive field names and redact entire value
		{
			Name:         "Sensitive Field Names",
			FieldPattern: `(?i)^(password|passwd|secret|api_key|apikey|api_secret|token|auth_token|access_token|refresh_token|private_key|credential|credentials)$`,
			Type:         "sensitive_field",
		},
		// Value-based patterns - match distinctive secret formats anywhere
		// IMPORTANT: Anthropic pattern must come before OpenAI pattern because
		// OpenAI's sk- prefix would also match Anthropic keys (sk-ant-api...).
		// More specific patterns should always precede more general ones.
		{
			Name:    "Anthropic API Key",
			Pattern: `sk-ant-api\d{2}-[A-Za-z0-9_-]{80,120}`,
			Type:    "api_key",
		},
		{
			Name:    "OpenAI API Key",
			Pattern: `sk-(?:proj-)?[A-Za-z0-9_-]{20,200}`,
			Type:    "api_key",
		},
		{
			Name:    "AWS Access Key",
			Pattern: `AKIA[0-9A-Z]{16}`,
			Type:    "aws_key",
		},
		{
			Name:         "AWS Secret Key (config file)",
			Pattern:      `aws_secret_access_key\s*=\s*([A-Za-z0-9/+=]{40})`,
			Type:         "aws_secret",
			CaptureGroup: 1,
		},
		{
			Name:         "AWS Secret Key (env var style)",
			Pattern:      `AWS_SECRET_ACCESS_KEY\s*=\s*["']?([A-Za-z0-9/+=]{40})["']?`,
			Type:         "aws_secret",
			CaptureGroup: 1,
		},
		{
			Name:    "GitHub Personal Access Token (Classic)",
			Pattern: `ghp_[A-Za-z0-9]{36,255}`,
			Type:    "github_token",
		},
		{
			Name:    "GitHub Personal Access Token (Fine-grained)",
			Pattern: `github_pat_[A-Za-z0-9]{22,255}`,
			Type:    "github_token",
		},
		{
			Name:    "GitHub OAuth Token",
			Pattern: `gho_[A-Za-z0-9]{36,255}`,
			Type:    "github_token",
		},
		{
			Name:    "GitHub App Token",
			Pattern: `(?:ghu|ghs)_[A-Za-z0-9]{36,255}`,
			Type:    "github_token",
		},
		{
			Name:    "GitHub Refresh Token",
			Pattern: `ghr_[A-Za-z0-9]{36,255}`,
			Type:    "github_token",
		},
		{
			Name:    "JWT Token",
			Pattern: `eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`,
			Type:    "jwt",
		},
		{
			Name:    "RSA Private Key",
			Pattern: `(?s)-----BEGIN RSA PRIVATE KEY-----.*?-----END RSA PRIVATE KEY-----`,
			Type:    "private_key",
		},
		{
			Name:    "EC Private Key",
			Pattern: `(?s)-----BEGIN EC PRIVATE KEY-----.*?-----END EC PRIVATE KEY-----`,
			Type:    "private_key",
		},
		{
			Name:    "OpenSSH Private Key",
			Pattern: `(?s)-----BEGIN OPENSSH PRIVATE KEY-----.*?-----END OPENSSH PRIVATE KEY-----`,
			Type:    "private_key",
		},
		{
			Name:    "Generic Private Key (PKCS#8)",
			Pattern: `(?s)-----BEGIN PRIVATE KEY-----.*?-----END PRIVATE KEY-----`,
			Type:    "private_key",
		},
		{
			Name:         "PostgreSQL Connection String Password",
			Pattern:      `(postgres(?:ql)?://[^:]+:)([^@\s]+)(@[^\s]+)`,
			Type:         "password",
			CaptureGroup: 2,
		},
		{
			Name:         "MySQL Connection String Password",
			Pattern:      `(mysql://[^:]+:)([^@\s]+)(@[^\s]+)`,
			Type:         "password",
			CaptureGroup: 2,
		},
		{
			Name:         "MongoDB Connection String Password",
			Pattern:      `(mongodb(?:\+srv)?://[^:]+:)([^@\s]+)(@[^\s]+)`,
			Type:         "password",
			CaptureGroup: 2,
		},
		{
			Name:         "Redis Connection String Password",
			Pattern:      `(redis://[^:/@\s]*:)([^@\s]+)(@[^\s]+)`,
			Type:         "password",
			CaptureGroup: 2,
		},
		{
			Name:         "Generic URL Password",
			Pattern:      `(://[^:/@\s]+:)([^@\s]+)(@)`,
			Type:         "password",
			CaptureGroup: 2,
		},
		{
			Name:    "Slack Token",
			Pattern: `xox[baprs]-[0-9a-zA-Z-]{10,255}`,
			Type:    "slack_token",
		},
		{
			Name:    "Slack Rotating Token",
			Pattern: `xoxe(?:\.[a-zA-Z0-9-]+)?-[0-9a-zA-Z-]{10,255}`,
			Type:    "slack_token",
		},
		{
			Name:    "Slack App-Level Token",
			Pattern: `xapp-[0-9a-zA-Z-]{10,255}`,
			Type:    "slack_token",
		},
		{
			Name:    "Stripe Secret API Key",
			Pattern: `sk_(?:live|test)_[0-9a-zA-Z]{24,}`,
			Type:    "stripe_key",
		},
		{
			Name:    "Stripe Restricted API Key",
			Pattern: `rk_(?:live|test)_[0-9a-zA-Z]{24,}`,
			Type:    "stripe_key",
		},
		{
			Name:    "Google API Key",
			Pattern: `AIza[0-9A-Za-z_-]{35}`,
			Type:    "google_api_key",
		},
		{
			Name:    "Twilio API Key",
			Pattern: `SK[0-9a-fA-F]{32}`,
			Type:    "twilio_key",
		},
		{
			Name:    "SendGrid API Key",
			Pattern: `SG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}`,
			Type:    "sendgrid_key",
		},
		{
			Name:    "MailChimp API Key",
			Pattern: `[0-9a-f]{32}-us[0-9]{1,2}`,
			Type:    "mailchimp_key",
		},
		{
			Name:    "npm Access Token",
			Pattern: `npm_[A-Za-z0-9]{36}`,
			Type:    "npm_token",
		},
		{
			Name:    "PyPI Token",
			Pattern: `pypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{70,}`,
			Type:    "pypi_token",
		},
		{
			Name:    "Confab API Key",
			Pattern: `cfb_[A-Za-z0-9]{40}`,
			Type:    "confab_key",
		},
	}
}

// EnsureDefaultRedaction ensures the config has a redaction section with defaults.
// If redaction config already exists (even if disabled), it's left unchanged.
// Returns true if defaults were added, false if config already had redaction settings.
func EnsureDefaultRedaction() (bool, error) {
	cfg, err := GetUploadConfig()
	if err != nil {
		return false, fmt.Errorf("failed to get config: %w", err)
	}

	// If redaction config already exists, don't overwrite
	if cfg.Redaction != nil {
		return false, nil
	}

	// Add default redaction config (enabled by default, use_default_patterns explicitly true)
	// Patterns array is empty - default patterns are applied automatically
	useDefaults := true
	cfg.Redaction = &RedactionConfig{
		Enabled:            true,
		UseDefaultPatterns: &useDefaults,
		Patterns:           []RedactionPattern{},
	}

	if err := SaveUploadConfig(cfg); err != nil {
		return false, fmt.Errorf("failed to save config: %w", err)
	}

	return true, nil
}

