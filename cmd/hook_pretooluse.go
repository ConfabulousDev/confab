package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

const (
	confabSessionURLBase = "https://confabulous.dev/sessions/"

	// Git commit trailer format
	commitTrailerPrefix = "Confabulous-Link: "

	// PR body link format: 📝 [Confabulous link](https://confabulous.dev/sessions/{id})
	prLinkPrefix = "📝 [Confabulous link]("
	prLinkSuffix = ")"
)

// gitCommitPattern matches git commit commands
// Matches: git commit, git commit -m, git -C /path commit, etc.
// The pattern allows flags (starting with -) and their values before "commit"
// Also matches in chains: git add . && git commit -m "..."
var gitCommitPattern = regexp.MustCompile(`\bgit\b\s+(-\S+(\s+\S+)?\s+)*commit\b`)

// gitPushPattern matches git push commands
// Matches: git push, git push origin main, git -C /path push, etc.
var gitPushPattern = regexp.MustCompile(`\bgit\b\s+(-\S+(\s+\S+)?\s+)*push\b`)

// ghPRCreatePattern matches gh pr create commands
// Matches: gh pr create, gh -R owner/repo pr create, etc.
var ghPRCreatePattern = regexp.MustCompile(`\bgh\b\s+(-\S+(\s+\S+)?\s+)*pr\s+create\b`)

