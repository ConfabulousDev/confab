package provider

// CodexRolloutMetadata is the per-rollout metadata transmitted on the FIRST
// chunk of a Codex rollout (root or descendant). The backend upserts it
// into the `codex_rollouts` table keyed by ThreadUUID. Omitted on chunks
// where chunk.FirstLine != 1, so the backend handler treats absence as
// "no metadata to record this round."
//
// Codex-only; the backend rejects this field on non-codex sessions with 400.
//
// Lives in pkg/provider so both pkg/sync (wire format) and pkg/provider's
// Codex implementation can construct one without a package import cycle.
// pkg/sync re-exports it via `type CodexRolloutMetadata = provider.CodexRolloutMetadata`.
//
// Redaction: fields below are sourced from Codex's session_meta (and the
// SQLite state DB for descendants) and ride on the first chunk unredacted.
// Rollout *content* is redacted in pkg/sync.FileTracker.ReadChunk; these
// metadata fields are not. Current fields are short, structured values
// (path, model name, agent role). Before adding a field that could carry
// free-text user content, plumb the redactor into Codex.InitTranscript /
// Codex.DiscoverDescendants rather than extending this struct.
type CodexRolloutMetadata struct {
	ThreadUUID       string `json:"thread_uuid"`
	ParentThreadUUID string `json:"parent_thread_uuid,omitempty"` // "" for roots
	RolloutPath      string `json:"rollout_path"`
	CWD              string `json:"cwd,omitempty"`
	Model            string `json:"model,omitempty"`
	// Source is the flattened discriminator from Codex's polymorphic
	// session_meta.source field — a short string like "cli" or "subagent".
	// The backend's `codex_rollouts.source` column caps this at 64 chars.
	Source        string `json:"source,omitempty"`
	ThreadSource  string `json:"thread_source,omitempty"`
	AgentPath     string `json:"agent_path,omitempty"`
	AgentRole     string `json:"agent_role,omitempty"`
	AgentNickname string `json:"agent_nickname,omitempty"`
}
