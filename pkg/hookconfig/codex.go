// Package hookconfig owns the install/uninstall/check logic for
// Confabulous Code hooks in Claude Code's settings.json, Codex's config.toml, and
// Cursor's hooks.json.
// Provider methods delegate here so pkg/provider doesn't carry the
// configuration-file detail.
package hookconfig

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	toml "github.com/pelletier/go-toml/v2"
)

const (
	confabCodexHooksStart = "# >>> confab codex hooks >>>"
	confabCodexHooksEnd   = "# <<< confab codex hooks <<<"

	// codexSessionStartStatusMessage is shown in Codex's UI while the
	// SessionStart hook runs. It is part of the trusted-hook hash identity,
	// so the TOML literal and the hash input must both come from here.
	codexSessionStartStatusMessage = "Starting Confabulous Code sync"

	// codexSessionStartMatcher selects which SessionStart sources run the
	// hook. "fork" is what Codex sends when the user branches a thread
	// ("branch in new chat" -> thread/fork); a fork is a new user thread with
	// no spawn edge, so it needs its own daemon. "compact" is left out on
	// purpose: it fires for a thread that already has a daemon. Like the
	// status message, the matcher is part of the trusted-hook hash identity.
	codexSessionStartMatcher = "startup|resume|clear|fork"
)

// InstallCodexHooks installs the Confabulous Code hooks into Codex's
// config.toml at configPath: its own hook groups are replaced in place (or
// appended on a first install), [features] hooks is set to true, and every
// other byte of the file is preserved. Returns the configPath.
func InstallCodexHooks(configPath string) (string, error) {
	return editCodexHooks(configPath, codexModeInstall)
}

// UninstallCodexHooks removes the Confabulous Code hook and trust-state
// tables from Codex's config.toml, preserving the rest of the file
// (including [features] hooks). Returns the configPath even if no hooks
// were present.
func UninstallCodexHooks(configPath string) (string, error) {
	return editCodexHooks(configPath, codexModeUninstall)
}

// RefreshCodexHooks brings an installed but outdated set of Confabulous Code
// hooks in configPath up to date. Unlike InstallCodexHooks it leaves an
// explicit [features] hooks = false and the extra keys Codex keeps in
// confab's trust-state tables. See CodexHooksOutdated.
func RefreshCodexHooks(configPath string) (string, error) {
	return editCodexHooks(configPath, codexModeRefresh)
}

// editCodexHooks runs one ownership-based edit of configPath (see
// codex_edit.go) and returns configPath.
func editCodexHooks(configPath string, mode codexEditMode) (string, error) {
	ed, err := newCodexEditor()
	if err != nil {
		return "", err
	}
	if err := ed.apply(configPath, mode); err != nil {
		return "", err
	}
	return configPath, nil
}

// BinaryPath resolves the confab binary written into Codex hook commands.
// It is a variable so tests can pin a path named "confab": test binaries
// have another name, which IsCodexHooksInstalled does not recognise.
var BinaryPath = config.GetBinaryPath

// CodexHooksOutdated reports whether configPath carries a full set of
// Confabulous Code hooks that differs from what this binary would write.
func CodexHooksOutdated(configPath string) (bool, error) {
	binPath, err := BinaryPath()
	if err != nil {
		return false, err
	}
	return codexHooksOutdated(configPath, binPath)
}

// codexHookGroup is one [[hooks.<EventName>]] block from config.toml as
// inspected by IsCodexHooksInstalled.
type codexHookGroup struct {
	Hooks []struct {
		Type    string `toml:"type"`
		Command string `toml:"command"`
	} `toml:"hooks"`
}

// codexHooksConfig is the minimal TOML schema for IsCodexHooksInstalled.
// We inspect the three event arrays Confabulous Code installs (SessionStart,
// PreToolUse, PostToolUse) and require a confab command in each.
type codexHooksConfig struct {
	Hooks struct {
		SessionStart []codexHookGroup `toml:"SessionStart"`
		PreToolUse   []codexHookGroup `toml:"PreToolUse"`
		PostToolUse  []codexHookGroup `toml:"PostToolUse"`
	} `toml:"hooks"`
}

// hasConfabCommand reports whether any entry in any group registers a
// confab command.
func hasConfabCommand(groups []codexHookGroup) bool {
	for _, group := range groups {
		for _, h := range group.Hooks {
			if h.Type == "command" && isConfabCommand(h.Command) {
				return true
			}
		}
	}
	return false
}

// IsCodexHooksInstalled parses configPath and returns true only when all
// three Confabulous Code hook events (SessionStart, PreToolUse, PostToolUse) carry
// a confab command. Existing SessionStart-only installs (pre-CF-492) read
// as "not installed" so `confab setup` installs the missing events and
// transparently upgrades them.
func IsCodexHooksInstalled(configPath string) (bool, error) {
	data, exists, err := readCodexConfig(configPath)
	if err != nil || !exists {
		return false, err
	}
	return codexHooksInstalledIn(data)
}

