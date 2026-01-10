package sync

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/icza/backscanner"
)

// maxLinesToSearch is the number of lines to search from the end of each transcript
const maxLinesToSearch = 10

// FindSessionByLeafUUID searches transcript files in the given directory for a message
// with the specified uuid in the last N lines. Returns the session ID (filename without
// extension) if found, empty string otherwise.
//
// Excludes the file specified by excludeFile from the search.
func FindSessionByLeafUUID(transcriptDir, leafUUID, excludeFile string) string {
	entries, err := os.ReadDir(transcriptDir)
	if err != nil {
		logger.Debug("Failed to read transcript directory: %v", err)
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Skip non-jsonl files
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		// Skip agent files
		if strings.HasPrefix(name, "agent-") {
			continue
		}

		// Skip the current transcript
		if name == excludeFile {
			continue
		}

		// Check if this transcript has the leafUUID in its last lines
		filePath := filepath.Join(transcriptDir, name)
		if hasUUIDInLastLines(filePath, leafUUID) {
			// Return session ID (filename without .jsonl extension)
			return strings.TrimSuffix(name, ".jsonl")
		}
	}

	return ""
}

// hasUUIDInLastLines checks if a file contains a message with the given uuid
// in its last maxLinesToSearch lines, using reverse scanning for efficiency.
func hasUUIDInLastLines(filePath, targetUUID string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return false
	}

	scanner := backscanner.New(f, int(fi.Size()))

	for i := 0; i < maxLinesToSearch; i++ {
		line, _, err := scanner.Line()
		if err != nil {
			if err == io.EOF {
				break
			}
			return false
		}

		// Parse line and check for uuid field
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if uuid, ok := msg["uuid"].(string); ok && uuid == targetUUID {
			return true
		}
	}

	return false
}

// linkSummaryToPreviousSession finds the session matching leafUUID and updates its summary via the API.
// The summary should already be sanitized by the caller.
// Errors are logged but not returned (non-disruptive to main sync flow).
func (e *Engine) linkSummaryToPreviousSession(summary, leafUUID string) {
	logger.Debug("Found summary with leafUuid: %s", leafUUID)

	// Search for the previous session
	transcriptDir := filepath.Dir(e.transcriptPath)
	currentFile := filepath.Base(e.transcriptPath)
	previousSessionID := FindSessionByLeafUUID(transcriptDir, leafUUID, currentFile)

	if previousSessionID == "" {
		logger.Debug("No matching session found for leafUuid: %s", leafUUID)
		return
	}

	logger.Info("Linking summary to previous session: %s", previousSessionID)

	// Update the previous session's summary via API
	if err := e.client.UpdateSessionSummary(previousSessionID, summary); err != nil {
		logger.Error("Failed to update summary for session %s: %v", previousSessionID, err)
		return
	}

	logger.Info("Successfully updated summary for session %s", previousSessionID)
}
