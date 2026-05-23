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
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

const (
	// Git commit trailer format
	commitTrailerPrefix = "Confab-Link: "

	// PR body link format: 📝 [Confab link]({session_url})
	prLinkPrefix = "📝 [Confab link]("
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
	Long: `Handler for PreToolUse hook events.

For git commit commands, ensures the commit message includes a
Confab session URL trailer (Confab-Link: {backend_url}/sessions/{session_id}).

For PR creation (gh pr create, GitHub MCP tool), ensures the PR body includes a
Confab session link (📝 [Confab link]({backend_url}/sessions/{session_id})).

For all other tool calls, exits silently (code 0) to allow normal flow.

This command is typically invoked by the provider runtime (Claude Code or
Codex), not directly by users. Provider is selected via --provider.`,
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
	if config.IsLinkFromGitHubDisabled() {
		logger.Info("GitHub linking disabled via %s", config.DisableLinkFromGitHubEnv)
		return nil
	}

	p, err := resolveCommitLinkingProvider()
	if err != nil {
		logger.Warn("PreToolUse skipped: %v", err)
		return nil
	}

	hookInput, err := readToolUseHookInput(p, r)
	if err != nil {
		logger.Warn("Failed to read hook input: %v", err)
		return nil // Exit silently, don't block the firing provider.
	}

	// MCP GitHub PR matcher is Claude-only. Codex managed hooks use
	// Bash for PR creation, so Codex never invokes this branch.
	if hookInput.ToolName == config.ToolNameMCPGitHubCreatePR {
		return handleMCPPRCreate(p, hookInput, w)
	}

	if hookInput.ToolName != config.ToolNameBash {
		return nil
	}

	command, ok := hookInput.ToolInput["command"].(string)
	if !ok || command == "" {
		return nil
	}

	// Earlier match wins so "git commit -m 'mentions gh pr create'" is treated
	// as a commit, not a PR.
	commitPos := firstMatch(gitCommitPattern, command)
	prCreatePos := firstMatch(ghPRCreatePattern, command)
	if commitPos < 0 && prCreatePos < 0 {
		return nil
	}
	isCommit := commitPos >= 0 && (prCreatePos < 0 || commitPos < prCreatePos)

	confabSessionID, err := getConfabSessionID(p, hookInput.SessionID)
	if err != nil || confabSessionID == "" {
		logger.Warn("Confab link skipped: no session ID available (err=%v)", err)
		return nil
	}

	sessionURL, err := formatSessionURL(confabSessionID)
	if err != nil {
		logger.Warn("Confab link skipped: %v", err)
		return nil
	}

	if containsSessionURL(command, confabSessionID) {
		logger.Info("Confab link already present in command")
		outputPreToolUseDecision(w, "allow", "Session URL already present")
		return nil
	}

	if isCommit {
		logger.Info("Requesting Confab link for git commit -> session %s", confabSessionID)
		trailerLine := formatTrailerLine(sessionURL)
		reason := fmt.Sprintf(
			"✓ Confab is linking this commit to your session. "+
				"Add this trailer to the end of your commit message (after any other trailers like Co-Authored-By):\n\n    %s\n\n"+
				"IMPORTANT: Copy this line verbatim. The value is a URL, NOT a ticket ID like CF-123.",
			trailerLine,
		)
		outputPreToolUseDecision(w, "deny", reason)
		return nil
	}

	logger.Info("Requesting Confab link for PR -> session %s", confabSessionID)
	outputPreToolUseDecision(w, "deny", formatPRDenyReason(sessionURL))
	return nil
}

