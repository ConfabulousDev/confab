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
)

// spawnDaemonFunc is the function used to spawn the daemon process.
// Overridable in tests.
var spawnDaemonFunc = spawnDaemonImpl

// daemonLaunchInput is the canonical wire format passed from a hook
// handler to a freshly-spawned daemon process. It is also the mutable
// spawn-side representation that hooks may mutate (e.g., after
// WalkUpToRoot resolves a Codex subagent to its root).
type daemonLaunchInput struct {
	Provider       string `json:"provider"`
	ExternalID     string `json:"external_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	ParentPID      int    `json:"parent_pid,omitempty"`
}

// launchAsHookInput satisfies provider.HookInput for the sole purpose
// of calling p.ShouldSpawnForInput inside maybeSpawnDaemon. HookEventName
// is intentionally empty (spawn-side code doesn't need it; the gate
// only inspects TranscriptPath in practice).
type launchAsHookInput struct{ l *daemonLaunchInput }

func (a launchAsHookInput) SessionID() string      { return a.l.ExternalID }
func (a launchAsHookInput) TranscriptPath() string { return a.l.TranscriptPath }
func (a launchAsHookInput) CWD() string            { return a.l.CWD }
func (a launchAsHookInput) HookEventName() string  { return "" }
func (a launchAsHookInput) ParentPID() int         { return a.l.ParentPID }

// maybeSpawnDaemon checks whether a daemon should be spawned for the
// (provider, session) pair. Returns true if a fresh daemon was spawned;
// false if one was already running or the provider gate refused. The
// caller pre-fills launch with parsed hook fields and any WalkUpToRoot
// rewrites; this function sets ParentPID + Provider before spawn.
func maybeSpawnDaemon(p provider.Provider, launch *daemonLaunchInput) (bool, error) {
	if launch.TranscriptPath == "" {
		return false, fmt.Errorf("transcript_path is required to spawn daemon")
	}

	if !p.ShouldSpawnForInput(launchAsHookInput{launch}) {
		logger.Info("Skipping %s daemon: provider gate refused (session_id=%s)",
			p.Name(), launch.ExternalID)
		return false, nil
	}

	existingState, err := daemon.LoadStateForProvider(p.Name(), launch.ExternalID)
	if err != nil {
		logger.Warn("Error checking existing %s state: %v", p.Name(), err)
	}
	if existingState != nil && existingState.IsDaemonRunning() {
		logger.Info("%s daemon already running: pid=%d", p.Name(), existingState.PID)
		return false, nil
	}

	launch.Provider = p.Name()
	launch.ParentPID = p.FindParentPID()

	if err := spawnDaemonFunc(launch); err != nil {
		return false, fmt.Errorf("failed to spawn %s daemon: %w", p.Name(), err)
	}
	logger.Info("%s daemon spawned successfully", p.Name())
	return true, nil
}

// spawnDaemonImpl starts a detached daemon process and writes initial
// state. The state file is written immediately after the process starts
// so no race window exists where another hook could spawn a duplicate.
func spawnDaemonImpl(launch *daemonLaunchInput) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	launchJSON, err := json.Marshal(launch)
	if err != nil {
		return fmt.Errorf("failed to serialize daemon launch input: %w", err)
	}

	cmd := exec.Command(executable, "hook", "session-start",
		"--provider", launch.Provider, "--bg-daemon", string(launchJSON))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	state := daemon.NewStateForProvider(launch.Provider, launch.ExternalID,
		launch.TranscriptPath, launch.CWD, launch.ParentPID)
	state.PID = cmd.Process.Pid
	if err := state.Save(); err != nil {
		logger.Warn("Failed to save initial state: %v", err)
	}

	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("failed to release daemon: %w", err)
	}
	return nil
}
