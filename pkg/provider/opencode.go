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

func (Opencode) ShouldSpawnForInput(HookInput) bool { return true }

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

func (Opencode) AnnotateChunk(_ ChunkView, _ bool, _ func(string) string) AnnotationResult {
	return AnnotationResult{}
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

func (Opencode) ScanSessions() ([]SessionInfo, error) {
	return nil, fmt.Errorf("ScanSessions not yet implemented for opencode (Phase 2)")
}

func (Opencode) FindSessionByID(string) (string, string, error) {
	return "", "", fmt.Errorf("FindSessionByID not yet implemented for opencode (Phase 2)")
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
var opencodePluginSourceRaw = `import type { Event, Plugin } from "@opencode-ai/plugin"

export const ConfabSync: Plugin = async ({ $, serverUrl }) => {
  const running = new Set<string>()

  async function spawn(sessionID: string, cwd: string) {
    if (running.has(sessionID)) return
    running.add(sessionID)
    const input = JSON.stringify({
      session_id: sessionID,
      server_url: serverUrl.href,
      cwd,
    })
    await $§BT§echo ${input} | confab hook session-start --provider opencode§BT§.quiet()
  }

  async function stop(sessionID: string) {
    if (!running.has(sessionID)) return
    running.delete(sessionID)
    const input = JSON.stringify({
      session_id: sessionID,
      server_url: serverUrl.href,
    })
    await $§BT§echo ${input} | confab hook session-end --provider opencode§BT§.quiet()
  }

  return {
    event: async ({ event }) => {
      if (event.type === "session.created") {
        const session = event.properties.info
        await spawn(session.id, session.directory)
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
