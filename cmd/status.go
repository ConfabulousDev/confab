package cmd

import (
	"fmt"

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

		orphans := printProviderSections()
		printOrphanFooter(orphans)

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
// fixed registry order and returns the canonical names of providers
// whose hooks are installed but whose CLI is missing from PATH.
func printProviderSections() []string {
	var orphans []string
	for _, name := range []string{provider.NameClaudeCode, provider.NameCodex} {
		p, err := provider.Get(name)
		if err != nil {
			continue
		}
		if printProviderBlock(p) {
			orphans = append(orphans, name)
		}
	}
	return orphans
}

// printProviderBlock renders a single provider's status block. Returns
// true if the provider is orphaned (hooks installed but CLI missing).
func printProviderBlock(p provider.Provider) bool {
	fmt.Printf("Provider: %s\n", p.Name())

	_, lookErr := provider.LookPath(p.CLIBinaryName())
	cliPresent := lookErr == nil
	if cliPresent {
		fmt.Println("  CLI: ✓ on PATH")
	} else {
		fmt.Println("  CLI: ✗ not on PATH")
	}

	hooksInstalled, err := p.IsHooksInstalled()
	orphaned := false
	switch {
	case err != nil:
		logger.Error("Failed to check hook status for %s: %v", p.Name(), err)
		fmt.Printf("  Hooks: ? (error: %v)\n", err)
	case hooksInstalled && !cliPresent:
		fmt.Println("  Hooks: ✓ Installed (orphaned — CLI not found)")
		orphaned = true
	case hooksInstalled:
		fmt.Println("  Hooks: ✓ Installed")
	default:
		fmt.Println("  Hooks: ✗ Not installed")
	}

	printSkillsRow(p)

	fmt.Println()
	return orphaned
}

// printSkillsRow renders the per-provider Skills line. Only Claude Code
// ships skills today; other providers are omitted to avoid noise.
func printSkillsRow(p provider.Provider) {
	if p.Name() != provider.NameClaudeCode {
		return
	}
	til := config.IsTilSkillInstalled()
	retro := config.IsRetroSkillInstalled()
	fmt.Printf("  Skills: /til %s, /retro %s\n", checkmark(til), checkmark(retro))
}

func checkmark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func printOrphanFooter(orphans []string) {
	for _, name := range orphans {
		fmt.Printf("⚠️  %s hooks are installed but the CLI is not on PATH.\n", name)
		fmt.Printf("   Run `confab hooks remove --provider %s` if you no longer use %s.\n", name, name)
		fmt.Println()
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
