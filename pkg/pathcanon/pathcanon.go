// Package pathcanon canonicalizes filesystem directory paths so that two
// different spellings of the same directory compare equal. It is a
// stdlib-only leaf so both pkg/config and pkg/provider can depend on it
// without creating an import cycle.
//
// Canonicalization is the matching contract behind per-(provider, dir)
// backend bindings (kata hpec): `setup` stores a binding under
// CanonicalDir(configDir), and at runtime the hook derives the config dir
// from the transcript path and looks it up under CanonicalDir(derivedDir).
// Because both sides pass through this same function on the same logical
// directory (which exists at both times), relative form, "."/".."/"//",
// trailing slash, tilde, and symlinked components all converge.
package pathcanon

import (
	"os"
	"path/filepath"
	"strings"
)

// CanonicalDir returns a canonical form of dir: tilde-expanded, made
// absolute, lexically cleaned, and with symlinks resolved when the path
// exists (falling back to the lexical form when it cannot be resolved).
// Two paths that name the same directory return the same string.
//
// An empty input is returned unchanged (callers use "" to mean "the default
// dir", which has no filesystem identity to canonicalize).
func CanonicalDir(dir string) string {
	if dir == "" {
		return ""
	}

	expanded := expandTilde(dir)

	// Lexical absolute form is the floor we always return; symlink
	// resolution only refines it when the path actually resolves.
	lexical := expanded
	if abs, err := filepath.Abs(expanded); err == nil {
		lexical = abs
	}
	lexical = filepath.Clean(lexical)

	if resolved, err := filepath.EvalSymlinks(lexical); err == nil {
		return resolved
	}
	return lexical
}

// expandTilde replaces a leading "~" (alone or followed by a separator) with
// the user's home directory. Other forms (e.g. "~user") are left untouched.
func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[1:])
}
