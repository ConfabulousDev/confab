package daemon

import (
	"syscall"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// StopOutcome says what StopAllDaemons did with one daemon.
type StopOutcome string

const (
	// StopStopped: the daemon exited after SIGTERM (its normal final flush ran).
	StopStopped StopOutcome = "stopped"
	// StopKilled: the daemon outlived the timeout and was SIGKILLed.
	StopKilled StopOutcome = "killed"
	// StopStale: the state file's process was already gone; the state was removed.
	StopStale StopOutcome = "stale"
	// StopFailed: the daemon could not be signalled; Err says why.
	StopFailed StopOutcome = "failed"
)

// StopResult is StopAllDaemons' report for one state file.
type StopResult struct {
	Provider   string
	ExternalID string
	PID        int
	Outcome    StopOutcome
	Err        error
}

// DefaultStopAllTimeout is how long StopAllDaemons waits for SIGTERMed
// daemons before SIGKILLing them: the daemon's own final-sync ceiling plus a
// margin for process exit.
func DefaultStopAllTimeout() time.Duration {
	return shutdownTimeout + 5*time.Second
}

// stopAllPollInterval is how often StopAllDaemons re-checks liveness.
const stopAllPollInterval = 100 * time.Millisecond

// StopAllDaemons stops every running sync daemon on the machine (kata 2n1f).
// It SIGTERMs each live daemon from ListAllStates, waits up to timeout for
// them to exit (each does its normal final flush), then SIGKILLs stragglers.
// State + inbox files of killed and stale daemons are removed; a daemon that
// exits cleanly removes its own. Liveness uses the same signal-0 check as
// everywhere else (State.IsDaemonRunning).
func StopAllDaemons(timeout time.Duration) ([]StopResult, error) {
	states, err := ListAllStates()
	if err != nil {
		return nil, err
	}

	results := make([]StopResult, len(states))
	var pending []int // indexes into states/results still waiting to exit
	for i, state := range states {
		results[i] = StopResult{Provider: state.Provider, ExternalID: state.ExternalID, PID: state.PID}
		if !state.IsDaemonRunning() {
			deleteStateIfOwned(state)
			results[i].Outcome = StopStale
			continue
		}
		// A signal error from a daemon that exited meanwhile is not a
		// failure: the poll below records it as stopped.
		if err := signalDaemon(state, syscall.SIGTERM); err != nil && state.IsDaemonRunning() {
			results[i].Outcome = StopFailed
			results[i].Err = err
			continue
		}
		pending = append(pending, i)
	}

	deadline := time.Now().Add(timeout)
	for len(pending) > 0 {
		var still []int
		for _, i := range pending {
			if states[i].IsDaemonRunning() {
				still = append(still, i)
				continue
			}
			deleteStateIfOwned(states[i])
			results[i].Outcome = StopStopped
		}
		pending = still
		if len(pending) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(stopAllPollInterval)
	}

	for _, i := range pending {
		state := states[i]
		if err := signalDaemon(state, syscall.SIGKILL); err != nil && state.IsDaemonRunning() {
			results[i].Outcome = StopFailed
			results[i].Err = err
			continue
		}
		deleteStateIfOwned(state)
		results[i].Outcome = StopKilled
	}
	return results, nil
}

// deleteStateIfOwned removes state's state + inbox files, but only while the
// state file on disk still records state's PID — a newer daemon that rewrote
// the file for the same session keeps it. Reports whether it deleted.
func deleteStateIfOwned(state *State) bool {
	current, err := LoadStateForProvider(state.Provider, state.ExternalID)
	if err != nil || current == nil || current.PID != state.PID {
		return false
	}
	if err := state.DeleteWithInbox(); err != nil {
		logger.Warn("Failed to delete state for %s/%s: %v", state.Provider, state.ExternalID, err)
		return false
	}
	return true
}
