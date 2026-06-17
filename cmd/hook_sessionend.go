package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
	"github.com/spf13/cobra"
)

var hookSessionEndCmd = &cobra.Command{
	Use:   "session-end",
	Short: "Handle SessionEnd hook events",
	Long: `Handle SessionEnd hook events.

This command is called by the SessionEnd hook configured in
~/.claude/settings.json. It signals the sync daemon to perform
a final sync and shut down gracefully.

When called from a hook, it reads session info from stdin and
signals the daemon to stop.

Claude Code only — Codex fires Stop at every agent/turn boundary, so a
Stop-driven shutdown would prematurely kill the root sync daemon. Codex
daemons shut down via parent-process liveness instead (see
Codex.FindParentPID).

For OpenCode, this command is called by the TS plugin's dispose hook (which
runs when OpenCode unloads the plugin on exit). The plugin pipes an
OpenCodeHookInput JSON payload with just session_id (and optional cwd).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		providerName, err := provider.NormalizeName(hookProviderName)
		if err != nil {
			return err
		}
		if providerName == provider.NameCodex {
			return fmt.Errorf("session-end is not used for codex; daemons shut down via parent-process liveness. Remove any [[hooks.Stop]] entry that invokes this command from ~/.codex/config.toml")
		}
		return sessionEndFromHook()
	},
}

func init() {
	hookCmd.AddCommand(hookSessionEndCmd)
}

// sessionEndFromHook handles stopping the daemon from a SessionEnd hook
func sessionEndFromHook() error {
	return sessionEndFromReader(os.Stdin)
}

// sessionEndFromReader handles stopping the daemon with input from the given reader.
// This is the testable core of sessionEndFromHook.
func sessionEndFromReader(r io.Reader) error {
	logger.Info("Stopping sync daemon (hook mode)")

	providerName, err := provider.NormalizeName(hookProviderName)
	if err != nil {
		return err
	}

	// OpenCode: read OpenCodeHookInput from stdin, stop daemon by external ID
	if providerName == provider.NameOpencode {
		return sessionEndOpencode(r)
	}

	// Cursor: read CursorHookInput from stdin, stop the daemon under the cursor
	// provider namespace. The default path below uses ClaudeCode parsing +
	// StopDaemon (hardcoded to claude-code), which both rejects the Cursor
	// transcript path and looks under the wrong provider namespace — so Cursor
	// needs its own route, like OpenCode. sessionEnd is Cursor's clean shutdown
	// signal (CLI always; IDE on window/app close); parent-PID liveness is the
	// IDE backstop in the daemon (kata 6kys).
	if providerName == provider.NameCursor {
		return sessionEndCursor(r)
	}

	// Always output valid hook response, even on error
	defer func() { writeClaudeHookResponse(os.Stdout, false) }()

	fmt.Fprintln(os.Stderr, "=== Confab: Stopping Sync Daemon ===")
	fmt.Fprintln(os.Stderr)

	// Read hook input from reader
	hookInput, err := provider.ClaudeCode{}.ReadSessionHookInput(r)
	if err != nil {
		logger.ErrorPrint("Error reading hook input: %v", err)
		return nil
	}

	// Signal daemon to stop (it will do final sync in background)
	// Pass hookInput so daemon can access the full SessionEnd payload
	if err := daemon.StopDaemon(hookInput.SessionID, hookInput); err != nil {
		logger.Warn("Could not stop daemon: %v", err)
		fmt.Fprintf(os.Stderr, "Note: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "Daemon signaled to stop (final sync in background)")
	}

	return nil
}

// sessionEndOpencode handles session-end for OpenCode. Reads the JSON payload
// piped from the TS plugin and stops the daemon by session ID.
func sessionEndOpencode(r io.Reader) error {
	fmt.Fprintln(os.Stderr, "=== Confab: Stopping Sync Daemon ===")
	fmt.Fprintln(os.Stderr)

	p := provider.Opencode{}
	in, err := p.ReadSessionHookInput(r)
	if err != nil {
		logger.ErrorPrint("Error reading OpenCode hook input: %v", err)
		return nil
	}

	if err := daemon.StopDaemonForProvider(provider.NameOpencode, in.SessionID, nil); err != nil {
		logger.Warn("Could not stop daemon: %v", err)
		fmt.Fprintf(os.Stderr, "Note: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "Daemon signaled to stop (final sync in background)")
	}

	return nil
}

// sessionEndCursor handles session-end for Cursor. Reads the Cursor sessionEnd
// payload (session_id, transcript_path, reason) and stops the daemon under the
// cursor provider namespace. The Cursor session_end event carries a reason
// (completed|aborted|error|window_close|user_close) which we forward to the
// backend via a ClaudeHookInput-shaped inbox event — the daemon's inbox plumbing
// is provider-agnostic and reads only session_id + reason.
func sessionEndCursor(r io.Reader) error {
	p := provider.Cursor{}

	// Always output a valid hook response, even on error (fire-and-forget;
	// Cursor's WriteHookResponse writes {}).
	defer func() { _ = p.WriteHookResponse(os.Stdout, false, "") }()

	fmt.Fprintln(os.Stderr, "=== Confab: Stopping Sync Daemon ===")
	fmt.Fprintln(os.Stderr)

	in, err := p.ReadSessionHookInput(r)
	if err != nil {
		logger.ErrorPrint("Error reading Cursor hook input: %v", err)
		return nil
	}

	// Forward the Cursor reason to the backend as a session_end event. Only
	// session_id + reason are consumed downstream (see Engine.SendSessionEnd).
	hookInput := &types.ClaudeHookInput{
		SessionID:      in.SessionID,
		TranscriptPath: in.TranscriptPath,
		Reason:         in.Reason,
		HookEventName:  "SessionEnd",
	}

	if err := daemon.StopDaemonForProvider(provider.NameCursor, in.SessionID, hookInput); err != nil {
		logger.Warn("Could not stop daemon: %v", err)
		fmt.Fprintf(os.Stderr, "Note: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "Daemon signaled to stop (final sync in background)")
	}

	return nil
}
