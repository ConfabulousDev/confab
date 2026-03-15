// ABOUTME: CLI commands for managing Claude Code skills installed by confab.
// ABOUTME: confab skills add/remove — analogous to confab hooks add/remove but for skill files.
package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage Claude Code skills",
	Long:  `Add or remove confab skills from Claude Code.`,
}

var skillsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Install skills",
	Long: `Installs confab skills in ~/.claude/skills/.

Installs:
- /til skill for capturing TILs (Today I Learned) during sessions`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running skills add command")

		fmt.Println("Installing /til skill...")
		if err := config.InstallTilSkill(); err != nil {
			logger.Error("Failed to install /til skill: %v", err)
			return fmt.Errorf("failed to install /til skill: %w", err)
		}

		claudeDir, err := config.GetClaudeStateDir()
		if err != nil {
			return fmt.Errorf("failed to get Claude state directory: %w", err)
		}
		logger.Info("Skills installed in %s/skills/", claudeDir)
		fmt.Printf("✓ Skills installed in %s/skills/\n", claudeDir)
		fmt.Println()
		fmt.Println("Available skills:")
		fmt.Println("  /til — capture TILs during your session")

		return nil
	},
}

var skillsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove skills",
	Long:  `Removes all confab skills from ~/.claude/skills/.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running skills remove command")

		fmt.Println("Removing skills...")
		if err := config.UninstallTilSkill(); err != nil {
			logger.Error("Failed to remove /til skill: %v", err)
			return fmt.Errorf("failed to remove /til skill: %w", err)
		}

		fmt.Println("✓ Skills removed.")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(skillsCmd)
	skillsCmd.AddCommand(skillsAddCmd)
	skillsCmd.AddCommand(skillsRemoveCmd)
}
