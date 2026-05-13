package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// spawnDaemonFunc is the function used to spawn the daemon process.
// It can be overridden in tests to avoid actually spawning processes.
var spawnDaemonFunc = spawnDaemonImpl

// maybeSpawnDaemon checks if a daemon is already running for the session,
// and spawns one if not. Returns true if a daemon was spawned.
//
// This is the shared entry point for spawning daemons from any hook
// (SessionStart, UserPromptSubmit, etc.).
func maybeSpawnDaemon(p provider.ClaudeCode, hookInput *types.ClaudeHookInput) (spawned bool, err error) {
	// Validate required fields for spawning a daemon
	if hookInput.TranscriptPath == "" {
		return false, fmt.Errorf("transcript_path is required to spawn daemon")
	}

	// Check if daemon already running for this session
	existingState, err := daemon.LoadState(hookInput.SessionID)
	if err != nil {
		logger.Warn("Error checking existing state: %v", err)
		// Continue - we'll try to spawn anyway
	}
	if existingState != nil && existingState.IsDaemonRunning() {
		logger.Info("Daemon already running: pid=%d", existingState.PID)
		return false, nil
	}

	// Find Claude Code's PID by walking up the process tree.
	hookInput.ParentPID = p.FindParentPID()

	// Spawn the daemon
	if err := spawnDaemonFunc(hookInput); err != nil {
		return false, fmt.Errorf("failed to spawn daemon: %w", err)
	}

	logger.Info("Daemon spawned successfully")
	return true, nil
}

// spawnDaemonImpl starts a detached daemon process and writes initial state.
// The state file is written immediately after the process starts, before
// this function returns. This ensures no race window where another hook
// could spawn a duplicate daemon.
func spawnDaemonImpl(hookInput *types.ClaudeHookInput) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Serialize hook input to pass to daemon
	hookInputJSON, err := json.Marshal(hookInput)
	if err != nil {
		return fmt.Errorf("failed to serialize hook input: %w", err)
	}

	// Spawn daemon using "hook session-start --bg-daemon"
	cmd := exec.Command(executable, "hook", "session-start", "--bg-daemon", string(hookInputJSON))

	// Detach from parent process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Redirect stdout/stderr to /dev/null (logs go to log file)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Write state immediately with daemon's PID.
	// This eliminates the race window between spawn and daemon's own state write.
	state := daemon.NewState(hookInput.SessionID, hookInput.TranscriptPath,
		hookInput.CWD, hookInput.ParentPID)
	state.PID = cmd.Process.Pid // Use daemon's PID, not ours
	if err := state.Save(); err != nil {
		// Log but don't fail - daemon will write its own state as backup
		logger.Warn("Failed to save initial state: %v", err)
	}

	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("failed to release daemon: %w", err)
	}

	return nil
}
