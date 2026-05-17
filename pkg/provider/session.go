package provider

import (
	"os"
	"time"

	"github.com/ConfabulousDev/confab/pkg/types"
)

// maxLinesForExtraction caps how many transcript lines ExtractMetadata
// reads before giving up. Summary and first user message normally appear
// in the first handful of lines; capping keeps the scan-time cost bounded
// for both Claude and Codex.
const maxLinesForExtraction = 50

// readHeadLines reads up to maxLinesForExtraction JSONL lines from the
// start of path. Errors (open or scan) degrade to (nil, err); callers
// that tolerate missing files can ignore err and use the empty slice.
func readHeadLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := types.NewJSONLScanner(f)
	lines := make([]string, 0, maxLinesForExtraction)
	for scanner.Scan() && len(lines) < maxLinesForExtraction {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// SessionInfo is the cross-provider shape returned by Provider.ScanSessions
// and Provider.FindSessionByID. Concrete provider types may keep richer
// internal forms (e.g. CodexSessionInfo) and project to SessionInfo at
// the seams.
type SessionInfo struct {
	SessionID        string
	TranscriptPath   string
	ProjectPath      string
	ModTime          time.Time
	SizeBytes        int64
	Summary          string
	FirstUserMessage string
}

// SessionMetadata is the parsed metadata for a transcript file or in-memory
// chunk. SummaryLinks are Claude-only and stay nil for other providers.
type SessionMetadata struct {
	Summary          string
	FirstUserMessage string
	SummaryLinks     []SummaryLink
}
