package cmd

import (
	"errors"
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/utils"
)

// newAuthedClient builds an authenticated client against the default/top-level
// binding. It is the unchanged no-flag path: a thin wrapper over
// newAuthedClientForBinding with the default binding.
func newAuthedClient() (*confabhttp.Client, error) {
	return newAuthedClientForBinding(config.Binding{IsDefault: true})
}

// newAuthedClientForBinding builds an authenticated client against a specific
// (provider, config-dir) binding (kata hpec). For the default binding this is
// byte-identical to the old default path; for a non-default binding with no
// stored credentials it surfaces config.ErrNoBinding (never falls back to the
// default backend — leak-free).
func newAuthedClientForBinding(b config.Binding) (*confabhttp.Client, error) {
	cfg, err := config.EnsureAuthenticatedFor(b)
	if err != nil {
		return nil, err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}
	return client, nil
}

// clientForFlags resolves the authenticated client for the retrieval commands'
// shared --provider/--config-dir flags. With both empty it takes the unchanged
// default-binding path; otherwise it resolves the binding for the named provider
// at the given config dir and authenticates against that backend. --config-dir
// requires --provider (a config dir is provider-specific), mirroring setup.
func clientForFlags(providerName, configDir string) (*confabhttp.Client, error) {
	if providerName == "" && configDir == "" {
		return newAuthedClient()
	}
	if configDir != "" && providerName == "" {
		return nil, fmt.Errorf("--config-dir requires --provider (a config dir is provider-specific)")
	}

	p, err := provider.Get(providerName)
	if err != nil {
		return nil, err
	}
	b := provider.BindingFor(p, configDir)
	client, err := newAuthedClientForBinding(b)
	if err != nil {
		if errors.Is(err, config.ErrNoBinding) {
			return nil, fmt.Errorf("%w: run 'confab setup --provider %s --config-dir %s' first",
				err, p.Name(), configDir)
		}
		return nil, err
	}
	return client, nil
}

func translateSessionErr(err error, action string) error {
	if errors.Is(err, confabhttp.ErrSessionNotFound) {
		return fmt.Errorf("session not found")
	}
	return fmt.Errorf("failed to %s: %w", action, err)
}
