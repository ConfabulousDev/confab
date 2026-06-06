package provider

import "testing"

// ocFakeInput satisfies HookInput plus the optional SessionParentID() accessor
// that Opencode.ShouldSpawnForInput type-asserts.
type ocFakeInput struct {
	sessionID string
	parentID  string
}

func (f ocFakeInput) SessionID() string       { return f.sessionID }
func (f ocFakeInput) TranscriptPath() string  { return "" }
func (f ocFakeInput) CWD() string             { return "" }
func (f ocFakeInput) HookEventName() string   { return "" }
func (f ocFakeInput) ParentPID() int          { return 0 }
func (f ocFakeInput) SessionParentID() string { return f.parentID }

func TestOpencodeShouldSpawnRootSession(t *testing.T) {
	if !(Opencode{}).ShouldSpawnForInput(ocFakeInput{sessionID: "ses_root"}) {
		t.Error("root session (no parent id) must spawn a daemon")
	}
}

func TestOpencodeShouldSpawnSuppressesSubagent(t *testing.T) {
	if (Opencode{}).ShouldSpawnForInput(ocFakeInput{sessionID: "ses_child", parentID: "ses_root"}) {
		t.Error("subagent session (parent id set) must be suppressed")
	}
}

// inputWithoutAccessor lacks SessionParentID(); such an input is treated as root.
type inputWithoutAccessor struct{}

func (inputWithoutAccessor) SessionID() string      { return "x" }
func (inputWithoutAccessor) TranscriptPath() string { return "" }
func (inputWithoutAccessor) CWD() string            { return "" }
func (inputWithoutAccessor) HookEventName() string  { return "" }
func (inputWithoutAccessor) ParentPID() int         { return 0 }

func TestOpencodeShouldSpawnWithoutAccessorIsRoot(t *testing.T) {
	if !(Opencode{}).ShouldSpawnForInput(inputWithoutAccessor{}) {
		t.Error("input lacking SessionParentID() must be treated as root")
	}
}
