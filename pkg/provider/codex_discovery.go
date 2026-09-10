package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/types"
)

// CodexSessionInfo is the rich Codex-specific session metadata returned
// by ScanCodexSessions and ReadSessionInfo. Internal callers (cmd/save.go,
// engine init paths) need the extras (CWD, Model, AgentRole, ...) that
// don't fit on the cross-provider SessionInfo.
type CodexSessionInfo struct {
	SessionID   string
	RolloutPath string
	CWD         string
	Model       string
	// Source is a short discriminator extracted from the rollout's `source`
	// field. Codex writes that field as either a bare string ("cli") for
	// user-initiated rollouts or a tagged object ({"subagent":{...}}) for
	// spawned subagents. The string case is passed through; the object case
	// is collapsed to its top-level key. Empty when session_meta omits the
	// field. Matches the backend's 64-char `codex_rollouts.source` column.
	Source        string
	ThreadSource  string
	AgentPath     string
	AgentRole     string
	AgentNickname string
	ModTime       time.Time
	SizeBytes     int64
}

type codexRolloutLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID  string `json:"id"`
	CWD string `json:"cwd"`
	// Source is parsed as raw JSON because Codex writes a polymorphic shape
	// — flatten via flattenCodexSource before exposing to callers.
	Source        json.RawMessage `json:"source"`
	Model         string          `json:"model"`
	ThreadSource  string          `json:"thread_source"`
	AgentPath     string          `json:"agent_path"`
	AgentRole     string          `json:"agent_role"`
	AgentNickname string          `json:"agent_nickname"`
	// AgentType mirrors Codex's `#[serde(alias = "agent_type")]` on
	// SessionMeta.agent_role: a rollout may carry either key. Read it via
	// agentRole() so a subagent tagged with the alias can't slip past
	// IsUserSession and spawn a stray daemon. Never read directly.
	AgentType string `json:"agent_type"`
}

// agentRole returns the rollout's agent role, honoring Codex's
// `agent_type` alias. The canonical field wins when both are present.
func (m codexSessionMeta) agentRole() string {
	if m.AgentRole != "" {
		return m.AgentRole
	}
	return m.AgentType
}

// codexUserMessagePayload decodes both event_msg payload shapes that can
// carry the human prompt. Codex changed the shape around 0.149.1:
//
//	pre-0.149  {"type":"user_message","message":"hi"}
//	0.149+     {"type":"item_completed","item":{"type":"UserMessage",
//	            "content":[{"type":"text","text":"hi"},...]}}
//
// Both are decoded from one struct because the discriminating `type` and
// the two payload bodies never collide. Old rollouts are still on disk and
// their shape is frozen, so this does not grow over time.
type codexUserMessagePayload struct {
	Type string `json:"type"`
	// Message carries the prompt in the pre-0.149 shape.
	Message string `json:"message"`
	// Item carries it in the 0.149+ shape.
	Item codexRolloutItem `json:"item"`
}

// codexRolloutItem is the `item` object of an item_completed event. Only
// the UserMessage variant is modeled; every other item type (AgentMessage,
// Reasoning, CommandExecution, FileChange, Extension, ...) decodes with an
// unmatched Type and is skipped.
type codexRolloutItem struct {
	Type    string                    `json:"type"`
	Content []codexUserMessageContent `json:"content"`
}

