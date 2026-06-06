package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeOCSource serves a programmable set of envelopes (optionally an error)
// and tracks the last sinceMessageID it was called with so tests can assert
// the collector's HWM threading.
type fakeOCSource struct {
	envs []ocRawEnvelope
	err  error

	calls []string // sinceMessageID seen on each call, in order
}

func (f *fakeOCSource) ReadSession(_ context.Context, _ string, sinceMessageID string) ([]ocRawEnvelope, error) {
	f.calls = append(f.calls, sinceMessageID)
	return f.envs, f.err
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func lineID(t *testing.T, line string) string {
	t.Helper()
	var env ocRawEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("bad line %q: %v", line, err)
	}
	info, err := ocPeekInfo(env.Info)
	if err != nil {
		t.Fatal(err)
	}
	return info.ID
}

func TestCollectorReconcileAppendsComplete(t *testing.T) {
	out := filepath.Join(t.TempDir(), "opencode", "ses_1", "messages.jsonl")
	source := &fakeOCSource{envs: []ocRawEnvelope{
		env(`{"id":"msg_1","role":"user"}`, `{"type":"text","text":"hi"}`),
		env(`{"id":"msg_2","role":"assistant","finish":"stop"}`, `{"type":"text","text":"yo"}`),
	}}
	c := NewOpenCodeCollector(source, "ses_1", out, time.Second)
	n, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 2 {
		t.Fatalf("appended %d, want 2", n)
	}
	lines := readLines(t, out)
	if len(lines) != 2 || lineID(t, lines[0]) != "msg_1" || lineID(t, lines[1]) != "msg_2" {
		t.Fatalf("file lines = %v", lines)
	}
}

func TestCollectorReconcileGapStop(t *testing.T) {
	out := filepath.Join(t.TempDir(), "messages.jsonl")
	// Middle message incomplete: only the first (complete) user message emits.
	source := &fakeOCSource{envs: []ocRawEnvelope{
		env(`{"id":"msg_1","role":"user"}`),
		env(`{"id":"msg_2","role":"assistant"}`), // no finish -> incomplete
		env(`{"id":"msg_3","role":"user"}`),
	}}
	c := NewOpenCodeCollector(source, "ses_1", out, time.Second)
	n, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("appended %d, want 1 (gap-stop at msg_2)", n)
	}

	// msg_2 completes -> next reconcile emits msg_2 then msg_3, in order.
	source.envs[1] = env(`{"id":"msg_2","role":"assistant","finish":"stop"}`)
	n, err = c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	if n != 2 {
		t.Fatalf("appended %d, want 2", n)
	}
	lines := readLines(t, out)
	got := []string{lineID(t, lines[0]), lineID(t, lines[1]), lineID(t, lines[2])}
	want := []string{"msg_1", "msg_2", "msg_3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestCollectorIdempotentAcrossRestart(t *testing.T) {
	out := filepath.Join(t.TempDir(), "messages.jsonl")
	envs := []ocRawEnvelope{
		env(`{"id":"msg_1","role":"user"}`),
		env(`{"id":"msg_2","role":"assistant","finish":"stop"}`),
	}
	c1 := NewOpenCodeCollector(&fakeOCSource{envs: envs}, "ses_1", out, time.Second)
	if _, err := c1.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Fresh collector (simulating daemon restart) seeds from the file, then a
	// reconcile over the same data appends nothing.
	c2 := NewOpenCodeCollector(&fakeOCSource{envs: envs}, "ses_1", out, time.Second)
	if err := c2.seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	n, err := c2.reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("re-seeded reconcile appended %d lines, want 0", n)
	}
	if got := len(readLines(t, out)); got != 2 {
		t.Errorf("file has %d lines, want 2 (no duplicates)", got)
	}
}

func TestCollectorReconcileFiltersNonTerminalToolParts(t *testing.T) {
	out := filepath.Join(t.TempDir(), "messages.jsonl")
	source := &fakeOCSource{envs: []ocRawEnvelope{
		env(`{"id":"msg_1","role":"assistant","finish":"tool-calls"}`,
			`{"type":"tool","tool":"Bash","state":{"status":"running"}}`,
			`{"type":"text","text":"done"}`),
	}}
	c := NewOpenCodeCollector(source, "ses_1", out, time.Second)
	if _, err := c.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, out)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	var got struct {
		Parts []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 1 {
		t.Errorf("emitted %d parts, want 1 (running tool dropped)", len(got.Parts))
	}
}

// TestCollectorAdvancesHWM asserts the collector passes its high-water
// mark down to the source on each successive call. This is the efficiency
// story: long sessions don't re-fetch the entire message history every
// 30s, they fetch only what's new.
func TestCollectorAdvancesHWM(t *testing.T) {
	out := filepath.Join(t.TempDir(), "messages.jsonl")
	source := &fakeOCSource{envs: []ocRawEnvelope{
		env(`{"id":"msg_a","role":"user"}`),
		env(`{"id":"msg_b","role":"assistant","finish":"stop"}`),
	}}
	c := NewOpenCodeCollector(source, "ses_1", out, time.Second)

	if _, err := c.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(source.calls) != 2 {
		t.Fatalf("source called %d times, want 2", len(source.calls))
	}
	if source.calls[0] != "" {
		t.Errorf("first call HWM = %q, want \"\"", source.calls[0])
	}
	if source.calls[1] != "msg_b" {
		t.Errorf("second call HWM = %q, want msg_b", source.calls[1])
	}
}

// TestCollectorErrorRecoveryResetsCadence asserts that a successful
// reconcile clears the consecutive-error counter so the next failure
// emits a fresh Warn (rather than being swallowed as "we already warned
// about this kind 12 cycles ago"). Tests behavior; log-line capture is
// covered separately if needed.
func TestCollectorErrorRecoveryResetsCadence(t *testing.T) {
	out := filepath.Join(t.TempDir(), "messages.jsonl")
	source := &fakeOCSource{err: errors.New("transient db error")}
	c := NewOpenCodeCollector(source, "ses_1", out, time.Second)

	c.tryReconcile(context.Background())
	if c.consecutiveErr != 1 {
		t.Errorf("after 1 failure: consecutiveErr=%d, want 1", c.consecutiveErr)
	}
	if c.lastErrKind == "" {
		t.Error("lastErrKind not recorded")
	}

	// Recover.
	source.err = nil
	source.envs = []ocRawEnvelope{env(`{"id":"msg_1","role":"user"}`)}
	c.tryReconcile(context.Background())
	if c.consecutiveErr != 0 {
		t.Errorf("after recovery: consecutiveErr=%d, want 0", c.consecutiveErr)
	}
	if c.lastErrKind != "" {
		t.Errorf("lastErrKind=%q after recovery, want \"\"", c.lastErrKind)
	}

	// New failure: counter starts fresh at 1.
	source.err = errors.New("different error")
	source.envs = nil
	c.tryReconcile(context.Background())
	if c.consecutiveErr != 1 {
		t.Errorf("after recovery+new failure: consecutiveErr=%d, want 1", c.consecutiveErr)
	}
}

func TestWarnEveryNTargetsOnePerMinute(t *testing.T) {
	cases := []struct {
		interval time.Duration
		wantN    int
	}{
		{5 * time.Second, 12},
		{30 * time.Second, 2},
		{60 * time.Second, 1},
		{2 * time.Minute, 1},
		{1 * time.Second, 60},
	}
	for _, c := range cases {
		if got := warnEveryN(c.interval); got != c.wantN {
			t.Errorf("warnEveryN(%v) = %d, want %d", c.interval, got, c.wantN)
		}
	}
}
