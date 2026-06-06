package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// OpenCodeCollector materializes a single OpenCode session's messages into a
// local JSONL file that the ordinary file-based sync pipeline then uploads.
//
// OpenCode has no transcript file: the authoritative state lives behind the
// HTTP API. The collector subscribes to the SSE event bus for liveness/triggers
// and always re-fetches authoritative message state over REST. Each *complete*
// message (see ocIsComplete) is appended once, in id order, stopping at the
// first incomplete message so the file stays append-only and monotonic — which
// is what pkg/sync's incremental line-offset tracking requires.
//
// Idempotency across daemon restarts needs no extra persisted state: on start
// the collector re-seeds its emitted-id set from any existing output file.

const (
	ocPollInterval = 5 * time.Second
	ocReconnectMin = 500 * time.Millisecond
	ocReconnectMax = 30 * time.Second
)

// ocClient is the subset of *OpenCodeClient the collector needs (also lets
// tests inject a fake).
type ocClient interface {
	SessionMessages(ctx context.Context, sessionID string) ([]ocRawEnvelope, error)
	SubscribeEvents(ctx context.Context) (<-chan OpenCodeEvent, error)
}

// OpenCodeCollector tracks one session.
type OpenCodeCollector struct {
	client       ocClient
	sessionID    string
	outputPath   string
	pollInterval time.Duration

	emitted map[string]bool // message ids already written to outputPath
}

// NewOpenCodeCollector builds a collector for one session, writing complete
// messages to outputPath.
func NewOpenCodeCollector(client ocClient, sessionID, outputPath string) *OpenCodeCollector {
	return &OpenCodeCollector{
		client:       client,
		sessionID:    sessionID,
		outputPath:   outputPath,
		pollInterval: ocPollInterval,
		emitted:      make(map[string]bool),
	}
}

// seed loads already-emitted message ids from an existing output file so a
// restarted daemon doesn't duplicate lines.
func (c *OpenCodeCollector) seed() error {
	f, err := os.Open(c.outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), types.MaxJSONLLineSize)
	for scanner.Scan() {
		var env ocRawEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			continue
		}
		info, err := ocPeekInfo(env.Info)
		if err != nil || info.ID == "" {
			continue
		}
		c.emitted[info.ID] = true
	}
	return scanner.Err()
}

// reconcile fetches authoritative session state and appends any newly-complete
// messages, in id order, stopping at the first incomplete one. Returns the
// number of lines appended.
func (c *OpenCodeCollector) reconcile(ctx context.Context) (int, error) {
	envs, err := c.client.SessionMessages(ctx, c.sessionID)
	if err != nil {
		return 0, err
	}
	sorted, err := ocSortByID(envs)
	if err != nil {
		return 0, err
	}

	type pending struct {
		id   string
		line []byte
	}
	var batch []pending
	for _, e := range sorted {
		info, err := ocPeekInfo(e.Info)
		if err != nil {
			return 0, err
		}
		if info.ID == "" || c.emitted[info.ID] {
			continue
		}
		if !ocIsComplete(info) {
			break // gap: don't emit later messages before this one settles
		}
		line, err := ocSerializeLine(e)
		if err != nil {
			return 0, err
		}
		batch = append(batch, pending{id: info.ID, line: line})
	}
	if len(batch) == 0 {
		return 0, nil
	}

	if err := os.MkdirAll(filepath.Dir(c.outputPath), 0700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(c.outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	written := 0
	for _, p := range batch {
		if _, err := f.Write(append(p.line, '\n')); err != nil {
			return written, err
		}
		c.emitted[p.id] = true
		written++
	}
	return written, nil
}

// Run seeds, then reconciles on SSE events and a fallback ticker until ctx is
// cancelled. Reconnects to the SSE stream with capped backoff indefinitely
// (daemon shutdown is driven by parent-PID death, not by this loop).
func (c *OpenCodeCollector) Run(ctx context.Context) error {
	if err := c.seed(); err != nil {
		logger.Warn("opencode collector seed failed (continuing): %v", err)
	}
	c.tryReconcile(ctx)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	reconnect := time.NewTimer(0)
	defer reconnect.Stop()

	var events <-chan OpenCodeEvent
	backoff := ocReconnectMin

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-reconnect.C:
			ev, err := c.client.SubscribeEvents(ctx)
			if err != nil {
				logger.Debug("opencode SSE connect failed (retry in %v): %v", backoff, err)
				reconnect.Reset(backoff)
				backoff = nextBackoff(backoff)
				continue
			}
			events = ev
			backoff = ocReconnectMin
			c.tryReconcile(ctx) // re-list on every (re)connect; SSE replay depth is unknown

		case ev, ok := <-events:
			if !ok {
				events = nil
				reconnect.Reset(backoff) // stream closed; reconnect
				backoff = nextBackoff(backoff)
				continue
			}
			if c.relevant(ev) {
				c.tryReconcile(ctx)
			}

		case <-ticker.C:
			c.tryReconcile(ctx) // safety net for any missed event
		}
	}
}

func (c *OpenCodeCollector) tryReconcile(ctx context.Context) {
	if n, err := c.reconcile(ctx); err != nil {
		logger.Debug("opencode reconcile error (will retry): %v", err)
	} else if n > 0 {
		logger.Debug("opencode collector appended %d message(s) for %s", n, c.sessionID)
	}
}

// relevant reports whether an event should trigger a reconcile: message/session
// events for our session (or whose session can't be determined). Other sessions'
// events are ignored; the fallback ticker + reconnect re-list cover any misses.
func (c *OpenCodeCollector) relevant(ev OpenCodeEvent) bool {
	if !strings.HasPrefix(ev.Type, "message.") && !strings.HasPrefix(ev.Type, "session.") {
		return false
	}
	if sid := ocEventSessionID(ev.Properties); sid != "" && sid != c.sessionID {
		return false
	}
	return true
}

// ocEventSessionID best-effort extracts a session id from an SSE event's
// properties across the shapes OpenCode uses (session events nest the session
// under info; message/part events carry sessionID directly or under part).
func ocEventSessionID(props json.RawMessage) string {
	if len(props) == 0 {
		return ""
	}
	var p struct {
		SessionID string `json:"sessionID"`
		Info      struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		} `json:"info"`
		Part struct {
			SessionID string `json:"sessionID"`
		} `json:"part"`
	}
	if err := json.Unmarshal(props, &p); err != nil {
		return ""
	}
	switch {
	case p.SessionID != "":
		return p.SessionID
	case p.Part.SessionID != "":
		return p.Part.SessionID
	case p.Info.SessionID != "":
		return p.Info.SessionID
	case p.Info.ID != "":
		return p.Info.ID // session.* events: properties.info is the Session
	default:
		return ""
	}
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > ocReconnectMax {
		return ocReconnectMax
	}
	return d
}
