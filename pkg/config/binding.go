package config

import (
	"errors"
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/pathcanon"
)

// BindingCreds holds the backend credentials for one (provider, config dir).
// Only the credentials vary per binding; redaction/log-level/auto-update are
// read from the global top-level config.
type BindingCreds struct {
	BackendURL string `json:"backend_url"`
	APIKey     string `json:"api_key"`
}

// Binding identifies a (provider, config dir) backend target. The default
// binding (IsDefault) maps to the top-level config fields for backward
// compatibility; any other binding maps to Bindings[Provider][Dir].
type Binding struct {
	Provider  string
	Dir       string // canonical config dir; empty for the default binding
	IsDefault bool
}

// ErrNoBinding is returned by GetUploadConfigFor when a non-default binding
// has no stored credentials. Callers MUST NOT fall back to the default
// (top-level) config — doing so would silently sync a custom-dir session to
// the wrong backend (leak-free policy).
var ErrNoBinding = errors.New("no confab binding for the requested provider/config dir")

// ResolveBinding builds the Binding for (provider, dir), treating dir as the
// default when it is empty or canonically equal to defaultDir. defaultDir is
// passed in (rather than resolved here) because pkg/config sits below
// pkg/provider and must not import it.
func ResolveBinding(provider, dir, defaultDir string) Binding {
	if dir == "" {
		return Binding{Provider: provider, IsDefault: true}
	}
	canonical := pathcanon.CanonicalDir(dir)
	if canonical == pathcanon.CanonicalDir(defaultDir) {
		return Binding{Provider: provider, IsDefault: true}
	}
	return Binding{Provider: provider, Dir: canonical}
}

// GetUploadConfigFor returns the effective UploadConfig for a binding: global
// fields (redaction, log level, auto-update) from the top-level config, with
// BackendURL/APIKey from the binding. For the default binding this is exactly
// GetUploadConfig(). For a non-default binding with no stored credentials it
// returns ErrNoBinding (callers must not fall back to the default).
func GetUploadConfigFor(b Binding) (*UploadConfig, error) {
	cfg, err := GetUploadConfig()
	if err != nil {
		return nil, err
	}
	if b.IsDefault {
		return cfg, nil
	}
	creds, ok := cfg.Bindings[b.Provider][b.Dir]
	if !ok {
		return nil, fmt.Errorf("%w: %s at %s", ErrNoBinding, b.Provider, b.Dir)
	}
	merged := *cfg
	merged.BackendURL = creds.BackendURL
	merged.APIKey = creds.APIKey
	merged.Bindings = nil // the effective config is for a single backend
	return &merged, nil
}

// SetBindingCredentials writes backendURL/apiKey to the binding's slot: the
// top-level fields for the default binding, or Bindings[provider][dir]
// otherwise. Global fields are preserved.
func SetBindingCredentials(b Binding, backendURL, apiKey string) error {
	if err := validateBackendURL(backendURL); err != nil {
		return fmt.Errorf("invalid backend URL: %w", err)
	}
	if err := validateAPIKey(apiKey); err != nil {
		return fmt.Errorf("invalid API key: %w", err)
	}

	cfg, err := GetUploadConfig()
	if err != nil {
		return err
	}

	if b.IsDefault {
		cfg.BackendURL = backendURL
		cfg.APIKey = apiKey
	} else {
		if cfg.Bindings == nil {
			cfg.Bindings = map[string]map[string]BindingCreds{}
		}
		if cfg.Bindings[b.Provider] == nil {
			cfg.Bindings[b.Provider] = map[string]BindingCreds{}
		}
		cfg.Bindings[b.Provider][b.Dir] = BindingCreds{BackendURL: backendURL, APIKey: apiKey}
	}

	return SaveUploadConfig(cfg)
}

// EnsureAuthenticatedFor is GetUploadConfigFor plus a credential check,
// mirroring EnsureAuthenticated for a specific binding.
func EnsureAuthenticatedFor(b Binding) (*UploadConfig, error) {
	cfg, err := GetUploadConfigFor(b)
	if err != nil {
		return nil, err
	}
	if cfg.BackendURL == "" || cfg.APIKey == "" {
		return nil, fmt.Errorf("not authenticated. Run 'confab setup' for this config dir first")
	}
	return cfg, nil
}

// HasBindings reports whether any non-default bindings exist for the provider.
// Hook handlers use this as the no-bindings short-circuit: pure single-dir
// users (no bindings) skip derivation entirely and take the default path.
func HasBindings(provider string) (bool, error) {
	cfg, err := GetUploadConfig()
	if err != nil {
		return false, err
	}
	return len(cfg.Bindings[provider]) > 0, nil
}
