package provider

import "github.com/ConfabulousDev/confab/pkg/types"

// claudeHookInputAdapter wraps *types.ClaudeHookInput so it satisfies the
// HookInput interface. The struct's exported SessionID field collides
// with a SessionID() method, so an adapter is required.
type claudeHookInputAdapter struct{ inner *types.ClaudeHookInput }

func (a claudeHookInputAdapter) SessionID() string      { return a.inner.SessionID }
func (a claudeHookInputAdapter) TranscriptPath() string { return a.inner.TranscriptPath }
func (a claudeHookInputAdapter) CWD() string            { return a.inner.CWD }
func (a claudeHookInputAdapter) HookEventName() string  { return a.inner.HookEventName }
func (a claudeHookInputAdapter) ParentPID() int         { return a.inner.ParentPID }

// codexHookInputAdapter wraps *types.CodexHookInput symmetrically.
type codexHookInputAdapter struct{ inner *types.CodexHookInput }

func (a codexHookInputAdapter) SessionID() string      { return a.inner.SessionID }
func (a codexHookInputAdapter) TranscriptPath() string { return a.inner.TranscriptPath }
func (a codexHookInputAdapter) CWD() string            { return a.inner.CWD }
func (a codexHookInputAdapter) HookEventName() string  { return a.inner.HookEventName }
func (a codexHookInputAdapter) ParentPID() int         { return a.inner.ParentPID }

var (
	_ HookInput = claudeHookInputAdapter{}
	_ HookInput = codexHookInputAdapter{}
)
