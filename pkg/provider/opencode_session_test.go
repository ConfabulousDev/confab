package provider

import (
	"encoding/json"
	"testing"
)

func env(info string, parts ...string) ocRawEnvelope {
	raw := make([]json.RawMessage, len(parts))
	for i, p := range parts {
		raw[i] = json.RawMessage(p)
	}
	return ocRawEnvelope{Info: json.RawMessage(info), Parts: raw}
}

func TestOpencodeIsCompleteUserAlways(t *testing.T) {
	info, err := ocPeekInfo(json.RawMessage(`{"id":"msg_1","role":"user"}`))
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if !ocIsComplete(info) {
		t.Error("user message must be complete on arrival")
	}
}

func TestOpencodeIsCompleteAssistantGating(t *testing.T) {
	cases := []struct {
		name string
		info string
		want bool
	}{
		{"no finish no error -> incomplete", `{"id":"m","role":"assistant"}`, false},
		{"null finish -> incomplete", `{"id":"m","role":"assistant","finish":null}`, false},
		{"finish set -> complete", `{"id":"m","role":"assistant","finish":"stop"}`, true},
		{"finish tool-calls -> complete", `{"id":"m","role":"assistant","finish":"tool-calls"}`, true},
		{"error present -> complete", `{"id":"m","role":"assistant","error":{"name":"APIError","message":"x"}}`, true},
		{"null error no finish -> incomplete", `{"id":"m","role":"assistant","error":null}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := ocPeekInfo(json.RawMessage(tc.info))
			if err != nil {
				t.Fatalf("peek: %v", err)
			}
			if got := ocIsComplete(info); got != tc.want {
				t.Errorf("ocIsComplete = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOpencodeKeepPartsDropsNonTerminalTools(t *testing.T) {
	parts := []json.RawMessage{
		json.RawMessage(`{"type":"text","text":"hi"}`),
		json.RawMessage(`{"type":"tool","tool":"Bash","state":{"status":"pending"}}`),
		json.RawMessage(`{"type":"tool","tool":"Bash","state":{"status":"running"}}`),
		json.RawMessage(`{"type":"tool","tool":"Bash","state":{"status":"completed","output":"ok"}}`),
		json.RawMessage(`{"type":"tool","tool":"Grep","state":{"status":"error","error":"boom"}}`),
		json.RawMessage(`{"type":"reasoning","text":"think"}`),
	}
	kept, err := ocKeepParts(parts)
	if err != nil {
		t.Fatalf("keepParts: %v", err)
	}
	if len(kept) != 4 {
		t.Fatalf("kept %d parts, want 4 (text, completed tool, error tool, reasoning)", len(kept))
	}
	// Non-tool parts and terminal tool parts survive; order preserved.
	for _, p := range kept {
		var pk ocPartPeek
		if err := json.Unmarshal(p, &pk); err != nil {
			t.Fatal(err)
		}
		if pk.Type == ocPartTypeTool && pk.State.Status != ocToolStatusCompleted && pk.State.Status != ocToolStatusError {
			t.Errorf("non-terminal tool part survived: %s", string(p))
		}
	}
}

func TestOpencodeSerializeLineShape(t *testing.T) {
	e := env(
		`{"id":"msg_1","role":"assistant","finish":"tool-calls","providerID":"anthropic","modelID":"claude-x"}`,
		`{"type":"tool","tool":"Bash","state":{"status":"pending"}}`,
		`{"type":"text","text":"done"}`,
	)
	line, err := ocSerializeLine(e)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	var got struct {
		Info  map[string]any    `json:"info"`
		Parts []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("unmarshal line: %v", err)
	}
	// Pending tool part filtered out; only the text part remains.
	if len(got.Parts) != 1 {
		t.Fatalf("got %d parts, want 1 (pending tool dropped)", len(got.Parts))
	}
	// info bytes preserved verbatim (providerID/modelID survive).
	if got.Info["providerID"] != "anthropic" || got.Info["modelID"] != "claude-x" {
		t.Errorf("info not preserved verbatim: %v", got.Info)
	}
}

func TestOpencodeSerializeLineEmptyPartsIsArray(t *testing.T) {
	// An assistant message whose only part is a non-terminal tool must emit
	// "parts":[] (a JSON array), never "parts":null.
	e := env(`{"id":"m","role":"assistant","finish":"stop"}`,
		`{"type":"tool","state":{"status":"running"}}`)
	line, err := ocSerializeLine(e)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if !json.Valid(line) {
		t.Fatalf("invalid json: %s", line)
	}
	var probe struct {
		Parts []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Parts == nil {
		t.Error("parts must serialize as [] not null")
	}
}

func TestOpencodeSortByID(t *testing.T) {
	envs := []ocRawEnvelope{
		env(`{"id":"msg_03","role":"user"}`),
		env(`{"id":"msg_01","role":"user"}`),
		env(`{"id":"msg_02","role":"assistant","finish":"stop"}`),
	}
	sorted, err := ocSortByID(envs)
	if err != nil {
		t.Fatalf("sort: %v", err)
	}
	want := []string{"msg_01", "msg_02", "msg_03"}
	for i, e := range sorted {
		info, _ := ocPeekInfo(e.Info)
		if info.ID != want[i] {
			t.Errorf("position %d = %q, want %q", i, info.ID, want[i])
		}
	}
}

func TestOpencodePeekInfoMalformed(t *testing.T) {
	if _, err := ocPeekInfo(json.RawMessage(`{not json`)); err == nil {
		t.Error("expected error on malformed info")
	}
}
