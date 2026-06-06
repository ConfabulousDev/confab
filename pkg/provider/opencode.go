package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

const opencodePluginFileName = "confab-sync.ts"

type Opencode struct{}

var _ Provider = Opencode{}

func (Opencode) Name() string { return NameOpencode }

func (Opencode) CLIBinaryName() string { return "opencode" }

func (Opencode) SupportsCommitLinking() bool { return false }

func (p Opencode) ParseSessionHook(r io.Reader) (HookInput, error) {
	in, err := p.ReadSessionHookInput(r)
	if err != nil {
		return nil, err
	}
	return opencodeHookInputAdapter{inner: in}, nil
}

func (Opencode) WalkUpToRoot(sessionID string) (string, string, error) {
	return sessionID, "", nil
}

// ShouldSpawnForInput refuses subagent (non-root) OpenCode sessions so only the
// user-initiated root session spawns a daemon; CF-538 will capture subagents as
// sidechain files under the root. A session is a subagent when the plugin
// forwarded a parent session id (surfaced via an optional SessionParentID()
// accessor on the input — kept off the shared HookInput interface so Claude/
// Codex inputs need not implement it). Inputs without the accessor (or with an
// empty parent id) are treated as root.
func (Opencode) ShouldSpawnForInput(in HookInput) bool {
	if sp, ok := in.(interface{ SessionParentID() string }); ok && sp.SessionParentID() != "" {
		return false
	}
	return true
}

func (p Opencode) InstallHooks() (string, error) {
	pluginDir, err := p.PluginDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(pluginDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create plugin directory: %w", err)
	}
	pluginPath := filepath.Join(pluginDir, opencodePluginFileName)
	source := strings.ReplaceAll(opencodePluginSourceRaw, "§BT§", "`")
	if err := os.WriteFile(pluginPath, []byte(source), 0644); err != nil {
		return "", fmt.Errorf("failed to write plugin: %w", err)
	}
	return pluginPath, nil
}

func (p Opencode) UninstallHooks() (string, error) {
	pluginDir, err := p.PluginDir()
	if err != nil {
		return "", err
	}
	pluginPath := filepath.Join(pluginDir, opencodePluginFileName)
	if err := os.Remove(pluginPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to remove plugin: %w", err)
	}
	return pluginPath, nil
}

