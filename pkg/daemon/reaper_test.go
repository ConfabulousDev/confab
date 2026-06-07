package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// setupReaperEnv points HOME at a fresh temp dir so daemon.State{}.Save
// and related helpers write to a clean ~/.confab/sync.
func setupReaperEnv(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	if err := os.MkdirAll(filepath.Join(tmpHome, ".confab", "sync"), 0o700); err != nil {
		t.Fatalf("mkdir sync dir: %v", err)
	}
	return tmpHome
}

// seedState writes a state file with the given fields. provider may be
// empty (legacy Claude layout) or one of the known provider names.
func seedState(t *testing.T, providerName, externalID string, pid int, startedAt time.Time) *State {
	t.Helper()
	s := NewStateForProvider(providerName, externalID, "/tmp/transcript.jsonl", "/tmp/cwd", pid)
	s.PID = pid
	s.StartedAt = startedAt
	if err := s.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}
	return s
}

// TestReapStaleStatesDeletesDeadPID asserts the reaper removes a state
// file whose PID is no longer alive. 999999 is well above the usual PID
// range; signal-0 returns ESRCH and the state qualifies for reap.
func TestReapStaleStatesDeletesDeadPID(t *testing.T) {
	setupReaperEnv(t)
	old := time.Now().Add(-1 * time.Minute) // past the grace window
	s := seedState(t, provider.NameOpencode, "ses_dead", 999999, old)

	reaped, err := ReapStaleStates()
	if err != nil {
		t.Fatalf("ReapStaleStates: %v", err)
	}
	if reaped < 1 {
		t.Errorf("reaped = %d, want >= 1", reaped)
	}
	statePath, _ := GetStatePathForProvider(s.Provider, s.ExternalID)
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state file %s should have been reaped, got err=%v", statePath, err)
	}
}

// TestReapStaleStatesPreservesLiveDaemon asserts the reaper does NOT
// remove a state file whose PID is the current test process (alive).
// Without this, the reaper would race with running daemons.
func TestReapStaleStatesPreservesLiveDaemon(t *testing.T) {
	setupReaperEnv(t)
	old := time.Now().Add(-1 * time.Minute)
	s := seedState(t, provider.NameOpencode, "ses_alive", os.Getpid(), old)

	if _, err := ReapStaleStates(); err != nil {
		t.Fatalf("ReapStaleStates: %v", err)
	}
	statePath, _ := GetStatePathForProvider(s.Provider, s.ExternalID)
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("live daemon's state file should not be reaped; stat err=%v", err)
	}
}

// TestReapStaleStatesSkipsRecent asserts the grace window: a state with
// StartedAt in the last few seconds is skipped even if its PID looks dead.
// This protects fresh spawns whose state file beats the daemon's startup.
func TestReapStaleStatesSkipsRecent(t *testing.T) {
	setupReaperEnv(t)
	fresh := time.Now() // within reapMinAge
	s := seedState(t, provider.NameOpencode, "ses_fresh", 999999, fresh)

	if _, err := ReapStaleStates(); err != nil {
		t.Fatalf("ReapStaleStates: %v", err)
	}
	statePath, _ := GetStatePathForProvider(s.Provider, s.ExternalID)
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("recent state should not be reaped; stat err=%v", err)
	}
}

// TestReapStaleStatesCrossesProviders asserts the reaper is
// provider-agnostic: it must walk all provider subdirs and reap stale
// entries in each. Seeded with one dead state under opencode and one
// under codex; both must be removed in a single pass.
func TestReapStaleStatesCrossesProviders(t *testing.T) {
	setupReaperEnv(t)
	old := time.Now().Add(-1 * time.Minute)
	ocState := seedState(t, provider.NameOpencode, "ses_oc_dead", 999998, old)
	cxState := seedState(t, provider.NameCodex, "ses_cx_dead", 999997, old)

	reaped, err := ReapStaleStates()
	if err != nil {
		t.Fatalf("ReapStaleStates: %v", err)
	}
	if reaped < 2 {
		t.Errorf("reaped = %d, want >= 2 across providers", reaped)
	}

	for _, s := range []*State{ocState, cxState} {
		statePath, _ := GetStatePathForProvider(s.Provider, s.ExternalID)
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Errorf("%s state should have been reaped; stat err=%v", s.Provider, err)
		}
	}
}

// TestReapStaleStatesDeletesInboxToo asserts the reaper removes the
// matching <id>.inbox.jsonl file alongside the state, so dead daemons
// don't leave orphan inbox files behind.
func TestReapStaleStatesDeletesInboxToo(t *testing.T) {
	setupReaperEnv(t)
	old := time.Now().Add(-1 * time.Minute)
	s := seedState(t, provider.NameOpencode, "ses_with_inbox", 999996, old)

	// Create the inbox file so the reaper has something to delete.
	if err := os.WriteFile(s.InboxPath, []byte(`{"type":"placeholder"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write inbox: %v", err)
	}

	if _, err := ReapStaleStates(); err != nil {
		t.Fatalf("ReapStaleStates: %v", err)
	}
	if _, err := os.Stat(s.InboxPath); !os.IsNotExist(err) {
		t.Errorf("inbox file %s should be removed alongside state; stat err=%v", s.InboxPath, err)
	}
}
