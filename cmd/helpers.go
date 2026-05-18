package cmd

import (
	"errors"
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
)

func newAuthedClient() (*confabhttp.Client, error) {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return nil, err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}
	return client, nil
}

func translateSessionErr(err error, action string) error {
	if errors.Is(err, confabhttp.ErrSessionNotFound) {
		return fmt.Errorf("session not found")
	}
	return fmt.Errorf("failed to %s: %w", action, err)
}
