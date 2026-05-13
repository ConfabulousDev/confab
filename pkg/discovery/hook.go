package discovery

import (
	"io"

	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/types"
)

// ReadHookInputFrom reads and parses hook data from the given reader.
// It delegates to types.ReadClaudeHookInput and additionally validates that
// transcript_path is non-empty and safe (required by SessionStart/SessionEnd hooks).
func ReadHookInputFrom(r io.Reader) (*types.ClaudeHookInput, error) {
	return provider.ClaudeCode{}.ReadSessionHookInput(r)
}
