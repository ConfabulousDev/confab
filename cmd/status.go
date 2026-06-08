package cmd

import (
	"fmt"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show confab status",
	Long:  `Displays backend authentication and per-provider hook/skill state for every supported provider.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		logger.Info("Running status command")

		fmt.Println("=== Confab Status ===")
		fmt.Println()

		printBackendSection()

		printProviderSections()

		return nil
	},
}

func printBackendSection() {
	fmt.Println("Backend Sync:")
	cfg, err := config.GetUploadConfig()
	if err != nil {
		logger.Error("Failed to get backend config: %v", err)
		fmt.Println("  ✗ Configuration error")
		fmt.Println()
		return
	}
	if cfg.APIKey == "" {
		fmt.Println("  Status: ✗ Not configured")
		fmt.Println("  Run 'confab login' to authenticate")
		fmt.Println()
		return
	}
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
	fmt.Println()
}

// printProviderSections renders one block per registered provider in
// fixed registry order.
func printProviderSections() {
	for _, name := range provider.OrderedNames() {
		p, err := provider.Get(name)
		if err != nil {
			continue
		}
		printProviderBlock(p)
	}
}

// printProviderBlock renders a single provider's status block. A provider
// is considered present when its CLI is on PATH OR its state/config dir
// exists (desktop-app or CLI-uninstalled install) — the same litmus
// `confab setup` uses (CF-572). Installed hooks live inside that state
// dir, so there is no "orphaned hooks" state to surface.
func printProviderBlock(p provider.Provider) {
	fmt.Printf("Provider: %s\n", p.Name())

	_, lookErr := provider.LookPath(p.CLIBinaryName())
	cliPresent := lookErr == nil
	switch {
	case cliPresent:
		fmt.Println("  CLI: ✓ on PATH")
	case provider.StateDirPresent(p):
		fmt.Println("  CLI: ✗ not on PATH (state dir present)")
	default:
		fmt.Println("  CLI: ✗ not on PATH")
	}

	hooksInstalled, err := p.IsHooksInstalled()
	switch {
	case err != nil:
		logger.Error("Failed to check hook status for %s: %v", p.Name(), err)
		fmt.Printf("  Hooks: ? (error: %v)\n", err)
	case hooksInstalled:
		fmt.Println("  Hooks: ✓ Installed")
	default:
		fmt.Println("  Hooks: ✗ Not installed")
	}

	printSkillsRow(p)

	fmt.Println()
}

// printSkillsRow renders the per-provider Skills line for shipped skills.
func printSkillsRow(p provider.Provider) {
	var parts []string
	for _, name := range config.BundledSkillNames() {
		parts = append(parts, fmt.Sprintf("/%s %s", name, checkmark(p.IsSkillInstalled(name))))
	}
	fmt.Printf("  Skills: %s\n", strings.Join(parts, ", "))
}

func checkmark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
