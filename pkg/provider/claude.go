package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/hookconfig"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// ClaudeStateDirEnv is the environment variable to override the default Claude state directory.
const ClaudeStateDirEnv = "CONFAB_CLAUDE_DIR"

// ClaudeCode contains Claude Code-specific local behavior.
type ClaudeCode struct{}

var _ Provider = ClaudeCode{}

// Name returns the canonical Claude Code provider name.
func (ClaudeCode) Name() string { return NameClaudeCode }

// CLIBinaryName returns "claude" — the binary `claude` users install via
// the Claude Code installer.
func (ClaudeCode) CLIBinaryName() string { return "claude" }

// SupportsCommitLinking reports that Claude Code installs PreToolUse +
// PostToolUse hooks that drive bidirectional GitHub linking.
func (ClaudeCode) SupportsCommitLinking() bool { return true }

// ParseSessionHook reads a Claude SessionStart hook payload and returns
// the provider-agnostic view.
func (p ClaudeCode) ParseSessionHook(r io.Reader) (HookInput, error) {
	in, err := p.ReadSessionHookInput(r)
	if err != nil {
		return nil, err
	}
	return claudeHookInputAdapter{inner: in}, nil
}

// WalkUpToRoot is the identity walk for Claude Code: there is no thread
// tree, so the firing session is always its own root and rootPath is "".
func (ClaudeCode) WalkUpToRoot(sessionID string) (string, string, error) {
	return sessionID, "", nil
}

// ShouldSpawnForInput is unconditional for Claude Code.
func (ClaudeCode) ShouldSpawnForInput(HookInput) bool { return true }

// InstallHooks installs all four Confab hook bundles (sync, PreToolUse,
// PostToolUse, UserPromptSubmit). Returns the settings.json path.
func (p ClaudeCode) InstallHooks() (string, error) {
	installers := []func() error{
		hookconfig.InstallSyncHooks,
		hookconfig.InstallPreToolUseHooks,
		hookconfig.InstallPostToolUseHooks,
		hookconfig.InstallUserPromptSubmitHook,
	}
	for _, install := range installers {
		if err := install(); err != nil {
			return "", err
		}
	}
	return p.SettingsPath()
}

// UninstallHooks removes all four Confab hook bundles. Returns the
// settings.json path even if no hooks were present.
func (p ClaudeCode) UninstallHooks() (string, error) {
	uninstallers := []func() error{
		hookconfig.UninstallSyncHooks,
		hookconfig.UninstallPreToolUseHooks,
		hookconfig.UninstallPostToolUseHooks,
		hookconfig.UninstallUserPromptSubmitHook,
	}
	for _, uninstall := range uninstallers {
		if err := uninstall(); err != nil {
			return "", err
		}
	}
	return p.SettingsPath()
}

// InstallSkills installs the Claude Code skills shipped with confab
// (/til and /retro).
func (p ClaudeCode) InstallSkills() error {
	stateDir, err := p.StateDir()
	if err != nil {
		return err
	}
	return config.InstallBundledSkills(stateDir, config.SkillProviderClaude)
}

// UninstallSkills removes the Claude Code skills shipped with confab.
func (p ClaudeCode) UninstallSkills() error {
	stateDir, err := p.StateDir()
	if err != nil {
		return err
	}
	return config.UninstallBundledSkills(stateDir)
}

// IsSkillInstalled reports whether a shipped Claude Code skill exists.
func (p ClaudeCode) IsSkillInstalled(name string) bool {
	stateDir, err := p.StateDir()
	if err != nil {
		return false
	}
	return config.IsBundledSkillInstalled(stateDir, name)
}

// WriteHookResponse writes a ClaudeHookResponse to w.
func (ClaudeCode) WriteHookResponse(w io.Writer, suppressOutput bool, systemMessage string) error {
	return json.NewEncoder(w).Encode(types.ClaudeHookResponse{
		Continue:       true,
		SuppressOutput: suppressOutput,
		SystemMessage:  systemMessage,
	})
}

// InitTranscript is a no-op for Claude Code — there is no root rollout
// metadata to attach (Codex-only concern).
func (ClaudeCode) InitTranscript(TranscriptRegistrar, string, string) error { return nil }

// DiscoverDescendants is a no-op for Claude Code. Claude's agent files are
// discovered transitively from transcript content (agent IDs embedded in
// JSONL messages) inside tracker.DiscoverNewFiles — no external state DB
// lookup is required.
func (ClaudeCode) DiscoverDescendants(DescendantRegistrar, string) error { return nil }

