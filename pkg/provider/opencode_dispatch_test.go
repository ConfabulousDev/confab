package provider_test

import (
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// ---- Opencode.AnnotateChunk ----
//
// CF-540: OpenCode must populate first_user_message so synced sessions appear
// in the web session list. AnnotateChunk extracts the first user message's
// first text part from the materialized {info, parts} JSONL.

func TestOpencode_AnnotateChunk_SetsFirstUserMessage(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"hello world"}]}`,
		`{"info":{"id":"msg_2","role":"assistant","finish":"stop"},"parts":[{"type":"text","text":"hi back"}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	result := (provider.Opencode{}).AnnotateChunk(cv, false, nil)
	if !result.IncludedFirstUserMessage {
		t.Error("IncludedFirstUserMessage = false, want true on first transcript chunk")
	}
	if cv.setFirstUserMessage != "hello world" {
		t.Errorf("SetFirstUserMessage = %q, want %q", cv.setFirstUserMessage, "hello world")
	}
}

func TestOpencode_AnnotateChunk_AlreadySentNoop(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"hello"}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 5, lines: lines}
	result := (provider.Opencode{}).AnnotateChunk(cv, true, nil)
	if result.IncludedFirstUserMessage {
		t.Error("IncludedFirstUserMessage = true, want false when already sent")
	}
	if cv.setFirstUserMessage != "" {
		t.Errorf("SetFirstUserMessage = %q, want \"\" when already sent", cv.setFirstUserMessage)
	}
}

func TestOpencode_AnnotateChunk_NonTranscriptFileNoop(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"hello"}]}`,
	}
	cv := &stubChunkView{fileType: "agent", firstLine: 1, lines: lines}
	result := (provider.Opencode{}).AnnotateChunk(cv, false, nil)
	if result.IncludedFirstUserMessage {
		t.Error("IncludedFirstUserMessage = true on non-transcript file, want false")
	}
	if cv.setFirstUserMessage != "" {
		t.Errorf("SetFirstUserMessage = %q on non-transcript file, want \"\"", cv.setFirstUserMessage)
	}
}

func TestOpencode_AnnotateChunk_RedactionApplied(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"secret AKIA-EXAMPLE here"}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	redact := func(s string) string { return strings.ReplaceAll(s, "AKIA-EXAMPLE", "[REDACTED]") }
	(provider.Opencode{}).AnnotateChunk(cv, false, redact)
	if strings.Contains(cv.setFirstUserMessage, "AKIA-EXAMPLE") {
		t.Errorf("SetFirstUserMessage = %q; redaction not applied", cv.setFirstUserMessage)
	}
	if !strings.Contains(cv.setFirstUserMessage, "[REDACTED]") {
		t.Errorf("SetFirstUserMessage = %q; want redaction marker", cv.setFirstUserMessage)
	}
}

func TestOpencode_AnnotateChunk_NilRedactNoPanic(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"raw text"}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	(provider.Opencode{}).AnnotateChunk(cv, false, nil)
	if cv.setFirstUserMessage != "raw text" {
		t.Errorf("SetFirstUserMessage = %q, want %q with nil redact", cv.setFirstUserMessage, "raw text")
	}
}

func TestOpencode_AnnotateChunk_NoUserMessageNoop(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"assistant","finish":"stop"},"parts":[{"type":"text","text":"only assistant"}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	result := (provider.Opencode{}).AnnotateChunk(cv, false, nil)
	if result.IncludedFirstUserMessage {
		t.Error("IncludedFirstUserMessage = true with no user message, want false")
	}
	if cv.setFirstUserMessage != "" {
		t.Errorf("SetFirstUserMessage = %q with no user message, want \"\"", cv.setFirstUserMessage)
	}
}

func TestOpencode_AnnotateChunk_SkipsNonTextPart(t *testing.T) {
	// A user message whose first part is a file attachment, followed by text:
	// the first text part wins.
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"file","filename":"a.png"},{"type":"text","text":"describe this"}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	(provider.Opencode{}).AnnotateChunk(cv, false, nil)
	if cv.setFirstUserMessage != "describe this" {
		t.Errorf("SetFirstUserMessage = %q, want %q", cv.setFirstUserMessage, "describe this")
	}
}

func TestOpencode_AnnotateChunk_TrimsWhitespace(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"  padded  "}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	(provider.Opencode{}).AnnotateChunk(cv, false, nil)
	if cv.setFirstUserMessage != "padded" {
		t.Errorf("SetFirstUserMessage = %q, want %q (trimmed)", cv.setFirstUserMessage, "padded")
	}
}

func TestOpencode_AnnotateChunk_WhitespaceOnlyTextNoop(t *testing.T) {
	lines := []string{
		`{"info":{"id":"msg_1","role":"user"},"parts":[{"type":"text","text":"   "}]}`,
	}
	cv := &stubChunkView{fileType: "transcript", firstLine: 1, lines: lines}
	result := (provider.Opencode{}).AnnotateChunk(cv, false, nil)
	if result.IncludedFirstUserMessage {
		t.Error("IncludedFirstUserMessage = true for whitespace-only text, want false")
	}
	if cv.setFirstUserMessage != "" {
		t.Errorf("SetFirstUserMessage = %q for whitespace-only text, want \"\"", cv.setFirstUserMessage)
	}
}
