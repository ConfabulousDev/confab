package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

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

	backendURL, err := cmd.Flags().GetString("backend-url")
	if err != nil {
		return fmt.Errorf("failed to get backend-url flag: %w", err)
	}

	apiKey, err := cmd.Flags().GetString("api-key")
	if err != nil {
		return fmt.Errorf("failed to get api-key flag: %w", err)
	}

	// Check if API key was provided directly
	needsLogin := true
	if apiKey != "" {
		if err := loginWithAPIKey(backendURL, apiKey); err != nil {
			return err
		}
		fmt.Println()
		needsLogin = false
	} else {
		// Check if already authenticated
		cfg, err := config.GetUploadConfig()
		if err == nil && cfg.APIKey != "" {
			// Check if backend URL matches
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

		// Login if needed
		if needsLogin {
			fmt.Println("Step 1/2: Authentication")
			fmt.Println()
			if err := doDeviceLogin(backendURL, defaultKeyName()); err != nil {
				return err
			}
			fmt.Println()
		}
	}

	// Ensure default redaction config exists
	added, err := config.EnsureDefaultRedaction()
	if err != nil {
		logger.Warn("Failed to initialize redaction config: %v", err)
	} else if added {
		logger.Info("Initialized default redaction config")
		fmt.Println("Redaction enabled (default patterns)")
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

	fmt.Println("✅ Setup complete. Claude Code sessions will sync to", backendURL)

	return nil
}

func init() {
	rootCmd.AddCommand(setupCmd)

	setupCmd.Flags().String("backend-url", "", "Backend API URL (required)")
	setupCmd.MarkFlagRequired("backend-url")
	setupCmd.Flags().String("api-key", "", "API key (bypasses device auth flow)")
}
