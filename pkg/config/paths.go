package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeStateDirEnv is the environment variable to override the default
// Claude state directory. Mirrored from pkg/provider; the two must match.
const ClaudeStateDirEnv = "CONFAB_CLAUDE_DIR"

// DisableLinkFromGitHubEnv is the environment variable to disable GitHub
// linking. When set to any non-empty value, GitHub linking (commits and
// PRs) is disabled.
const DisableLinkFromGitHubEnv = "CONFAB_DISABLE_LINK_FROM_GITHUB"

// IsLinkFromGitHubDisabled returns true if GitHub linking is disabled
// via environment variable.
func IsLinkFromGitHubDisabled() bool {
	return os.Getenv(DisableLinkFromGitHubEnv) != ""
}

// GetClaudeStateDir returns the Claude state directory path.
// Defaults to ~/.claude but can be overridden with CONFAB_CLAUDE_DIR.
func GetClaudeStateDir() (string, error) {
	if envDir := os.Getenv(ClaudeStateDirEnv); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

