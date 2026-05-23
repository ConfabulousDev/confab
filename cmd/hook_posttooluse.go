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
	"github.com/ConfabulousDev/confab/pkg/provider"
	pkgsync "github.com/ConfabulousDev/confab/pkg/sync"
	"github.com/spf13/cobra"
)

// prURLPattern matches GitHub PR URLs in output
// Matches: https://github.com/owner/repo/pull/123
var prURLPattern = regexp.MustCompile(`https://github\.com/[^/\s]+/[^/\s]+/pull/\d+`)

var hookPostToolUseCmd = &cobra.Command{
	Use:   "post-tool-use",
	Short: "Handle PostToolUse hook events",
	Long: `Handler for PostToolUse hook events.

For successful PR creation (gh pr create, GitHub MCP tool), extracts the PR URL
from the output and links it to the current Confab session.

For successful git commits or pushes, retrieves the HEAD commit SHA via git
rev-parse and links the GitHub commit URL to the current Confab session.

For all other tool calls, exits silently (code 0).

This command is typically invoked by the provider runtime (Claude Code or
Codex), not directly by users. Provider is selected via --provider.`,
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
	if config.IsLinkFromGitHubDisabled() {
		logger.Info("GitHub linking disabled via %s", config.DisableLinkFromGitHubEnv)
		return nil
	}

	p, err := resolveCommitLinkingProvider()
	if err != nil {
		logger.Warn("PostToolUse skipped: %v", err)
		return nil
	}

	hookInput, err := readToolUseHookInput(p, r)
	if err != nil {
		logger.Warn("Failed to read hook input: %v", err)
		return nil
	}

	// PR creation paths: MCP tool (Claude-only matcher) or `gh pr create`
	// via Bash. Both extract the PR URL from the tool response and link
	// it under the firing session.
	if hookInput.ToolName == config.ToolNameMCPGitHubCreatePR {
		return linkPRFromResponse(p, hookInput)
	}

	if hookInput.ToolName != config.ToolNameBash {
		return nil
	}

	command, ok := hookInput.ToolInput["command"].(string)
	if !ok || command == "" {
		return nil
	}

	if firstMatch(ghPRCreatePattern, command) >= 0 {
		return linkPRFromResponse(p, hookInput)
	}

	if firstMatch(gitCommitPattern, command) >= 0 || firstMatch(gitPushPattern, command) >= 0 {
		if !isSuccessfulBashResponse(hookInput.ToolResponse) {
			logger.Debug("Git command did not succeed, skipping link")
			return nil
		}
		return linkCommitToSession(p, hookInput.SessionID, hookInput.CWD)
	}

	return nil
}

// linkPRFromResponse extracts a GitHub PR URL from the tool response and
// links it to the firing session. Shared by the MCP GitHub PR matcher
// (Claude-only) and the `gh pr create` Bash matcher.
func linkPRFromResponse(p provider.Provider, hookInput *toolUseHookInput) error {
	prURL := extractPRURLFromResponse(hookInput.ToolResponse)
	if prURL == "" {
		logger.Debug("No PR URL found in tool response")
		return nil
	}
	return linkGitHubURL(p, hookInput.SessionID, prURL)
}

// linkGitHubURL links a GitHub URL (PR or commit) to the current Confab
// session. Walks up to the root session for providers with a thread tree
// (Codex) so subagent-initiated commits/PRs link to the user-facing root.
func linkGitHubURL(p provider.Provider, sessionID, githubURL string) error {
	logger.Info("Linking GitHub URL to session: %s", githubURL)

	confabSessionID, err := getConfabSessionID(p, sessionID)
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
func linkCommitToSession(p provider.Provider, sessionID, cwd string) error {
	if cwd == "" {
		logger.Warn("GitHub commit link failed: no CWD provided")
		return nil
	}

	commitSHA, err := git.GetHeadSHA(cwd)
	if err != nil || commitSHA == "" {
		logger.Warn("GitHub commit link failed: could not get HEAD SHA from %s (err=%v)", cwd, err)
		return nil
	}

	logger.Info("Linking commit to session: %s", commitSHA)

	repoURL, err := git.GetRepoURL(cwd)
	if err != nil || repoURL == "" {
		logger.Warn("GitHub commit link failed: could not get repo URL from %s (err=%v)", cwd, err)
		return nil
	}

	githubURL := git.ToGitHubURL(repoURL)
	if githubURL == "" {
		logger.Info("GitHub commit link skipped: repo is not on GitHub (%s)", repoURL)
		return nil
	}

	commitURL := githubURL + "/commit/" + commitSHA
	return linkGitHubURL(p, sessionID, commitURL)
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
