package utils

import "time"

// TruncateSecret safely truncates a secret string for display.
// Returns a string like "abc123...wxyz" showing prefix and suffix.
// If the string is too short, returns a masked version.
func TruncateSecret(s string, prefixLen, suffixLen int) string {
	minLen := prefixLen + suffixLen
	if len(s) < minLen {
		// String too short - mask it entirely
		if len(s) == 0 {
			return "(empty)"
		}
		return "***"
	}
	return s[:prefixLen] + "..." + s[len(s)-suffixLen:]
}

// TruncateEnd shortens a string for display by keeping the beginning
// and adding ellipsis at the end if it exceeds maxLen
func TruncateEnd(s string, maxLen int) string {
	if maxLen < 4 {
		maxLen = 4
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// HTTP client timeouts
const (
	// DefaultHTTPTimeout is used for API calls (validation, sync uploads, etc.)
	DefaultHTTPTimeout = 30 * time.Second
)
