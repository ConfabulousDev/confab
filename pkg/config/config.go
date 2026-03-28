package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// ClaudeSettings wraps the raw settings map to preserve all fields.
// This is similar to Python's json.load/json.dump pattern.
// We intentionally avoid typed structs for hooks since the schema
// is controlled by Claude Code and evolves rapidly.
type ClaudeSettings struct {
	raw map[string]any
}

// ErrHooksTypeMismatch is returned when the "hooks" field in settings.json
// exists but is not a JSON object. This prevents silently overwriting user config.
var ErrHooksTypeMismatch = errors.New("settings.json: 'hooks' field exists but is not a JSON object — please fix manually")

// getHooksMap returns the hooks map, creating it if it doesn't exist.
// Returns an error if the hooks field exists but has the wrong type,
// to prevent silently overwriting user configuration.
func (s *ClaudeSettings) getHooksMap() (map[string]any, error) {
	hooksRaw, exists := s.raw["hooks"]
	if !exists {
		hooks := make(map[string]any)
		s.raw["hooks"] = hooks
		return hooks, nil
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		return nil, ErrHooksTypeMismatch
	}
	return hooks, nil
}

// getEventHooks returns the array of matchers for an event, as []any.
// This is a read-only operation that does not create the hooks map if it doesn't exist.
func (s *ClaudeSettings) getEventHooks(eventName string) []any {
	hooksRaw, exists := s.raw["hooks"]
	if !exists {
		return nil
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		logger.Debug("settings.json: 'hooks' has unexpected type %T (expected object), skipping", hooksRaw)
		return nil
	}
	eventHooksRaw, exists := hooks[eventName]
	if !exists {
		return nil
	}
	eventHooks, ok := eventHooksRaw.([]any)
	if !ok {
		logger.Debug("settings.json: hooks[%q] has unexpected type %T (expected array), skipping", eventName, eventHooksRaw)
		return nil
	}
	return eventHooks
}

// setEventHooks sets the array of matchers for an event.
// If matchers is nil or empty, the event key is removed.
// If the hooks map becomes empty, it is removed from settings.
func (s *ClaudeSettings) setEventHooks(eventName string, matchers []any) error {
	hooks, err := s.getHooksMap()
	if err != nil {
		return err
	}

	if len(matchers) == 0 {
		// Remove the event key entirely instead of leaving null/empty
		delete(hooks, eventName)

		// If hooks map is now empty, remove it from settings
		if len(hooks) == 0 {
			delete(s.raw, "hooks")
		}
		return nil
	}

	hooks[eventName] = matchers
	return nil
}

// GetSettingsPath returns the path to the Claude settings file
// (defaults to ~/.claude/settings.json, can be overridden with CONFAB_CLAUDE_DIR)
func GetSettingsPath() (string, error) {
	return GetClaudeSettingsPath()
}

// ReadSettings reads the Claude settings file, preserving all fields
func ReadSettings() (*ClaudeSettings, error) {
	settingsPath, err := GetSettingsPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get settings path: %w", err)
	}

	// If file doesn't exist, return empty settings
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		return &ClaudeSettings{
			raw: make(map[string]any),
		}, nil
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read Claude settings file (%s): %w", settingsPath, err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		//lint:ignore ST1005 "Claude" is a proper noun
		return nil, fmt.Errorf("Claude settings file has invalid JSON (%s): %w", settingsPath, err)
	}

	if raw == nil {
		raw = make(map[string]any)
	}

	return &ClaudeSettings{raw: raw}, nil
}

