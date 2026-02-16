package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/spf13/cobra"
)

var autoupdateCmd = &cobra.Command{
	Use:   "autoupdate",
	Short: "Manage automatic update behavior",
	Long: `View or change whether confab automatically checks for and installs updates.

When enabled (the default), confab checks for updates on session start and
notifies about available updates on other commands.

Manual updates via 'confab update' are always available regardless of this setting.`,
	RunE: runAutoupdateStatus,
}

var autoupdateEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable automatic updates",
	RunE:  runAutoupdateEnable,
}

var autoupdateDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable automatic updates",
	RunE:  runAutoupdateDisable,
}

func init() {
	rootCmd.AddCommand(autoupdateCmd)
	autoupdateCmd.AddCommand(autoupdateEnableCmd)
	autoupdateCmd.AddCommand(autoupdateDisableCmd)
}

func runAutoupdateStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.GetUploadConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	if cfg.IsAutoUpdateEnabled() {
		fmt.Println("Auto-update is enabled")
	} else {
		fmt.Println("Auto-update is disabled")
	}
	return nil
}

func runAutoupdateEnable(cmd *cobra.Command, args []string) error {
	return setAutoUpdate(true)
}

func runAutoupdateDisable(cmd *cobra.Command, args []string) error {
	return setAutoUpdate(false)
}

func setAutoUpdate(enabled bool) error {
	cfg, err := config.GetUploadConfig()
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	cfg.AutoUpdate = &enabled

	if err := config.SaveUploadConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if enabled {
		fmt.Println("Auto-update enabled")
	} else {
		fmt.Println("Auto-update disabled")
	}
	return nil
}