// codexUserMessageContent is one typed part of a UserMessage's content
// array. Only `type == "text"` carries prompt text — real rollouts also
// contain e.g. {"type":"skill","name":...,"path":...} parts, which must
// not leak into the session title.
type codexUserMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// text returns the prompt text of a UserMessage item: its text parts
// joined by newlines, in wire order. Non-text parts are dropped.
func (i codexRolloutItem) text() string {
	parts := make([]string, 0, len(i.Content))
	for _, c := range i.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

var codexRolloutPattern = regexp.MustCompile(`^rollout-.+-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

// IsUserSession reports whether this rollout is a top-level user session
// (not a subagent or memory rollout).
func (s CodexSessionInfo) IsUserSession() bool {
	if s.ThreadSource != "" && s.ThreadSource != "user" {
		return false
	}
	return s.AgentPath == "" && s.AgentRole == "" && s.AgentNickname == ""
}

// flattenCodexSource collapses Codex's polymorphic `source` field to a short
// discriminator string suitable for the backend's `codex_rollouts.source`
// column. Returns "" when raw is empty, the unquoted string when raw is a
// JSON string ("cli" -> "cli"), or the single top-level key when raw is a
// JSON object ({"subagent":{...}} -> "subagent"). Anything else falls back
// to "" so the malformed input doesn't trip the backend's 64-char limit.
func flattenCodexSource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		for k := range obj {
			return k
		}
	}
	return ""
}

// SessionIDFromRolloutPath parses the UUID embedded in a Codex rollout
// filename. Returns ("", false) on filenames that don't match the rollout
// pattern.
func (Codex) SessionIDFromRolloutPath(path string) (string, bool) {
	matches := codexRolloutPattern.FindStringSubmatch(filepath.Base(path))
	if matches == nil {
		return "", false
	}
	return matches[1], true
}

// ScanCodexSessions returns the rich Codex-specific session info for every
// user-initiated rollout. Internal callers that need CodexSessionInfo's
// extras (CWD, Model, AgentRole, ...) use this directly; the cross-provider
// Provider.ScanSessions interface method projects to []SessionInfo.
func (p Codex) ScanCodexSessions() ([]CodexSessionInfo, error) {
	sessionsDir, err := p.SessionsDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var sessions []CodexSessionInfo
	err = p.walkRollouts(sessionsDir, func(path, sessionID string) {
		info, err := p.ReadSessionInfo(path)
		if err != nil {
			return
		}
		info.SessionID = sessionID
		if info.IsUserSession() {
			sessions = append(sessions, info)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk Codex sessions directory: %w", err)
	}
	return sessions, nil
}

// ScanSessions projects ScanCodexSessions to the cross-provider
// SessionInfo shape. FirstUserMessage is extracted from each rollout's
// first user-message event_msg line, in either shape Codex emits (capped
// to maxLinesForExtraction). Sessions are returned oldest first to match
// Claude's ordering.
func (p Codex) ScanSessions() ([]SessionInfo, error) {
	codex, err := p.ScanCodexSessions()
	if err != nil {
		return nil, err
	}
	sessions := make([]SessionInfo, 0, len(codex))
	for _, s := range codex {
		sessions = append(sessions, SessionInfo{
			SessionID:        s.SessionID,
			TranscriptPath:   s.RolloutPath,
			ProjectPath:      s.CWD,
			ModTime:          s.ModTime,
			SizeBytes:        s.SizeBytes,
			FirstUserMessage: p.firstUserMessageForScan(s.RolloutPath),
		})
	}
	return sessions, nil
}

// firstUserMessageForScan reads the head of a rollout file and extracts
// the first user message from it. Errors degrade silently to "" — the
// list command tolerates missing titles.
func (p Codex) firstUserMessageForScan(path string) string {
	lines, _ := readHeadLines(path)
	return p.ExtractFirstUserMessageFromLines(lines)
}

// FindSessionByID resolves a full or partial UUID to the ROOT thread's
// UUID and rollout path. If partialID matches a subagent, this walks up
// to the top-most user session via WalkUpToRoot so callers transparently
// upload the whole tree.
func (p Codex) FindSessionByID(partialID string) (string, string, error) {
	id, _, err := p.findRolloutByID(partialID, false)
	if err != nil {
		return "", "", err
	}
	rootID, rootPath, _ := p.WalkUpToRoot(id)
	if rootID == "" {
		rootID = id
	}
	if rootPath == "" {
		// WalkUpToRoot returns rootPath="" when the walk lands on a
		// thread the DB doesn't carry a rollout_path for (e.g., the
		// firing UUID already IS the root). Fall back to scanning the
		// sessions directory for the root's rollout file by filename.
		_, p2, err := p.findRolloutByID(rootID, false)
		if err == nil {
			rootPath = p2
		}
	}
	return rootID, rootPath, nil
}

// findRolloutByID is the shared implementation: scans the sessions directory
// for rollouts whose filename UUID matches partialID, optionally filtering
// out non-user (subagent) rollouts.
func (p Codex) findRolloutByID(partialID string, userOnly bool) (string, string, error) {
	sessionsDir, err := p.SessionsDir()
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		return "", "", fmt.Errorf("session not found: %s", partialID)
	}

	type rolloutMatch struct {
		id   string
		path string
	}
	var matches []rolloutMatch
	err = p.walkRollouts(sessionsDir, func(path, sessionID string) {
		if strings.HasPrefix(sessionID, partialID) {
			matches = append(matches, rolloutMatch{id: sessionID, path: path})
		}
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to walk Codex sessions directory: %w", err)
	}
	if len(matches) == 0 {
		return "", "", fmt.Errorf("session not found: %s", partialID)
	}
	if len(matches) > 1 {
		return "", "", fmt.Errorf("ambiguous session ID %q matches %d sessions", partialID, len(matches))
	}

	info, err := p.ReadSessionInfo(matches[0].path)
	if err != nil {
		return "", "", err
	}
	if userOnly && !info.IsUserSession() {
		return "", "", fmt.Errorf("session not found: %s", partialID)
	}
	return matches[0].id, matches[0].path, nil
}

// walkRollouts visits every Codex rollout JSONL file under root, invoking fn
// with the file path and the session ID parsed from its filename. Entries with
// walk errors or unrecognized names are silently skipped.
func (p Codex) walkRollouts(root string, fn func(path, sessionID string)) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		sessionID, ok := p.SessionIDFromRolloutPath(path)
		if !ok {
			return nil
		}
		fn(path, sessionID)
		return nil
	})
}

// ReadSessionInfo reads a rollout's session_meta line and returns the
// parsed CodexSessionInfo (with file stat info populated).
func (p Codex) ReadSessionInfo(path string) (CodexSessionInfo, error) {
	if err := p.ValidateRolloutPath(path); err != nil {
		return CodexSessionInfo{}, err
	}

	stat, err := os.Stat(path)
	if err != nil {
		return CodexSessionInfo{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		return CodexSessionInfo{}, err
	}
	defer f.Close()

	info := CodexSessionInfo{
		RolloutPath: path,
		ModTime:     stat.ModTime(),
		SizeBytes:   stat.Size(),
	}

	scanner := types.NewJSONLScanner(f)
	for scanner.Scan() {
		var line codexRolloutLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if line.Type != "session_meta" {
			continue
		}
		var meta codexSessionMeta
		if err := json.Unmarshal(line.Payload, &meta); err != nil {
			return info, fmt.Errorf("failed to parse session_meta payload: %w", err)
		}
		info.CWD = meta.CWD
		info.Model = meta.Model
		info.Source = flattenCodexSource(meta.Source)
		info.ThreadSource = meta.ThreadSource
		info.AgentPath = meta.AgentPath
		info.AgentRole = meta.agentRole()
		info.AgentNickname = meta.AgentNickname
		return info, nil
	}
	if err := scanner.Err(); err != nil {
		return info, fmt.Errorf("failed to scan Codex rollout: %w", err)
	}
	return info, nil
}

// ExtractFirstUserMessageFromLines returns the first non-empty user message
// found in the given rollout lines, truncated to MaxMetadataFieldLength/2
// bytes on a UTF-8 boundary. Returns "" when no user message is present.
//
// Both `event_msg` shapes Codex has emitted are recognized (see
// codexUserMessagePayload); whichever appears first in line order wins. The
// shape is sniffed from the payload rather than gated on `cli_version`,
// which degrades better against versions we have not sampled. A modern
// message spanning several text parts is joined with newlines.
//
// `response_item` lines are deliberately NOT consulted as a fallback: they
// are contaminated by injected context (<environment_context>, AGENTS.md),
// so a miss here is better than an injected string becoming the title.
func (Codex) ExtractFirstUserMessageFromLines(lines []string) string {
	for _, raw := range lines {
		var line codexRolloutLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			continue
		}
		if line.Type != "event_msg" {
			continue
		}
		var payload codexUserMessagePayload
		if err := json.Unmarshal(line.Payload, &payload); err != nil {
			continue
		}
		var message string
		switch {
		case payload.Type == "user_message":
			message = payload.Message
		case payload.Type == "item_completed" && payload.Item.Type == "UserMessage":
			message = payload.Item.text()
		default:
			continue
		}
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		return TruncateUTF8(message, types.MaxMetadataFieldLength/2)
	}
	return ""
}

// ExtractMetadata returns the in-memory metadata for a Codex chunk.
// Summary and SummaryLinks stay empty — those are Claude-only concepts.
// Lines are capped to maxLinesForExtraction to mirror Claude's bound.
func (p Codex) ExtractMetadata(lines []string) SessionMetadata {
	if len(lines) > maxLinesForExtraction {
		lines = lines[:maxLinesForExtraction]
	}
	return SessionMetadata{
		FirstUserMessage: p.ExtractFirstUserMessageFromLines(lines),
	}
}

// OnAlreadyRunning is a no-op for Codex: hook deduplication is the normal
// path (Codex fires SessionStart for every spawned subagent). See Provider
// interface doc for the OpenCode-specific behavior.
func (Codex) OnAlreadyRunning(string) {}

// DefaultCWD reads session_meta.cwd from the rollout. Falls back to
// filepath.Dir on read failure or empty CWD so the upload still has a
// directory to record.
func (p Codex) DefaultCWD(transcriptPath string) string {
	info, err := p.ReadSessionInfo(transcriptPath)
	if err == nil && info.CWD != "" {
		return info.CWD
	}
	return filepath.Dir(transcriptPath)
}
