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

// NewClaudeSettings returns an empty ClaudeSettings. Useful for tests
// and for callers that want to build a settings object before writing.
func NewClaudeSettings() *ClaudeSettings {
	return &ClaudeSettings{raw: make(map[string]any)}
}

// MarshalJSON implements json.Marshaler so callers can inspect the
// serialized shape directly (used by tests).
func (s *ClaudeSettings) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.raw)
}

// ErrHooksTypeMismatch is returned when the "hooks" field in settings.json
// exists but is not a JSON object. This prevents silently overwriting user config.
var ErrHooksTypeMismatch = errors.New("settings.json: 'hooks' field exists but is not a JSON object — please fix manually")

// GetHooksMap returns the hooks map, creating it if it doesn't exist.
// Returns an error if the hooks field exists but has the wrong type,
// to prevent silently overwriting user configuration.
func (s *ClaudeSettings) GetHooksMap() (map[string]any, error) {
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

// GetEventHooks returns the array of matchers for an event, as []any.
// This is a read-only operation that does not create the hooks map if it doesn't exist.
func (s *ClaudeSettings) GetEventHooks(eventName string) []any {
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

// SetEventHooks sets the array of matchers for an event.
// If matchers is nil or empty, the event key is removed.
// If the hooks map becomes empty, it is removed from settings.
func (s *ClaudeSettings) SetEventHooks(eventName string, matchers []any) error {
	hooks, err := s.GetHooksMap()
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

// Tool names for PreToolUse/PostToolUse hook matching.
const (
	ToolNameBash              = "Bash"
	ToolNameMCPGitHubCreatePR = "mcp__github__create_pull_request"
)

