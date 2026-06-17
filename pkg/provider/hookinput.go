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

// opencodeHookInputAdapter wraps *types.OpenCodeHookInput so it satisfies
// the HookInput interface. OpenCode has no transcript_path, so
// TranscriptPath() returns "".
type opencodeHookInputAdapter struct{ inner *types.OpenCodeHookInput }

func (a opencodeHookInputAdapter) SessionID() string      { return a.inner.SessionID }
func (a opencodeHookInputAdapter) TranscriptPath() string { return "" }
func (a opencodeHookInputAdapter) CWD() string            { return a.inner.CWD }
func (a opencodeHookInputAdapter) HookEventName() string  { return "" }
func (a opencodeHookInputAdapter) ParentPID() int         { return a.inner.ParentPID }

// cursorHookInputAdapter wraps *types.CursorHookInput so it satisfies the
// HookInput interface. TranscriptPath() returns the (possibly derived) path
// the provider populated at sessionStart. CWD() reports the first workspace
// root, which Cursor uses as the session's working directory.
type cursorHookInputAdapter struct{ inner *types.CursorHookInput }

func (a cursorHookInputAdapter) SessionID() string      { return a.inner.SessionID }
func (a cursorHookInputAdapter) TranscriptPath() string { return a.inner.TranscriptPath }
func (a cursorHookInputAdapter) CWD() string {
	if len(a.inner.WorkspaceRoots) > 0 {
		return a.inner.WorkspaceRoots[0]
	}
	return ""
}
func (a cursorHookInputAdapter) HookEventName() string { return a.inner.HookEventName }
func (a cursorHookInputAdapter) ParentPID() int        { return a.inner.ParentPID }

var (
	_ HookInput = claudeHookInputAdapter{}
	_ HookInput = codexHookInputAdapter{}
	_ HookInput = opencodeHookInputAdapter{}
	_ HookInput = cursorHookInputAdapter{}
)
