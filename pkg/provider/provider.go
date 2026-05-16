package provider

import (
	"fmt"
	"io"
)

const (
	NameClaudeCode = "claude-code"
	NameCodex      = "codex"
)

// HookInput is the provider-agnostic view of a hook payload, exposing the
// fields used by daemon spawning and bookkeeping. Concrete shapes
// (types.ClaudeHookInput, types.CodexHookInput) satisfy this via adapter
// types defined in hookinput.go.
type HookInput interface {
	SessionID() string
	TranscriptPath() string
	CWD() string
	HookEventName() string
	ParentPID() int
}

// Provider abstracts per-tool local behavior. Adding a new provider means
// implementing this interface and registering the instance.
type Provider interface {
	Name() string
	StateDir() (string, error)
	FindParentPID() int
	IsProcess(pid int) bool

	// ParseSessionHook reads a SessionStart-style hook payload from r and
	// returns the provider-agnostic view.
	ParseSessionHook(r io.Reader) (HookInput, error)

	InstallHooks() (string, error)
	UninstallHooks() (string, error)
	IsHooksInstalled() (bool, error)

	// InstallSkills installs provider-specific Claude Code skills. No-op
	// for providers that don't ship skills.
	InstallSkills() error

	// WalkUpToRoot returns the root session ID and its rollout path. For
	// providers without a separate root file identifier (Claude Code),
	// rootPath is "".
	WalkUpToRoot(sessionID string) (rootID, rootPath string, err error)

	// ShouldSpawnForInput is the per-provider gate on whether a fresh
	// SessionStart should result in a daemon. Codex returns false for
	// subagent rollouts; Claude is always true.
	ShouldSpawnForInput(in HookInput) bool

	// WriteHookResponse writes a hook response payload to w. The response
	// shape is provider-specific but the (continue, suppressOutput,
	// systemMessage) tuple is shared.
	WriteHookResponse(w io.Writer, suppressOutput bool, systemMessage string) error
}

var registry = map[string]Provider{
	NameClaudeCode: ClaudeCode{},
	NameCodex:      Codex{},
}

// Get returns the registered Provider for name. An empty string resolves
// to Claude Code for backwards compatibility with NormalizeName.
func Get(name string) (Provider, error) {
	if name == "" {
		name = NameClaudeCode
	}
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q (expected %q or %q)",
			name, NameClaudeCode, NameCodex)
	}
	return p, nil
}

// NormalizeName returns the canonical provider name. Backed by the
// registry so it can't drift from the Provider list.
func NormalizeName(name string) (string, error) {
	p, err := Get(name)
	if err != nil {
		return "", err
	}
	return p.Name(), nil
}
