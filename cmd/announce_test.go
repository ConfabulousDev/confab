// ABOUTME: Tests for the general announcement system.
// ABOUTME: Validates check/setup/message flow, combined messages, and failure handling.
package cmd

import (
	"fmt"
	"strings"
	"testing"
)

func TestRunAnnouncements_NoPending(t *testing.T) {
	// Save and restore
	orig := announcements
	defer func() { announcements = orig }()

	announcements = []Announcement{
		{
			Check:   func() bool { return false },
			Setup:   func() error { return nil },
			Message: "should not appear",
		},
	}

	msg := RunAnnouncements()
	if msg != "" {
		t.Errorf("RunAnnouncements() = %q, want empty", msg)
	}
}

func TestRunAnnouncements_OnePending(t *testing.T) {
	orig := announcements
	defer func() { announcements = orig }()

	setupCalled := false
	announcements = []Announcement{
		{
			Check:   func() bool { return true },
			Setup:   func() error { setupCalled = true; return nil },
			Message: "Feature A is available",
		},
	}

	msg := RunAnnouncements()
	if msg != "Feature A is available" {
		t.Errorf("RunAnnouncements() = %q, want %q", msg, "Feature A is available")
	}
	if !setupCalled {
		t.Error("Setup was not called")
	}
}

func TestRunAnnouncements_MultiplePending(t *testing.T) {
	orig := announcements
	defer func() { announcements = orig }()

	announcements = []Announcement{
		{
			Check:   func() bool { return true },
			Setup:   func() error { return nil },
			Message: "Feature A",
		},
		{
			Check:   func() bool { return true },
			Setup:   func() error { return nil },
			Message: "Feature B",
		},
	}

	msg := RunAnnouncements()
	if !strings.Contains(msg, "Feature A") || !strings.Contains(msg, "Feature B") {
		t.Errorf("RunAnnouncements() = %q, want both Feature A and B", msg)
	}
}

func TestRunAnnouncements_SetupFailure(t *testing.T) {
	orig := announcements
	defer func() { announcements = orig }()

	announcements = []Announcement{
		{
			Check:   func() bool { return true },
			Setup:   func() error { return fmt.Errorf("disk full") },
			Message: "should not appear",
		},
	}

	msg := RunAnnouncements()
	if msg != "" {
		t.Errorf("RunAnnouncements() = %q, want empty when setup fails", msg)
	}
}

func TestRunAnnouncements_IdempotentAfterSetup(t *testing.T) {
	orig := announcements
	defer func() { announcements = orig }()

	installed := false
	announcements = []Announcement{
		{
			Check:   func() bool { return !installed },
			Setup:   func() error { installed = true; return nil },
			Message: "Feature A",
		},
	}

	// First call: should announce
	msg1 := RunAnnouncements()
	if msg1 != "Feature A" {
		t.Errorf("First call: RunAnnouncements() = %q, want %q", msg1, "Feature A")
	}

	// Second call: check returns false, no announcement
	msg2 := RunAnnouncements()
	if msg2 != "" {
		t.Errorf("Second call: RunAnnouncements() = %q, want empty", msg2)
	}
}
