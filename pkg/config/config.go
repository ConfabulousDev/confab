package config

import (
	"encoding/json"
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

// getHooksMap returns the hooks map, creating it if needed
func (s *ClaudeSettings) getHooksMap() map[string]any {
	hooksRaw, exists := s.raw["hooks"]
	if !exists {
		hooks := make(map[string]any)
		s.raw["hooks"] = hooks
		return hooks
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		logger.Debug("settings.json: 'hooks' has unexpected type %T (expected object), creating new hooks map", hooksRaw)
		hooks = make(map[string]any)
		s.raw["hooks"] = hooks
	}
	return hooks
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
func (s *ClaudeSettings) setEventHooks(eventName string, matchers []any) {
	hooks := s.getHooksMap()

	if len(matchers) == 0 {
		// Remove the event key entirely instead of leaving null/empty
		delete(hooks, eventName)

		// If hooks map is now empty, remove it from settings
		if len(hooks) == 0 {
			delete(s.raw, "hooks")
		}
		return
	}

	hooks[eventName] = matchers
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
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
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
	if err := os.Chmod(tempPath, 0644); err != nil {
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
		// Install SessionStart hook
		installHookForEvent(settings, "SessionStart", sessionStartHook)

		// Install SessionEnd hook
		installHookForEvent(settings, "SessionEnd", sessionEndHook)

		return nil
	})
}

// installHookForEvent installs a hook for a specific event type
func installHookForEvent(settings *ClaudeSettings, eventName string, hook map[string]any) {
	eventHooks := settings.getEventHooks(eventName)

	// Check if hook is already installed in an existing "*" matcher
	for i, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), skipping", eventName, i, matcherAny)
			continue
		}
		if matcher["matcher"] != "*" {
			continue
		}

		// Found a "*" matcher, check if confab is already in its hooks
		hooksListRaw, exists := matcher["hooks"]
		var hooksList []any
		if exists {
			hooksList, ok = hooksListRaw.([]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks has unexpected type %T (expected array), treating as empty", eventName, i, hooksListRaw)
				hooksList = []any{}
			}
		}

		for j, existingHookAny := range hooksList {
			existingHook, ok := existingHookAny.(map[string]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks[%d] has unexpected type %T (expected object), skipping", eventName, i, j, existingHookAny)
				continue
			}
			cmd, _ := existingHook["command"].(string)
			if existingHook["type"] == "command" && isConfabCommand(cmd) {
				// Already installed, update it in place
				hooksList[j] = hook
				matcher["hooks"] = hooksList
				eventHooks[i] = matcher
				settings.setEventHooks(eventName, eventHooks)
				return
			}
		}

		// No existing confab hook, add to this matcher
		hooksList = append(hooksList, hook)
		matcher["hooks"] = hooksList
		eventHooks[i] = matcher
		settings.setEventHooks(eventName, eventHooks)
		return
	}

	// No "*" matcher found, create new one
	newMatcher := map[string]any{
		"matcher": "*",
		"hooks":   []any{hook},
	}
	eventHooks = append(eventHooks, newMatcher)
	settings.setEventHooks(eventName, eventHooks)
}

// UninstallSyncHooks removes the sync daemon hooks
// This handles both old ("sync start/stop") and new ("hook session-start/end") patterns
func UninstallSyncHooks() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		// Remove both old and new patterns for SessionStart
		uninstallSyncHookForEvent(settings, "SessionStart", "sync start")
		uninstallSyncHookForEvent(settings, "SessionStart", "hook session-start")

		// Remove both old and new patterns for SessionEnd
		uninstallSyncHookForEvent(settings, "SessionEnd", "sync stop")
		uninstallSyncHookForEvent(settings, "SessionEnd", "hook session-end")
		return nil
	})
}

