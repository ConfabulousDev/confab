package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show confab status",
	Long:  `Displays hook installation status and cloud authentication status.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()

		logger.Info("Running status command")

		fmt.Println("=== Confab: Status ===")
		fmt.Println()

		// Check sync hooks installation
		syncHooksInstalled, err := config.IsSyncHooksInstalled()
		if err != nil {
			logger.Error("Failed to check sync hook status: %v", err)
			return fmt.Errorf("failed to check hook status: %w", err)
		}

		logger.Info("Sync hooks installed: %v", syncHooksInstalled)
		if syncHooksInstalled {
			fmt.Println("Sync Hooks: ✓ Installed")
		} else {
			fmt.Println("Sync Hooks: ✗ Not installed")
		}

		// Check PreToolUse hook installation
		preToolUseInstalled, err := config.IsPreToolUseHooksInstalled()
		if err != nil {
			logger.Error("Failed to check PreToolUse hook status: %v", err)
		}

		logger.Info("PreToolUse hook installed: %v", preToolUseInstalled)
		if preToolUseInstalled {
			fmt.Println("PreToolUse Hook: ✓ Installed (git commit trailers)")
		} else {
			fmt.Println("PreToolUse Hook: ✗ Not installed")
		}

		// Check PostToolUse hook installation
		postToolUseInstalled, err := config.IsPostToolUseHooksInstalled()
		if err != nil {
			logger.Error("Failed to check PostToolUse hook status: %v", err)
		}

		logger.Info("PostToolUse hook installed: %v", postToolUseInstalled)
		if postToolUseInstalled {
			fmt.Println("PostToolUse Hook: ✓ Installed (GitHub PR linking)")
		} else {
			fmt.Println("PostToolUse Hook: ✗ Not installed")
		}

		// Check UserPromptSubmit hook installation
		userPromptSubmitInstalled, err := config.IsUserPromptSubmitHookInstalled()
		if err != nil {
			logger.Error("Failed to check UserPromptSubmit hook status: %v", err)
		}

		logger.Info("UserPromptSubmit hook installed: %v", userPromptSubmitInstalled)
		if userPromptSubmitInstalled {
			fmt.Println("UserPromptSubmit Hook: ✓ Installed (teleport support)")
		} else {
			fmt.Println("UserPromptSubmit Hook: ✗ Not installed")
		}

		if !syncHooksInstalled || !preToolUseInstalled || !postToolUseInstalled || !userPromptSubmitInstalled {
			fmt.Println()
			fmt.Println("Run 'confab setup' to install missing hooks.")
		}

		fmt.Println()

		// Check cloud sync status
		cfg, err := config.GetUploadConfig()
		if err != nil {
			logger.Error("Failed to get cloud config: %v", err)
			fmt.Println("Cloud Sync: ✗ Configuration error")
		} else {
			fmt.Println("Cloud Sync:")
			if cfg.APIKey != "" {
				fmt.Printf("  Backend: %s\n", cfg.BackendURL)

				// Validate API key
				fmt.Print("  Validating API key... ")
				if err := validateAPIKey(cfg.BackendURL, cfg.APIKey); err != nil {
					logger.Error("API key validation failed: %v", err)
					fmt.Println("✗ Invalid")
					fmt.Printf("  Error: %v\n", err)
					fmt.Println("  Run 'confab login' to re-authenticate")
				} else {
					logger.Info("API key is valid")
					fmt.Println("✓ Valid")
					fmt.Println("  Status: ✓ Authenticated and ready")
				}
			} else {
				fmt.Println("  Status: ✗ Not configured")
				fmt.Println("  Run 'confab login' to authenticate")
			}
		}

		fmt.Println()

		return nil
	},
}

// validateAPIKey checks if the API key is valid by calling the backend
func validateAPIKey(backendURL, apiKey string) error {
	// Create a temporary config for the HTTP client
	cfg := &config.UploadConfig{
		BackendURL: backendURL,
		APIKey:     apiKey,
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
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

func init() {
	rootCmd.AddCommand(statusCmd)
}
