package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/ConfabulousDev/confab/pkg/daemon"
	"github.com/ConfabulousDev/confab/pkg/logger"
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
func maybeSpawnDaemon(hookInput *types.HookInput) (spawned bool, err error) {
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

	// Find Claude Code's PID by walking up the process tree
	hookInput.ParentPID = findClaudePID()

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
func spawnDaemonImpl(hookInput *types.HookInput) error {
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

// findClaudePID walks up the process tree to find the Claude Code process.
// Checks parent and grandparent (up to 2 levels) to handle cases where
// Claude spawns hooks via a shell wrapper (e.g., /bin/sh -c on Linux).
func findClaudePID() int {
	parentPID := os.Getppid()
	if isClaudeProcess(parentPID) {
		return parentPID
	}

	grandparentPID := getParentPID(parentPID)
	if grandparentPID > 0 && isClaudeProcess(grandparentPID) {
		return grandparentPID
	}

	// Could not find Claude - return 0 to disable parent PID monitoring
	logger.Warn("Could not find Claude in process tree, disabling parent PID monitoring")
	return 0
}

// claudeProcessPattern matches "claude" as a word boundary to avoid false positives
// like "claudette" or other strings containing "claude" as a substring.
var claudeProcessPattern = regexp.MustCompile(`(?i)\bclaude\b`)

// isClaudeProcess checks if the given PID is a Claude Code process
func isClaudeProcess(pid int) bool {
	cmd := getProcCmdline(pid)
	return matchesClaudeProcess(cmd)
}

// matchesClaudeProcess checks if a command string matches Claude Code.
// Exported for testing.
func matchesClaudeProcess(cmd string) bool {
	return claudeProcessPattern.MatchString(cmd)
}

// getProcCmdline gets the command line of a process
func getProcCmdline(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getParentPID gets the parent PID of a process
func getParentPID(pid int) int {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}
