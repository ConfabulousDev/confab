// ABOUTME: General announcement system for post-update feature notifications.
// ABOUTME: Each feature registers a check function and message; shown via systemMessage on session start.
package cmd

import (
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
)

// Announcement represents a feature announcement shown on session start.
type Announcement struct {
	// Check returns true if this announcement should be shown.
	Check func() bool
	// Setup runs if Check returns true (e.g., install a skill file).
	Setup func() error
	// Message is the text included in the systemMessage.
	Message string
}

// announcements is the registry of all feature announcements.
// Add new entries here when shipping features that need user notification.
var announcements = []Announcement{
	{
		Check:   func() bool { return !config.IsTilSkillInstalled() },
		Setup:   config.InstallTilSkill,
		Message: `/til is now available — capture TILs during your session. Try: /til "what you learned"`,
	},
}

// RunAnnouncements checks all pending announcements, runs setup for each,
// and returns a combined systemMessage string (empty if nothing to announce).
func RunAnnouncements() string {
	var messages []string
	for _, a := range announcements {
		if !a.Check() {
			continue
		}
		if err := a.Setup(); err != nil {
			logger.Debug("Announcement setup failed: %v", err)
			continue
		}
		messages = append(messages, a.Message)
	}
	return strings.Join(messages, "\n")
}
