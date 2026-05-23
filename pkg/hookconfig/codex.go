// Package hookconfig owns the install/uninstall/check logic for
// Confab hooks in Claude Code's settings.json and Codex's config.toml.
// Provider methods delegate here so pkg/provider doesn't carry the
// configuration-file detail.
package hookconfig

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	toml "github.com/pelletier/go-toml/v2"
)

const (
	confabCodexHooksStart = "# >>> confab codex hooks >>>"
	confabCodexHooksEnd   = "# <<< confab codex hooks <<<"
)

// InstallCodexHooks writes the managed Confab hook block into Codex's
// config.toml at configPath, preserving user content and creating a
// backup. Returns the configPath that was written.
func InstallCodexHooks(configPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return "", fmt.Errorf("failed to create Codex state directory: %w", err)
	}

	var existing []byte
	if data, err := os.ReadFile(configPath); err == nil {
		existing = data
		backupPath := fmt.Sprintf("%s.confab-backup-%s", configPath, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backupPath, data, 0600); err != nil {
			return "", fmt.Errorf("failed to create backup: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read Codex config: %w", err)
	}

	binPath, err := config.GetBinaryPath()
	if err != nil {
		return "", err
	}

	updated := ensureCodexHooksConfig(string(existing), configPath, binPath)
	if err := writeFileAtomic(configPath, []byte(updated), 0600); err != nil {
		return "", fmt.Errorf("failed to write Codex config: %w", err)
	}
	return configPath, nil
}

// UninstallCodexHooks removes the managed Confab hook block from
// Codex's config.toml, preserving the rest of the file. Returns the
// configPath even if no block was present.
func UninstallCodexHooks(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return configPath, nil
		}
		return "", fmt.Errorf("failed to read Codex config: %w", err)
	}
	backupPath := fmt.Sprintf("%s.confab-backup-%s", configPath, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to create backup: %w", err)
	}
	updated := removeManagedBlock(string(data), confabCodexHooksStart, confabCodexHooksEnd)
	if err := writeFileAtomic(configPath, []byte(strings.TrimRight(updated, "\n")+"\n"), 0600); err != nil {
		return "", fmt.Errorf("failed to write Codex config: %w", err)
	}
	return configPath, nil
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
// We inspect the three event arrays Confab installs (SessionStart,
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
// three Confab hook events (SessionStart, PreToolUse, PostToolUse) carry
// a confab command. Existing SessionStart-only installs (pre-CF-492) read
// as "not installed" so `confab setup` re-emits the managed block and
// transparently upgrades them.
func IsCodexHooksInstalled(configPath string) (bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read Codex config: %w", err)
	}
	var cfg codexHooksConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("failed to parse Codex config: %w", err)
	}
	return hasConfabCommand(cfg.Hooks.SessionStart) &&
		hasConfabCommand(cfg.Hooks.PreToolUse) &&
		hasConfabCommand(cfg.Hooks.PostToolUse), nil
}

func ensureCodexHooksConfig(config, configPath, binPath string) string {
	config = removeManagedBlock(config, confabCodexHooksStart, confabCodexHooksEnd)
	config = ensureCodexHooksFeature(config)
	// Count groups per event AFTER stripping our managed block so re-installs
	// produce stable positional trust keys. Each event is counted
	// independently — the user may have pre-existing unmanaged blocks for
	// any subset of events.
	groupIndices := codexHookGroupIndices{
		sessionStart: countCodexHookMatcherGroups(config, "SessionStart"),
		preToolUse:   countCodexHookMatcherGroups(config, "PreToolUse"),
		postToolUse:  countCodexHookMatcherGroups(config, "PostToolUse"),
	}
	return appendTOMLBlock(config, confabCodexHooksStart+"\n"+codexHooksTOML(configPath, binPath, groupIndices)+confabCodexHooksEnd+"\n")
}

func ensureCodexHooksFeature(config string) string {
	config = removeCodexHooksDeprecatedFeature(config)

	re := regexp.MustCompile(`(?m)^hooks\s*=\s*false\s*$`)
	if re.MatchString(config) {
		return re.ReplaceAllString(config, "hooks = true")
	}
	re = regexp.MustCompile(`(?m)^hooks\s*=\s*true\s*$`)
	if re.MatchString(config) {
		return config
	}
	if strings.Contains(config, "[features]") {
		lines := strings.Split(config, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "[features]" {
				next := append([]string{}, lines[:i+1]...)
				next = append(next, "hooks = true")
				next = append(next, lines[i+1:]...)
				return strings.Join(next, "\n")
			}
		}
	}
	return appendTOMLBlock(config, "[features]\nhooks = true\n")
}

func removeCodexHooksDeprecatedFeature(config string) string {
	lines := strings.Split(config, "\n")
	out := lines[:0]
	for _, line := range lines {
		if regexp.MustCompile(`^\s*codex_hooks\s*=`).MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func appendTOMLBlock(config, block string) string {
	config = strings.TrimRight(config, "\n")
	if config == "" {
		return block
	}
	return config + "\n\n" + block
}

func removeManagedBlock(config, start, end string) string {
	startIdx := strings.Index(config, start)
	if startIdx == -1 {
		return config
	}
	endIdx := strings.Index(config[startIdx:], end)
	if endIdx == -1 {
		return config
	}
	endIdx += startIdx + len(end)
	for endIdx < len(config) && (config[endIdx] == '\n' || config[endIdx] == '\r') {
		endIdx++
	}
	return strings.TrimRight(config[:startIdx], "\n") + "\n" + config[endIdx:]
}

func countCodexHookMatcherGroups(config, eventName string) int {
	re := regexp.MustCompile(`(?m)^\s*\[\[\s*hooks\.` + regexp.QuoteMeta(eventName) + `\s*\]\]\s*(?:#.*)?$`)
	return len(re.FindAllStringIndex(config, -1))
}

// codexHookGroupIndices carries the per-event group offset we use to build
// each event's positional trust-state key. Counted by
// countCodexHookMatcherGroups against the post-strip config so the keys
// land on our hook even when the user has pre-existing unmanaged blocks
// for any subset of the three events.
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

	sessionStartHash := codexTrustedHookHash("session_start", "startup|resume|clear", sessionStartCommand, "Starting Confab sync")
	preToolUseHash := codexTrustedHookHash("pre_tool_use", "Bash", preToolUseCommand, "")
	postToolUseHash := codexTrustedHookHash("post_tool_use", "Bash", postToolUseCommand, "")

	sessionStartKey := tomlQuoteString(fmt.Sprintf("%s:session_start:%d:0", configPath, idx.sessionStart))
	preToolUseKey := tomlQuoteString(fmt.Sprintf("%s:pre_tool_use:%d:0", configPath, idx.preToolUse))
	postToolUseKey := tomlQuoteString(fmt.Sprintf("%s:post_tool_use:%d:0", configPath, idx.postToolUse))

	return fmt.Sprintf(`[[hooks.SessionStart]]
matcher = "startup|resume|clear"
[[hooks.SessionStart.hooks]]
type = "command"
command = "%s hook session-start --provider codex"
statusMessage = "Starting Confab sync"

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
		escapedBinaryPath,
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
