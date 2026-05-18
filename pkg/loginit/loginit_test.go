package loginit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// Spec: ApplyLogLevel reads upload config's log_level and applies it.
// A "debug" config value sets the logger to DEBUG, so a DEBUG-level
// emission lands in the log file.
func TestApplyLogLevel_ValidLevel(t *testing.T) {
	logDir := setupLogger(t)
	configPath := writeTestConfig(t, map[string]any{
		"backend_url": "https://example.test",
		"api_key":     "cfb_aaaaaaaaaaaaaaaaaaaa",
		"log_level":   "debug",
	})
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	ApplyLogLevel()

	logger.Debug("probe-debug-line-xyz")

	if !logFileContains(t, logDir, "probe-debug-line-xyz") {
		t.Errorf("ApplyLogLevel(debug): DEBUG probe missing from log file; logger level was not set to DEBUG")
	}
}

// Spec: An invalid log_level value logs a warning and leaves the logger
// at its default level (INFO). DEBUG probes should NOT appear.
func TestApplyLogLevel_InvalidLevel(t *testing.T) {
	logDir := setupLogger(t)
	configPath := writeTestConfig(t, map[string]any{
		"backend_url": "https://example.test",
		"api_key":     "cfb_aaaaaaaaaaaaaaaaaaaa",
		"log_level":   "bogus",
	})
	t.Setenv("CONFAB_CONFIG_PATH", configPath)

	ApplyLogLevel()

	logger.Debug("probe-debug-not-allowed")

	if logFileContains(t, logDir, "probe-debug-not-allowed") {
		t.Errorf("ApplyLogLevel(bogus): DEBUG probe appeared in log file; level should remain at default INFO")
	}
	if !logFileContains(t, logDir, "Invalid log_level") {
		t.Errorf("ApplyLogLevel(bogus): expected warning about invalid log_level; not found in log")
	}
}

// Spec: Missing config file is a no-op (graceful degradation).
// DEBUG probes should NOT appear since level stays at default INFO.
func TestApplyLogLevel_MissingConfig(t *testing.T) {
	logDir := setupLogger(t)
	missing := filepath.Join(t.TempDir(), "no-such-config.json")
	t.Setenv("CONFAB_CONFIG_PATH", missing)

	ApplyLogLevel() // must not panic

	logger.Debug("probe-debug-no-config")

	if logFileContains(t, logDir, "probe-debug-no-config") {
		t.Errorf("ApplyLogLevel(missing): DEBUG probe unexpectedly present; missing config should be no-op")
	}
}

// setupLogger points the logger at a temp dir (instead of the test-mode
// discard sink) so we can observe what was actually emitted.
func setupLogger(t *testing.T) string {
	t.Helper()
	logDir := t.TempDir()
	t.Setenv("CONFAB_LOG_DIR", logDir)
	logger.ResetForTesting()
	t.Cleanup(logger.ResetForTesting)
	if err := logger.Init(); err != nil {
		t.Fatalf("logger.Init: %v", err)
	}
	return logDir
}

// logFileContains reads the rotating log file and reports whether it
// contains needle. Returns false if the file doesn't exist (logger
// hasn't written yet).
func logFileContains(t *testing.T, logDir, needle string) bool {
	t.Helper()
	logFile := filepath.Join(logDir, "confab.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("read log file %s: %v", logFile, err)
	}
	return strings.Contains(string(data), needle)
}

func writeTestConfig(t *testing.T, payload map[string]any) string {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
