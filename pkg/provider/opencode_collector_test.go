package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeOCClient serves a programmable set of envelopes; SubscribeEvents returns
// an already-closed channel (collector falls back to polling/explicit calls).
type fakeOCClient struct {
	envs []ocRawEnvelope
	err  error
}

func (f *fakeOCClient) SessionMessages(context.Context, string) ([]ocRawEnvelope, error) {
	return f.envs, f.err
}

func (f *fakeOCClient) SubscribeEvents(context.Context) (<-chan OpenCodeEvent, error) {
	ch := make(chan OpenCodeEvent)
	close(ch)
	return ch, nil
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
	client := &fakeOCClient{envs: []ocRawEnvelope{
		env(`{"id":"msg_1","role":"user"}`, `{"type":"text","text":"hi"}`),
		env(`{"id":"msg_2","role":"assistant","finish":"stop"}`, `{"type":"text","text":"yo"}`),
	}}
	c := NewOpenCodeCollector(client, "ses_1", out)
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
	client := &fakeOCClient{envs: []ocRawEnvelope{
		env(`{"id":"msg_1","role":"user"}`),
		env(`{"id":"msg_2","role":"assistant"}`), // no finish -> incomplete
		env(`{"id":"msg_3","role":"user"}`),
	}}
	c := NewOpenCodeCollector(client, "ses_1", out)
	n, err := c.reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("appended %d, want 1 (gap-stop at msg_2)", n)
	}

	// msg_2 completes -> next reconcile emits msg_2 then msg_3, in order.
	client.envs[1] = env(`{"id":"msg_2","role":"assistant","finish":"stop"}`)
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
	c1 := NewOpenCodeCollector(&fakeOCClient{envs: envs}, "ses_1", out)
	if _, err := c1.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Fresh collector (simulating daemon restart) seeds from the file, then a
	// reconcile over the same data appends nothing.
	c2 := NewOpenCodeCollector(&fakeOCClient{envs: envs}, "ses_1", out)
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
	client := &fakeOCClient{envs: []ocRawEnvelope{
		env(`{"id":"msg_1","role":"assistant","finish":"tool-calls"}`,
			`{"type":"tool","tool":"Bash","state":{"status":"running"}}`,
			`{"type":"text","text":"done"}`),
	}}
	c := NewOpenCodeCollector(client, "ses_1", out)
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
