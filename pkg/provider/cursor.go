package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/hookconfig"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// CursorStateDirEnv overrides the default Cursor state directory (~/.cursor)
// for tests and non-standard installs.
const CursorStateDirEnv = "CONFAB_CURSOR_DIR"

// Cursor contains Cursor-specific local behavior (cursor-agent CLI + Cursor
// desktop IDE). This T2 scope covers the core provider: registration, types,
// path derivation, and process matching. Hook installation, daemon wiring,
// transcript parsing, and subagent capture are fleshed out in T3–T6.
type Cursor struct{}

var _ Provider = Cursor{}

// Name returns the canonical Cursor provider name.
func (Cursor) Name() string { return NameCursor }

// CLIBinaryName returns "cursor-agent" — the CLI binary users install.
func (Cursor) CLIBinaryName() string { return "cursor-agent" }

// SupportsCommitLinking reports false: commit/PR linking is deferred for
// Cursor (matches OpenCode). No PreToolUse/PostToolUse equivalent is installed.
func (Cursor) SupportsCommitLinking() bool { return false }

// StateDir returns the Cursor config/install directory. Precedence:
// CONFAB_CURSOR_DIR > the default ~/.cursor.
func (Cursor) StateDir() (string, error) {
	if envDir := os.Getenv(CursorStateDirEnv); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".cursor"), nil
}

// ProjectsDir returns <stateDir>/projects, the root under which Cursor stores
// per-workspace transcripts.
func (p Cursor) ProjectsDir() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", fmt.Errorf("failed to get cursor state directory: %w", err)
	}
	return filepath.Join(stateDir, "projects"), nil
}

var cursorNonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// sanitizeWorkspaceRoot mirrors the cursor-agent bundle's path sanitizer used
// to map a workspace root to its on-disk projects subdirectory: replace every
// run of non-alphanumeric characters with a single hyphen and strip leading /
// trailing hyphens. Verified against real dirs (kata 6kys): "/Users/jackie/dev/
// confab" → "Users-jackie-dev-confab".
func sanitizeWorkspaceRoot(root string) string {
	hyphenated := cursorNonAlnum.ReplaceAllString(root, "-")
	return strings.Trim(hyphenated, "-")
}

// deriveTranscriptPath computes the transcript path for a Cursor session at
// sessionStart, where the payload's transcript_path is null. Layout (verified
// kata 6kys):
//
//	<stateDir>/projects/<sanitize(workspace_roots[0])>/agent-transcripts/<id>/<id>.jsonl
//
// Returns "" when inputs are insufficient (no session id or no workspace root),
// leaving the caller to fall back to whatever the payload carried.
func (p Cursor) deriveTranscriptPath(sessionID string, workspaceRoots []string) string {
	if sessionID == "" || len(workspaceRoots) == 0 || workspaceRoots[0] == "" {
		return ""
	}
	projectsDir, err := p.ProjectsDir()
	if err != nil {
		return ""
	}
	root := sanitizeWorkspaceRoot(workspaceRoots[0])
	return filepath.Join(projectsDir, root, "agent-transcripts", sessionID, sessionID+".jsonl")
}

// ParseSessionHook reads a Cursor sessionStart-style hook payload and returns
// the provider-agnostic view. Because transcript_path is null at sessionStart,
// it derives the path from workspace_roots[0] + session_id so the standard
// launch path (which reads HookInput.TranscriptPath()) works unchanged. A
// payload that already carries an explicit transcript_path (e.g. sessionEnd)
// keeps it.
func (p Cursor) ParseSessionHook(r io.Reader) (HookInput, error) {
	in, err := p.ReadHookInput(r)
	if err != nil {
		return nil, err
	}
	if in.TranscriptPath == "" {
		in.TranscriptPath = p.deriveTranscriptPath(in.SessionID, in.WorkspaceRoots)
	}
	return cursorHookInputAdapter{inner: in}, nil
}

// ReadHookInput reads and validates raw Cursor hook JSON: session_id required
// and safe; transcript_path NOT required (null at sessionStart, derived by
// ParseSessionHook). This is the non-strict reader used on the spawn path.
func (Cursor) ReadHookInput(r io.Reader) (*types.CursorHookInput, error) {
	return types.ReadCursorHookInput(r)
}

