package discovery

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/ConfabulousDev/confab/pkg/types"
)

// ReadHookInputFrom reads and parses hook data from the given reader
func ReadHookInputFrom(r io.Reader) (*types.HookInput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	var input types.HookInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("failed to parse hook input: %w", err)
	}

	// Basic validation
	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if input.TranscriptPath == "" {
		return nil, fmt.Errorf("transcript_path is required")
	}

	return &input, nil
}
