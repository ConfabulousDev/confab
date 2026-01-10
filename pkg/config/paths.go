package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeStateDirEnv is the environment variable to override the default Claude state directory
const ClaudeStateDirEnv = "CONFAB_CLAUDE_DIR"

// DisableLinkFromGitHubEnv is the environment variable to disable GitHub linking.
// When set to any non-empty value, GitHub linking (commits and PRs) is disabled.
const DisableLinkFromGitHubEnv = "CONFAB_DISABLE_LINK_FROM_GITHUB"

// IsLinkFromGitHubDisabled returns true if GitHub linking is disabled via environment variable.
func IsLinkFromGitHubDisabled() bool {
	return os.Getenv(DisableLinkFromGitHubEnv) != ""
}

// GetClaudeStateDir returns the Claude state directory path.
// Defaults to ~/.claude but can be overridden with CONFAB_CLAUDE_DIR env var.
// This is useful for testing and non-standard installations.
func GetClaudeStateDir() (string, error) {
	// Check environment variable first
	if envDir := os.Getenv(ClaudeStateDirEnv); envDir != "" {
		return envDir, nil
	}

	// Default to ~/.claude
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(home, ".claude"), nil
}

// GetProjectsDir returns the path to the Claude projects directory
func GetProjectsDir() (string, error) {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return "", fmt.Errorf("failed to get claude state directory: %w", err)
	}
	return filepath.Join(claudeDir, "projects"), nil
}

// GetSettingsPath returns the path to the Claude settings file
func GetClaudeSettingsPath() (string, error) {
	claudeDir, err := GetClaudeStateDir()
	if err != nil {
		return "", fmt.Errorf("failed to get claude state directory: %w", err)
	}
	return filepath.Join(claudeDir, "settings.json"), nil
}
