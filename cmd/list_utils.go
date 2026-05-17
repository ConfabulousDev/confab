package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/ConfabulousDev/confab/pkg/provider"
)

// parseDuration parses a duration string like "5d", "12h", "30m".
// Returns the duration. If empty string, returns 0 (no filter).
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}

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

// scanAndFilterSessions returns the provider's sessions, optionally filtered
// by duration, sorted most-recent first.
func scanAndFilterSessions(p provider.Provider, durationStr string) ([]provider.SessionInfo, error) {
	duration, err := parseDuration(durationStr)
	if err != nil {
		return nil, err
	}

	sessions, err := p.ScanSessions()
	if err != nil {
		return nil, fmt.Errorf("failed to scan sessions: %w", err)
	}

	if len(sessions) == 0 {
		return nil, nil
	}

	if duration > 0 {
		cutoff := time.Now().Add(-duration)
		var filtered []provider.SessionInfo
		for _, s := range sessions {
			if s.ModTime.After(cutoff) {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}
