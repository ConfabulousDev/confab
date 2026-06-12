package cmd

import (
	"fmt"
	"io"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// toolUseHookInput is the provider-agnostic view of a PreToolUse /
// PostToolUse hook payload. Both ClaudeHookInput and CodexHookInput share
// these field names on the wire per their respective schemas, so the
// handlers in hook_pretooluse.go / hook_posttooluse.go work off this single
// shape regardless of which provider fired.
type toolUseHookInput struct {
	SessionID    string
	ToolName     string
	ToolInput    map[string]any
	ToolResponse map[string]any
	CWD          string
	// TranscriptPath drives per-(provider, dir) backend resolution (kata
	// hpec). Populated for Claude (used to derive the config dir); left empty
	// for Codex, which is not wired for --config-dir yet (default binding).
	TranscriptPath string
}

// readToolUseHookInput parses the per-provider JSON shape into a
// provider-agnostic toolUseHookInput. Returns an error for unknown
// providers so the caller can silently no-op.
func readToolUseHookInput(p provider.Provider, r io.Reader) (*toolUseHookInput, error) {
	switch p.Name() {
	case provider.NameClaudeCode:
		in, err := provider.ClaudeCode{}.ReadHookInput(r)
		if err != nil {
			return nil, err
		}
		return &toolUseHookInput{
			SessionID:      in.SessionID,
			ToolName:       in.ToolName,
			ToolInput:      in.ToolInput,
			ToolResponse:   in.ToolResponse,
			CWD:            in.CWD,
			TranscriptPath: in.TranscriptPath,
		}, nil
	case provider.NameCodex:
		in, err := provider.Codex{}.ReadHookInput(r)
		if err != nil {
			return nil, err
		}
		return &toolUseHookInput{
			SessionID:    in.SessionID,
			ToolName:     in.ToolName,
			ToolInput:    in.ToolInput,
			ToolResponse: in.ToolResponseMap(),
			CWD:          in.CWD,
		}, nil
	default:
		return nil, fmt.Errorf("provider %q does not support tool-use hook input", p.Name())
	}
}
