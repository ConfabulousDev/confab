package sync

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/git"
	"github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/utils"
)

// Client handles communication with the sync API endpoints
type Client struct {
	httpClient *http.Client
}

// NewClient creates a new sync API client
func NewClient(cfg *config.UploadConfig) (*Client, error) {
	httpClient, err := http.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return nil, err
	}
	return &Client{
		httpClient: httpClient,
	}, nil
}

// InitMetadata contains optional metadata for session initialization
type InitMetadata struct {
	CWD      string          `json:"cwd,omitempty"`
	GitInfo  json.RawMessage `json:"git_info,omitempty"`
	Hostname string          `json:"hostname,omitempty"`
	Username string          `json:"username,omitempty"`
}

// InitRequest is the request body for POST /api/v1/sync/init
type InitRequest struct {
	Provider       string        `json:"provider"`
	ExternalID     string        `json:"external_id"`
	TranscriptPath string        `json:"transcript_path"`
	Metadata       *InitMetadata `json:"metadata,omitempty"`
}

// InitResponse is the response for POST /api/v1/sync/init
type InitResponse struct {
	SessionID string               `json:"session_id"`
	Files     map[string]FileState `json:"files"`
}

// FileState represents the sync state for a single file from the backend
type FileState struct {
	LastSyncedLine int `json:"last_synced_line"`
}

// ChunkRequest is the request body for POST /api/v1/sync/chunk
type ChunkRequest struct {
	SessionID string         `json:"session_id"`
	FileName  string         `json:"file_name"`
	FileType  string         `json:"file_type"`
	FirstLine int            `json:"first_line"`
	Lines     []string       `json:"lines"`
	Metadata  *ChunkMetadata `json:"metadata,omitempty"`
}

// ChunkMetadata contains metadata sent to the backend with a chunk
type ChunkMetadata struct {
	GitInfo          *git.GitInfo          `json:"git_info,omitempty"`
	Summary          string                `json:"summary,omitempty"`
	FirstUserMessage string                `json:"first_user_message,omitempty"`
	CodexRollout     *CodexRolloutMetadata `json:"codex_rollout,omitempty"`
}

// CodexRolloutMetadata is the per-rollout metadata transmitted on the FIRST
// chunk of a Codex rollout. The canonical definition lives in pkg/provider
// so the Codex implementation can construct one without an import cycle;
// pkg/sync re-exports it here as an alias so existing call sites that
// reference sync.CodexRolloutMetadata keep working. Wire format unchanged.
type CodexRolloutMetadata = provider.CodexRolloutMetadata

// ChunkResponse is the response for POST /api/v1/sync/chunk
type ChunkResponse struct {
	LastSyncedLine int `json:"last_synced_line"`
}

