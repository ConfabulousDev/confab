package config

import (
	"fmt"
	"os"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// ClaudeStateDirEnv is the environment variable to override the default Claude state directory
const ClaudeStateDirEnv = provider.ClaudeStateDirEnv

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
	return provider.ClaudeCode{}.StateDir()
}

// GetProjectsDir returns the path to the Claude projects directory
func GetProjectsDir() (string, error) {
	projectsDir, err := provider.ClaudeCode{}.ProjectsDir()
	if err != nil {
		return "", fmt.Errorf("failed to get projects directory: %w", err)
	}
	return projectsDir, nil
}

// GetClaudeSettingsPath returns the path to the Claude settings file
func GetClaudeSettingsPath() (string, error) {
	settingsPath, err := provider.ClaudeCode{}.SettingsPath()
	if err != nil {
		return "", fmt.Errorf("failed to get settings path: %w", err)
	}
	return settingsPath, nil
}
