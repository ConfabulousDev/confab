package sync

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/git"
	"github.com/ConfabulousDev/confab/pkg/http"
)

// Client handles communication with the sync API endpoints
type Client struct {
	httpClient *http.Client
}

// NewClient creates a new sync API client
func NewClient(cfg *config.UploadConfig) (*Client, error) {
	httpClient, err := http.NewClient(cfg, 30*time.Second)
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
	ExternalID     string       `json:"external_id"`
	TranscriptPath string       `json:"transcript_path"`
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
	GitInfo          *git.GitInfo `json:"git_info,omitempty"`
	Summary          string       `json:"summary,omitempty"`
	FirstUserMessage string       `json:"first_user_message,omitempty"`
}

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
// Returns the session ID and current sync state for all files
func (c *Client) Init(externalID, transcriptPath string, metadata *InitMetadata) (*InitResponse, error) {
	req := InitRequest{
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