// ReadSessionHookInput reads Cursor session hook JSON and additionally requires
// + validates transcript_path, mirroring ClaudeCode.ReadSessionHookInput. Used
// where a populated transcript_path is required (e.g. sessionEnd / offline
// flows); the spawn path uses ReadHookInput + path derivation instead.
func (p Cursor) ReadSessionHookInput(r io.Reader) (*types.CursorHookInput, error) {
	input, err := p.ReadHookInput(r)
	if err != nil {
		return nil, err
	}
	if input.TranscriptPath == "" {
		return nil, fmt.Errorf("transcript_path is required")
	}
	if err := p.ValidateTranscriptPath(input.TranscriptPath); err != nil {
		return nil, fmt.Errorf("invalid transcript_path: %w", err)
	}
	return input, nil
}

// ValidateTranscriptPath checks that a Cursor transcript path is safe, mirroring
// ClaudeCode.ValidateTranscriptPath: absolute, no ".." components, and under the
// Cursor projects directory.
func (p Cursor) ValidateTranscriptPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("must be an absolute path")
	}
	cleaned := filepath.Clean(path)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return fmt.Errorf("must not contain '..' components")
		}
	}
	projectsDir, err := p.ProjectsDir()
	if err != nil {
		return err
	}
	if pathIsUnderAnyRoot(cleaned, []string{projectsDir}) {
		return nil
	}
	return fmt.Errorf("must be under Cursor projects directory (%s)", projectsDir)
}

// WalkUpToRoot is the identity walk for Cursor: subagents fire their own
// dedicated subagentStart/Stop hooks (never sessionStart), so the firing
// session is always its own root and rootPath is "".
func (Cursor) WalkUpToRoot(sessionID string) (string, string, error) {
	return sessionID, "", nil
}

// ShouldSpawnForInput is unconditional for Cursor: subagents never fire
// sessionStart, so there is no duplicate-daemon risk to suppress (kata 6kys).
func (Cursor) ShouldSpawnForInput(HookInput) bool { return true }

// WriteHookResponse writes an empty JSON object ({}) and relies on exit 0.
// Cursor's sessionStart response schema is {env?, additional_context?} and is
// fire-and-forget; confab injects no context, so the Claude/Codex response
// shape (and systemMessage) is intentionally NOT emitted. suppressOutput and
// systemMessage are ignored.
func (Cursor) WriteHookResponse(w io.Writer, _ bool, _ string) error {
	_, err := io.WriteString(w, "{}\n")
	return err
}

// InitTranscript is a no-op for Cursor — no root rollout metadata to attach.
func (Cursor) InitTranscript(TranscriptRegistrar, string, string) error { return nil }

// OnAlreadyRunning is a no-op for Cursor: subagents never fire sessionStart,
// so an already-running hit is ordinary hook dedup, not an error.
func (Cursor) OnAlreadyRunning(string) {}

// DefaultCWD returns filepath.Dir(transcriptPath). Cursor's transcript lives at
// .../agent-transcripts/<id>/<id>.jsonl, so this is the per-session directory.
func (Cursor) DefaultCWD(transcriptPath string) string {
	return filepath.Dir(transcriptPath)
}

// DiscoverDescendants discovers Cursor subagent transcripts — see
// cursor_subagents.go for the implementation and layout notes.

// DiscoverWorkflowFiles is a no-op: Cursor has no Workflow-tool equivalent.
func (Cursor) DiscoverWorkflowFiles(WorkflowRegistrar, func(string) bool) (int, error) {
	return 0, nil
}