// EventRequest is the request body for POST /api/v1/sync/event
type EventRequest struct {
	SessionID string          `json:"session_id"`
	EventType string          `json:"event_type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// EventResponse is the response for POST /api/v1/sync/event
type EventResponse struct {
	Success bool `json:"success"`
}

// Init initializes or resumes a sync session
// Returns the session ID and current sync state for all files. The
// providerName must be a canonical provider name (callers via Engine.Init
// pass e.provider.Name(), which is always non-empty).
func (c *Client) Init(providerName, externalID, transcriptPath string, metadata *InitMetadata) (*InitResponse, error) {
	req := InitRequest{
		Provider:       providerName,
		ExternalID:     externalID,
		TranscriptPath: transcriptPath,
		Metadata:       metadata,
	}

	var resp InitResponse
	if err := c.httpClient.Post("/api/v1/sync/init", req, &resp); err != nil {
		return nil, fmt.Errorf("sync init failed: %w", err)
	}

	return &resp, nil
}

// Capabilities is the backend's optional-feature signal (CF-533). The
// response body of GET /api/v1/capabilities IS this map (no outer wrapper).
// Absent fields default to false (zero value), so a backend that omits a
// field — or the whole endpoint (404) — is treated as not supporting it.
type Capabilities struct {
	// WorkflowFiles reports that the backend resolves path-encoded workflow
	// subagent file_names (subagents/workflows/<runId>/agent-<id>.jsonl).
	WorkflowFiles bool `json:"workflow_files"`
	// WorkflowJournal reports that the backend accepts the workflow_journal
	// file_type (subagents/workflows/<runId>/journal.jsonl).
	WorkflowJournal bool `json:"workflow_journal"`
	// OpencodeSubagentFiles reports that the backend resolves path-encoded
	// OpenCode subagent file_names (opencode/<child-id>/messages.jsonl) and
	// stitches them into the root session's analytics (CF-538/CF-539).
	OpencodeSubagentFiles bool `json:"opencode_subagent_files"`
}

// Capabilities probes GET /api/v1/capabilities (public, no auth). Any failure
// — 404 on an older backend, a network error, or a malformed body — is
// returned to the caller, which treats it as "no capabilities". The 404
// sentinel (http.ErrSessionNotFound) is preserved via %w for errors.Is.
func (c *Client) Capabilities() (Capabilities, error) {
	var caps Capabilities
	if err := c.httpClient.Get("/api/v1/capabilities", &caps); err != nil {
		return Capabilities{}, fmt.Errorf("capabilities probe failed: %w", err)
	}
	return caps, nil
}

// UploadChunk uploads a chunk of lines for a file with optional metadata
// Returns the new last synced line number
func (c *Client) UploadChunk(sessionID, fileName, fileType string, firstLine int, lines []string, metadata *ChunkMetadata) (int, error) {
	req := ChunkRequest{
		SessionID: sessionID,
		FileName:  fileName,
		FileType:  fileType,
		FirstLine: firstLine,
		Lines:     lines,
		Metadata:  metadata,
	}

	var resp ChunkResponse
	if err := c.httpClient.Post("/api/v1/sync/chunk", req, &resp); err != nil {
		return 0, fmt.Errorf("chunk upload failed: %w", err)
	}

	return resp.LastSyncedLine, nil
}

// SendEvent sends a session lifecycle event to the backend
func (c *Client) SendEvent(sessionID, eventType string, timestamp time.Time, payload json.RawMessage) error {
	req := EventRequest{
		SessionID: sessionID,
		EventType: eventType,
		Timestamp: timestamp,
		Payload:   payload,
	}

	var resp EventResponse
	if err := c.httpClient.Post("/api/v1/sync/event", req, &resp); err != nil {
		return fmt.Errorf("send event failed: %w", err)
	}

	return nil
}

// UpdateSummaryRequest is the request body for PATCH /api/v1/sessions/{external_id}/summary
type UpdateSummaryRequest struct {
	Summary string `json:"summary"`
}

// UpdateSummaryResponse is the response for PATCH /api/v1/sessions/{external_id}/summary
type UpdateSummaryResponse struct {
	Status string `json:"status"`
}

// UpdateSessionSummary updates the summary for a session identified by its external_id
func (c *Client) UpdateSessionSummary(externalID, summary string) error {
	req := UpdateSummaryRequest{
		Summary: summary,
	}

	var resp UpdateSummaryResponse
	path := fmt.Sprintf("/api/v1/sessions/%s/summary", externalID)
	if err := c.httpClient.Patch(path, req, &resp); err != nil {
		return fmt.Errorf("update summary failed: %w", err)
	}

	return nil
}

// GitHubLinkRequest is the request body for POST /api/v1/sessions/{id}/github-links
type GitHubLinkRequest struct {
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"`
	Source string `json:"source"` // "cli_hook" or "manual"
}

// GitHubLinkResponse is the response for POST /api/v1/sessions/{id}/github-links
type GitHubLinkResponse struct {
	ID       int64  `json:"id"`
	LinkType string `json:"link_type"` // "commit" or "pull_request"
	URL      string `json:"url"`
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Ref      string `json:"ref"`
}

// LinkGitHub creates a GitHub link for a session
func (c *Client) LinkGitHub(sessionID string, req *GitHubLinkRequest) (*GitHubLinkResponse, error) {
	var resp GitHubLinkResponse
	path := fmt.Sprintf("/api/v1/sessions/%s/github-links", sessionID)
	if err := c.httpClient.Post(path, req, &resp); err != nil {
		return nil, fmt.Errorf("link github failed: %w", err)
	}

	return &resp, nil
}
