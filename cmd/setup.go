package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var setupProviderName string

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up confab (login + install hooks)",
	Long: `Complete setup for confab in one command.

This command:
1. Authenticates with the backend (if not already logged in)
2. Installs the full Confab hook set for the selected provider
3. Installs provider-specific skills (Claude only; no-op for Codex)

If you're already authenticated with a valid API key, the login step is
skipped. Use --api-key to provide an API key directly (bypasses device
auth flow).`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	logger.Info("Starting setup")
	providerName, err := provider.NormalizeName(setupProviderName)
	if err != nil {
		return err
	}
	p, err := provider.Get(providerName)
	if err != nil {
		return err
	}

	backendURL, needsLogin, err := runSetupAuth(cmd)
	if err != nil {
		return err
	}

	if needsLogin {
		fmt.Printf("Step 2/2: Installing %s hooks\n", p.Name())
	} else {
		fmt.Printf("Installing %s hooks...\n", p.Name())
	}
	fmt.Println()

	path, err := p.InstallHooks()
	if err != nil {
		logger.Error("Failed to install %s hooks: %v", p.Name(), err)
		return fmt.Errorf("failed to install %s hooks: %w", p.Name(), err)
	}
	logger.Info("%s hooks installed in %s", p.Name(), path)

	fmt.Println()
	fmt.Println("Installing skills...")
	if err := p.InstallSkills(); err != nil {
		logger.Error("Failed to install %s skills: %v", p.Name(), err)
		return fmt.Errorf("failed to install %s skills: %w", p.Name(), err)
	}

	fmt.Println()
	fmt.Printf("✅ Setup complete. %s sessions will sync to %s\n", p.Name(), backendURL)
	return nil
}

func runSetupAuth(cmd *cobra.Command) (backendURL string, needsLogin bool, err error) {
	backendURL, err = cmd.Flags().GetString("backend-url")
	if err != nil {
		return "", false, fmt.Errorf("failed to get backend-url flag: %w", err)
	}
	apiKey, err := cmd.Flags().GetString("api-key")
	if err != nil {
		return "", false, fmt.Errorf("failed to get api-key flag: %w", err)
	}

	fmt.Printf("Backend URL: %s\n", backendURL)
	fmt.Println()

	needsLogin = true
	if apiKey != "" {
		if err := loginWithAPIKey(backendURL, apiKey); err != nil {
			return "", false, err
		}
		fmt.Println()
		needsLogin = false
	} else {
		cfg, err := config.GetUploadConfig()
		if err == nil && cfg.APIKey != "" {
			if cfg.BackendURL == backendURL {
				fmt.Println("Checking existing authentication...")
				if err := verifyAPIKey(cfg); err == nil {
					logger.Info("Existing API key is valid, skipping login")
					fmt.Println("Already authenticated")
					fmt.Println()
					needsLogin = false
				} else {
					logger.Info("Existing API key is invalid: %v", err)
					fmt.Println("❌ Existing credentials invalid, need to re-authenticate")
					fmt.Println()
				}
			} else {
				logger.Info("Backend URL changed from %s to %s, need to re-login", cfg.BackendURL, backendURL)
				fmt.Println("Backend URL changed, need to re-authenticate")
				fmt.Println()
			}
		}

		if needsLogin {
			fmt.Println("Step 1/2: Authentication")
			fmt.Println()
			if err := doDeviceLogin(backendURL, defaultKeyName()); err != nil {
				return "", false, err
			}
			fmt.Println()
		}
	}

	if added, err := config.EnsureDefaultRedaction(); err != nil {
		logger.Warn("Failed to initialize redaction config: %v", err)
	} else if added {
		logger.Info("Initialized default redaction config")
		fmt.Println("Redaction enabled (default patterns)")
	}

	return backendURL, needsLogin, nil
}

func init() {
	rootCmd.AddCommand(setupCmd)

	setupCmd.Flags().StringVar(&setupProviderName, "provider", provider.NameClaudeCode, "Provider to set up (claude-code or codex)")
	setupCmd.Flags().String("backend-url", "", "Backend API URL (required)")
	setupCmd.MarkFlagRequired("backend-url")
	setupCmd.Flags().String("api-key", "", "API key (bypasses device auth flow)")
}