// AnnotateChunk extracts the local summary, first user message, and
// summary-link records from a Claude Code transcript chunk. Summary links
// are returned via AnnotationResult.SummaryLinks so the engine can perform
// the backend HTTP after AnnotateChunk returns — keeping the provider
// side-effect-free.
//
// Non-transcript files are a no-op (Claude extracts only from transcripts).
//
// Claude does not gate first-user-message extraction on a "first time"
// flag — the discovery helper handles dedup internally and the engine
// historically does not flip sentFirstUserMessage for Claude. The returned
// IncludedFirstUserMessage stays false so the engine's flag is untouched.
func (p ClaudeCode) AnnotateChunk(c ChunkView, _ bool, redact func(string) string) AnnotationResult {
	if c.FileType() != "transcript" {
		return AnnotationResult{}
	}
	meta := p.ExtractMetadata(c.Lines())
	summary := meta.Summary
	firstUserMessage := meta.FirstUserMessage
	if redact != nil {
		summary = redact(summary)
		firstUserMessage = redact(firstUserMessage)
	}
	c.SetSummary(summary)
	c.SetFirstUserMessage(firstUserMessage)

	return AnnotationResult{SummaryLinks: meta.SummaryLinks}
}

// DefaultCWD returns filepath.Dir(transcriptPath); Claude has no richer
// per-session CWD source.
func (p ClaudeCode) DefaultCWD(transcriptPath string) string {
	return filepath.Dir(transcriptPath)
}

// IsHooksInstalled reports whether all four Confab hook bundles for
// Claude Code are installed. Mirrors InstallHooks: true only when every
// bundle is present.
func (ClaudeCode) IsHooksInstalled() (bool, error) {
	checks := []func() (bool, error){
		hookconfig.IsSyncHooksInstalled,
		hookconfig.IsPreToolUseHooksInstalled,
		hookconfig.IsPostToolUseHooksInstalled,
		hookconfig.IsUserPromptSubmitHookInstalled,
	}
	for _, check := range checks {
		ok, err := check()
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// StateDir returns the Claude state directory.
// Defaults to ~/.claude but can be overridden with CONFAB_CLAUDE_DIR.
func (ClaudeCode) StateDir() (string, error) {
	if envDir := os.Getenv(ClaudeStateDirEnv); envDir != "" {
		return envDir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(home, ".claude"), nil
}

// ProjectsDir returns the Claude projects directory.
func (p ClaudeCode) ProjectsDir() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", fmt.Errorf("failed to get claude state directory: %w", err)
	}
	return filepath.Join(stateDir, "projects"), nil
}

// SettingsPath returns the Claude settings file path.
func (p ClaudeCode) SettingsPath() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", fmt.Errorf("failed to get claude state directory: %w", err)
	}
	return filepath.Join(stateDir, "settings.json"), nil
}

// ReadHookInput reads and validates Claude hook JSON.
func (ClaudeCode) ReadHookInput(r io.Reader) (*types.ClaudeHookInput, error) {
	return types.ReadClaudeHookInput(r)
}

// ReadSessionHookInput reads Claude session hook JSON and validates transcript_path.
func (p ClaudeCode) ReadSessionHookInput(r io.Reader) (*types.ClaudeHookInput, error) {
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

// ValidateTranscriptPath checks that a Claude transcript path is safe:
// - Must be absolute
// - Must not contain ".." components
// - Must resolve to a location under the Claude projects directory
func (p ClaudeCode) ValidateTranscriptPath(path string) error {
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

	allowedRoots := []string{projectsDir}
	if envDir := os.Getenv(ClaudeStateDirEnv); envDir != "" {
		// Preserve legacy validation behavior: older code treated CONFAB_CLAUDE_DIR
		// itself as the allowed transcript root for hook payloads.
		allowedRoots = append(allowedRoots, envDir)
	}

	if pathIsUnderAnyRoot(cleaned, allowedRoots) {
		return nil
	}
	return fmt.Errorf("must be under Claude projects directory (%s)", projectsDir)
}

// FindParentPID walks up the process tree to find the Claude Code process.
func (p ClaudeCode) FindParentPID() int {
	parentPID := os.Getppid()
	if p.IsProcess(parentPID) {
		return parentPID
	}

	grandparentPID := getParentPID(parentPID)
	if grandparentPID > 0 && p.IsProcess(grandparentPID) {
		return grandparentPID
	}

	logger.Warn("Could not find Claude in process tree, disabling parent PID monitoring")
	return 0
}

// IsProcess checks if the given PID is a Claude Code process.
func (p ClaudeCode) IsProcess(pid int) bool {
	cmd := getProcCmdline(pid)
	return p.MatchesProcess(cmd)
}

var claudeProcessPattern = regexp.MustCompile(`(?i)\bclaude\b`)

// MatchesProcess checks if a command string matches Claude Code.
func (ClaudeCode) MatchesProcess(cmd string) bool {
	return claudeProcessPattern.MatchString(cmd)
}

func getProcCmdline(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getParentPID(pid int) int {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}
