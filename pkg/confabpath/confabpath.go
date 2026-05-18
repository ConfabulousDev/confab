// Package confabpath builds paths under the user's ~/.confab directory,
// where all confab local state (config, sync state, inboxes, logs,
// update timestamps) lives. Stdlib-only so any package can depend on it.
package confabpath

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns the absolute path of ~/.confab.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".confab"), nil
}

// Subpath joins ~/.confab with one or more path segments. The first
// segment is required — call Dir() if you need just ~/.confab itself.
func Subpath(first string, rest ...string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{dir, first}, rest...)...), nil
}
