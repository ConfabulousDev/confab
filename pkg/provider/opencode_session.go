package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// OpenCode message/part assembly + completeness gating.
//
// A "session" is fetched from the OpenCode HTTP API as a list of envelopes,
// one per conversation message: {info: <message>, parts: [<part>, ...]}. The
// daemon materializes each *complete* envelope as one JSONL line (the backend's
// OpenCode provider parses exactly this {info, parts} shape).
//
// To filter parts without re-modelling all 12 part types (and risking data
// loss), envelopes are decoded shallowly: info and every part stay as raw
// JSON, and only the handful of fields gating needs are peeked. The emitted
// line preserves the upstream bytes of info and each kept part verbatim;
// redaction happens later in pkg/sync's ReadChunk.

const (
	ocRoleUser            = "user"
	ocRoleAssistant       = "assistant"
	ocPartTypeText        = "text"
	ocPartTypeTool        = "tool"
	ocToolStatusCompleted = "completed"
	ocToolStatusError     = "error"
)

// ocRawEnvelope is one message with its parts, both kept raw.
type ocRawEnvelope struct {
	Info  json.RawMessage   `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

// ocInfoPeek decodes only the message-info fields completeness gating needs.
// Finish is a pointer so an absent/null finish (still streaming) is
// distinguishable from a set one (settled); Error stays raw so presence —
// not its 5-variant shape — is what we test.
type ocInfoPeek struct {
	ID     string          `json:"id"`
	Role   string          `json:"role"`
	Finish *string         `json:"finish"`
	Error  json.RawMessage `json:"error"`
}

// ocPartPeek decodes only the part discriminator + tool state needed to drop
// non-terminal tool parts.
type ocPartPeek struct {
	Type  string `json:"type"`
	State struct {
		Status string `json:"status"`
	} `json:"state"`
}

// ocTextPart decodes a text part's content, used to extract the first user
// message preview (first_user_message) in AnnotateChunk.
type ocTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ocPeekInfo decodes the gating fields from a raw message-info object.
func ocPeekInfo(raw json.RawMessage) (ocInfoPeek, error) {
	var info ocInfoPeek
	if err := json.Unmarshal(raw, &info); err != nil {
		return ocInfoPeek{}, fmt.Errorf("decode message info: %w", err)
	}
	return info, nil
}

// ocPeekPart decodes the gating fields from a raw part object.
func ocPeekPart(raw json.RawMessage) (ocPartPeek, error) {
	var part ocPartPeek
	if err := json.Unmarshal(raw, &part); err != nil {
		return ocPartPeek{}, fmt.Errorf("decode part: %w", err)
	}
	return part, nil
}

// ocIsTerminalToolStatus reports whether a tool part's state.status is settled.
func ocIsTerminalToolStatus(status string) bool {
	return status == ocToolStatusCompleted || status == ocToolStatusError
}

// ocIsComplete reports whether a message is settled and may be emitted as a
// line. User messages are complete on arrival; assistant messages are complete
// once finish is non-null or an error is present.
func ocIsComplete(info ocInfoPeek) bool {
	if info.Role != ocRoleAssistant {
		return true
	}
	return info.Finish != nil || ocHasError(info.Error)
}

// ocKeepParts returns the parts to emit: every non-tool part verbatim, and
// only terminal (completed/error) tool parts. Order is preserved. The result
// is always non-nil so it serializes as [] rather than null.
func ocKeepParts(parts []json.RawMessage) ([]json.RawMessage, error) {
	kept := make([]json.RawMessage, 0, len(parts))
	for _, p := range parts {
		pk, err := ocPeekPart(p)
		if err != nil {
			return nil, err
		}
		if pk.Type == ocPartTypeTool && !ocIsTerminalToolStatus(pk.State.Status) {
			continue
		}
		kept = append(kept, p)
	}
	return kept, nil
}

// ocSerializeLine renders {info, parts} for one envelope with non-terminal
// tool parts removed, preserving the raw bytes of info and each kept part.
func ocSerializeLine(env ocRawEnvelope) ([]byte, error) {
	kept, err := ocKeepParts(env.Parts)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Info  json.RawMessage   `json:"info"`
		Parts []json.RawMessage `json:"parts"`
	}{Info: env.Info, Parts: kept})
}

// ocSortByID returns the envelopes ordered by message id ascending. OpenCode
// ids are ULIDs, so lexical order is chronological. Callers walk this list and
// stop at the first incomplete message to keep the materialized file
// append-only and monotonic.
func ocSortByID(envs []ocRawEnvelope) ([]ocRawEnvelope, error) {
	type keyed struct {
		id  string
		env ocRawEnvelope
	}
	keys := make([]keyed, 0, len(envs))
	for _, e := range envs {
		info, err := ocPeekInfo(e.Info)
		if err != nil {
			return nil, err
		}
		keys = append(keys, keyed{id: info.ID, env: e})
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].id < keys[j].id })
	out := make([]ocRawEnvelope, len(keys))
	for i, k := range keys {
		out[i] = k.env
	}
	return out, nil
}

// ocHasError reports whether a raw error field is present and non-null.
func ocHasError(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// ocFirstUserMessageText returns the trimmed text of the first user message's
// first text part across the given materialized {info, parts} JSONL lines, or
// "" if none is found. Used for the first_user_message preview (CF-540): a
// non-empty value makes a synced OpenCode session visible in the web session
// list. Blank lines are skipped; a malformed line returns an error so the
// caller can decide how to degrade.
func ocFirstUserMessageText(lines []string) (string, error) {
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var env ocRawEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			return "", fmt.Errorf("decode envelope: %w", err)
		}
		info, err := ocPeekInfo(env.Info)
		if err != nil {
			return "", err
		}
		if info.Role != ocRoleUser {
			continue
		}
		for _, raw := range env.Parts {
			var part ocTextPart
			if err := json.Unmarshal(raw, &part); err != nil {
				return "", fmt.Errorf("decode text part: %w", err)
			}
			if part.Type != ocPartTypeText {
				continue
			}
			if text := strings.TrimSpace(part.Text); text != "" {
				return text, nil
			}
		}
		return "", nil // first user message has no usable text part
	}
	return "", nil
}
