package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpencodeClientSessionMessages(t *testing.T) {
	const sessionID = "ses_abc"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/"+sessionID+"/message" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"hi"}]},
			{"info":{"id":"msg_2","role":"assistant","finish":"stop"},"parts":[{"type":"text","text":"yo"}]}
		]`)
	}))
	defer srv.Close()

	c := NewOpenCodeClient(srv.URL)
	envs, err := c.SessionMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("SessionMessages: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want 2", len(envs))
	}
	info, err := ocPeekInfo(envs[1].Info)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "msg_2" || info.Role != "assistant" {
		t.Errorf("envelope[1] info = %+v", info)
	}
	if len(envs[0].Parts) != 1 {
		t.Errorf("envelope[0] parts = %d, want 1", len(envs[0].Parts))
	}
}

func TestOpencodeClientSessionMessagesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewOpenCodeClient(srv.URL)
	if _, err := c.SessionMessages(context.Background(), "ses_x"); err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestOpencodeClientSubscribeEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" {
			t.Errorf("unexpected SSE path %q", r.URL.Path)
		}
		fl, _ := w.(http.Flusher)
		// server.connected, then a session.idle for our session.
		fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"session.idle\",\"properties\":{\"sessionID\":\"ses_abc\"}}\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	c := NewOpenCodeClient(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, err := c.SubscribeEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}

	var types []string
	for ev := range ch {
		types = append(types, ev.Type)
	}
	if len(types) != 2 || types[0] != "server.connected" || types[1] != "session.idle" {
		t.Fatalf("events = %v, want [server.connected session.idle]", types)
	}
}

func TestOpencodeClientSubscribeEventsClosesOnStreamEnd(t *testing.T) {
	// A server that closes immediately after server.connected (matches a known
	// upstream bug) must surface as a closed channel so the collector reconnects.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
	}))
	defer srv.Close()

	c := NewOpenCodeClient(srv.URL)
	ch, err := c.SubscribeEvents(context.Background())
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	got := 0
	done := make(chan struct{})
	go func() {
		for range ch {
			got++
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after stream ended")
	}
	if got != 1 {
		t.Errorf("got %d events, want 1", got)
	}
}
