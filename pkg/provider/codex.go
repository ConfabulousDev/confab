package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ConfabulousDev/confab/pkg/hookconfig"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

const CodexStateDirEnv = "CONFAB_CODEX_DIR"

type Codex struct{}

var _ Provider = Codex{}

// Name returns the canonical Codex provider name.
func (Codex) Name() string { return NameCodex }

// ParseSessionHook reads a Codex SessionStart hook payload and returns
// the provider-agnostic view.
func (p Codex) ParseSessionHook(r io.Reader) (HookInput, error) {
	in, err := p.ReadSessionHookInput(r)
	if err != nil {
		return nil, err
	}
	return codexHookInputAdapter{inner: in}, nil
}

// ShouldSpawnForInput inspects the rollout file's session_meta to
// decide whether the firing hook represents a user-initiated rollout
// (true) or a subagent (false).
func (p Codex) ShouldSpawnForInput(in HookInput) bool {
	info, err := p.ReadSessionInfo(in.TranscriptPath())
	if err != nil {
		// Codex SessionStart can fire before Codex finishes writing the
		// rollout file (~5–50ms race on a fresh session). For os.IsNotExist
		// we err toward over-spawning rather than missing a user session;
		// the daemon's per-cycle DiscoverCodexDescendants catches the rest.
		// Other errors (permission, malformed JSON) signal a real problem,
		// so refuse — matches the pre-CF-396 behavior.
		if os.IsNotExist(err) {
			return true
		}
		logger.Warn("Codex ShouldSpawnForInput: failed to inspect rollout %s: %v", in.TranscriptPath(), err)
		return false
	}
	return info.IsUserSession()
}

// InstallSkills is a no-op for Codex (no skill files shipped).
func (Codex) InstallSkills() error { return nil }

// WriteHookResponse writes a CodexHookResponse to w.
func (Codex) WriteHookResponse(w io.Writer, suppressOutput bool, systemMessage string) error {
	return json.NewEncoder(w).Encode(types.CodexHookResponse{
		Continue:       true,
		SuppressOutput: suppressOutput,
		SystemMessage:  systemMessage,
	})
}

// IsHooksInstalled delegates to pkg/hookconfig, which parses
// ~/.codex/config.toml and returns true iff a confab command is
// registered under [[hooks.SessionStart]].
func (p Codex) IsHooksInstalled() (bool, error) {
	configPath, err := p.ConfigPath()
	if err != nil {
		return false, err
	}
	return hookconfig.IsCodexHooksInstalled(configPath)
}

// FindParentPID walks up the process tree to find the Codex process.
// Mirrors ClaudeCode.FindParentPID for daemon parent-liveness monitoring.
func (p Codex) FindParentPID() int {
	parentPID := os.Getppid()
	if p.IsProcess(parentPID) {
		return parentPID
	}

	grandparentPID := getParentPID(parentPID)
	if grandparentPID > 0 && p.IsProcess(grandparentPID) {
		return grandparentPID
	}

	logger.Warn("Could not find Codex in process tree, disabling parent PID monitoring")
	return 0
}

// IsProcess checks if the given PID is a Codex process.
func (p Codex) IsProcess(pid int) bool {
	cmd := getProcCmdline(pid)
	return p.MatchesProcess(cmd)
}

var codexProcessPattern = regexp.MustCompile(`(?i)\bcodex\b`)

// MatchesProcess checks if a command string matches a Codex invocation.
func (Codex) MatchesProcess(cmd string) bool {
	return codexProcessPattern.MatchString(cmd)
}

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
	Source         string
	ThreadSource   string
	AgentPath      string
	AgentRole      string
	AgentNickname  string
	ModTime        time.Time
	SizeBytes      int64
	FirstUserInput string
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

type codexUserMessagePayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

