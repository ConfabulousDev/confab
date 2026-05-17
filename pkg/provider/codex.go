package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

// InitTranscript reads the root rollout's session_meta and attaches the
// resulting CodexRolloutMetadata to the transcript file so the very first
// uploaded chunk carries codex_rollout metadata. On read failure (missing
// or malformed rollout file) logs at warn level and still attaches the
// bare-minimum metadata (ThreadUUID + RolloutPath) so the backend can
// upsert a row; CWD/Model/etc. stay empty. Matches pre-CF-397 behavior.
// Never returns an error — failure modes are recoverable.
func (p Codex) InitTranscript(target TranscriptRegistrar, transcriptPath, externalID string) error {
	info, err := p.ReadSessionInfo(transcriptPath)
	if err != nil {
		logger.Warn("Codex root session_meta read failed: %v", err)
		// Fall through with zero CodexSessionInfo so the partial metadata
		// still goes out. Backend treats missing fields as "unknown".
	}
	target.SetCodexRollout(&CodexRolloutMetadata{
		ThreadUUID:    externalID,
		RolloutPath:   transcriptPath,
		CWD:           info.CWD,
		Model:         info.Model,
		Source:        info.Source,
		ThreadSource:  info.ThreadSource,
		AgentPath:     info.AgentPath,
		AgentRole:     info.AgentRole,
		AgentNickname: info.AgentNickname,
		// ParentThreadUUID stays "" for the root.
	})
	return nil
}

// DiscoverDescendants queries the local Codex SQLite state DB for every
// descendant of rootThreadUUID, verifies each rollout file exists and looks
// like an actual subagent (ValidateRolloutPath + !IsUserSession check on
// session_meta), and registers verified ones via reg.RegisterCodexRollout.
//
// Idempotent across calls (skips already-tracked filenames). Degrades
// gracefully when the state DB is missing or its schema doesn't match —
// ListSubtree returns (nil, nil) and we return nil. Per-descendant
// verification failures log at warn level and skip the row.
func (p Codex) DiscoverDescendants(reg DescendantRegistrar, rootThreadUUID string) error {
	rows, err := p.ListSubtree(rootThreadUUID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		fileName := filepath.Base(row.RolloutPath)
		if reg.IsTracked(fileName) {
			continue
		}
		if err := p.ValidateRolloutPath(row.RolloutPath); err != nil {
			logger.Warn("Codex descendant %s: invalid rollout path %q: %v",
				row.ThreadUUID, row.RolloutPath, err)
			continue
		}
		info, err := p.ReadSessionInfo(row.RolloutPath)
		if err != nil {
			logger.Warn("Codex descendant %s: failed to read session_meta: %v",
				row.ThreadUUID, err)
			continue
		}
		// The DB says this is a descendant, but only trust the row if the
		// rollout itself confirms it's a subagent. Symmetric to provider.IsUserSession.
		if info.IsUserSession() {
			logger.Warn("Codex descendant %s: session_meta says user-session, skipping",
				row.ThreadUUID)
			continue
		}
		// SQLite's `threads.source` mirrors the rollout's polymorphic shape
		// (a 167-char JSON object in recent Codex versions). Use the
		// rollout-side flattened discriminator to stay under the backend's
		// 64-char `source` cap.
		meta := CodexRolloutMetadata{
			ThreadUUID:       row.ThreadUUID,
			ParentThreadUUID: row.ParentThreadUUID,
			RolloutPath:      row.RolloutPath,
			CWD:              row.CWD,
			Model:            row.Model,
			Source:           info.Source,
			ThreadSource:     row.ThreadSource,
			AgentPath:        row.AgentPath,
			AgentRole:        row.AgentRole,
			AgentNickname:    row.AgentNickname,
		}
		reg.RegisterCodexRollout(row.RolloutPath, fileName, false, meta)
		logger.Info("Discovered Codex descendant: thread=%s path=%s",
			row.ThreadUUID, row.RolloutPath)
	}
	return nil
}

// AnnotateChunk attaches Codex-specific chunk metadata. Two concerns are
// handled independently:
//
//   - first_user_message: extracted via ExtractMetadata from the root
//     transcript's chunks (Codex emits the user prompt once at the start).
//     Gated by c.FileType() == "transcript" + !sentFirstUserMessage. The
//     closure `redact` (nil-safe) is applied before attaching.
//
//   - codex_rollout: per-rollout metadata so the backend can upsert the
//     `codex_rollouts` row. Emitted on the FIRST chunk of any Codex rollout
//     (root or descendant) — detected via c.FirstLine() == 1. No
//     persistent state flag is needed; retries preserve FirstLine == 1.
//     Backend upsert is idempotent.
func (p Codex) AnnotateChunk(c ChunkView, sentFirstUserMessage bool, redact func(string) string) AnnotationResult {
	var result AnnotationResult
	if !sentFirstUserMessage && c.FileType() == "transcript" {
		meta := p.ExtractMetadata(c.Lines())
		msg := meta.FirstUserMessage
		if msg != "" {
			if redact != nil {
				msg = redact(msg)
			}
			c.SetFirstUserMessage(msg)
			result.IncludedFirstUserMessage = true
		}
	}
	if roll := c.FileCodexRollout(); roll != nil && c.FirstLine() == 1 {
		c.SetCodexRolloutMetadata(roll)
	}
	return result
}

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