// codexHooksInstalledIn is IsCodexHooksInstalled on already-read config data.
func codexHooksInstalledIn(data []byte) (bool, error) {
	var cfg codexHooksConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("failed to parse Codex config: %w", err)
	}
	return hasConfabCommand(cfg.Hooks.SessionStart) &&
		hasConfabCommand(cfg.Hooks.PreToolUse) &&
		hasConfabCommand(cfg.Hooks.PostToolUse), nil
}

// codexHookGroupIndices carries the per-event group index used to build
// each event's positional trust-state key.
type codexHookGroupIndices struct {
	sessionStart int
	preToolUse   int
	postToolUse  int
}

// codexHooksTOML emits the managed Codex hook block: SessionStart for
// daemon lifecycle, PreToolUse + PostToolUse for bidirectional GitHub
// link injection. We deliberately do NOT install [[hooks.Stop]] (fires
// per turn; daemon shutdown is parent-PID driven) or
// [[hooks.UserPromptSubmit]] (Claude-only teleport case; not applicable
// to Codex's parent-PID model).
//
// StatusMessage is "" for the tool-use events to match Codex's Option<String>
// round-trip: emitting statusMessage="" in TOML deserializes to Some("")
// which canonical-JSON-serializes as "statusMessage":"" — matching the
// JSON our Go side hashes. Omitting the field would round-trip to None and
// produce a hash mismatch, triggering a first-run trust prompt.
func codexHooksTOML(configPath, binPath string, idx codexHookGroupIndices) string {
	escapedBinaryPath := strings.ReplaceAll(binPath, `\`, `\\`)
	escapedBinaryPath = strings.ReplaceAll(escapedBinaryPath, `"`, `\"`)

	sessionStartCommand := binPath + " hook session-start --provider codex"
	preToolUseCommand := binPath + " hook pre-tool-use --provider codex"
	postToolUseCommand := binPath + " hook post-tool-use --provider codex"

	sessionStartHash := codexTrustedHookHash("session_start", codexSessionStartMatcher, sessionStartCommand, codexSessionStartStatusMessage)
	preToolUseHash := codexTrustedHookHash("pre_tool_use", "Bash", preToolUseCommand, "")
	postToolUseHash := codexTrustedHookHash("post_tool_use", "Bash", postToolUseCommand, "")

	sessionStartKey := tomlQuoteString(fmt.Sprintf("%s:session_start:%d:0", configPath, idx.sessionStart))
	preToolUseKey := tomlQuoteString(fmt.Sprintf("%s:pre_tool_use:%d:0", configPath, idx.preToolUse))
	postToolUseKey := tomlQuoteString(fmt.Sprintf("%s:post_tool_use:%d:0", configPath, idx.postToolUse))

	return fmt.Sprintf(`[[hooks.SessionStart]]
matcher = %s
[[hooks.SessionStart.hooks]]
type = "command"
command = "%s hook session-start --provider codex"
statusMessage = %s

[[hooks.PreToolUse]]
matcher = "Bash"
[[hooks.PreToolUse.hooks]]
type = "command"
command = "%s hook pre-tool-use --provider codex"
statusMessage = ""

[[hooks.PostToolUse]]
matcher = "Bash"
[[hooks.PostToolUse.hooks]]
type = "command"
command = "%s hook post-tool-use --provider codex"
statusMessage = ""

[hooks.state.%s]
trusted_hash = "%s"

[hooks.state.%s]
trusted_hash = "%s"

[hooks.state.%s]
trusted_hash = "%s"
`,
		tomlQuoteString(codexSessionStartMatcher),
		escapedBinaryPath,
		tomlQuoteString(codexSessionStartStatusMessage),
		escapedBinaryPath,
		escapedBinaryPath,
		sessionStartKey, sessionStartHash,
		preToolUseKey, preToolUseHash,
		postToolUseKey, postToolUseHash,
	)
}

type codexHookTrustIdentity struct {
	EventName string                  `json:"event_name"`
	Hooks     []codexTrustedHookEntry `json:"hooks"`
	Matcher   string                  `json:"matcher,omitempty"`
}

type codexTrustedHookEntry struct {
	Async         bool   `json:"async"`
	Command       string `json:"command"`
	StatusMessage string `json:"statusMessage"`
	Timeout       int    `json:"timeout"`
	Type          string `json:"type"`
}

func codexTrustedHookHash(eventName, matcher, command, statusMessage string) string {
	identity := codexHookTrustIdentity{
		EventName: eventName,
		Hooks: []codexTrustedHookEntry{{
			Async:         false,
			Command:       command,
			StatusMessage: statusMessage,
			Timeout:       600,
			Type:          "command",
		}},
		Matcher: matcher,
	}
	b, _ := json.Marshal(identity)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum)
}

func tomlQuoteString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// backupFile writes data to a timestamped "<path>.confab-backup-<ts>" sibling
// file, preserving prior config content before an in-place rewrite. It
// returns the backup's path.
func backupFile(path string, data []byte) (string, error) {
	backupPath := fmt.Sprintf("%s.confab-backup-%s", path, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to create backup: %w", err)
	}
	return backupPath, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
