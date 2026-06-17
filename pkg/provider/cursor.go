package provider

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
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
	in, err := p.ReadSessionHookInput(r)
	if err != nil {
		return nil, err
	}
	if in.TranscriptPath == "" {
		in.TranscriptPath = p.deriveTranscriptPath(in.SessionID, in.WorkspaceRoots)
	}
	return cursorHookInputAdapter{inner: in}, nil
}

// ReadSessionHookInput reads and validates Cursor hook JSON. Unlike Claude it
// does not require transcript_path (null at sessionStart — derived later).
func (Cursor) ReadSessionHookInput(r io.Reader) (*types.CursorHookInput, error) {
	return types.ReadCursorHookInput(r)
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

// DiscoverDescendants is a stub for T2 (subagent sidechain capture is T6).
func (Cursor) DiscoverDescendants(DescendantRegistrar, string) error { return nil }

// DiscoverWorkflowFiles is a no-op: Cursor has no Workflow-tool equivalent.
func (Cursor) DiscoverWorkflowFiles(WorkflowRegistrar, func(string) bool) (int, error) {
	return 0, nil
}

// AnnotateChunk is a stub for T2 (metadata extraction is fleshed out in T3).
func (Cursor) AnnotateChunk(ChunkView, bool, func(string) string) AnnotationResult {
	return AnnotationResult{}
}

// ExtractMetadata is a stub for T2 (transcript parsing is T3).
func (Cursor) ExtractMetadata([]string) SessionMetadata {
	return SessionMetadata{}
}

// ScanSessions is unsupported for Cursor: live capture happens via the sync
// daemon, and offline manual mode is deferred.
func (Cursor) ScanSessions() ([]SessionInfo, error) {
	return nil, fmt.Errorf("cursor: manual session scan not supported (sessions sync live via the daemon; offline manual mode is not yet implemented)")
}

// FindSessionByID is unsupported for Cursor for the same reason as ScanSessions.
func (Cursor) FindSessionByID(string) (string, string, error) {
	return "", "", fmt.Errorf("cursor: manual session lookup not supported (sessions sync live via the daemon; offline manual mode is not yet implemented)")
}

// InstallHooks is not yet implemented (T4 installs the ~/.cursor/hooks.json
// bundle). Returning an error keeps `confab setup --provider cursor` from
// silently appearing to succeed before the installer lands.
func (Cursor) InstallHooks() (string, error) {
	return "", fmt.Errorf("cursor: hook installation not yet implemented")
}

// UninstallHooks is not yet implemented (T4).
func (Cursor) UninstallHooks() (string, error) {
	return "", fmt.Errorf("cursor: hook uninstallation not yet implemented")
}

// IsHooksInstalled reports false until T4 wires hook installation.
func (Cursor) IsHooksInstalled() (bool, error) { return false, nil }

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
