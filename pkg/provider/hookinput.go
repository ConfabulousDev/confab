package provider

import "github.com/ConfabulousDev/confab/pkg/types"

// claudeHookInputAdapter wraps *types.ClaudeHookInput so it satisfies the
// HookInput interface. Inner() returns the underlying typed struct for
// callers that need hook-specific fields (Prompt, ToolName, etc.).
type claudeHookInputAdapter struct{ inner *types.ClaudeHookInput }

func (a claudeHookInputAdapter) SessionID() string             { return a.inner.SessionID }
func (a claudeHookInputAdapter) TranscriptPath() string        { return a.inner.TranscriptPath }
func (a claudeHookInputAdapter) CWD() string                   { return a.inner.CWD }
func (a claudeHookInputAdapter) HookEventName() string         { return a.inner.HookEventName }
func (a claudeHookInputAdapter) ParentPID() int                { return a.inner.ParentPID }
func (a claudeHookInputAdapter) Inner() *types.ClaudeHookInput { return a.inner }

// codexHookInputAdapter wraps *types.CodexHookInput symmetrically.
type codexHookInputAdapter struct{ inner *types.CodexHookInput }

func (a codexHookInputAdapter) SessionID() string            { return a.inner.SessionID }
func (a codexHookInputAdapter) TranscriptPath() string       { return a.inner.TranscriptPath }
func (a codexHookInputAdapter) CWD() string                  { return a.inner.CWD }
func (a codexHookInputAdapter) HookEventName() string        { return a.inner.HookEventName }
func (a codexHookInputAdapter) ParentPID() int               { return a.inner.ParentPID }
func (a codexHookInputAdapter) Inner() *types.CodexHookInput { return a.inner }

var (
	_ HookInput = claudeHookInputAdapter{}
	_ HookInput = codexHookInputAdapter{}
)