func (p Opencode) IsHooksInstalled() (bool, error) {
	pluginDir, err := p.PluginDir()
	if err != nil {
		return false, err
	}
	pluginPath := filepath.Join(pluginDir, opencodePluginFileName)
	_, err = os.Stat(pluginPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (p Opencode) InstallSkills() error {
	stateDir, err := p.StateDir()
	if err != nil {
		return err
	}
	return config.ReconcileBundledSkills(stateDir, config.SkillProviderOpencode)
}

func (p Opencode) UninstallSkills() error {
	stateDir, err := p.StateDir()
	if err != nil {
		return err
	}
	return config.UninstallBundledSkills(stateDir)
}

func (p Opencode) IsSkillInstalled(name string) bool {
	stateDir, err := p.StateDir()
	if err != nil {
		return false
	}
	return config.IsBundledSkillInstalled(stateDir, name)
}

func (Opencode) WriteHookResponse(w io.Writer, _ bool, _ string) error {
	return nil
}

func (Opencode) InitTranscript(TranscriptRegistrar, string, string) error { return nil }

func (Opencode) DiscoverDescendants(DescendantRegistrar, string) error { return nil }

func (Opencode) DiscoverWorkflowFiles(WorkflowRegistrar, func(string) bool) (int, error) {
	return 0, nil
}

// AnnotateChunk sets first_user_message on the first transcript chunk so synced
// OpenCode sessions appear in the web session list (CF-540) — the backend's
// list query hides any session with neither a summary nor a first_user_message,
// and the CLI is the only source for those fields. OpenCode has no summary
// concept, so only first_user_message is set (mirroring Codex). The text is the
// first user message's first text part, trimmed and redacted (redact is
// nil-safe). A malformed materialized line degrades to "no message found"
// rather than failing the sync — we wrote these lines ourselves, so a parse
// error signals a collector bug worth a debug log, not a blocked upload.
func (Opencode) AnnotateChunk(c ChunkView, sentFirstUserMessage bool, redact func(string) string) AnnotationResult {
	var result AnnotationResult
	if sentFirstUserMessage || c.FileType() != "transcript" {
		return result
	}
	msg, err := ocFirstUserMessageText(c.Lines())
	if err != nil {
		logger.Debug("opencode: failed to extract first user message: %v", err)
		return result
	}
	if msg == "" {
		return result
	}
	if redact != nil {
		msg = redact(msg)
	}
	c.SetFirstUserMessage(msg)
	result.IncludedFirstUserMessage = true
	return result
}

func (Opencode) DefaultCWD(transcriptPath string) string {
	return filepath.Dir(transcriptPath)
}

func (Opencode) FindParentPID() int {
	for pid, depth := os.Getppid(), 0; pid > 1 && depth < 5; pid, depth = getParentPID(pid), depth+1 {
		if opencodeProcessPattern.MatchString(getProcName(pid)) {
			return pid
		}
	}
	return 0
}

func (Opencode) IsProcess(pid int) bool {
	return opencodeProcessPattern.MatchString(getProcName(pid))
}

var opencodeProcessPattern = regexp.MustCompile(`(?i)\bopencode\b`)

func (p Opencode) StateDir() (string, error) {
	if envDir := os.Getenv("CONFAB_OPENCODE_CONFIG_DIR"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

func (p Opencode) PluginDir() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "plugins"), nil
}

func (p Opencode) ReadSessionHookInput(r io.Reader) (*types.OpenCodeHookInput, error) {
	data, err := io.ReadAll(io.LimitReader(r, types.MaxJSONLLineSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}
	var input types.OpenCodeHookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("failed to parse OpenCode hook input: %w", err)
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if err := types.ValidateSessionID(input.SessionID); err != nil {
		return nil, err
	}
	if input.OpenCodeServerURL == "" {
		return nil, fmt.Errorf("server_url is required")
	}
	return &input, nil
}

// ScanSessions is unsupported for OpenCode: sessions live behind a running
// OpenCode HTTP server (no on-disk transcript to scan), and offline manual mode
// is deferred. Live capture happens via the sync daemon's collector instead.
func (Opencode) ScanSessions() ([]SessionInfo, error) {
	return nil, fmt.Errorf("opencode: manual session scan not supported (sessions sync live via the daemon; offline manual mode is not yet implemented)")
}

// FindSessionByID is unsupported for OpenCode for the same reason as
// ScanSessions; manual `confab save <id>` is deferred.
func (Opencode) FindSessionByID(string) (string, string, error) {
	return "", "", fmt.Errorf("opencode: manual session lookup not supported (sessions sync live via the daemon; offline manual mode is not yet implemented)")
}

func (Opencode) ExtractMetadata([]string) SessionMetadata {
	return SessionMetadata{}
}

// opencodePluginSourceRaw is the TypeScript plugin source with §BT§ as a
// placeholder for backtick characters (Go raw string literals cannot contain
// backticks). The replacement happens at InstallHooks time.
//
// The canonical source lives at pkg/provider/plugins/confab-sync.ts (with real
// backticks). Tests validate the two stay in sync.
var opencodePluginSourceRaw = `import type { Plugin } from "@opencode-ai/plugin"

export const ConfabSync: Plugin = async ({ $, serverUrl }) => {
  const running = new Set<string>()

  async function spawn(sessionID: string, cwd: string, parentID?: string) {
    if (running.has(sessionID)) return
    running.add(sessionID)
    const payload: Record<string, unknown> = {
      session_id: sessionID,
      server_url: serverUrl.href,
      cwd,
    }
    // Forward the session's parent id (subagents only) so the CLI can suppress
    // daemons for non-root sessions; omitted for root sessions.
    if (parentID) payload.parent_id = parentID
    const input = JSON.stringify(payload)
    try {
      await $§BT§echo ${input} | confab hook session-start --provider opencode§BT§.quiet()
    } catch (err) {
      // Spawn failed (e.g. confab not on PATH). Drop the session from the
      // running set so dispose doesn't try to stop a daemon that never
      // started, and a later event can retry.
      running.delete(sessionID)
      console.error(§BT§[confab] failed to start sync daemon for ${sessionID}:§BT§, err)
    }
  }

  async function stop(sessionID: string) {
    if (!running.has(sessionID)) return
    running.delete(sessionID)
    const input = JSON.stringify({
      session_id: sessionID,
      server_url: serverUrl.href,
    })
    try {
      await $§BT§echo ${input} | confab hook session-end --provider opencode§BT§.quiet()
    } catch (err) {
      // Don't let one failed stop abort shutdown of the remaining sessions.
      console.error(§BT§[confab] failed to stop sync daemon for ${sessionID}:§BT§, err)
    }
  }

  return {
    event: async ({ event }) => {
      if (event.type === "session.created") {
        const session = event.properties.info
        await spawn(session.id, session.directory, session.parentID)
      }
    },
    dispose: async () => {
      for (const sid of [...running]) {
        await stop(sid)
      }
    },
  }
}
`
