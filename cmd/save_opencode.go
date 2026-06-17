package cmd

import (
	"context"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/sync"
)

// opencodeSaveMaterializeTimeout bounds the per-child synchronous
// materialization the offline save path runs. Generous enough for a busy DB,
// bounded so one wedged child can't hang the whole `confab save`.
const opencodeSaveMaterializeTimeout = 30 * time.Second

// opencodeOfflineRegistrar is the offline counterpart to the daemon's
// opencodeRegistrar (pkg/daemon/opencode_children.go). It satisfies
// provider.OpencodeDescendantRegistrar so the same Opencode.DiscoverDescendants
// seam the daemon drives also captures the descendant tree for `confab save`.
//
// The one difference from the daemon's registrar: there is no background
// collector. RegisterOpencodeChild materializes the child's transcript
// SYNCHRONOUSLY (one-shot, via MaterializeOpenCodeSession) so the file exists
// on disk before the engine's SyncAll BFS reaches it, then registers it as an
// agent sidechain through the same FileTracker primitive. Capability gating
// reuses the engine's cached OpencodeChildFilesAllowed, identical to live.
type opencodeOfflineRegistrar struct {
	tracker *sync.FileTracker
	engine  *sync.Engine
	source  *provider.OpenCodeDBReader
}

var _ provider.OpencodeDescendantRegistrar = (*opencodeOfflineRegistrar)(nil)

func newOpencodeOfflineRegistrar(e *sync.Engine, source *provider.OpenCodeDBReader) *opencodeOfflineRegistrar {
	return &opencodeOfflineRegistrar{tracker: e.Tracker(), engine: e, source: source}
}

func (r *opencodeOfflineRegistrar) IsTracked(fileName string) bool {
	return r.tracker.IsTracked(fileName)
}

// RegisterCodexRollout is required by DescendantRegistrar but never invoked for
// OpenCode. No-op for interface satisfaction (mirrors the daemon registrar).
func (r *opencodeOfflineRegistrar) RegisterCodexRollout(string, string, bool, provider.CodexRolloutMetadata) {
}

// RegisterOpencodeChild materializes the child session to its nested local path
// and registers it as a path-encoded agent sidechain — the offline analog of
// the daemon's child-collector spawn. Gated on the backend advertising
// opencode_subagent_files (same capability the daemon honors); when off, the
// child is silently skipped so an old backend never receives unsupported files.
func (r *opencodeOfflineRegistrar) RegisterOpencodeChild(childID, localPath string) {
	if !r.engine.OpencodeChildFilesAllowed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), opencodeSaveMaterializeTimeout)
	defer cancel()
	if _, err := provider.MaterializeOpenCodeSession(ctx, r.source, childID, localPath, 0); err != nil {
		logger.Warn("opencode save: materialize child %s failed: %v", childID, err)
		return
	}
	name := provider.OpencodeChildBackendName(childID)
	r.tracker.RegisterSidechainFile(localPath, name, "agent")
}

// setupOpencodeSaveEngine wires the offline descendant registrar onto the engine
// for an OpenCode save so Engine.SyncAll's DiscoverDescendants call captures the
// whole tree as agent sidechains. No-op for other providers.
func setupOpencodeSaveEngine(engine *sync.Engine, providerName string) error {
	if providerName != provider.NameOpencode {
		return nil
	}
	dbPath, err := provider.OpenCodeDBPath()
	if err != nil {
		return err
	}
	reader := provider.NewOpenCodeDBReader(dbPath)
	engine.SetDescendantRegistrar(newOpencodeOfflineRegistrar(engine, reader))
	return nil
}
