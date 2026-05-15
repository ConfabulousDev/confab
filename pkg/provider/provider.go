package provider

import "fmt"

const (
	NameClaudeCode = "claude-code"
	NameCodex      = "codex"
)

// NormalizeName returns the canonical provider name for CLI/provider selection.
func NormalizeName(name string) (string, error) {
	switch name {
	case "", NameClaudeCode:
		return NameClaudeCode, nil
	case NameCodex:
		return NameCodex, nil
	default:
		return "", fmt.Errorf("unsupported provider %q (expected %q or %q)", name, NameClaudeCode, NameCodex)
	}
}