var codexRolloutPattern = regexp.MustCompile(`^rollout-.+-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

func (Codex) StateDir() (string, error) {
	if envDir := os.Getenv(CodexStateDirEnv); envDir != "" {
		return envDir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(home, ".codex"), nil
}

func (p Codex) SessionsDir() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "sessions"), nil
}

func (p Codex) ConfigPath() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "config.toml"), nil
}

func (Codex) SessionIDFromRolloutPath(path string) (string, bool) {
	matches := codexRolloutPattern.FindStringSubmatch(filepath.Base(path))
	if matches == nil {
		return "", false
	}
	return matches[1], true
}

func (p Codex) ScanSessions() ([]CodexSessionInfo, error) {
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

func (p Codex) FindSessionByID(partialID string) (string, string, error) {
	id, path, err := p.findRolloutByID(partialID, true)
	return id, path, err
}

// FindRolloutByID is like FindSessionByID but accepts subagent rollouts as
// well as user-initiated ones. Callers that want to support `confab save
// <subagent-uuid>` (then transparently walk up to the root) should use this.
//
// The returned id + path refer to the rollout the partial ID resolved to;
// they are NOT walked up to the root. Use WalkUpToRoot on the result if
// you want the top-most user session.
func (p Codex) FindRolloutByID(partialID string) (string, string, error) {
	return p.findRolloutByID(partialID, false)
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
		if sessionID == partialID || strings.HasPrefix(sessionID, partialID) {
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
		info.AgentRole = meta.AgentRole
		info.AgentNickname = meta.AgentNickname
		return info, nil
	}
	if err := scanner.Err(); err != nil {
		return info, fmt.Errorf("failed to scan Codex rollout: %w", err)
	}
	return info, nil
}

// ExtractFirstUserMessageFromLines returns the first non-empty user message
// found in the given rollout lines, truncated to MaxFirstUserMessageLength
// bytes on a UTF-8 boundary. Returns "" when no user message is present.
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
		if payload.Type != "user_message" {
			continue
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			continue
		}
		return truncateUTF8Bytes(message, types.MaxFirstUserMessageLength)
	}
	return ""
}

func (s CodexSessionInfo) IsUserSession() bool {
	if s.ThreadSource != "" && s.ThreadSource != "user" {
		return false
	}
	return s.AgentPath == "" && s.AgentRole == "" && s.AgentNickname == ""
}

func (p Codex) ValidateRolloutPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("must be an absolute path")
	}
	if _, ok := p.SessionIDFromRolloutPath(path); !ok {
		return fmt.Errorf("must be a Codex rollout JSONL file")
	}

	sessionsDir, err := p.SessionsDir()
	if err != nil {
		return err
	}

	cleaned := filepath.Clean(path)
	parentDir := filepath.Dir(cleaned)
	resolvedParent, parentErr := filepath.EvalSymlinks(parentDir)
	resolvedPath := ""
	if parentErr == nil {
		resolvedPath = filepath.Join(resolvedParent, filepath.Base(cleaned))
	}

	cleanRoot := filepath.Clean(sessionsDir)
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		resolvedRoot = cleanRoot
	}
	if parentErr == nil {
		if strings.HasPrefix(resolvedPath, resolvedRoot+string(filepath.Separator)) {
			return nil
		}
	} else if strings.HasPrefix(cleaned, cleanRoot+string(filepath.Separator)) {
		return nil
	}

	return fmt.Errorf("must be under Codex sessions directory (%s)", sessionsDir)
}

// truncateUTF8Bytes returns s truncated so its byte length is at most maxBytes,
// without splitting a multi-byte rune. Returns an empty string when maxBytes is
// non-positive.
func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Walk runes in order; stop at the first rune that wouldn't fit.
	for i, r := range s {
		if i+utf8.RuneLen(r) > maxBytes {
			return s[:i]
		}
	}
	// Defensive: unreachable for valid UTF-8 (the loop above always returns
	// before completion when len(s) > maxBytes). For invalid bytes, fall back
	// to a hard byte cut so the byte-limit invariant still holds.
	return s[:maxBytes]
}

func (p Codex) ReadHookInput(r io.Reader) (*types.CodexHookInput, error) {
	data, err := io.ReadAll(io.LimitReader(r, types.MaxJSONLLineSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}
	var input types.CodexHookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("failed to parse hook input: %w", err)
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if err := types.ValidateSessionID(input.SessionID); err != nil {
		return nil, err
	}
	return &input, nil
}

func (p Codex) ReadSessionHookInput(r io.Reader) (*types.CodexHookInput, error) {
	input, err := p.ReadHookInput(r)
	if err != nil {
		return nil, err
	}
	if input.TranscriptPath == "" {
		return nil, fmt.Errorf("transcript_path is required")
	}
	if err := p.ValidateRolloutPath(input.TranscriptPath); err != nil {
		return nil, fmt.Errorf("invalid transcript_path: %w", err)
	}
	return input, nil
}

// InstallHooks delegates to pkg/hookconfig.
func (p Codex) InstallHooks() (string, error) {
	configPath, err := p.ConfigPath()
	if err != nil {
		return "", err
	}
	return hookconfig.InstallCodexHooks(configPath)
}

// UninstallHooks delegates to pkg/hookconfig.
func (p Codex) UninstallHooks() (string, error) {
	configPath, err := p.ConfigPath()
	if err != nil {
		return "", err
	}
	return hookconfig.UninstallCodexHooks(configPath)
}
