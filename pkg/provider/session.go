package provider

import (
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

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

// TruncateUTF8 returns s truncated so its byte length is at most maxBytes,
// without splitting a multi-byte rune. If truncation occurs and maxBytes >= 3,
// appends "..." (3 ASCII bytes) to indicate continuation. Returns s unchanged
// if len(s) <= maxBytes. Returns "" when maxBytes <= 0.
func TruncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) == 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// If maxBytes is too small to hold any content plus "...", just
	// truncate to maxBytes without a suffix.
	if maxBytes < 3 {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut]
	}
	// Reserve space for the "..." suffix.
	cut := maxBytes - 3
	if cut <= 0 {
		return "..."
	}
	// Back up to a valid UTF-8 boundary.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
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

// pathIsUnderAnyRoot reports whether cleaned (an absolute, cleaned path)
// lies under any of the allowedRoots after resolving parent-directory
// symlinks. Falls back to lexical containment if symlink resolution fails.
func pathIsUnderAnyRoot(cleaned string, allowedRoots []string) bool {
	parentDir := filepath.Dir(cleaned)
	resolvedParent, parentErr := filepath.EvalSymlinks(parentDir)
	resolvedPath := ""
	if parentErr == nil {
		resolvedPath = filepath.Join(resolvedParent, filepath.Base(cleaned))
	}

	for _, root := range allowedRoots {
		cleanRoot := filepath.Clean(root)
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolvedRoot = cleanRoot
		}
		if parentErr == nil {
			if strings.HasPrefix(resolvedPath, resolvedRoot+string(filepath.Separator)) {
				return true
			}
		} else {
			// The file's parent may not exist yet when a fresh hook fires.
			// Fall back to lexical containment.
			if strings.HasPrefix(cleaned, cleanRoot+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// SessionMetadata is the parsed metadata for a transcript file or in-memory
// chunk. SummaryLinks are Claude-only and stay nil for other providers.
type SessionMetadata struct {
	Summary          string
	FirstUserMessage string
	SummaryLinks     []SummaryLink
}