// uninstallSyncHookForEvent removes sync hooks from a specific event type
// It removes hooks that either match isConfabCommand OR contain the syncCommand string
func uninstallSyncHookForEvent(settings *ClaudeSettings, eventName, syncCommand string) {
	eventHooks := settings.getEventHooks(eventName)
	if len(eventHooks) == 0 {
		return
	}

	var updatedMatchers []any
	for i, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), preserving as-is", eventName, i, matcherAny)
			updatedMatchers = append(updatedMatchers, matcherAny)
			continue
		}

		hooksListRaw, exists := matcher["hooks"]
		if !exists {
			updatedMatchers = append(updatedMatchers, matcher)
			continue
		}
		hooksList, ok := hooksListRaw.([]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d].hooks has unexpected type %T (expected array), preserving matcher as-is", eventName, i, hooksListRaw)
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

			cmd, _ := hook["command"].(string)
			isSyncHook := hook["type"] == "command" &&
				(isConfabCommand(cmd) || strings.Contains(cmd, syncCommand))
			if !isSyncHook {
				remainingHooks = append(remainingHooks, hook)
			}
		}

		if len(remainingHooks) > 0 {
			matcher["hooks"] = remainingHooks
			updatedMatchers = append(updatedMatchers, matcher)
		}
	}
	settings.setEventHooks(eventName, updatedMatchers)
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
	eventHooks := settings.getEventHooks(eventName)
	for i, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), skipping", eventName, i, matcherAny)
			continue
		}
		hooksListRaw, exists := matcher["hooks"]
		if !exists {
			continue
		}
		hooksList, ok := hooksListRaw.([]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d].hooks has unexpected type %T (expected array), skipping", eventName, i, hooksListRaw)
			continue
		}
		for j, hookAny := range hooksList {
			hook, ok := hookAny.(map[string]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks[%d] has unexpected type %T (expected object), skipping", eventName, i, j, hookAny)
				continue
			}
			cmd, _ := hook["command"].(string)
			if hook["type"] == "command" && isConfabCommand(cmd) && strings.Contains(cmd, cmdSubstring) {
				return true
			}
		}
	}
	return false
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
		installPreToolUseHook(settings, preToolUseHook)
		return nil
	})
}

// Tool names for PreToolUse hook matching
const (
	ToolNameBash              = "Bash"
	ToolNameMCPGitHubCreatePR = "mcp__github__create_pull_request"
)

// preToolUseMatchers are the tool names we want to intercept for session linking.
var preToolUseMatchers = []string{
	ToolNameBash,              // git commit, gh pr create
	ToolNameMCPGitHubCreatePR, // GitHub MCP tool
}

// installPreToolUseHook installs hooks for PreToolUse with specific matchers.
func installPreToolUseHook(settings *ClaudeSettings, hook map[string]any) {
	for _, matcher := range preToolUseMatchers {
		installHookForMatcher(settings, hook, "PreToolUse", matcher)
	}
}

// installHookForMatcher installs a hook for a specific event and tool matcher.
func installHookForMatcher(settings *ClaudeSettings, hook map[string]any, eventName, matcherValue string) {
	eventHooks := settings.getEventHooks(eventName)

	// Check if hook is already installed in an existing matcher
	for i, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), skipping", eventName, i, matcherAny)
			continue
		}
		if matcher["matcher"] != matcherValue {
			continue
		}

		// Found matching matcher, check if confab is already in its hooks
		hooksListRaw, exists := matcher["hooks"]
		var hooksList []any
		if exists {
			hooksList, ok = hooksListRaw.([]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks has unexpected type %T (expected array), treating as empty", eventName, i, hooksListRaw)
				hooksList = []any{}
			}
		}

		for j, existingHookAny := range hooksList {
			existingHook, ok := existingHookAny.(map[string]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks[%d] has unexpected type %T (expected object), skipping", eventName, i, j, existingHookAny)
				continue
			}
			cmd, _ := existingHook["command"].(string)
			if existingHook["type"] == "command" && isConfabCommand(cmd) {
				// Already installed, update it in place
				hooksList[j] = hook
				matcher["hooks"] = hooksList
				eventHooks[i] = matcher
				settings.setEventHooks(eventName, eventHooks)
				return
			}
		}

		// No existing confab hook, add to this matcher
		hooksList = append(hooksList, hook)
		matcher["hooks"] = hooksList
		eventHooks[i] = matcher
		settings.setEventHooks(eventName, eventHooks)
		return
	}

	// No matching matcher found, create new one
	newMatcher := map[string]any{
		"matcher": matcherValue,
		"hooks":   []any{hook},
	}
	eventHooks = append(eventHooks, newMatcher)
	settings.setEventHooks(eventName, eventHooks)
}

// UninstallPreToolUseHooks removes the PreToolUse hook
func UninstallPreToolUseHooks() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		uninstallHook(settings, "PreToolUse")
		return nil
	})
}