// writeSettingsInternal writes settings with optional mtime-based optimistic locking
// If expectedMtime is zero, mtime checking is skipped
// If expectedMtime is non-zero, it checks mtime and returns error on mismatch
func writeSettingsInternal(settings *ClaudeSettings, expectedMtime time.Time) error {
	settingsPath, err := GetSettingsPath()
	if err != nil {
		return fmt.Errorf("failed to get settings path: %w", err)
	}

	// Ensure directory exists
	settingsDir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	// Marshal the raw map to preserve all fields
	data, err := json.MarshalIndent(settings.raw, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Use temp file + atomic rename to prevent corruption
	// Create a unique temp file in the same directory to avoid conflicts
	tempFile, err := os.CreateTemp(settingsDir, ".settings-*.json.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Write data and close
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to write temp settings: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set proper permissions
	if err := os.Chmod(tempPath, 0600); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to set temp file permissions: %w", err)
	}

	// If mtime checking is enabled, verify file hasn't changed RIGHT BEFORE rename.
	// Note: There's still a small race window between this check and the rename below.
	// The retry logic in AtomicUpdateSettings handles conflicts that slip through.
	if !expectedMtime.IsZero() {
		info, err := os.Stat(settingsPath)
		if err != nil && !os.IsNotExist(err) {
			os.Remove(tempPath)
			return fmt.Errorf("failed to stat settings for mtime check: %w", err)
		}

		// Check mtime mismatch (file was modified by another process)
		if info != nil && !info.ModTime().Equal(expectedMtime) {
			os.Remove(tempPath)
			return fmt.Errorf("settings file was modified by another process (expected mtime: %v, actual: %v)",
				expectedMtime, info.ModTime())
		}
	}

	// Atomic rename (this is where mtime gets updated by OS)
	if err := os.Rename(tempPath, settingsPath); err != nil {
		os.Remove(tempPath) // Clean up temp file on error
		return fmt.Errorf("failed to rename temp settings: %w", err)
	}

	return nil
}

// AtomicUpdateSettings performs a read-modify-write with optimistic locking.
// It retries up to maxRetries times if the file is modified by another process.
// The updateFn receives the current settings and should modify them in-place.
//
// Race condition limitation: The mtime check and rename are not truly atomic.
// There's a small window (<1ms) between os.Stat() and os.Rename() where another
// process could modify the file. The retry mechanism mitigates but does not
// eliminate this race. For most use cases (CLI hook installation, infrequent
// config changes), the retry logic provides sufficient reliability. If truly
// atomic updates are required, file locking (flock) would be needed.
func AtomicUpdateSettings(updateFn func(*ClaudeSettings) error) error {
	const maxRetries = 10
	const baseRetryDelay = 5 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Read current settings and capture mtime
		settingsPath, err := GetSettingsPath()
		if err != nil {
			return fmt.Errorf("failed to get settings path: %w", err)
		}

		var mtime time.Time
		if info, err := os.Stat(settingsPath); err == nil {
			mtime = info.ModTime()
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat settings: %w", err)
		}
		// If file doesn't exist, mtime stays zero (no conflict possible)

		settings, err := ReadSettings()
		if err != nil {
			return fmt.Errorf("failed to read settings: %w", err)
		}

		// Apply user's modifications
		if err := updateFn(settings); err != nil {
			return fmt.Errorf("update function failed: %w", err)
		}

		// Try to write with mtime check
		err = writeSettingsInternal(settings, mtime)
		if err == nil {
			return nil // Success!
		}

		// Check if error is due to concurrent modification
		if strings.Contains(err.Error(), "modified by another process") {
			// Retry with exponential backoff + jitter
			if attempt < maxRetries-1 {
				// Exponential backoff: 5ms, 10ms, 20ms, 40ms, ...
				backoff := baseRetryDelay * time.Duration(1<<uint(attempt))
				// Add jitter (0-50% of backoff) to avoid thundering herd
				jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
				time.Sleep(backoff + jitter)
				continue
			}
			return fmt.Errorf("failed to update settings after %d attempts: %w", maxRetries, err)
		}

		// Other error, don't retry
		return err
	}

	return fmt.Errorf("failed to update settings after %d attempts", maxRetries)
}

// GetBinaryPath returns the absolute path to the confab binary
func GetBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks
	realPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	return realPath, nil
}

// isConfabCommand checks if a command string is a confab command
// More precise than simple string contains to avoid false positives
func isConfabCommand(command string) bool {
	// Extract the executable name from the command
	// Command format is typically: "/path/to/confab save" or "confab save"
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}

	executable := parts[0]
	baseName := filepath.Base(executable)

	// Check if the executable is exactly "confab"
	return baseName == "confab"
}

// isConfabHookEntry checks if a hook entry is a confab command hook.
func isConfabHookEntry(hook map[string]any) bool {
	cmd, _ := hook["command"].(string)
	return hook["type"] == "command" && isConfabCommand(cmd)
}

// getHooksList extracts and validates the hooks array from a matcher entry.
// Returns nil with debug logging if missing or invalid type.
func getHooksList(entry map[string]any, eventName string, entryIdx int) []any {
	hooksListRaw, exists := entry["hooks"]
	if !exists {
		return nil
	}
	hooksList, ok := hooksListRaw.([]any)
	if !ok {
		logger.Debug("settings.json: hooks[%q][%d].hooks has unexpected type %T (expected array)", eventName, entryIdx, hooksListRaw)
		return nil
	}
	return hooksList
}

