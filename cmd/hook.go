package cmd

import (
	"encoding/json"
	"io"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

var hookProviderName string

// writeClaudeHookResponse writes a standard Claude hook response to the given writer.
// All hooks must output valid JSON, even on error, so Claude Code can continue.
func writeClaudeHookResponse(w io.Writer, suppressOutput bool) {
	json.NewEncoder(w).Encode(types.ClaudeHookResponse{
		Continue:       true,
		SuppressOutput: suppressOutput,
	})
}

// hookCmd is the parent command for hook handlers.
// This is distinct from hooksCmd which manages hook installation.
var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Hook handlers for provider events",
	Long: `Hook handlers invoked by the configured provider during session events.

These commands are typically called by hooks configured in the provider's
settings file (Claude Code: ~/.claude/settings.json; Codex: ~/.codex/config.toml),
not directly by users.

Available handlers:
  session-start       Handle SessionStart events (starts sync daemon)
  session-end         Handle SessionEnd events (Claude Code only)
  pre-tool-use        Handle PreToolUse events
  post-tool-use       Handle PostToolUse events
  user-prompt-submit  Handle UserPromptSubmit events (Claude Code only)`,
}

// skipHookWhenSyncDisabled reports whether a hook handler must do nothing
// because `confab disable` / `confab uninstall` turned sync off (kata 2n1f).
// When it does, it drains stdin so the firing agent never sees a broken pipe,
// and the caller returns nil with no stdout — an empty stdout with exit 0 is
// the neutral "allow, no change" response for Claude, Codex and Cursor (the
// same one the CONFAB_DISABLE_LINK_FROM_GITHUB early return already uses). A
// config read error is treated as enabled (config.SyncDisabledLenient).
func skipHookWhenSyncDisabled(stdin io.Reader) bool {
	if !config.SyncDisabledLenient() {
		return false
	}
	_, _ = io.Copy(io.Discard, stdin)
	logger.Debug("Sync disabled; hook is a no-op")
	return true
}

// gateOnSyncDisabled wraps a hook handler's RunE with the sync_disabled
// gate. The daemon itself also runs as `hook session-start` (hidden
// --daemon-data flag); that path is not gated here because the daemon checks
// the flag on its own after saving its state file (the race-free ordering in
// pkg/daemon). `sync start` / `sync stop` call these RunE fields directly, so
// they are gated too.
func gateOnSyncDisabled(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if bgDaemonData == "" && skipHookWhenSyncDisabled(cmd.InOrStdin()) {
			return nil
		}
		return run(cmd, args)
	}
}

func init() {
	// Every `confab hook …` handler no-ops while sync is disabled. The
	// subcommand vars are package-level, so they exist before any init runs.
	for _, c := range []*cobra.Command{
		hookSessionStartCmd,
		hookSessionEndCmd,
		hookPreToolUseCmd,
		hookPostToolUseCmd,
		hookUserPromptSubmitCmd,
	} {
		c.RunE = gateOnSyncDisabled(c.RunE)
	}

	// Deliberate back-compat exception (m9mb): unlike the user-facing list/save
	// commands, the machine-invoked `hook` default stays claude-code. Installed
	// Claude hook command strings now pass `--provider claude-code` explicitly
	// (see pkg/hookconfig/claude.go), but OLD installs predating that migration
	// invoke `confab hook session-start` with no flag. Retaining this default
	// keeps those installs working until enough releases pass to drop it.
	hookCmd.PersistentFlags().StringVar(&hookProviderName, "provider", provider.NameClaudeCode, "Provider for hook input (claude-code, codex, opencode, or cursor)")
	rootCmd.AddCommand(hookCmd)
}
