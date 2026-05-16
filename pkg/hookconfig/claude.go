package hookconfig

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
)

// toolUseMatchers are the tool names we intercept for session linking
// and PR tracking.
var toolUseMatchers = []string{
	config.ToolNameBash,              // git commit, gh pr create
	config.ToolNameMCPGitHubCreatePR, // GitHub MCP tool
}

// isConfabCommand checks if a command string invokes the confab binary.
// More precise than substring contains to avoid false positives.
func isConfabCommand(command string) bool {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	return filepath.Base(parts[0]) == "confab"
}

// isConfabHookEntry returns true if a hook entry is a confab command hook.
func isConfabHookEntry(hook map[string]any) bool {
	cmd, _ := hook["command"].(string)
	return hook["type"] == "command" && isConfabCommand(cmd)
}

// getHooksList extracts and validates the hooks array from a matcher entry.
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
// When hasMatcher is true, looks for an entry whose "matcher" key equals
// matcherValue. When false, looks for an entry where "matcher" is absent.
func installHook(settings *config.ClaudeSettings, hook map[string]any, eventName, matcherValue string, hasMatcher bool) error {
	eventHooks := settings.GetEventHooks(eventName)

	for i, entryAny := range eventHooks {
		entry, ok := entryAny.(map[string]any)
		if !ok {
			logger.Debug("settings.json: hooks[%q][%d] has unexpected type %T (expected object), skipping", eventName, i, entryAny)
			continue
		}

		if hasMatcher {
			if entry["matcher"] != matcherValue {
				continue
			}
		} else {
			if _, has := entry["matcher"]; has {
				continue
			}
		}

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
				return settings.SetEventHooks(eventName, eventHooks)
			}
		}

		hooksList = append(hooksList, hook)
		entry["hooks"] = hooksList
		eventHooks[i] = entry
		return settings.SetEventHooks(eventName, eventHooks)
	}

	newEntry := map[string]any{
		"hooks": []any{hook},
	}
	if hasMatcher {
		newEntry["matcher"] = matcherValue
	}
	eventHooks = append(eventHooks, newEntry)
	return settings.SetEventHooks(eventName, eventHooks)
}

// removeHooksFromEvent removes hooks matching a predicate from all
// matchers of an event. Empty matchers are dropped.
func removeHooksFromEvent(settings *config.ClaudeSettings, eventName string, shouldRemove func(map[string]any) bool) error {
	eventHooks := settings.GetEventHooks(eventName)
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
	return settings.SetEventHooks(eventName, updatedMatchers)
}

// findHookInEvent searches for a hook matching a predicate across all
// matchers of an event.
func findHookInEvent(settings *config.ClaudeSettings, eventName string, matches func(map[string]any) bool) bool {
	eventHooks := settings.GetEventHooks(eventName)
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

// hasHookWithCommand returns true iff a confab hook command for the
// given event contains cmdSubstring.
func hasHookWithCommand(settings *config.ClaudeSettings, eventName, cmdSubstring string) bool {
	return findHookInEvent(settings, eventName, func(hook map[string]any) bool {
		cmd, _ := hook["command"].(string)
		return hook["type"] == "command" && isConfabCommand(cmd) && strings.Contains(cmd, cmdSubstring)
	})
}

// InstallSyncHooks installs SessionStart + SessionEnd hooks for the
// incremental sync daemon.
func InstallSyncHooks() error {
	binaryPath, err := config.GetBinaryPath()
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
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		if err := installHook(settings, sessionStartHook, "SessionStart", "*", true); err != nil {
			return err
		}
		return installHook(settings, sessionEndHook, "SessionEnd", "*", true)
	})
}

// UninstallSyncHooks removes the sync daemon hooks. Handles both old
// ("sync start/stop") and new ("hook session-start/end") patterns.
func UninstallSyncHooks() error {
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
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

// IsSyncHooksInstalled checks whether sync daemon hooks are installed.
// Recognizes both old ("sync start/stop") and new ("hook session-start/end").
func IsSyncHooksInstalled() (bool, error) {
	settings, err := config.ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}
	hasStart := hasHookWithCommand(settings, "SessionStart", "sync start") ||
		hasHookWithCommand(settings, "SessionStart", "hook session-start")
	hasEnd := hasHookWithCommand(settings, "SessionEnd", "sync stop") ||
		hasHookWithCommand(settings, "SessionEnd", "hook session-end")
	return hasStart && hasEnd, nil
}

// InstallPreToolUseHooks installs the PreToolUse hook for git commit
// validation. Installs with a "Bash" matcher to intercept git commits.
func InstallPreToolUseHooks() error {
	binaryPath, err := config.GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}
	preToolUseHook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook pre-tool-use", binaryPath),
	}
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		for _, matcher := range toolUseMatchers {
			if err := installHook(settings, preToolUseHook, "PreToolUse", matcher, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// UninstallPreToolUseHooks removes the PreToolUse hook.
func UninstallPreToolUseHooks() error {
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		return removeHooksFromEvent(settings, "PreToolUse", isConfabHookEntry)
	})
}

// IsPreToolUseHooksInstalled checks if the PreToolUse hook is installed.
func IsPreToolUseHooksInstalled() (bool, error) {
	settings, err := config.ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}
	return hasHookWithCommand(settings, "PreToolUse", "hook pre-tool-use"), nil
}

// InstallPostToolUseHooks installs the PostToolUse hook for GitHub
// link tracking.
func InstallPostToolUseHooks() error {
	binaryPath, err := config.GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}
	postToolUseHook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook post-tool-use", binaryPath),
	}
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		for _, matcher := range toolUseMatchers {
			if err := installHook(settings, postToolUseHook, "PostToolUse", matcher, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// UninstallPostToolUseHooks removes the PostToolUse hook.
func UninstallPostToolUseHooks() error {
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		return removeHooksFromEvent(settings, "PostToolUse", isConfabHookEntry)
	})
}

// IsPostToolUseHooksInstalled checks if the PostToolUse hook is installed.
func IsPostToolUseHooksInstalled() (bool, error) {
	settings, err := config.ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}
	return hasHookWithCommand(settings, "PostToolUse", "hook post-tool-use"), nil
}

// InstallUserPromptSubmitHook installs the UserPromptSubmit hook.
// Unlike other hooks, UserPromptSubmit doesn't use matchers.
func InstallUserPromptSubmitHook() error {
	binaryPath, err := config.GetBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}
	hook := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%s hook user-prompt-submit", binaryPath),
	}
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		return installHook(settings, hook, "UserPromptSubmit", "", false)
	})
}

// UninstallUserPromptSubmitHook removes the UserPromptSubmit hook.
func UninstallUserPromptSubmitHook() error {
	return config.AtomicUpdateSettings(func(settings *config.ClaudeSettings) error {
		return removeHooksFromEvent(settings, "UserPromptSubmit", isConfabHookEntry)
	})
}

// IsUserPromptSubmitHookInstalled checks if the UserPromptSubmit hook
// is installed.
func IsUserPromptSubmitHookInstalled() (bool, error) {
	settings, err := config.ReadSettings()
	if err != nil {
		return false, fmt.Errorf("failed to read settings: %w", err)
	}
	return hasHookWithCommand(settings, "UserPromptSubmit", "hook user-prompt-submit"), nil
}