var hookPreToolUseCmd = &cobra.Command{
	Use:   "pre-tool-use",
	Short: "Handle PreToolUse hook events",
	Long: `Handler for PreToolUse hook events from Claude Code.

For git commit commands, ensures the commit message includes a
Confab session URL trailer (Confab-Session-URL: https://confabulous.dev/sessions/{session_id}).

For PR creation (gh pr create, GitHub MCP tool), ensures the PR body includes a
Confab session link (📝 [Confabulous link](https://confabulous.dev/sessions/{session_id})).

For all other tool calls, exits silently (code 0) to allow normal flow.

This command is typically invoked by Claude Code, not directly by users.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return handlePreToolUse(os.Stdin, os.Stdout)
	},
}

func init() {
	hookCmd.AddCommand(hookPreToolUseCmd)
}

// handlePreToolUse processes PreToolUse hook events.
// Errors are logged but not printed to stderr - tool hooks run frequently
// and visible errors would be too noisy. See SessionStart hook for visible errors.
func handlePreToolUse(r io.Reader, w io.Writer) error {
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
		return handleMCPPRCreate(hookInput, w)
	}

	// Only process Bash tool calls from here
	if hookInput.ToolName != config.ToolNameBash {
		return nil
	}

	// Extract command from tool_input
	command, ok := hookInput.ToolInput["command"].(string)
	if !ok || command == "" {
		return nil
	}

	// Check if this is a command we care about.
	// Use match position to determine primary command - earlier match wins.
	// This handles cases like: git commit -m "mentions gh pr create"
	commitPos := findGitCommitPosition(command)
	prCreatePos := findGHPRCreatePosition(command)

	if commitPos < 0 && prCreatePos < 0 {
		return nil
	}

	// Determine which command type based on earliest match position.
	// If not commit and we got here, it must be PR create (at least one matched).
	isCommit := commitPos >= 0 && (prCreatePos < 0 || commitPos < prCreatePos)

	// Get the Confab session ID from daemon state
	confabSessionID, err := getConfabSessionID(hookInput.SessionID)
	if err != nil || confabSessionID == "" {
		// No Confab session ID available - allow without link
		// This can happen if daemon hasn't initialized yet or sync is not set up
		logger.Warn("Confab link skipped: no session ID available (err=%v)", err)
		return nil
	}

	sessionURL := formatSessionURL(confabSessionID)

	// Check if session URL is already present
	if containsSessionURL(command, confabSessionID) {
		logger.Info("Confab link already present in command")
		outputAllow(w, "Session URL already present")
		return nil
	}

	// Handle git commit
	if isCommit {
		logger.Info("Requesting Confab link for git commit -> session %s", confabSessionID)
		trailerLine := formatTrailerLine(sessionURL)
		reason := fmt.Sprintf(
			"✓ Confab is linking this commit to your session. "+
				"Please include this trailer at the end of your commit message:\n\n    %s",
			trailerLine,
		)
		outputDeny(w, reason)
		return nil
	}

	// Handle gh pr create
	logger.Info("Requesting Confab link for PR -> session %s", confabSessionID)
	prLink := formatPRLink(sessionURL)
	reason := fmt.Sprintf(
		"✓ Confab is linking this PR to your session. "+
			"Please include this link in your PR body:\n\n    %s",
		prLink,
	)
	outputDeny(w, reason)
	return nil
}

// handleMCPPRCreate handles GitHub MCP tool PR creation
func handleMCPPRCreate(hookInput *types.HookInput, w io.Writer) error {
	// Get the Confab session ID from daemon state
	confabSessionID, err := getConfabSessionID(hookInput.SessionID)
	if err != nil || confabSessionID == "" {
		logger.Warn("Confab link skipped: no session ID available (err=%v)", err)
		return nil
	}

	sessionURL := formatSessionURL(confabSessionID)

	// Check if session URL is already in the body field
	if body, ok := hookInput.ToolInput["body"].(string); ok {
		if strings.Contains(body, sessionURL) {
			logger.Info("Confab link already present in MCP PR body")
			outputAllow(w, "Session URL already present")
			return nil
		}
	}

	// Deny and ask Claude to add the link
	logger.Info("Requesting Confab link for MCP PR -> session %s", confabSessionID)
	prLink := formatPRLink(sessionURL)
	reason := fmt.Sprintf(
		"✓ Confab is linking this PR to your session. "+
			"Please include this link in your PR body:\n\n    %s",
		prLink,
	)
	outputDeny(w, reason)
	return nil
}

// getConfabSessionID retrieves the Confab session ID from daemon state.
// Returns empty string if not available.
func getConfabSessionID(claudeSessionID string) (string, error) {
	state, err := daemon.LoadState(claudeSessionID)
	if err != nil {
		return "", err
	}
	if state == nil {
		return "", nil
	}
	return state.ConfabSessionID, nil
}

// findGitCommitPosition returns the position of a git commit command, or -1 if not found
func findGitCommitPosition(command string) int {
	loc := gitCommitPattern.FindStringIndex(command)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// findGHPRCreatePosition returns the position of a gh pr create command, or -1 if not found
func findGHPRCreatePosition(command string) int {
	loc := ghPRCreatePattern.FindStringIndex(command)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// findGitPushPosition returns the position of a git push command, or -1 if not found
func findGitPushPosition(command string) int {
	loc := gitPushPattern.FindStringIndex(command)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// containsSessionURL checks if the command already includes the session URL.
// This handles various quoting styles by checking for the URL anywhere in the command.
func containsSessionURL(command, sessionID string) bool {
	sessionURL := formatSessionURL(sessionID)
	return strings.Contains(command, sessionURL)
}

// formatSessionURL returns the session URL
func formatSessionURL(sessionID string) string {
	return confabSessionURLBase + sessionID
}

// formatTrailerLine returns the formatted trailer line
func formatTrailerLine(sessionURL string) string {
	return commitTrailerPrefix + sessionURL
}

// formatPRLink returns the formatted PR body link
func formatPRLink(sessionURL string) string {
	return prLinkPrefix + sessionURL + prLinkSuffix
}

// outputAllow outputs a PreToolUse response allowing the tool call
func outputAllow(w io.Writer, reason string) {
	response := types.PreToolUseResponse{
		HookSpecificOutput: &types.PreToolUseOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "allow",
			PermissionDecisionReason: reason,
		},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Debug("Failed to write allow response: %v", err)
	}
}

// outputDeny outputs a PreToolUse response denying the tool call
func outputDeny(w io.Writer, reason string) {
	response := types.PreToolUseResponse{
		HookSpecificOutput: &types.PreToolUseOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
		},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Debug("Failed to write deny response: %v", err)
	}
}