// uninstallHook removes confab hooks from the specified event
func uninstallHook(settings *ClaudeSettings, eventName string) {
	eventHooks := settings.getEventHooks(eventName)
	if len(eventHooks) == 0 {
		return
	}

	var updatedMatchers []any
	for i, matcherAny := range eventHooks {
		matcher, ok := matcherAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), preserving as-is", eventName, i, matcherAny)
			updatedMatchers = append(updatedMatchers, matcherAny)
			continue
		}

		hooksListRaw, exists := matcher["hooks"]
		if !exists {
			updatedMatchers = append(updatedMatchers, matcher)
			continue
		}
		hooksList, ok := hooksListRaw.([]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d].hooks has unexpected type %T (expected array), preserving matcher as-is", eventName, i, hooksListRaw)
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

			cmd, _ := hook["command"].(string)
			isConfabHook := hook["type"] == "command" && isConfabCommand(cmd)
			if !isConfabHook {
				remainingHooks = append(remainingHooks, hook)
			}
		}

		if len(remainingHooks) > 0 {
			matcher["hooks"] = remainingHooks
			updatedMatchers = append(updatedMatchers, matcher)
		}
	}
	settings.setEventHooks(eventName, updatedMatchers)
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
		installPostToolUseHook(settings, postToolUseHook)
		return nil
	})
}

// postToolUseMatchers are the tool names we want to intercept for PR link tracking.
// Same as PreToolUse since we need to capture output from the same tools.
var postToolUseMatchers = []string{
	ToolNameBash,              // gh pr create
	ToolNameMCPGitHubCreatePR, // GitHub MCP tool
}

// installPostToolUseHook installs hooks for PostToolUse with specific matchers.
func installPostToolUseHook(settings *ClaudeSettings, hook map[string]any) {
	for _, matcher := range postToolUseMatchers {
		installHookForMatcher(settings, hook, "PostToolUse", matcher)
	}
}

// UninstallPostToolUseHooks removes the PostToolUse hook
func UninstallPostToolUseHooks() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		uninstallHook(settings, "PostToolUse")
		return nil
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
		installHookWithoutMatcher(settings, "UserPromptSubmit", hook)
		return nil
	})
}

// installHookWithoutMatcher installs a hook for events that don't use matchers
// (e.g., UserPromptSubmit). The structure is: { "hooks": [...] } without a "matcher" key.
func installHookWithoutMatcher(settings *ClaudeSettings, eventName string, hook map[string]any) {
	eventHooks := settings.getEventHooks(eventName)

	// Look for an existing entry without a matcher (or update existing confab hook)
	for i, entryAny := range eventHooks {
		entry, ok := entryAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), skipping", eventName, i, entryAny)
			continue
		}

		// Skip entries that have a matcher (shouldn't happen for UserPromptSubmit, but be safe)
		if _, hasMatcher := entry["matcher"]; hasMatcher {
			continue
		}

		hooksListRaw, exists := entry["hooks"]
		var hooksList []any
		if exists {
			hooksList, ok = hooksListRaw.([]any)
			if !ok {
				logger.Debug("settings.json: hooks[%q][%d].hooks has unexpected type %T (expected array), treating as empty", eventName, i, hooksListRaw)
				hooksList = []any{}
			}
		}

		// Check if confab hook already exists
		for j, existingHookAny := range hooksList {
			existingHook, ok := existingHookAny.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := existingHook["command"].(string)
			if existingHook["type"] == "command" && isConfabCommand(cmd) {
				// Update existing hook
				hooksList[j] = hook
				entry["hooks"] = hooksList
				eventHooks[i] = entry
				settings.setEventHooks(eventName, eventHooks)
				return
			}
		}

		// Add to existing entry
		hooksList = append(hooksList, hook)
		entry["hooks"] = hooksList
		eventHooks[i] = entry
		settings.setEventHooks(eventName, eventHooks)
		return
	}

	// No suitable entry found, create new one (without matcher)
	newEntry := map[string]any{
		"hooks": []any{hook},
	}
	eventHooks = append(eventHooks, newEntry)
	settings.setEventHooks(eventName, eventHooks)
}

// UninstallUserPromptSubmitHook removes the UserPromptSubmit hook
func UninstallUserPromptSubmitHook() error {
	return AtomicUpdateSettings(func(settings *ClaudeSettings) error {
		uninstallHook(settings, "UserPromptSubmit")
		return nil
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

