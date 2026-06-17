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
in ~/.codex/config.toml. Shutdown stays parent-PID driven.

For Cursor: sessionStart and sessionEnd hooks are installed in
~/.cursor/hooks.json (no commit/PR linking).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running hooks add command")
		targets, err := hooksAddTargets()
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println("No supported provider CLIs detected; no hooks installed.")
			return nil
		}
		for _, p := range targets {
			fmt.Printf("Installing %s hooks...\n", p.Name())
			path, err := p.InstallHooks()
			if err != nil {
				logger.Error("Failed to install %s hooks: %v", p.Name(), err)
				return fmt.Errorf("failed to install %s hooks: %w", p.Name(), err)
			}
			logger.Info("%s hooks installed in %s", p.Name(), path)
			fmt.Printf("✓ %s hooks installed in %s\n", p.Name(), path)
		}
		return nil
	},
}

var hooksRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove hooks",
	Long:  `Removes the Confab hook set for the selected provider.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running hooks remove command")
		targets, err := hooksRemoveTargets()
		if err != nil {
			return err
		}
		for _, p := range targets {
			fmt.Printf("Removing %s hooks...\n", p.Name())
			path, err := p.UninstallHooks()
			if err != nil {
				logger.Error("Failed to remove %s hooks: %v", p.Name(), err)
				return fmt.Errorf("failed to remove %s hooks: %w", p.Name(), err)
			}
			logger.Info("%s hooks removed from %s", p.Name(), path)
			fmt.Printf("✓ %s hooks removed from %s\n", p.Name(), path)
		}
		return nil
	},
}

func hooksAddTargets() ([]provider.Provider, error) {
	return detectedOrNamedProviders(hooksProviderName)
}

func hooksRemoveTargets() ([]provider.Provider, error) {
	return allOrNamedProviders(hooksProviderName)
}

// providersByName resolves each name via provider.Get.
func providersByName(names []string) ([]provider.Provider, error) {
	targets := make([]provider.Provider, 0, len(names))
	for _, name := range names {
		p, err := provider.Get(name)
		if err != nil {
			return nil, err
		}
		targets = append(targets, p)
	}
	return targets, nil
}

// detectedOrNamedProviders resolves the target list for `add` subcommands
// (hooks/skills). With an explicit providerName it scopes to that one;
// otherwise it auto-detects installed providers (matching setup — no hardcoded
// claude-code default). Every provider implements InstallHooks/InstallSkills,
// so no provider is filtered out.
func detectedOrNamedProviders(providerName string) ([]provider.Provider, error) {
	if providerName != "" {
		return providersByName([]string{providerName})
	}
	return providersByName(provider.DetectInstalled())
}

// allOrNamedProviders resolves the target list for `remove` subcommands
// (hooks/skills). With an explicit providerName it scopes to that one;
// otherwise it returns every provider regardless of current detection, so a
// leftover hook/skill (or OpenCode plugin) in any provider is cleaned up.
func allOrNamedProviders(providerName string) ([]provider.Provider, error) {
	if providerName != "" {
		return providersByName([]string{providerName})
	}
	return providersByName([]string{
		provider.NameClaudeCode,
		provider.NameCodex,
		provider.NameCursor,
		provider.NameOpencode,
	})
}

func init() {
	hooksCmd.PersistentFlags().StringVar(&hooksProviderName, "provider", "", "Provider to manage hooks for (claude-code, codex, opencode, or cursor); defaults to detected providers for add and all providers for remove")
	rootCmd.AddCommand(hooksCmd)
	hooksCmd.AddCommand(hooksAddCmd)
	hooksCmd.AddCommand(hooksRemoveCmd)
}
