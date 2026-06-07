package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// OpenCodeCollector materializes a single OpenCode session's messages into
// a local JSONL file that the ordinary file-based sync pipeline uploads.
//
// OpenCode keeps session data in a local SQLite DB (one row per message,
// one row per part). The collector polls the DB on a ticker, fetches new
// messages strictly after the high-water mark, filters out non-terminal
// tool parts, walks in ULID order stopping at the first incomplete
// message (see ocIsComplete), and appends each emitted envelope as one
// line of JSON. The file stays append-only and monotonic — which is what
// pkg/sync's incremental line-offset tracking requires.
//
// Idempotency across daemon restarts needs no extra persisted state: on
// start, seed re-builds the emitted-id set and the HWM from any existing
// output file.
type OpenCodeCollector struct {
	source       ocSource
	sessionID    string
	outputPath   string
	pollInterval time.Duration

	emitted        map[string]bool // message ids already written to outputPath
	highWaterMark  string          // highest message id emitted (lex ULID order)
	consecutiveErr int             // count of consecutive reconcile errors of the same kind
	lastErrKind    string          // first line of the last reconcile error
}

// ocSource is the subset of *OpenCodeDBReader the collector needs. Lets
// tests inject a fake without spinning up SQLite, and pins the seam
// between data acquisition and assembly.
type ocSource interface {
	ReadSession(ctx context.Context, sessionID, sinceMessageID string) ([]ocRawEnvelope, error)
}

// NewOpenCodeCollector builds a collector for one session. interval is the
// poll cadence; pass 0 for a sensible default. The interval is wired from
// daemon.syncInterval so CONFAB_SYNC_INTERVAL_MS tunes both backend sync
// and SQLite polling with a single knob.
func NewOpenCodeCollector(source ocSource, sessionID, outputPath string, interval time.Duration) *OpenCodeCollector {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &OpenCodeCollector{
		source:       source,
		sessionID:    sessionID,
		outputPath:   outputPath,
		pollInterval: interval,
		emitted:      make(map[string]bool),
	}
}

// seed loads already-emitted message ids from an existing output file so a
// restarted daemon doesn't duplicate lines. Also sets the high-water mark
// to the highest emitted id so the next ReadSession can filter at the SQL
// level rather than redundantly re-fetching old rows.
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
		if info.ID > c.highWaterMark {
			c.highWaterMark = info.ID
		}
	}
	return scanner.Err()
}

// reconcile fetches authoritative session state strictly after the HWM
// and appends any newly-complete messages, in id order, stopping at the
// first incomplete one. Returns the number of lines appended.
func (c *OpenCodeCollector) reconcile(ctx context.Context) (int, error) {
	envs, err := c.source.ReadSession(ctx, c.sessionID, c.highWaterMark)
	if err != nil {
		return 0, err
	}
	// Reader already orders by (time_created, message.id, part.id), but
	// keep the explicit sort as belt-and-braces: it's cheap on a small
	// slice and decouples collector correctness from query-plan details.
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
		if p.id > c.highWaterMark {
			c.highWaterMark = p.id
		}
		written++
	}
	return written, nil
}

// Run seeds, then reconciles on every tick until ctx is cancelled. There
// is no remote stream to reconnect to — the SQLite DB is local and the
// poll is the only liveness mechanism.
func (c *OpenCodeCollector) Run(ctx context.Context) error {
	if err := c.seed(); err != nil {
		logger.Warn("opencode collector seed failed (continuing): %v", err)
	}
	c.tryReconcile(ctx)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.tryReconcile(context.Background())
			return ctx.Err()
		case <-ticker.C:
			c.tryReconcile(ctx)
		}
	}
}

// tryReconcile runs one reconcile pass and routes any error through the
// debounced Warn cadence (see warnReconcileError).
func (c *OpenCodeCollector) tryReconcile(ctx context.Context) {
	n, err := c.reconcile(ctx)
	if err != nil {
		c.warnReconcileError(err)
		return
	}
	if c.consecutiveErr > 0 {
		logger.Info("opencode collector recovered after %d failed cycle(s) for %s",
			c.consecutiveErr, c.sessionID)
		c.consecutiveErr = 0
		c.lastErrKind = ""
	}
	if n > 0 {
		logger.Debug("opencode collector appended %d message(s) for %s", n, c.sessionID)
	}
}

// warnReconcileError implements the CF-542 observability fix: surface
// errors loudly enough that a stuck collector isn't silent, without
// spamming logs during a sustained outage. Warn on the first error of a
// new kind; suppress immediate repeats; Warn again every Nth cycle, where
// N targets one Warn per ~minute (so the cadence is honest regardless of
// the poll interval the daemon was started with).
func (c *OpenCodeCollector) warnReconcileError(err error) {
	kind := errKind(err)
	n := warnEveryN(c.pollInterval)
	if kind != c.lastErrKind {
		c.lastErrKind = kind
		c.consecutiveErr = 1
		logger.Warn("opencode collector reconcile failed for %s: %v", c.sessionID, err)
		return
	}
	c.consecutiveErr++
	if c.consecutiveErr%n == 0 {
		logger.Warn("opencode collector reconcile still failing for %s (%d consecutive): %v",
			c.sessionID, c.consecutiveErr, err)
	}
}

// warnEveryN computes how many consecutive failures should pass between
// Warn lines, targeting one Warn per minute. Floor at 1 so very long
// intervals always Warn.
func warnEveryN(interval time.Duration) int {
	if interval <= 0 {
		return 1
	}
	n := int(math.Ceil(float64(time.Minute) / float64(interval)))
	if n < 1 {
		return 1
	}
	return n
}

// errKind reduces an error to a stable signature for "same kind" dedup.
// The first line of the formatted error is a good proxy: it's the
// outermost wrap message, which is what changes when the failure mode
// changes (e.g. "db not found" vs "query: context deadline exceeded").
func errKind(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}
