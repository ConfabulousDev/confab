package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var hooksProviderName string

var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Manage Claude Code hooks",
	Long:  `Add or remove confab hooks from Claude Code settings.`,
}

var hooksAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Install hooks",
	Long: `Installs confab hooks in ~/.claude/settings.json.

Installs:
- SessionStart + SessionEnd hooks for background sync daemon
- PreToolUse hook to add session URLs to git commits and PRs
- PostToolUse hook to track created PRs on Confab
- UserPromptSubmit hook for prompt logging (debug)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running hooks add command")
		providerName, err := provider.NormalizeName(hooksProviderName)
		if err != nil {
			return err
		}
		if providerName == provider.NameCodex {
			return installCodexHooks()
		}

		fmt.Println("Installing sync hooks (SessionStart + SessionEnd)...")
		if err := config.InstallSyncHooks(); err != nil {
			logger.Error("Failed to install sync hooks: %v", err)
			return fmt.Errorf("failed to install sync hooks: %w", err)
		}

		fmt.Println("Installing PreToolUse hook (git commit trailers)...")
		if err := config.InstallPreToolUseHooks(); err != nil {
			logger.Error("Failed to install PreToolUse hooks: %v", err)
			return fmt.Errorf("failed to install PreToolUse hooks: %w", err)
		}

		fmt.Println("Installing PostToolUse hook (GitHub PR linking)...")
		if err := config.InstallPostToolUseHooks(); err != nil {
			logger.Error("Failed to install PostToolUse hooks: %v", err)
			return fmt.Errorf("failed to install PostToolUse hooks: %w", err)
		}

		fmt.Println("Installing UserPromptSubmit hook (prompt logging)...")
		if err := config.InstallUserPromptSubmitHook(); err != nil {
			logger.Error("Failed to install UserPromptSubmit hook: %v", err)
			return fmt.Errorf("failed to install UserPromptSubmit hook: %w", err)
		}

		settingsPath, _ := config.GetSettingsPath()
		logger.Info("Hooks installed in %s", settingsPath)
		fmt.Printf("✓ Hooks installed in %s\n", settingsPath)
		fmt.Println()
		fmt.Println("Confab will now:")
		fmt.Println("  - Sync sessions incrementally (every 30 seconds)")
		fmt.Println("  - Add session URLs to git commits and PRs")
		fmt.Println("  - Link PRs to Confab sessions")

		return nil
	},
}

var hooksRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove hooks",
	Long:  `Removes all confab hooks from ~/.claude/settings.json.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running hooks remove command")
		providerName, err := provider.NormalizeName(hooksProviderName)
		if err != nil {
			return err
		}
		if providerName == provider.NameCodex {
			return uninstallCodexHooks()
		}

		fmt.Println("Removing hooks...")
		if err := config.UninstallSyncHooks(); err != nil {
			logger.Error("Failed to remove sync hooks: %v", err)
			return fmt.Errorf("failed to remove sync hooks: %w", err)
		}

		if err := config.UninstallPreToolUseHooks(); err != nil {
			logger.Error("Failed to remove PreToolUse hooks: %v", err)
			return fmt.Errorf("failed to remove PreToolUse hooks: %w", err)
		}

		if err := config.UninstallPostToolUseHooks(); err != nil {
			logger.Error("Failed to remove PostToolUse hooks: %v", err)
			return fmt.Errorf("failed to remove PostToolUse hooks: %w", err)
		}

		if err := config.UninstallUserPromptSubmitHook(); err != nil {
			logger.Error("Failed to remove UserPromptSubmit hook: %v", err)
			return fmt.Errorf("failed to remove UserPromptSubmit hook: %w", err)
		}

		settingsPath, _ := config.GetSettingsPath()
		logger.Info("Hooks removed from %s", settingsPath)
		fmt.Printf("✓ Hooks removed from %s\n", settingsPath)
		fmt.Println()
		fmt.Println("Confab hooks have been removed.")

		return nil
	},
}

func installCodexHooks() error {
	fmt.Println("Installing Codex hooks...")
	fmt.Println("Enabling Codex feature flag: features.hooks = true")

	configPath, err := provider.Codex{}.InstallHooks()
	if err != nil {
		logger.Error("Failed to install Codex hooks: %v", err)
		return fmt.Errorf("failed to install Codex hooks: %w", err)
	}

	logger.Info("Codex hooks installed in %s", configPath)
	fmt.Printf("✓ Codex hooks installed in %s\n", configPath)
	fmt.Println()
	fmt.Println("Confab will now sync Codex root rollout sessions to the configured backend.")
	return nil
}

func uninstallCodexHooks() error {
	fmt.Println("Removing Codex hooks...")
	configPath, err := provider.Codex{}.UninstallHooks()
	if err != nil {
		logger.Error("Failed to remove Codex hooks: %v", err)
		return fmt.Errorf("failed to remove Codex hooks: %w", err)
	}
	logger.Info("Codex hooks removed from %s", configPath)
	fmt.Printf("✓ Codex hooks removed from %s\n", configPath)
	return nil
}

func init() {
	hooksCmd.PersistentFlags().StringVar(&hooksProviderName, "provider", provider.NameClaudeCode, "Provider to manage hooks for (claude-code or codex)")
	rootCmd.AddCommand(hooksCmd)
	hooksCmd.AddCommand(hooksAddCmd)
	hooksCmd.AddCommand(hooksRemoveCmd)
}