// handleMCPPRCreate handles the Claude GitHub MCP PR creation tool. Codex
// doesn't install this matcher, so this is invoked only for Claude.
func handleMCPPRCreate(p provider.Provider, hookInput *toolUseHookInput, w io.Writer) error {
	confabSessionID, err := getConfabSessionID(p, hookInput.SessionID)
	if err != nil || confabSessionID == "" {
		logger.Warn("Confab link skipped: no session ID available (err=%v)", err)
		return nil
	}

	sessionURL, err := formatSessionURL(confabSessionID)
	if err != nil {
		logger.Warn("Confab link skipped: %v", err)
		return nil
	}

	if body, ok := hookInput.ToolInput["body"].(string); ok {
		if strings.Contains(body, sessionURL) {
			logger.Info("Confab link already present in MCP PR body")
			outputPreToolUseDecision(w, "allow", "Session URL already present")
			return nil
		}
	}

	logger.Info("Requesting Confab link for MCP PR -> session %s", confabSessionID)
	outputPreToolUseDecision(w, "deny", formatPRDenyReason(sessionURL))
	return nil
}

// resolveCommitLinkingProvider reads the --provider hook flag, normalizes
// it, and gates on the provider's SupportsCommitLinking. Providers that
// don't advertise support cause the caller to silently no-op.
func resolveCommitLinkingProvider() (provider.Provider, error) {
	name, err := provider.NormalizeName(hookProviderName)
	if err != nil {
		return nil, err
	}
	p, err := provider.Get(name)
	if err != nil {
		return nil, err
	}
	if !p.SupportsCommitLinking() {
		return nil, fmt.Errorf("provider %q does not support GitHub commit linking", p.Name())
	}
	return p, nil
}

// getConfabSessionID returns the Confab session ID for a firing tool-use
// hook, walking up to the root session when the firing UUID has no daemon
// state of its own (e.g., a Codex subagent ran the tool but the daemon
// state is keyed off the root rollout). Identity for Claude; SQLite walk
// for Codex.
func getConfabSessionID(p provider.Provider, sessionID string) (string, error) {
	state, err := daemon.LoadStateForProvider(p.Name(), sessionID)
	if err != nil {
		return "", err
	}
	if state != nil {
		return state.ConfabSessionID, nil
	}

	rootID, _, _ := p.WalkUpToRoot(sessionID)
	if rootID == "" || rootID == sessionID {
		return "", nil
	}
	rootState, err := daemon.LoadStateForProvider(p.Name(), rootID)
	if err != nil || rootState == nil {
		return "", err
	}
	return rootState.ConfabSessionID, nil
}

func firstMatch(re *regexp.Regexp, s string) int {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// containsSessionURL checks if the command already includes the session URL.
// This handles various quoting styles by checking for the URL anywhere in the command.
func containsSessionURL(command, sessionID string) bool {
	sessionURL, err := formatSessionURL(sessionID)
	if err != nil {
		return false
	}
	return strings.Contains(command, sessionURL)
}

// formatSessionURL returns the session URL derived from the configured backend URL.
// Returns error if backend URL is not configured.
func formatSessionURL(sessionID string) (string, error) {
	cfg, err := config.GetUploadConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}
	if cfg.BackendURL == "" {
		return "", fmt.Errorf("backend URL not configured")
	}
	return strings.TrimSuffix(cfg.BackendURL, "/") + "/sessions/" + sessionID, nil
}

// formatTrailerLine returns the formatted trailer line
func formatTrailerLine(sessionURL string) string {
	return commitTrailerPrefix + sessionURL
}

// formatPRLink returns the formatted PR body link
func formatPRLink(sessionURL string) string {
	return prLinkPrefix + sessionURL + prLinkSuffix
}

func formatPRDenyReason(sessionURL string) string {
	return fmt.Sprintf(
		"✓ Confab is linking this PR to your session. "+
			"Add this line at the bottom of the PR body:\n\n    %s\n\n"+
			"IMPORTANT: Copy this line verbatim. The value is a URL, NOT a ticket ID like CF-123.",
		formatPRLink(sessionURL),
	)
}

func outputPreToolUseDecision(w io.Writer, decision, reason string) {
	response := types.PreToolUseResponse{
		HookSpecificOutput: &types.PreToolUseOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       decision,
			PermissionDecisionReason: reason,
		},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Debug("Failed to write %s response: %v", decision, err)
	}
}
