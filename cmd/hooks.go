package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var hooksProviderName string

var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Manage Confab hooks for a provider",
	Long:  `Add or remove confab hooks from the selected provider's settings file.`,
}

var hooksAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Install hooks",
	Long: `Installs the full Confab hook set for the selected provider.

For Claude Code: SessionStart/End, PreToolUse, PostToolUse, and
UserPromptSubmit hooks are installed in ~/.claude/settings.json.

For Codex: SessionStart, PreToolUse, and PostToolUse hooks are installed
in ~/.codex/config.toml. Shutdown stays parent-PID driven.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running hooks add command")
		providerName, err := provider.NormalizeName(hooksProviderName)
		if err != nil {
			return err
		}
		p, err := provider.Get(providerName)
		if err != nil {
			return err
		}

		fmt.Printf("Installing %s hooks...\n", p.Name())
		path, err := p.InstallHooks()
		if err != nil {
			logger.Error("Failed to install %s hooks: %v", p.Name(), err)
			return fmt.Errorf("failed to install %s hooks: %w", p.Name(), err)
		}
		logger.Info("%s hooks installed in %s", p.Name(), path)
		fmt.Printf("✓ %s hooks installed in %s\n", p.Name(), path)
		return nil
	},
}

var hooksRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove hooks",
	Long:  `Removes the Confab hook set for the selected provider.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running hooks remove command")
		providerName, err := provider.NormalizeName(hooksProviderName)
		if err != nil {
			return err
		}
		p, err := provider.Get(providerName)
		if err != nil {
			return err
		}

		fmt.Printf("Removing %s hooks...\n", p.Name())
		path, err := p.UninstallHooks()
		if err != nil {
			logger.Error("Failed to remove %s hooks: %v", p.Name(), err)
			return fmt.Errorf("failed to remove %s hooks: %w", p.Name(), err)
		}
		logger.Info("%s hooks removed from %s", p.Name(), path)
		fmt.Printf("✓ %s hooks removed from %s\n", p.Name(), path)
		return nil
	},
}

func init() {
	hooksCmd.PersistentFlags().StringVar(&hooksProviderName, "provider", provider.NameClaudeCode, "Provider to manage hooks for (claude-code or codex)")
	rootCmd.AddCommand(hooksCmd)
	hooksCmd.AddCommand(hooksAddCmd)
	hooksCmd.AddCommand(hooksRemoveCmd)
}
