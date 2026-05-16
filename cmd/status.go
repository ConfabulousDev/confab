package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var statusProviderName string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show confab status",
	Long:  `Displays hook installation status and backend authentication status for the selected provider.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()

		logger.Info("Running status command (provider=%s)", statusProviderName)

		providerName, err := provider.NormalizeName(statusProviderName)
		if err != nil {
			return err
		}
		p, err := provider.Get(providerName)
		if err != nil {
			return err
		}

		fmt.Printf("=== Confab: %s Status ===\n\n", p.Name())

		installed, err := p.IsHooksInstalled()
		if err != nil {
			logger.Error("Failed to check hook status: %v", err)
			return fmt.Errorf("failed to check hook status: %w", err)
		}
		logger.Info("%s hooks installed: %v", p.Name(), installed)
		if installed {
			fmt.Println("Hooks: ✓ Installed")
		} else {
			fmt.Println("Hooks: ✗ Not installed")
			fmt.Printf("Run 'confab hooks add --provider %s' to install.\n", p.Name())
		}

		fmt.Println()
		fmt.Println("=== Skills ===")
		fmt.Println()

		tilInstalled := config.IsTilSkillInstalled()
		if tilInstalled {
			fmt.Println("/til Skill: ✓ Installed")
		} else {
			fmt.Println("/til Skill: ✗ Not installed")
		}

		retroInstalled := config.IsRetroSkillInstalled()
		if retroInstalled {
			fmt.Println("/retro Skill: ✓ Installed")
		} else {
			fmt.Println("/retro Skill: ✗ Not installed")
		}

		if !tilInstalled || !retroInstalled {
			fmt.Println()
			fmt.Println("Run 'confab skills add' to install missing skills.")
		}

		fmt.Println()

		cfg, err := config.GetUploadConfig()
		if err != nil {
			logger.Error("Failed to get backend config: %v", err)
			fmt.Println("Backend Sync: ✗ Configuration error")
		} else {
			fmt.Println("Backend Sync:")
			if cfg.APIKey != "" {
				fmt.Printf("  Backend: %s\n", cfg.BackendURL)
				fmt.Print("  Validating API key... ")
				if err := verifyAPIKey(cfg); err != nil {
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

func init() {
	statusCmd.Flags().StringVar(&statusProviderName, "provider", provider.NameClaudeCode, "Provider to check status for (claude-code or codex)")
	rootCmd.AddCommand(statusCmd)
}
