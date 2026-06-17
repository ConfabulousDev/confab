package provider

import (
	"os"
	"os/exec"
)

// LookPath is the package-level seam tests stub to simulate CLI presence.
var LookPath = exec.LookPath

// StateDirPresent is the package-level seam tests stub to simulate a
// provider's state/config dir being present. It reports whether the dir
// into which the provider installs its hooks/plugins exists — the
// "configured locally" signal (desktop-app or CLI) that complements
// binary-on-PATH. Defaults to stateDirPresent (inspects the filesystem).
var StateDirPresent = stateDirPresent

// stateDirPresent reports whether the provider's StateDir() exists and is
// a directory. A StateDir() resolution error or a non-directory at the
// path reads as absent. This is the hook/plugin install target for every
// provider (Claude ~/.claude, Codex ~/.codex, OpenCode ~/.config/opencode),
// so its presence is the litmus for "we should configure this provider".
func stateDirPresent(p Provider) bool {
	dir, err := p.StateDir()
	if err != nil {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// orderedNames is the fixed registry order callers iterate in, so output
// is deterministic regardless of map lookup order.
var orderedNames = []string{NameClaudeCode, NameCodex, NameOpencode, NameCursor}

// OrderedNames returns the canonical provider names in fixed registry
// order. Callers that render or detect per-provider iterate this instead
// of re-hardcoding the list. Returns a fresh copy each call.
func OrderedNames() []string {
	return append([]string(nil), orderedNames...)
}

// DetectInstalled returns the canonical names of providers a user
// actually uses — those whose CLI binary is on PATH OR whose state/config
// dir is present (desktop-app or CLI-uninstalled installs) — in fixed
// registry order. Each provider appears at most once. Result is never nil
// but may be empty.
func DetectInstalled() []string {
	out := make([]string, 0, len(orderedNames))
	for _, name := range orderedNames {
		p, err := Get(name)
		if err != nil {
			continue
		}
		_, onPath := LookPath(p.CLIBinaryName())
		if onPath == nil || StateDirPresent(p) {
			out = append(out, name)
		}
	}
	return out
}
