package daemon

import (
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// reapMinAge is the grace window for fresh state files. spawnDaemonImpl
// writes the state file ~1ms after cmd.Start(), so a reaper running
// concurrently could observe a state pointing at a not-yet-fully-registered
// PID. Five seconds is well above that ~1ms window and well below any
// realistic "long-dead daemon" age, so it's safe to skip younger files.
const reapMinAge = 5 * time.Second

// ReapStaleStates walks every provider subdirectory under ~/.confab/sync
// and removes state + inbox files whose daemon PID is no longer alive.
// Provider-agnostic: the signal-0 liveness check is OS-level, not
// provider-specific, so one pass covers Claude / Codex / OpenCode.
//
// Returns the number of state files removed and the first error from the
// directory walk; per-file removal errors are logged at debug and skipped.
// Intended to be called in a goroutine from session-start handlers so the
// cleanup is invisible to the user (CF-549 F-up A).
func ReapStaleStates() (reaped int, err error) {
	states, err := ListAllStates()
	if err != nil {
		return 0, err
	}
	for _, state := range states {
		if time.Since(state.StartedAt) < reapMinAge {
			continue
		}
		if state.IsDaemonRunning() {
			continue
		}
		if err := state.DeleteWithInbox(); err != nil {
			logger.Debug("reap: failed to fully delete %s: %v", state.ExternalID, err)
			continue
		}
		reaped++
		logger.Debug("reap: removed stale state for %s/%s", state.Provider, state.ExternalID)
	}
	return reaped, nil
}