// installHook installs a confab hook for a specific event.
// When hasMatcher is true, it looks for an entry whose "matcher" key equals matcherValue.
// When hasMatcher is false, it looks for an entry where the "matcher" key is entirely absent.
func installHook(settings *ClaudeSettings, hook map[string]any, eventName, matcherValue string, hasMatcher bool) error {
	eventHooks := settings.getEventHooks(eventName)

	for i, entryAny := range eventHooks {
		entry, ok := entryAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), skipping", eventName, i, entryAny)
			continue
		}

		// Check if this entry matches our target
		if hasMatcher {
			if entry["matcher"] != matcherValue {
				continue
			}
		} else {
			if _, has := entry["matcher"]; has {
				continue
			}
		}

		// Found matching entry — look for existing confab hook to update
		hooksList := getHooksList(entry, eventName, i)
		for j, existingHookAny := range hooksList {
			existingHook, ok := existingHookAny.(map[string]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks[%d] has unexpected type %T (expected object), skipping", eventName, i, j, existingHookAny)
				continue
			}
			if isConfabHookEntry(existingHook) {
				hooksList[j] = hook
				entry["hooks"] = hooksList
				eventHooks[i] = entry
				return settings.setEventHooks(eventName, eventHooks)
			}
		}

		// No existing confab hook, append
		hooksList = append(hooksList, hook)
		entry["hooks"] = hooksList
		eventHooks[i] = entry
		return settings.setEventHooks(eventName, eventHooks)
	}

	// No matching entry found, create new one
	newEntry := map[string]any{
		"hooks": []any{hook},
	}
	if hasMatcher {
		newEntry["matcher"] = matcherValue
	}
	eventHooks = append(eventHooks, newEntry)
	return settings.setEventHooks(eventName, eventHooks)
}

// removeHooksFromEvent removes hooks matching a predicate from all matchers of an event.
// Empty matchers (no remaining hooks) are dropped.
func removeHooksFromEvent(settings *ClaudeSettings, eventName string, shouldRemove func(map[string]any) bool) error {
	eventHooks := settings.getEventHooks(eventName)
	if len(eventHooks) == 0 {
		return nil
	}

	var updatedMatchers []any
	for i, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), preserving as-is", eventName, i, matcherAny)
			updatedMatchers = append(updatedMatchers, matcherAny)
			continue
		}

		hooksList := getHooksList(matcher, eventName, i)
		if hooksList == nil {
			updatedMatchers = append(updatedMatchers, matcher)
			continue
		}

		var remainingHooks []any
		for j, hookAny := range hooksList {
			hook, ok := hookAny.(map[string]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks[%d] has unexpected type %T (expected object), preserving as-is", eventName, i, j, hookAny)
				remainingHooks = append(remainingHooks, hookAny)
				continue
			}
			if !shouldRemove(hook) {
				remainingHooks = append(remainingHooks, hook)
			}
		}

		if len(remainingHooks) > 0 {
			matcher["hooks"] = remainingHooks
			updatedMatchers = append(updatedMatchers, matcher)
		}
	}
	return settings.setEventHooks(eventName, updatedMatchers)
}

// findHookInEvent searches for a hook matching a predicate across all matchers of an event.
func findHookInEvent(settings *ClaudeSettings, eventName string, matches func(map[string]any) bool) bool {
	eventHooks := settings.getEventHooks(eventName)
	for i, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), skipping", eventName, i, matcherAny)
			continue
		}
		for j, hookAny := range getHooksList(matcher, eventName, i) {
			hook, ok := hookAny.(map[string]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks[%d] has unexpected type %T (expected object), skipping", eventName, i, j, hookAny)
				continue
			}
			if matches(hook) {
				return true
			}
		}
	}
	return false
}

// InstallSyncHooks installs hooks for incremental sync daemon
// This installs both SessionStart (to start daemon) and SessionEnd (to stop daemon)
func InstallSyncHooks() error {
	binaryPath, err := GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}

	sessionStartHook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook session-start", binaryPath),
	}

	sessionEndHook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook session-end", binaryPath),
	}

	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		if err := installHook(settings, sessionStartHook, "SessionStart", "*", true); err != nil {
			return err
		}
		return installHook(settings, sessionEndHook, "SessionEnd", "*", true)
	})
}

// UninstallSyncHooks removes the sync daemon hooks.
// This handles both old ("sync start/stop") and new ("hook session-start/end") patterns.
func UninstallSyncHooks() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		isSyncHook := func(hook map[string]any) bool {
			cmd, _ := hook["command"].(string)
			return hook["type"] == "command" &&
				(isConfabCommand(cmd) ||
					strings.Contains(cmd, "sync start") ||
					strings.Contains(cmd, "sync stop") ||
					strings.Contains(cmd, "hook session-start") ||
					strings.Contains(cmd, "hook session-end"))
		}
		if err := removeHooksFromEvent(settings, "SessionStart", isSyncHook); err != nil {
			return err
		}
		return removeHooksFromEvent(settings, "SessionEnd", isSyncHook)
	})
}

