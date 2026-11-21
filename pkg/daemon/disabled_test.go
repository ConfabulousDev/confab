package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// writeDisabledConfig rewrites the test config.json with sync_disabled set,
// keeping the mock backend credentials (kata 2n1f). Written by hand because
// the test API key would not pass SaveUploadConfig's validation.
func writeDisabledConfig(t *testing.T, serverURL string, disabled bool) {
	t.Helper()
	configJSON := fmt.Sprintf(`{"backend_url":"%s","api_key":"test-api-key-12345678","sync_disabled":%t}`, serverURL, disabled)
	if err := os.WriteFile(os.Getenv("CONFAB_CONFIG_PATH"), []byte(configJSON), 0600); err != nil {
		t.Fatal(err)
	}
}

func runDaemonUntilExit(t *testing.T, d *Daemon, within time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("daemon returned error: %v", err)
		}
	case <-time.After(within):
		t.Fatalf("daemon did not exit on its own within %v while sync is disabled", within)
	}
}

func TestDaemonExitsWithoutUploadingWhenDisabledAtStartup(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	writeDisabledConfig(t, server.URL, true)
	os.WriteFile(transcriptPath, []byte(`{"type":"user","line":1}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "disabled-at-start",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   20 * time.Millisecond,
	})
	runDaemonUntilExit(t, d, 3*time.Second)

	if n := len(mock.getInitRequests()); n != 0 {
		t.Errorf("disabled daemon must not contact the backend: got %d init requests", n)
	}
	if n := len(mock.getChunkRequests()); n != 0 {
		t.Errorf("disabled daemon must not upload: got %d chunk requests", n)
	}
	state, err := LoadStateForProvider(provider.NameClaudeCode, "disabled-at-start")
	if err != nil {
		t.Fatal(err)
	}
	if state != nil {
		t.Errorf("disabled daemon must clean up its state file on exit")
	}
}

func TestDaemonExitsWithoutUploadingWhenDisabledBetweenCycles(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	os.WriteFile(transcriptPath, []byte(`{"type":"user","line":1}`+"\n"), 0644)

	d := New(Config{
		ExternalID:     "disabled-mid-run",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   30 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for len(mock.getChunkRequests()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("expected the first sync while enabled")
		}
		time.Sleep(10 * time.Millisecond)
	}

	writeDisabledConfig(t, server.URL, true)
	// Lines written after the flag is set must never reach the backend:
	// neither a regular cycle nor a final sync may upload them.
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"assistant","line":2}` + "\n")
	f.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("daemon returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not exit on its own after sync was disabled")
	}

	for _, c := range mock.getChunkRequests() {
		for _, line := range c.Lines {
			if strings.Contains(line, `"line":2`) {
				t.Errorf("line written after disable was uploaded: %s", line)
			}
		}
	}
}

// The race-free invariant: the daemon saves its state file before its first
// flag check, so a daemon that `confab disable` did not see in its listing
// necessarily reads the flag after it was set.
func TestDaemonSavesStateBeforeFirstFlagCheck(t *testing.T) {
	mock := newMockBackend(t)
	server := httptest.NewServer(mock)
	defer server.Close()

	tmpDir, transcriptPath := setupTestEnv(t, server.URL)
	os.WriteFile(transcriptPath, []byte(`{"type":"user","line":1}`+"\n"), 0644)

	var checks, stateMissingAtFirstCheck atomic.Int32
	orig := isSyncDisabled
	isSyncDisabled = func() bool {
		if checks.Add(1) == 1 {
			s, err := LoadStateForProvider(provider.NameClaudeCode, "ordering")
			if err != nil || s == nil {
				stateMissingAtFirstCheck.Store(1)
			}
		}
		return true
	}
	t.Cleanup(func() { isSyncDisabled = orig })

	d := New(Config{
		ExternalID:     "ordering",
		TranscriptPath: transcriptPath,
		CWD:            tmpDir,
		SyncInterval:   20 * time.Millisecond,
	})
	runDaemonUntilExit(t, d, 3*time.Second)

	if checks.Load() == 0 {
		t.Fatal("daemon never checked the sync_disabled flag")
	}
	if stateMissingAtFirstCheck.Load() != 0 {
		t.Error("state file must be saved before the first sync_disabled check")
	}
}

// startFakeDaemon starts a child process and writes a state file for it, as
// a real daemon would. The child is reaped in the background so its exit is
// observable through signal-0 liveness (an unreaped zombie still "exists").
func startFakeDaemon(t *testing.T, providerName, externalID string, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-done
	})
	s := NewStateForProvider(providerName, externalID, "/tmp/x.jsonl", "/tmp", 0)
	s.PID = cmd.Process.Pid
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.InboxPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestStopAllDaemonsTermKillAndStale(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Exits on SIGTERM (default disposition).
	startFakeDaemon(t, provider.NameClaudeCode, "polite", "exec sleep 60")
	// Ignores SIGTERM; must be SIGKILLed after the timeout.
	startFakeDaemon(t, provider.NameCodex, "stubborn", `trap "" TERM; exec sleep 60`)
	// Give the stubborn child time to install its trap before we signal it.
	time.Sleep(200 * time.Millisecond)

	// Stale: a state whose process is gone.
	stale := NewStateForProvider(provider.NameCursor, "stale", "/tmp/x.jsonl", "/tmp", 0)
	stale.PID = 999999
	if err := stale.Save(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	results, err := StopAllDaemons(500 * time.Millisecond)
	if err != nil {
		t.Fatalf("StopAllDaemons: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("StopAllDaemons took %v; it must give up waiting after the timeout", elapsed)
	}

	got := map[string]StopOutcome{}
	for _, r := range results {
		got[r.ExternalID] = r.Outcome
		if r.Err != nil {
			t.Errorf("%s: unexpected error %v", r.ExternalID, r.Err)
		}
	}
	want := map[string]StopOutcome{"polite": StopStopped, "stubborn": StopKilled, "stale": StopStale}
	for id, outcome := range want {
		if got[id] != outcome {
			t.Errorf("%s: outcome = %q, want %q", id, got[id], outcome)
		}
	}

	remaining, err := ListAllStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("every state must be cleaned up, %d left", len(remaining))
	}
	for _, id := range []struct{ p, id string }{{provider.NameClaudeCode, "polite"}, {provider.NameCodex, "stubborn"}} {
		inbox, _ := GetInboxPathForProvider(id.p, id.id)
		if _, err := os.Stat(inbox); !os.IsNotExist(err) {
			t.Errorf("%s inbox must be deleted with its state", id.id)
		}
	}
}

func TestStopAllDaemonsWithNoStatesIsNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	results, err := StopAllDaemons(time.Second)
	if err != nil {
		t.Fatalf("StopAllDaemons: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("no daemons → no results, got %d", len(results))
	}
}

// A state file rewritten by a newer daemon for the same session (different
// PID) must not be deleted by the cleanup of the old one.
func TestStopAllDaemonsKeepsReplacedState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := NewStateForProvider(provider.NameClaudeCode, "sess", "/tmp/x.jsonl", "/tmp", 0)
	old.PID = 999999
	newer := NewStateForProvider(provider.NameClaudeCode, "sess", "/tmp/x.jsonl", "/tmp", 0)
	if err := newer.Save(); err != nil { // PID = this test process (alive)
		t.Fatal(err)
	}
	if deleteStateIfOwned(old) {
		t.Error("must not delete a state file owned by a different PID")
	}
	s, _ := LoadStateForProvider(provider.NameClaudeCode, "sess")
	if s == nil {
		t.Error("the newer daemon's state file was deleted")
	}
}
