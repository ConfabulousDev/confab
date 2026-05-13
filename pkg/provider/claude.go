package provider

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// ClaudeStateDirEnv is the environment variable to override the default Claude state directory.
const ClaudeStateDirEnv = "CONFAB_CLAUDE_DIR"

// ClaudeCode contains Claude Code-specific local behavior.
type ClaudeCode struct{}

// StateDir returns the Claude state directory.
// Defaults to ~/.claude but can be overridden with CONFAB_CLAUDE_DIR.
func (ClaudeCode) StateDir() (string, error) {
	if envDir := os.Getenv(ClaudeStateDirEnv); envDir != "" {
		return envDir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(home, ".claude"), nil
}

// ProjectsDir returns the Claude projects directory.
func (p ClaudeCode) ProjectsDir() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", fmt.Errorf("failed to get claude state directory: %w", err)
	}
	return filepath.Join(stateDir, "projects"), nil
}

// SettingsPath returns the Claude settings file path.
func (p ClaudeCode) SettingsPath() (string, error) {
	stateDir, err := p.StateDir()
	if err != nil {
		return "", fmt.Errorf("failed to get claude state directory: %w", err)
	}
	return filepath.Join(stateDir, "settings.json"), nil
}

// ReadHookInput reads and validates Claude hook JSON.
func (ClaudeCode) ReadHookInput(r io.Reader) (*types.ClaudeHookInput, error) {
	return types.ReadClaudeHookInput(r)
}

// ReadSessionHookInput reads Claude session hook JSON and validates transcript_path.
func (p ClaudeCode) ReadSessionHookInput(r io.Reader) (*types.ClaudeHookInput, error) {
	input, err := p.ReadHookInput(r)
	if err != nil {
		return nil, err
	}

	if input.TranscriptPath == "" {
		return nil, fmt.Errorf("transcript_path is required")
	}

	if err := p.ValidateTranscriptPath(input.TranscriptPath); err != nil {
		return nil, fmt.Errorf("invalid transcript_path: %w", err)
	}

	return input, nil
}

// ValidateTranscriptPath checks that a Claude transcript path is safe:
// - Must be absolute
// - Must not contain ".." components
// - Must resolve to a location under the Claude projects directory
func (p ClaudeCode) ValidateTranscriptPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("must be an absolute path")
	}

	cleaned := filepath.Clean(path)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return fmt.Errorf("must not contain '..' components")
		}
	}

	projectsDir, err := p.ProjectsDir()
	if err != nil {
		return err
	}

	allowedRoots := []string{projectsDir}
	if envDir := os.Getenv(ClaudeStateDirEnv); envDir != "" {
		// Preserve legacy validation behavior: older code treated CONFAB_CLAUDE_DIR
		// itself as the allowed transcript root for hook payloads.
		allowedRoots = append(allowedRoots, envDir)
	}

	parentDir := filepath.Dir(cleaned)
	resolvedParent, parentErr := filepath.EvalSymlinks(parentDir)
	resolvedPath := ""
	if parentErr == nil {
		resolvedPath = filepath.Join(resolvedParent, filepath.Base(cleaned))
	}

	for _, root := range allowedRoots {
		cleanRoot := filepath.Clean(root)
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolvedRoot = cleanRoot
		}
		if parentErr == nil {
			if strings.HasPrefix(resolvedPath, resolvedRoot+string(filepath.Separator)) {
				return nil
			}
		} else {
			// The transcript parent may not exist yet when a fresh hook fires.
			// In that case fall back to lexical validation after the traversal check above.
			if strings.HasPrefix(cleaned, cleanRoot+string(filepath.Separator)) {
				return nil
			}
		}
	}

	if len(allowedRoots) > 0 {
		return fmt.Errorf("must be under Claude projects directory (%s)", projectsDir)
	}
	return fmt.Errorf("must be under Claude projects directory")
}

// FindParentPID walks up the process tree to find the Claude Code process.
func (p ClaudeCode) FindParentPID() int {
	parentPID := os.Getppid()
	if p.IsProcess(parentPID) {
		return parentPID
	}

	grandparentPID := getParentPID(parentPID)
	if grandparentPID > 0 && p.IsProcess(grandparentPID) {
		return grandparentPID
	}

	logger.Warn("Could not find Claude in process tree, disabling parent PID monitoring")
	return 0
}

// IsProcess checks if the given PID is a Claude Code process.
func (p ClaudeCode) IsProcess(pid int) bool {
	cmd := getProcCmdline(pid)
	return p.MatchesProcess(cmd)
}

var claudeProcessPattern = regexp.MustCompile(`(?i)\bclaude\b`)

// MatchesProcess checks if a command string matches Claude Code.
func (ClaudeCode) MatchesProcess(cmd string) bool {
	return claudeProcessPattern.MatchString(cmd)
}

func getProcCmdline(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getParentPID(pid int) int {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}
