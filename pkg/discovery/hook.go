package discovery

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/types"
)

// ReadHookInputFrom reads and parses hook data from the given reader.
// It delegates to types.ReadHookInput and additionally validates that
// transcript_path is non-empty and safe (required by SessionStart/SessionEnd hooks).
func ReadHookInputFrom(r io.Reader) (*types.HookInput, error) {
	input, err := types.ReadHookInput(r)
	if err != nil {
		return nil, err
	}

	if input.TranscriptPath == "" {
		return nil, fmt.Errorf("transcript_path is required")
	}

	if err := validateTranscriptPath(input.TranscriptPath); err != nil {
		return nil, fmt.Errorf("invalid transcript_path: %w", err)
	}

	return input, nil
}

// validateTranscriptPath checks that a transcript path is safe:
// - Must be absolute
// - Must not contain ".." components
// - Must resolve to a location under the Claude projects directory
func validateTranscriptPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("must be an absolute path")
	}

	// Check for ".." in the cleaned path
	cleaned := filepath.Clean(path)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return fmt.Errorf("must not contain '..' components")
		}
	}

	// Resolve symlinks and verify the path is under the Claude projects directory
	claudeDir := os.Getenv("CONFAB_CLAUDE_DIR")
	if claudeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		claudeDir = filepath.Join(home, ".claude", "projects")
	}

	// Resolve the allowed directory (it may itself contain symlinks)
	resolvedClaudeDir, err := filepath.EvalSymlinks(claudeDir)
	if err != nil {
		// If the directory doesn't exist yet, use the raw path
		resolvedClaudeDir = claudeDir
	}

	// The transcript file may not exist yet (daemon waits for it),
	// so resolve the parent directory instead
	parentDir := filepath.Dir(cleaned)
	resolvedParent, err := filepath.EvalSymlinks(parentDir)
	if err != nil {
		// Parent doesn't exist yet — accept the cleaned path as-is
		// (the ".." check above already blocks traversal)
		resolvedParent = parentDir
	}
	resolvedPath := filepath.Join(resolvedParent, filepath.Base(cleaned))

	if !strings.HasPrefix(resolvedPath, resolvedClaudeDir+string(filepath.Separator)) {
		return fmt.Errorf("must be under Claude projects directory (%s)", claudeDir)
	}

	return nil
}
