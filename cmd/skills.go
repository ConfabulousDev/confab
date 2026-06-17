// ABOUTME: CLI commands for managing bundled provider skills installed by confab.
// ABOUTME: confab skills add/remove — analogous to confab hooks add/remove but for skill files.
package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var skillsProviderName string

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage Confab skills",
	Long:  `Add or remove bundled confab skills from supported providers.`,
}

var skillsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Install skills",
	Long: `Installs bundled confab skills for detected providers by default.

Installs:
- /retro skill for reviewing and discussing session transcripts`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running skills add command")

		targets, err := skillsAddTargets()
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println("No supported provider CLIs detected; no skills installed.")
			return nil
		}
		for _, p := range targets {
			fmt.Printf("Installing %s skills...\n", p.Name())
			if err := p.InstallSkills(); err != nil {
				logger.Error("Failed to install %s skills: %v", p.Name(), err)
				return fmt.Errorf("failed to install %s skills: %w", p.Name(), err)
			}
			stateDir, err := p.StateDir()
			if err != nil {
				return fmt.Errorf("failed to get %s state directory: %w", p.Name(), err)
			}
			logger.Info("%s skills installed in %s/skills/", p.Name(), stateDir)
			fmt.Printf("✓ %s skills installed in %s/skills/\n", p.Name(), stateDir)
		}
		fmt.Println()
		fmt.Println("Available skills:")
		fmt.Println("  /retro — review and discuss session transcripts")

		return nil
	},
}

var skillsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove skills",
	Long:  `Removes bundled confab skills from all supported providers by default.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running skills remove command")

		targets, err := skillsRemoveTargets()
		if err != nil {
			return err
		}
		for _, p := range targets {
			fmt.Printf("Removing %s skills...\n", p.Name())
			if err := p.UninstallSkills(); err != nil {
				logger.Error("Failed to remove %s skills: %v", p.Name(), err)
				return fmt.Errorf("failed to remove %s skills: %w", p.Name(), err)
			}
		}

		fmt.Println("✓ Skills removed.")

		return nil
	},
}

func skillsAddTargets() ([]provider.Provider, error) {
	return detectedOrNamedProviders(skillsProviderName)
}

func skillsRemoveTargets() ([]provider.Provider, error) {
	return allOrNamedProviders(skillsProviderName)
}

func init() {
	skillsCmd.PersistentFlags().StringVar(&skillsProviderName, "provider", "", "Provider to manage skills for (claude-code, codex, or cursor); defaults to detected providers for add and all providers for remove")
	rootCmd.AddCommand(skillsCmd)
	skillsCmd.AddCommand(skillsAddCmd)
	skillsCmd.AddCommand(skillsRemoveCmd)
}
