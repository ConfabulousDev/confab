package provider

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Claude workflow subagent discovery (CF-533).
//
// The Workflow tool writes each spawned subagent's transcript to
// <session>/subagents/workflows/<runId>/agent-<id>.jsonl and a per-run
// journal to .../journal.jsonl. Unlike classic subagents, workflow agents
// carry no toolUseResult.agentId in the main transcript, so they are not
// found by ExtractAgentIDsFromMessage; they are discovered by scanning the
// workflows directory instead. Files are uploaded under path-encoded backend
// names so the backend (CF-532) can resolve <runId> from the path and <id>
// from path.Base — the names are load-bearing and written verbatim.
//
// Not uploaded: agent-<id>.meta.json, wf_<runId>.json, and the script file
// (which lives outside the run dir entirely) — deferred per CF-533.

const workflowsSubdir = "workflows"

// workflowFileType classifies a file inside a workflow run dir, returning the
// sync file_type to upload it under, or "" if it must not be uploaded.
func workflowFileType(base string) string {
	switch {
	case base == "journal.jsonl":
		return FileTypeWorkflowJournal
	case strings.HasPrefix(base, "agent-") && strings.HasSuffix(base, ".jsonl"):
		return "agent"
	default:
		return "" // *.meta.json, wf_*.json, scripts, etc.
	}
}

// DiscoverWorkflowFiles implements provider.Provider — see the interface doc.
func (ClaudeCode) DiscoverWorkflowFiles(reg WorkflowRegistrar, allow func(fileType string) bool) (int, error) {
	workflowsDir := filepath.Join(reg.SubagentsDir(), workflowsSubdir)
	runDirs, err := os.ReadDir(workflowsDir)
	if err != nil {
		// No workflows dir (the common case) — nothing to do. Crucially we
		// return before ever calling allow(), so a non-workflow session
		// never triggers a backend capability probe.
		return 0, nil
	}

	count := 0
	for _, rd := range runDirs {
		if !rd.IsDir() {
			continue
		}
		runID := rd.Name()
		runPath := filepath.Join(workflowsDir, runID)
		entries, err := os.ReadDir(runPath)
		if err != nil {
			continue // unreadable run dir — skip, try the others
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			base := ent.Name()
			fileType := workflowFileType(base)
			if fileType == "" {
				continue
			}
			if !allow(fileType) {
				continue // backend doesn't support this file type (per-flag gate)
			}
			// Path-encoded backend name (forward slashes — load-bearing S3 key
			// segments the backend parses); absolute path for local reads.
			name := path.Join("subagents", workflowsSubdir, runID, base)
			if reg.RegisterSidechainFile(filepath.Join(runPath, base), name, fileType) {
				count++
			}
		}
	}
	return count, nil
}
