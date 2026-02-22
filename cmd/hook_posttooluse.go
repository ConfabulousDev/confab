package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/git"
	"github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	pkgsync "github.com/ConfabulousDev/confab/pkg/sync"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

// prURLPattern matches GitHub PR URLs in output
// Matches: https://github.com/owner/repo/pull/123
var prURLPattern = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/pull/\d+`)

var hookPostToolUseCmd = &cobra.Command{
	Use:   "post-tool-use",
	Short: "Handle PostToolUse hook events",
	Long: `Handler for PostToolUse hook events from Claude Code.

For successful PR creation (gh pr create, GitHub MCP tool), extracts the PR URL
from the output and links it to the current Confab session.

For successful git commits or pushes, retrieves the HEAD commit SHA via git
rev-parse and links the GitHub commit URL to the current Confab session.

For all other tool calls, exits silently (code 0).

This command is typically invoked by Claude Code, not directly by users.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return handlePostToolUse(os.Stdin, os.Stdout)
	},
}

func init() {
	hookCmd.AddCommand(hookPostToolUseCmd)
}

// handlePostToolUse processes PostToolUse hook events.
// Errors are logged but not printed to stderr - tool hooks run frequently
// and visible errors would be too noisy. See SessionStart hook for visible errors.
func handlePostToolUse(r io.Reader, _ io.Writer) error {
	// Check if GitHub linking is disabled
	if config.IsLinkFromGitHubDisabled() {
		logger.Info("GitHub linking disabled via %s", config.DisableLinkFromGitHubEnv)
		return nil
	}

	hookInput, err := types.ReadHookInput(r)
	if err != nil {
		logger.Warn("Failed to read hook input: %v", err)
		return nil // Exit silently, don't block Claude
	}

	// Check for MCP GitHub PR creation tool
	if hookInput.ToolName == config.ToolNameMCPGitHubCreatePR {
		return handleMCPPRCreateOutput(hookInput)
	}

	// Only process Bash tool calls
	if hookInput.ToolName != config.ToolNameBash {
		return nil
	}

	// Extract command from tool_input
	command, ok := hookInput.ToolInput["command"].(string)
	if !ok || command == "" {
		return nil
	}

	// Check for gh pr create
	if findGHPRCreatePosition(command) >= 0 {
		prURL := extractPRURLFromResponse(hookInput.ToolResponse)
		if prURL == "" {
			logger.Debug("No PR URL found in tool response")
			return nil
		}
		return linkGitHubURL(hookInput.SessionID, prURL)
	}

	// Check for git commit
	if findGitCommitPosition(command) >= 0 {
		// Only link if the command succeeded (no error indicators)
		if !isSuccessfulBashResponse(hookInput.ToolResponse) {
			logger.Debug("Git commit command did not succeed, skipping link")
			return nil
		}
		return linkCommitToSession(hookInput.SessionID, hookInput.CWD)
	}

	// Check for git push
	if findGitPushPosition(command) >= 0 {
		// Only link if the command succeeded (no error indicators)
		if !isSuccessfulBashResponse(hookInput.ToolResponse) {
			logger.Debug("Git push command did not succeed, skipping link")
			return nil
		}
		return linkCommitToSession(hookInput.SessionID, hookInput.CWD)
	}

	return nil
}

// handleMCPPRCreateOutput handles PostToolUse for GitHub MCP PR creation
func handleMCPPRCreateOutput(hookInput *types.HookInput) error {
	// Extract PR URL from MCP tool response
	prURL := extractPRURLFromResponse(hookInput.ToolResponse)
	if prURL == "" {
		logger.Debug("No PR URL found in MCP tool response")
		return nil
	}

	return linkGitHubURL(hookInput.SessionID, prURL)
}

// linkGitHubURL links a GitHub URL (PR or commit) to the current Confab session
func linkGitHubURL(claudeSessionID, githubURL string) error {
	logger.Info("Linking GitHub URL to session: %s", githubURL)

	// Get the Confab session ID from daemon state
	confabSessionID, err := getConfabSessionID(claudeSessionID)
	if err != nil || confabSessionID == "" {
		logger.Warn("GitHub link failed: no Confab session ID available (err=%v)", err)
		return nil // Can't link without session ID, but don't error
	}

	// Get upload config for API client
	cfg, err := config.GetUploadConfig()
	if err != nil {
		logger.Warn("GitHub link failed: %v", err)
		return nil // Best-effort linking
	}

	// Create sync client
	client, err := pkgsync.NewClient(cfg)
	if err != nil {
		logger.Warn("GitHub link failed: %v", err)
		return nil
	}

	// Link the URL
	_, err = client.LinkGitHub(confabSessionID, &pkgsync.GitHubLinkRequest{
		URL:    githubURL,
		Source: "cli_hook",
	})
	if err != nil {
		if errors.Is(err, http.ErrConflict) {
			logger.Info("GitHub link already exists: %s -> session %s", githubURL, confabSessionID)
			return nil
		}
		logger.Warn("GitHub link failed: %v", err)
		return nil // Best-effort, log and continue
	}

	logger.Info("GitHub link success: %s -> session %s", githubURL, confabSessionID)
	return nil
}

// linkCommitToSession links a git commit to the current Confab session.
// It gets the HEAD commit SHA and repo URL via git commands, then constructs
// the GitHub commit URL.
func linkCommitToSession(claudeSessionID, cwd string) error {
	if cwd == "" {
		logger.Warn("GitHub commit link failed: no CWD provided")
		return nil
	}

	// Get the HEAD commit SHA
	commitSHA, err := git.GetHeadSHA(cwd)
	if err != nil || commitSHA == "" {
		logger.Warn("GitHub commit link failed: could not get HEAD SHA from %s (err=%v)", cwd, err)
		return nil
	}

	logger.Info("Linking commit to session: %s", commitSHA)

	// Get the repo URL from git
	repoURL, err := git.GetRepoURL(cwd)
	if err != nil || repoURL == "" {
		logger.Warn("GitHub commit link failed: could not get repo URL from %s (err=%v)", cwd, err)
		return nil
	}

	// Convert to GitHub HTTPS URL
	githubURL := git.ToGitHubURL(repoURL)
	if githubURL == "" {
		logger.Info("GitHub commit link skipped: repo is not on GitHub (%s)", repoURL)
		return nil
	}

	// Construct the commit URL
	commitURL := githubURL + "/commit/" + commitSHA

	return linkGitHubURL(claudeSessionID, commitURL)
}

// isSuccessfulBashResponse checks if a Bash tool response indicates success.
// Returns false if exit_code is non-zero or if there's only stderr output.
func isSuccessfulBashResponse(response map[string]any) bool {
	if response == nil {
		return false
	}

	// Check for non-zero exit code
	if exitCode, ok := response["exit_code"]; ok {
		switch v := exitCode.(type) {
		case float64:
			if v != 0 {
				return false
			}
		case int:
			if v != 0 {
				return false
			}
		}
	}

	// If there's stderr but no stdout, likely a failure
	_, hasStdout := response["stdout"]
	stderr, hasStderr := response["stderr"].(string)
	if hasStderr && stderr != "" && !hasStdout {
		return false
	}

	return true
}

// extractPRURLFromResponse extracts a GitHub PR URL from tool response
func extractPRURLFromResponse(response map[string]any) string {
	if response == nil {
		return ""
	}

	// Serialize the response to JSON and search for PR URL
	data, err := json.Marshal(response)
	if err != nil {
		return ""
	}

	match := prURLPattern.FindString(string(data))
	return match
}

