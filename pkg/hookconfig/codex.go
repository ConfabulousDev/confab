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

// codexHooksConfig is the minimal TOML schema for IsCodexHooksInstalled.
// We only inspect [[hooks.SessionStart]].hooks entries to decide
// whether at least one confab command is registered.
type codexHooksConfig struct {
	Hooks struct {
		SessionStart []struct {
			Hooks []struct {
				Type    string `toml:"type"`
				Command string `toml:"command"`
			} `toml:"hooks"`
		} `toml:"SessionStart"`
	} `toml:"hooks"`
}

// IsCodexHooksInstalled parses configPath and returns true iff at least
// one [[hooks.SessionStart.hooks]] entry invokes the confab binary.
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
	for _, group := range cfg.Hooks.SessionStart {
		for _, h := range group.Hooks {
			if h.Type == "command" && isConfabCommand(h.Command) {
				return true, nil
			}
		}
	}
	return false, nil
}

func ensureCodexHooksConfig(config, configPath, binPath string) string {
	config = removeManagedBlock(config, confabCodexHooksStart, confabCodexHooksEnd)
	config = ensureCodexHooksFeature(config)
	sessionStartGroupIndex := countCodexHookMatcherGroups(config, "SessionStart")
	return appendTOMLBlock(config, confabCodexHooksStart+"\n"+codexHooksTOML(configPath, binPath, sessionStartGroupIndex)+confabCodexHooksEnd+"\n")
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

// codexHooksTOML emits the managed Codex hook block. We install only
// SessionStart — Codex fires Stop at every agent/turn boundary, so a
// Stop hook would prematurely kill the root sync daemon. Daemon
// shutdown is driven by parent-process liveness instead.
func codexHooksTOML(configPath, binPath string, sessionStartGroupIndex int) string {
	escapedBinaryPath := strings.ReplaceAll(binPath, `\`, `\\`)
	escapedBinaryPath = strings.ReplaceAll(escapedBinaryPath, `"`, `\"`)
	sessionStartCommand := binPath + " hook session-start --provider codex"
	sessionStartHash := codexTrustedHookHash("session_start", "startup|resume|clear", sessionStartCommand, "Starting Confab sync")
	sessionStartKey := tomlQuoteString(fmt.Sprintf("%s:session_start:%d:0", configPath, sessionStartGroupIndex))
	return fmt.Sprintf(`[[hooks.SessionStart]]
matcher = "startup|resume|clear"
[[hooks.SessionStart.hooks]]
type = "command"
command = "%s hook session-start --provider codex"
statusMessage = "Starting Confab sync"

[hooks.state.%s]
trusted_hash = "%s"
`, escapedBinaryPath, sessionStartKey, sessionStartHash)
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
