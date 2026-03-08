package discovery

import (
	"fmt"
	"io"

	"github.com/ConfabulousDev/confab/pkg/types"
)

// ReadHookInputFrom reads and parses hook data from the given reader.
// It delegates to types.ReadHookInput and additionally validates that
// transcript_path is non-empty (required by SessionStart/SessionEnd hooks).
func ReadHookInputFrom(r io.Reader) (*types.HookInput, error) {
	input, err := types.ReadHookInput(r)
	if err != nil {
		return nil, err
	}

	if input.TranscriptPath == "" {
		return nil, fmt.Errorf("transcript_path is required")
	}

	return input, nil
}