// AnnotateChunk annotates transcript chunks with the session metadata the
// backend needs for Cursor (spm9). On every transcript chunk it sets:
//
//   - first_user_message — so the session is listable (the backend hides
//     sessions with neither a summary nor a first_user_message; kata kk5t).
//   - latest_message_at — from the transcript file's mtime. Cursor JSONL lines
//     carry NO per-line timestamp, so the backend opts Cursor out of per-line
//     extraction and feeds session.last_message_at SOLELY from this field. The
//     mtime is universal across the CLI and IDE.
//   - summary — from the CLI meta.json title when present (CLI-only; absent for
//     IDE sessions, which keep first_user_message alone). Best-effort.
//
// Non-transcript (agent sidechain) chunks are a no-op. IncludedFirstUserMessage
// stays false so the engine's sentFirstUserMessage flag is untouched (same as
// Claude). The model is set engine-side from the daemon config (sourced from
// the sessionStart hook), not here. Every read here is best-effort: a missing
// file or meta.json never errors the chunk.
func (p Cursor) AnnotateChunk(c ChunkView, _ bool, redact func(string) string) AnnotationResult {
	if c.FileType() != FileTypeTranscript {
		return AnnotationResult{}
	}
	firstUserMessage := p.ExtractMetadata(c.Lines()).FirstUserMessage
	if redact != nil {
		firstUserMessage = redact(firstUserMessage)
	}
	c.SetFirstUserMessage(firstUserMessage)

	path := c.FilePath()

	// latest_message_at from the transcript file mtime (the only Cursor recency
	// signal). Normalized to UTC — os.Stat's ModTime() is Local-zoned and the
	// backend applies this value as-is (it trusts providers to send UTC, like
	// OpenCode's explicit .UTC() and Claude/Codex's UTC-Z timestamps); without
	// .UTC() the web list recency is off by the host's tz offset (kata 1zjr).
	// Stat failures (missing/empty path) leave it unset.
	if path != "" {
		if info, err := os.Stat(path); err == nil {
			c.SetLatestMessageAt(info.ModTime().UTC())
		}
	}

	// summary from the CLI meta.json title (best-effort; CLI-only).
	if title := p.metaJSONTitle(path); title != "" {
		if redact != nil {
			title = redact(title)
		}
		c.SetSummary(title)
	}

	return AnnotationResult{}
}

// cursorMetaJSON is the read contract for ~/.cursor/chats/<hash>/<id>/meta.json,
// written by the cursor-agent CLI (absent for IDE sessions). Verified on disk:
// {schemaVersion, createdAtMs, hasConversation, title?, updatedAtMs}. Only the
// title is consumed here; title is optional (CLI omits it for untitled chats).
type cursorMetaJSON struct {
	Title string `json:"title"`
}

