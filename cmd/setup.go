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
2. Installs hooks (sync daemon + git commit trailers + PR linking)

If you're already authenticated with a valid API key, the login step is skipped.

Use --api-key to provide an API key directly (bypasses device auth flow).`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	logger.Info("Starting setup")
	providerName, err := provider.NormalizeName(setupProviderName)
	if err != nil {
		return err
	}
	if providerName == provider.NameCodex {
		return runCodexSetup(cmd)
	}

	backendURL, needsLogin, err := runSetupAuth(cmd)
	if err != nil {
		return err
	}

	// Install hooks
	if needsLogin {
		fmt.Println("Step 2/2: Installing hooks")
	} else {
		fmt.Println("Installing hooks...")
	}
	fmt.Println()

	if err := config.InstallSyncHooks(); err != nil {
		logger.Error("Failed to install sync hooks: %v", err)
		return fmt.Errorf("failed to install sync hooks: %w", err)
	}

	if err := config.InstallPreToolUseHooks(); err != nil {
		logger.Error("Failed to install PreToolUse hooks: %v", err)
		return fmt.Errorf("failed to install PreToolUse hooks: %w", err)
	}

	if err := config.InstallPostToolUseHooks(); err != nil {
		logger.Error("Failed to install PostToolUse hooks: %v", err)
		return fmt.Errorf("failed to install PostToolUse hooks: %w", err)
	}

	if err := config.InstallUserPromptSubmitHook(); err != nil {
		logger.Error("Failed to install UserPromptSubmit hook: %v", err)
		return fmt.Errorf("failed to install UserPromptSubmit hook: %w", err)
	}

	settingsPath, _ := config.GetSettingsPath()
	logger.Info("Hooks installed in %s", settingsPath)

	// Install skills
	fmt.Println()
	fmt.Println("Installing skills...")
	fmt.Println()

	if err := config.InstallTilSkill(); err != nil {
		logger.Error("Failed to install /til skill: %v", err)
		return fmt.Errorf("failed to install /til skill: %w", err)
	}
	fmt.Println("  ✓ /til skill")

	if err := config.InstallRetroSkill(); err != nil {
		logger.Error("Failed to install /retro skill: %v", err)
		return fmt.Errorf("failed to install /retro skill: %w", err)
	}
	fmt.Println("  ✓ /retro skill")

	fmt.Println()
	fmt.Println("✅ Setup complete. Claude Code sessions will sync to", backendURL)

	return nil
}

func runCodexSetup(cmd *cobra.Command) error {
	fmt.Println("Setting up Confab for Codex")
	fmt.Println()

	backendURL, needsLogin, err := runSetupAuth(cmd)
	if err != nil {
		return err
	}

	if needsLogin {
		fmt.Println("Step 2/2: Installing Codex hooks")
	} else {
		fmt.Println("Installing Codex hooks...")
	}
	fmt.Println("Installing Codex hooks in ~/.codex/config.toml...")
	fmt.Println("Enabling Codex feature flag: features.hooks = true")

	configPath, err := provider.Codex{}.InstallHooks()
	if err != nil {
		logger.Error("Failed to install Codex hooks: %v", err)
		return fmt.Errorf("failed to install Codex hooks: %w", err)
	}

	fmt.Println()
	fmt.Printf("✅ Setup complete. Codex hooks installed in %s\n", configPath)
	fmt.Println("Codex root rollout sessions will sync to", backendURL)
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
