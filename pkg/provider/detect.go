package provider

import "os/exec"

// LookPath is the package-level seam tests stub to simulate CLI presence.
var LookPath = exec.LookPath

// detectOrder is the fixed registry order DetectInstalled returns, so
// callers see deterministic output regardless of LookPath call order.
var detectOrder = []string{NameClaudeCode, NameCodex}

// DetectInstalled returns the canonical names of providers whose CLI
// binary is on PATH, in fixed registry order. Result is never nil but
// may be empty.
func DetectInstalled() []string {
	out := make([]string, 0, len(detectOrder))
	for _, name := range detectOrder {
		p, err := Get(name)
		if err != nil {
			continue
		}
		if _, err := LookPath(p.CLIBinaryName()); err == nil {
			out = append(out, name)
		}
	}
	return out
}
