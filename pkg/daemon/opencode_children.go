package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	pkgsync "github.com/ConfabulousDev/confab/pkg/sync"
)

// CF-538: OpenCode subagent sessions as sidechain files.
//
// The daemon owns N+1 collector goroutines for an OpenCode session: one
// for the root (created in daemon.go's Run) and one per discovered
// descendant (created lazily here on demand). Each child collector polls
// the SAME SQLite DB the root reads from, materializes complete messages
// into a per-child JSONL under the root's nested directory, and lets the
// ordinary file-sync pipeline upload it.
//
// Discovery flows through provider.Opencode.DiscoverDescendants, which the
// engine calls each SyncAll cycle. The provider's per-cycle call lands on
// opencodeRegistrar.RegisterOpencodeChild defined below.

// opencodeChildCollector tracks one running per-descendant collector
// goroutine. cancel stops it; done closes when the goroutine exits.
type opencodeChildCollector struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// opencodeRegistrar is the daemon's provider.OpencodeDescendantRegistrar
// implementation. It satisfies DescendantRegistrar by delegating to the
// engine's *FileTracker (IsTracked / RegisterCodexRollout — the latter is
// never invoked for OpenCode, but the interface embedding forces us to
// expose it), and implements the OpenCode-specific extension by talking
// to the daemon's child-collector pool and the engine's capability cache.
type opencodeRegistrar struct {
	tracker *pkgsync.FileTracker
	engine  *pkgsync.Engine
	daemon  *Daemon
}

// Compile-time assertion: opencodeRegistrar satisfies the provider's
// OpenCode-specific descendant registrar interface, which itself embeds
// DescendantRegistrar. If the interface ever grows a method, this line
// fails to compile until the registrar grows it too.
var _ provider.OpencodeDescendantRegistrar = (*opencodeRegistrar)(nil)

func newOpencodeRegistrar(t *pkgsync.FileTracker, e *pkgsync.Engine, d *Daemon) *opencodeRegistrar {
	return &opencodeRegistrar{tracker: t, engine: e, daemon: d}
}

// IsTracked delegates to the engine's FileTracker. Part of
// DescendantRegistrar; OpenCode's DiscoverDescendants does not currently
// call it, but the interface requires the method.
func (r *opencodeRegistrar) IsTracked(fileName string) bool {
	return r.tracker.IsTracked(fileName)
}

// RegisterCodexRollout is required by DescendantRegistrar but never
// invoked for OpenCode (only Codex's DiscoverDescendants calls it).
// Implemented as a no-op for interface satisfaction.
func (r *opencodeRegistrar) RegisterCodexRollout(string, string, bool, provider.CodexRolloutMetadata) {
}

// RegisterOpencodeChild registers the child file (path-encoded backend
// file_name = "opencode/<childID>/messages.jsonl", file_type = "agent")
// AND ensures a collector goroutine is running for it (CF-538).
//
// Idempotent on all three layers: capability check is constant-time
// against the engine's cache, RegisterSidechainFile returns false (and
// preserves the sync position) when the name is already tracked, and
// startChildCollector is a no-op if a goroutine is already running for
// the child id.
//
// When the capability flag is off, both register and spawn no-op silently
// — the engine has already logged the capability state once via
// resolveCaps.
func (r *opencodeRegistrar) RegisterOpencodeChild(childID, localPath string) {
	if !r.engine.OpencodeChildFilesAllowed() {
		return
	}
	name := provider.OpencodeChildBackendName(childID)
	r.tracker.RegisterSidechainFile(localPath, name, "agent")
	r.daemon.startChildCollector(childID, localPath)
}

// startChildCollector spawns a per-child OpenCode collector goroutine.
// Idempotent: a no-op if one is already running for childID. Logs Info
// on first spawn.
//
// Each child collector shares the daemon's *OpenCodeDBReader (one *sql.DB
// per ReadSession call, no shared state), and uses the same poll cadence
// as the root. Lifetime is daemon-scoped: shutdown() cancels every
// collector and waits for done before the final backend sync.
func (d *Daemon) startChildCollector(childID, localPath string) {
	d.childCollectorsMu.Lock()
	defer d.childCollectorsMu.Unlock()
	if d.childCollectors == nil {
		d.childCollectors = make(map[string]*opencodeChildCollector)
	}
	if _, ok := d.childCollectors[childID]; ok {
		return
	}
	if d.dbReader == nil {
		// Pre-condition violation: child-collector spawn requires the
		// shared reader set up at daemon Run start. Log and skip rather
		// than panic.
		logger.Warn("startChildCollector(%s): dbReader not initialized; skipping", childID)
		return
	}
	ctx, cancel := context.WithCancel(d.childCollectorBase)
	done := make(chan struct{})
	d.childCollectors[childID] = &opencodeChildCollector{cancel: cancel, done: done}
	collector := provider.NewOpenCodeCollector(d.dbReader, childID, localPath, d.syncInterval)
	logger.Info("Discovered OpenCode child: session=%s path=%s", childID, localPath)
	go func() {
		defer close(done)
		if err := collector.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("OpenCode child collector exited: session=%s err=%v", childID, err)
		}
	}()
}

// waitForCollectors waits for the root collector + every child collector
// done channel, capped at a single shared timeout. If the timeout elapses
// before all channels close, logs a Warn and returns — the daemon proceeds
// with the final sync regardless, so a wedged collector cannot block
// shutdown indefinitely.
func waitForCollectors(rootDone chan struct{}, childDones []chan struct{}, timeout time.Duration) {
	deadline := time.After(timeout)
	all := append([]chan struct{}{rootDone}, childDones...)
	for _, done := range all {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-deadline:
			logger.Warn("OpenCode collector did not stop within %v; proceeding with final sync", timeout)
			return
		}
	}
}

// childCollectorDones returns a snapshot of every running child collector's
// done channel so shutdown() can wait on them outside the daemon's mutex.
// Cancellation is driven by the parent context (childCollectorBase), so
// callers don't need to call each child's cancel here.
func (d *Daemon) childCollectorDones() []chan struct{} {
	d.childCollectorsMu.Lock()
	defer d.childCollectorsMu.Unlock()
	dones := make([]chan struct{}, 0, len(d.childCollectors))
	for _, cc := range d.childCollectors {
		dones = append(dones, cc.done)
	}
	return dones
}

