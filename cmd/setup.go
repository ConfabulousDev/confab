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
1. Authenticates with the cloud backend (if not already logged in)
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

	// Apply default backend URL early so we can compare against saved config
	if backendURL == "" {
		backendURL = "https://confabulous.dev"
	}

	fmt.Println("=== Confab Setup ===")
	fmt.Println()

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
					fmt.Println("✅ Already authenticated")
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
		fmt.Println("✅ Redaction enabled (default patterns)")
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
	fmt.Println("✅ Sync hooks installed (SessionStart + SessionEnd)")

	if err := config.InstallPreToolUseHooks(); err != nil {
		logger.Error("Failed to install PreToolUse hooks: %v", err)
		return fmt.Errorf("failed to install PreToolUse hooks: %w", err)
	}
	fmt.Println("✅ PreToolUse hook installed (git commit trailers)")

	if err := config.InstallPostToolUseHooks(); err != nil {
		logger.Error("Failed to install PostToolUse hooks: %v", err)
		return fmt.Errorf("failed to install PostToolUse hooks: %w", err)
	}
	fmt.Println("✅ PostToolUse hook installed (GitHub PR linking)")

	if err := config.InstallUserPromptSubmitHook(); err != nil {
		logger.Error("Failed to install UserPromptSubmit hook: %v", err)
		return fmt.Errorf("failed to install UserPromptSubmit hook: %w", err)
	}
	fmt.Println("✅ UserPromptSubmit hook installed (teleport support)")

	settingsPath, _ := config.GetSettingsPath()
	logger.Info("Hooks installed in %s", settingsPath)
	fmt.Println()

	fmt.Println("=== Setup Complete ===")
	fmt.Println()
	fmt.Println("Confab will now:")
	fmt.Println("  - Sync sessions incrementally (every 30 seconds)")
	fmt.Println("  - Add session URLs to git commits and PRs")
	fmt.Println("  - Link created PRs to sessions on Confabulous")
	fmt.Println()
	fmt.Println("Try it out:")
	fmt.Println("  1. Start a new Claude Code session")
	fmt.Println("  2. Your session data will sync in the background")
	fmt.Println("  3. Run 'confab status' to check your setup")

	return nil
}

func init() {
	rootCmd.AddCommand(setupCmd)

	setupCmd.Flags().String("backend-url", "", "Backend API URL (default: https://confabulous.dev)")
	setupCmd.Flags().String("api-key", "", "API key (bypasses device auth flow)")
}
