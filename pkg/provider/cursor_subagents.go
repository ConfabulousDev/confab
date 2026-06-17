package provider

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// Cursor subagent sidechain discovery (T6).
//
// Cursor's Workflow-tool-style subagents write their own transcript JSONL files
// to <root-dir>/subagents/<subagent-id>.jsonl, a sibling of the root transcript
// file (root: .../agent-transcripts/<id>/<id>.jsonl, subagents:
// .../agent-transcripts/<id>/subagents/<sub-id>.jsonl — verified kata 6kys).
// They are uploaded as ordinary file_type=agent sidechains under the path-
// relative backend name "subagents/<subagent-id>.jsonl", reusing the normal
// incremental/redacted chunk path (no new upload code). This is UNGATED: the
// backend accepts file_type=agent universally (it predates capabilities), so
// no capability probe is needed — unlike Claude's workflow files, which gate on
// the workflow_files capability.
//
// Layout note: the subagents directory is filepath.Dir(rootTranscript)/subagents.
// We deliberately do NOT use WorkflowRegistrar.SubagentsDir(), which is computed
// for Claude's layout (<session-id>/subagents, a nested dir) and would point at
// the wrong path for Cursor.

const cursorSubagentsDir = "subagents"

// DiscoverDescendants scans <root-dir>/subagents/ each SyncAll cycle and
// registers every *.jsonl file there as a file_type=agent sidechain. Idempotent:
// RegisterSidechainFile returns false for an already-tracked file, so re-scanning
// every cycle costs only a directory read. A no-op when the registrar lacks the
// sidechain surface, when the root transcript path is unavailable, or when the
// subagents directory does not exist (the common case).
func (Cursor) DiscoverDescendants(reg DescendantRegistrar, _ string) error {
	wreg, ok := reg.(WorkflowRegistrar)
	if !ok {
		return nil // registrar can't capture sidechains (e.g. a plain DescendantRegistrar)
	}
	rooter, ok := reg.(RootTranscriptProvider)
	if !ok {
		return nil // can't resolve the Cursor subagents dir without the root path
	}
	rootTranscript := rooter.RootTranscriptPath()
	if rootTranscript == "" {
		return nil
	}

	// Cursor subagents sit beside the root transcript, NOT under SubagentsDir().
	subagentsDir := filepath.Join(filepath.Dir(rootTranscript), cursorSubagentsDir)
	entries, err := os.ReadDir(subagentsDir)
	if err != nil {
		// No subagents dir (the common case) or unreadable — nothing to do.
		return nil
	}

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue // skip nested dirs, *.meta.json sidecars, and stray files
		}
		base := ent.Name()
		// Path-relative backend file_name (forward slashes — load-bearing, like
		// Claude/OpenCode sidechains); absolute path for local reads.
		name := path.Join(cursorSubagentsDir, base)
		if wreg.RegisterSidechainFile(filepath.Join(subagentsDir, base), name, FileTypeAgent) {
			logger.Info("Discovered Cursor subagent sidechain: %s", name)
		}
	}
	return nil
}
