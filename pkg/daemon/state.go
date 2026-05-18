package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
	providerpkg "github.com/ConfabulousDev/confab/pkg/provider"
)

// State represents the daemon's persistent state
type State struct {
	Provider        string    `json:"provider,omitempty"`
	ExternalID      string    `json:"external_id"`
	TranscriptPath  string    `json:"transcript_path"`
	CWD             string    `json:"cwd"`
	PID             int       `json:"pid"`
	ParentPID       int       `json:"parent_pid,omitempty"` // Claude Code process ID
	InboxPath       string    `json:"inbox_path"`           // Path to event inbox (JSONL)
	StartedAt       time.Time `json:"started_at"`
	ConfabSessionID string    `json:"confab_session_id,omitempty"` // Backend session ID (set after Init)
}

// NewStateForProvider creates a daemon state under a provider namespace.
func NewStateForProvider(provider, externalID, transcriptPath, cwd string, parentPID int) *State {
	inboxPath, _ := GetInboxPathForProvider(provider, externalID)

	return &State{
		Provider:       provider,
		ExternalID:     externalID,
		TranscriptPath: transcriptPath,
		CWD:            cwd,
		PID:            os.Getpid(),
		ParentPID:      parentPID,
		InboxPath:      inboxPath,
		StartedAt:      time.Now(),
	}
}

func legacyStatePath(externalID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".confab", "sync", externalID+".json"), nil
}

// GetStatePathForProvider returns the namespaced state file path.
func GetStatePathForProvider(provider, externalID string) (string, error) {
	if provider == "" {
		return legacyStatePath(externalID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".confab", "sync", provider, externalID+".json"), nil
}

func legacyInboxPath(externalID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".confab", "sync", externalID+".inbox.jsonl"), nil
}

// GetInboxPathForProvider returns the namespaced inbox file path.
func GetInboxPathForProvider(provider, externalID string) (string, error) {
	if provider == "" {
		return legacyInboxPath(externalID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".confab", "sync", provider, externalID+".inbox.jsonl"), nil
}

// GetSyncDir returns the path to the sync state directory
func GetSyncDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".confab", "sync"), nil
}

// LoadStateForProvider reads a provider-namespaced state file. Claude Code falls
// back to the legacy flat path so old daemons and existing hooks keep working.
func LoadStateForProvider(provider, externalID string) (*State, error) {
	if provider == "" {
		path, err := legacyStatePath(externalID)
		if err != nil {
			return nil, err
		}
		return loadStateAt(path)
	}

	path, err := GetStatePathForProvider(provider, externalID)
	if err != nil {
		return nil, err
	}

	state, err := loadStateAt(path)
	if err != nil {
		return nil, err
	}
	if state != nil {
		if state.Provider == "" {
			state.Provider = provider
		}
		if provider == providerpkg.NameClaudeCode && !state.IsDaemonRunning() {
			legacyState, legacyErr := LoadStateForProvider("", externalID)
			if legacyErr != nil {
				return nil, legacyErr
			}
			if legacyState != nil && legacyState.IsDaemonRunning() {
				return legacyState, nil
			}
		}
		return state, nil
	}

	if provider == providerpkg.NameClaudeCode {
		return LoadStateForProvider("", externalID)
	}
	return nil, nil
}

func loadStateAt(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read sync state file (%s): %w", path, err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("sync state file has invalid JSON (%s): %w", path, err)
	}

	return &state, nil
}

// Save writes the state to disk
func (s *State) Save() error {
	path, err := GetStatePathForProvider(s.Provider, s.ExternalID)
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create sync directory: %w", err)
	}

	// Marshal state to JSON
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write atomically via temp file
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	return nil
}

// Delete removes the state file from disk
func (s *State) Delete() error {
	path, err := GetStatePathForProvider(s.Provider, s.ExternalID)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete state file: %w", err)
	}

	return nil
}

// IsDaemonRunning checks if the daemon process is still alive
func (s *State) IsDaemonRunning() bool {
	return isProcessRunning(s.PID)
}

// IsParentRunning checks if the parent Claude Code process is still alive
func (s *State) IsParentRunning() bool {
	return isProcessRunning(s.ParentPID)
}

// isProcessRunning checks if a process with the given PID is still alive
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// ListAllStates returns all active sync states
func ListAllStates() ([]*State, error) {
	syncDir, err := GetSyncDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(syncDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read sync directory: %w", err)
	}

	var states []*State
	for _, entry := range entries {
		if entry.IsDir() {
			provider := entry.Name()
			providerDir := filepath.Join(syncDir, provider)
			providerStates, err := listStatesInDir(providerDir, provider)
			if err != nil {
				logger.Debug("Skipping provider state dir %s: %v", provider, err)
				continue
			}
			states = append(states, providerStates...)
			continue
		}

		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		externalID := strings.TrimSuffix(entry.Name(), ".json")
		state, err := LoadStateForProvider("", externalID)
		if err != nil {
			logger.Debug("Skipping invalid state file %s: %v", externalID, err)
			continue
		}
		if state != nil && state.Provider == "" {
			state.Provider = providerpkg.NameClaudeCode
		}
		if state != nil {
			states = append(states, state)
		}
	}

	return states, nil
}

func listStatesInDir(dir, provider string) ([]*State, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var states []*State
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		state, err := loadStateAt(path)
		if err != nil {
			logger.Debug("Skipping invalid state file %s: %v", path, err)
			continue
		}
		if state != nil {
			if state.Provider == "" {
				state.Provider = provider
			}
			states = append(states, state)
		}
	}
	return states, nil
}