// metaJSONTitle returns the CLI meta.json title for the session whose transcript
// lives at transcriptPath, or "" when the file or title is absent. The session
// id is the transcript's basename (sans .jsonl); meta.json lives at a sibling
// tree ~/.cursor/chats/<hash>/<id>/meta.json, so the <hash> is resolved by glob.
// Entirely best-effort: any error yields "" (no summary), never an error.
func (p Cursor) metaJSONTitle(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	sessionID := strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl")
	if sessionID == "" {
		return ""
	}
	stateDir, err := p.StateDir()
	if err != nil {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(stateDir, "chats", "*", sessionID, "meta.json"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return ""
	}
	var meta cursorMetaJSON
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	if meta.Title == "" {
		return ""
	}
	return TruncateUTF8(sanitizeText(meta.Title), types.MaxMetadataFieldLength/2)
}

// ExtractMetadata parses the first user message from in-memory Cursor
// transcript lines. Cursor's JSONL message lines are {role, message:{content}}
// with NO top-level "type" field; status lines are {type:"turn_ended", …} and
// carry no role (kata 6kys §7). FirstUserMessage is the first role=="user"
// line's first text part, with the <user_query>…</user_query> wrapper stripped.
// Summary stays empty (Cursor has no inline summary) and SummaryLinks stays nil
// (Cursor has none). Lines beyond maxLinesForExtraction are ignored.
func (Cursor) ExtractMetadata(lines []string) SessionMetadata {
	if len(lines) > maxLinesForExtraction {
		lines = lines[:maxLinesForExtraction]
	}
	return extractCursorMetadata(lines)
}

// cursorUserQueryRe strips the <user_query>…</user_query> sentinel Cursor wraps
// around user prompts. It is non-greedy and matches across newlines so the
// inner text is recovered intact.
var cursorUserQueryRe = regexp.MustCompile(`(?s)^\s*<user_query>(.*?)</user_query>\s*$`)

// extractCursorMetadata is the in-memory extraction primitive shared by
// ExtractMetadata (chunk-time) and the scan-time file reader.
func extractCursorMetadata(lines []string) SessionMetadata {
	var result SessionMetadata
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry struct {
			Role    string `json:"role"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Role != "user" {
			continue // skip assistant lines and status lines (turn_ended/error)
		}
		for _, part := range entry.Message.Content {
			if part.Type == "text" && part.Text != "" {
				text := stripCursorUserQuery(part.Text)
				result.FirstUserMessage = TruncateUTF8(sanitizeText(text), types.MaxMetadataFieldLength/2)
				return result
			}
		}
	}
	return result
}

// stripCursorUserQuery removes the <user_query>…</user_query> wrapper Cursor
// adds to user prompts, returning the inner text. Text without the wrapper is
// returned unchanged.
func stripCursorUserQuery(text string) string {
	if m := cursorUserQueryRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return text
}

// ScanSessions walks <stateDir>/projects/*/agent-transcripts/*/<id>.jsonl and
// returns all user sessions sorted oldest first. Subagent sidechain files
// (nested under .../subagents/) are excluded — only the top-level transcript
// whose basename equals its parent directory name is a session. Cursor writes
// real transcript files, so offline `confab save <id>` is supported (unlike
// OpenCode, which has no on-disk file). Permission errors per path are reported
// to stderr and do not fail the scan.
func (p Cursor) ScanSessions() ([]SessionInfo, error) {
	projectsDir, err := p.ProjectsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get projects directory: %w", err)
	}
	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var sessions []SessionInfo
	var skippedPaths []string
	err = filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			logger.Warn("Failed to access path during scan: %s: %v", path, walkErr)
			skippedPaths = append(skippedPaths, path)
			return nil
		}
		if session := parseCursorSessionFromPath(path, d); session != nil {
			sessions = append(sessions, *session)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk projects directory: %w", err)
	}
	reportSkippedPaths(skippedPaths, "scan")

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.Before(sessions[j].ModTime)
	})
	return sessions, nil
}

// FindSessionByID resolves a full or partial Cursor session ID to its full ID
// and transcript path (prefix match, mirroring the other providers). Walk-up is
// identity for Cursor (subagents fire their own hooks). Returns an error on
// no-match or ambiguous prefix.
func (p Cursor) FindSessionByID(partialID string) (string, string, error) {
	projectsDir, err := p.ProjectsDir()
	if err != nil {
		return "", "", err
	}

	var matches []SessionInfo
	var skippedPaths []string
	err = filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			logger.Warn("Failed to access path during search: %s: %v", path, walkErr)
			skippedPaths = append(skippedPaths, path)
			return nil
		}
		session := parseCursorSessionFromPath(path, d)
		if session != nil && strings.HasPrefix(session.SessionID, partialID) {
			matches = append(matches, *session)
		}
		return nil
	})
	if err != nil {
		logger.Warn("Failed to walk projects directory: %v", err)
	}
	reportSkippedPaths(skippedPaths, "search")

	if len(matches) == 0 {
		return "", "", fmt.Errorf("session not found: %s", partialID)
	}
	if len(matches) > 1 {
		return "", "", fmt.Errorf("ambiguous session ID '%s' matches %d sessions", partialID, len(matches))
	}
	return matches[0].SessionID, matches[0].TranscriptPath, nil
}

// parseCursorSessionFromPath checks whether a path is a top-level Cursor
// session transcript and, if so, returns its SessionInfo with the first user
// message extracted. A session transcript lives at
// .../agent-transcripts/<id>/<id>.jsonl, so the file basename (sans .jsonl)
// must equal its parent directory name. This excludes subagent files (nested
// under .../subagents/, whose parent dir is "subagents") and any stray files.
func parseCursorSessionFromPath(path string, d os.DirEntry) *SessionInfo {
	if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
		return nil
	}
	sessionID := strings.TrimSuffix(d.Name(), ".jsonl")
	if filepath.Base(filepath.Dir(path)) != sessionID {
		return nil
	}
	info, err := d.Info()
	if err != nil {
		return nil
	}
	meta := extractCursorSessionMetadataFromFile(path)
	return &SessionInfo{
		SessionID:        sessionID,
		TranscriptPath:   path,
		ModTime:          info.ModTime(),
		SizeBytes:        info.Size(),
		FirstUserMessage: meta.FirstUserMessage,
	}
}

// extractCursorSessionMetadataFromFile reads the head of a transcript file and
// returns its first user message. Open failures degrade silently (empty
// metadata); scan errors log a warning but whatever was read is still parsed.
func extractCursorSessionMetadataFromFile(transcriptPath string) SessionMetadata {
	lines, err := readHeadLines(transcriptPath)
	if err != nil && lines != nil {
		logger.Warn("Error reading transcript %s during metadata extraction: %v", transcriptPath, err)
	}
	return extractCursorMetadata(lines)
}

// hooksPath returns <stateDir>/hooks.json — the user-level Cursor hooks file
// (honors CONFAB_CURSOR_DIR). This covers the local IDE + CLI; cloud agents
// don't read it and are out of scope (kata 6kys).
func (p Cursor) hooksPath() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "hooks.json"), nil
}

// InstallHooks installs Confab's sessionStart (daemon spawn) and sessionEnd
// (signal shutdown) hooks into ~/.cursor/hooks.json, preserving user hooks.
// preToolUse/postToolUse are intentionally not installed (commit/PR linking
// is deferred for Cursor). Returns the written hooks.json path.
func (p Cursor) InstallHooks() (string, error) {
	path, err := p.hooksPath()
	if err != nil {
		return "", err
	}
	return hookconfig.InstallCursorHooks(path)
}

// UninstallHooks removes Confab's managed hook entries from
// ~/.cursor/hooks.json, preserving any user-authored hooks.
func (p Cursor) UninstallHooks() (string, error) {
	path, err := p.hooksPath()
	if err != nil {
		return "", err
	}
	return hookconfig.UninstallCursorHooks(path)
}

// IsHooksInstalled reports true only when both managed Cursor hook events
// (sessionStart + sessionEnd) carry a confab command.
func (p Cursor) IsHooksInstalled() (bool, error) {
	path, err := p.hooksPath()
	if err != nil {
		return false, err
	}
	return hookconfig.IsCursorHooksInstalled(path)
}

// InstallSkills installs confab's bundled skills (/retro) into ~/.cursor/skills/.
// Cursor auto-loads global skills from <stateDir>/skills/ using the generic
// SKILL.md template (kata 6kys addendum 2), so the existing reconcile path
// works unchanged.
func (p Cursor) InstallSkills() error {
	stateDir, err := p.StateDir()
	if err != nil {
		return err
	}
	return config.ReconcileBundledSkills(stateDir, config.SkillProviderCursor)
}

// UninstallSkills removes confab's bundled skills from ~/.cursor/skills/.
func (p Cursor) UninstallSkills() error {
	stateDir, err := p.StateDir()
	if err != nil {
		return err
	}
	return config.UninstallBundledSkills(stateDir)
}

// IsSkillInstalled reports whether a shipped Cursor skill exists.
func (p Cursor) IsSkillInstalled(name string) bool {
	stateDir, err := p.StateDir()
	if err != nil {
		return false
	}
	return config.IsBundledSkillInstalled(stateDir, name)
}

// FindParentPID walks up the process tree to find the Cursor process (CLI or
// IDE), mirroring ClaudeCode.FindParentPID. The IDE app process can be the
// grandparent of the hook (the hook is spawned by a "Cursor Helper (Plugin)"
// child of /Applications/Cursor.app), so the parent+grandparent walk suffices.
func (p Cursor) FindParentPID() int {
	parentPID := os.Getppid()
	if p.IsProcess(parentPID) {
		return parentPID
	}
	grandparentPID := getParentPID(parentPID)
	if grandparentPID > 0 && p.IsProcess(grandparentPID) {
		return grandparentPID
	}
	return 0
}

// IsProcess reports whether pid is a Cursor process (CLI or IDE).
func (p Cursor) IsProcess(pid int) bool {
	cmd := getProcCmdline(pid)
	return p.MatchesProcess(cmd)
}

// cursorProcessPattern matches both the cursor-agent CLI (whose bundle path
// always contains "cursor-agent", even when invoked via the "agent" symlink)
// and the Cursor desktop IDE ("/Applications/Cursor.app/...", "Cursor Helper
// ..."), while NOT matching lowercase "~/.cursor/" path references (verified
// kata 6kys). The capitalized "Cursor" alternatives are case-sensitive on
// purpose so a "/Users/x/.cursor/foo.sh" hook command is not a false match.
var cursorProcessPattern = regexp.MustCompile(`cursor-agent|Cursor\.app|Cursor Helper`)

// MatchesProcess reports whether a command string matches a Cursor process.
func (Cursor) MatchesProcess(cmd string) bool {
	return cursorProcessPattern.MatchString(cmd)
}
