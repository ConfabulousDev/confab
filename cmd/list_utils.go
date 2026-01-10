package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/ConfabulousDev/confab/pkg/discovery"
)

// parseDuration parses a duration string like "5d", "12h", "30m"
// Returns the duration. If empty string, returns 0 (meaning no filter).
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}

	// Match pattern like "5d", "12h", "30m"
	re := regexp.MustCompile(`^(\d+)([dhm])$`)
	matches := re.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("invalid duration format: %s (use e.g., 5d, 12h, 30m)", s)
	}

	value, _ := strconv.Atoi(matches[1])
	unit := matches[2]

	switch unit {
	case "d":
		return time.Duration(value) * 24 * time.Hour, nil
	case "h":
		return time.Duration(value) * time.Hour, nil
	case "m":
		return time.Duration(value) * time.Minute, nil
	default:
		return 0, fmt.Errorf("invalid duration unit: %s", unit)
	}
}

// scanAndFilterSessions scans for sessions and optionally filters by duration.
// Returns sessions sorted by mod time (most recent first).
func scanAndFilterSessions(durationStr string) ([]discovery.SessionInfo, error) {
	// Parse duration filter
	duration, err := parseDuration(durationStr)
	if err != nil {
		return nil, err
	}

	// Scan for sessions
	sessions, err := discovery.ScanAllSessions()
	if err != nil {
		return nil, fmt.Errorf("failed to scan sessions: %w", err)
	}

	if len(sessions) == 0 {
		return nil, nil
	}

	// Filter by duration if specified
	if duration > 0 {
		cutoff := time.Now().Add(-duration)
		var filtered []discovery.SessionInfo
		for _, s := range sessions {
			if s.ModTime.After(cutoff) {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	// Sort by mod time (most recent first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}
