package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// OpenCodeClient talks to a running OpenCode HTTP server (the local server a
// session's plugin handed us via serverUrl). It is deliberately trimmed to
// exactly what the live single-session collector needs: fetch a session's
// messages, and subscribe to the SSE event bus. Session listing / health
// probes are intentionally absent until a caller (CF-538 / manual mode) needs
// them. The OpenCode local server requires no auth.
type OpenCodeClient struct {
	baseURL string
	http    *http.Client
}

// OpenCodeEvent is one frame off the SSE bus: {type, properties}. Properties
// stays raw — the collector only routes on type and (best-effort) a session id
// inside properties, then re-fetches authoritative state via SessionMessages.
type OpenCodeEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// NewOpenCodeClient builds a client for the given base server URL
// (e.g. "http://localhost:4096"). The HTTP client has no global timeout so the
// SSE stream can stay open; SessionMessages bounds itself via context.
func NewOpenCodeClient(serverURL string) *OpenCodeClient {
	return &OpenCodeClient{
		baseURL: strings.TrimRight(serverURL, "/"),
		http:    &http.Client{},
	}
}

// SessionMessages fetches every message (with parts) for a session via
// GET /session/{id}/message, returning them as raw {info, parts} envelopes.
func (c *OpenCodeClient) SessionMessages(ctx context.Context, sessionID string) ([]ocRawEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch session messages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("session messages: unexpected status %d", resp.StatusCode)
	}
	var envelopes []ocRawEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelopes); err != nil {
		return nil, fmt.Errorf("decode session messages: %w", err)
	}
	return envelopes, nil
}

// SubscribeEvents opens the SSE stream (GET /event) and returns a channel of
// decoded events. The channel closes when the stream ends, errors, or ctx is
// cancelled; callers reconnect with backoff. Returns an error only if the
// initial connection fails.
func (c *OpenCodeClient) SubscribeEvents(ctx context.Context) (<-chan OpenCodeEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/event", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscribe events: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("subscribe events: unexpected status %d", resp.StatusCode)
	}

	ch := make(chan OpenCodeEvent)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		streamSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}

// streamSSE parses an SSE body and forwards each event's JSON data payload as a
// decoded OpenCodeEvent. Each event's accumulated `data:` lines are a single
// {type, properties} JSON object (OpenCode sends the whole envelope as data).
func streamSSE(ctx context.Context, body io.Reader, ch chan<- OpenCodeEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), types.MaxJSONLLineSize)

	var data strings.Builder
	flush := func() {
		if data.Len() == 0 {
			return
		}
		payload := data.String()
		data.Reset()
		var ev OpenCodeEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return // skip malformed frame, keep streaming
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(d)
		default:
			// Ignore "event:", "id:", "retry:", and ":" comment lines.
		}
	}
	if err := scanner.Err(); err != nil {
		logger.Debug("opencode SSE stream read error: %v", err)
	}
	flush() // final event if the stream ended without a trailing blank line
}