// IsSyncHooksInstalled checks if sync daemon hooks are installed
// This checks for both old ("sync start/stop") and new ("hook session-start/end") patterns
func IsSyncHooksInstalled() (bool, error) {
	settings, err := ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}

	// Check for either old or new pattern for SessionStart
	hasStart := hasHookWithCommand(settings, "SessionStart", "sync start") ||
		hasHookWithCommand(settings, "SessionStart", "hook session-start")

	// Check for either old or new pattern for SessionEnd
	hasEnd := hasHookWithCommand(settings, "SessionEnd", "sync stop") ||
		hasHookWithCommand(settings, "SessionEnd", "hook session-end")

	return hasStart && hasEnd, nil
}

// hasHookWithCommand checks if a confab hook with the given command substring exists
func hasHookWithCommand(settings *ClaudeSettings, eventName, cmdSubstring string) bool {
	return findHookInEvent(settings, eventName, func(hook map[string]any) bool {
		cmd, _ := hook["command"].(string)
		return hook["type"] == "command" && isConfabCommand(cmd) && strings.Contains(cmd, cmdSubstring)
	})
}

// InstallPreToolUseHooks installs the PreToolUse hook for git commit validation.
// This installs a hook with a "Bash" matcher to intercept git commit commands.
func InstallPreToolUseHooks() error {
	binaryPath, err := GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}

	preToolUseHook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook pre-tool-use", binaryPath),
	}

	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		for _, matcher := range toolUseMatchers {
			if err := installHook(settings, preToolUseHook, "PreToolUse", matcher, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// Tool names for PreToolUse/PostToolUse hook matching
const (
	ToolNameBash              = "Bash"
	ToolNameMCPGitHubCreatePR = "mcp__github__create_pull_request"
)

// toolUseMatchers are the tool names we intercept for session linking and PR tracking.
var toolUseMatchers = []string{
	ToolNameBash,              // git commit, gh pr create
	ToolNameMCPGitHubCreatePR, // GitHub MCP tool
}

// UninstallPreToolUseHooks removes the PreToolUse hook
func UninstallPreToolUseHooks() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		return removeHooksFromEvent(settings, "PreToolUse", isConfabHookEntry)
	})
}

// IsPreToolUseHooksInstalled checks if the PreToolUse hook is installed
func IsPreToolUseHooksInstalled() (bool, error) {
	settings, err := ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}

	return hasHookWithCommand(settings, "PreToolUse", "hook pre-tool-use"), nil
}

// InstallPostToolUseHooks installs the PostToolUse hook for GitHub link tracking.
// This installs hooks with "Bash" and MCP matchers to capture PR creation output.
func InstallPostToolUseHooks() error {
	binaryPath, err := GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}

	postToolUseHook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook post-tool-use", binaryPath),
	}

	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		for _, matcher := range toolUseMatchers {
			if err := installHook(settings, postToolUseHook, "PostToolUse", matcher, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// UninstallPostToolUseHooks removes the PostToolUse hook
func UninstallPostToolUseHooks() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		return removeHooksFromEvent(settings, "PostToolUse", isConfabHookEntry)
	})
}

// IsPostToolUseHooksInstalled checks if the PostToolUse hook is installed
func IsPostToolUseHooksInstalled() (bool, error) {
	settings, err := ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}

	return hasHookWithCommand(settings, "PostToolUse", "hook post-tool-use"), nil
}

// InstallUserPromptSubmitHook installs the UserPromptSubmit hook.
// Unlike other hooks, UserPromptSubmit doesn't use matchers.
func InstallUserPromptSubmitHook() error {
	binaryPath, err := GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}

	hook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook user-prompt-submit", binaryPath),
	}

	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		return installHook(settings, hook, "UserPromptSubmit", "", false)
	})
}

// UninstallUserPromptSubmitHook removes the UserPromptSubmit hook
func UninstallUserPromptSubmitHook() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		return removeHooksFromEvent(settings, "UserPromptSubmit", isConfabHookEntry)
	})
}

// IsUserPromptSubmitHookInstalled checks if the UserPromptSubmit hook is installed
func IsUserPromptSubmitHookInstalled() (bool, error) {
	settings, err := ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}

	return hasHookWithCommand(settings, "UserPromptSubmit", "hook user-prompt-submit"), nil
}

