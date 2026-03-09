// ABOUTME: CLI command for capturing learnings from the current session.
// ABOUTME: Auto-detects the active session and posts a learning artifact to the backend API.
package cmd

import (
	"fmt"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/daemon"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var (
	learnTags    []string
	learnBody    string
	learnSession string
)

type learnRequest struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Tags       []string `json:"tags"`
	Source     string   `json:"source"`
	SessionIDs []string `json:"session_ids"`
}

type learnResponse struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

var learnCmd = &cobra.Command{
	Use:   "learn <title>",
	Short: "Capture a learning from the current session",
	Long: `Record a reusable insight, pattern, or gotcha as a learning artifact.

The learning is saved as a draft and automatically linked to the active
Claude Code session (if one is running). Use --body to provide a detailed
description separate from the title.

Examples:
  confab learn "Proxy blocks OCP signature verification"
  confab learn --tag openshift --tag upgrade "Must inject configmap manually"
  confab learn --body "When upgrading behind a proxy, inject the signature configmap" "OCP proxy workaround"
  confab learn --session abc123 "Offline learning"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		title := strings.Join(args, " ")
		return runLearn(title, learnBody, learnTags, learnSession)
	},
}

// findActiveSessionID returns the confab backend session ID for the currently
// active Claude Code session, or empty string if none is found.
func findActiveSessionID() string {
	states, err := daemon.ListAllStates()
	if err != nil {
		logger.Debug("Failed to list daemon states: %v", err)
		return ""
	}

	var active []*daemon.State
	for _, s := range states {
		if s.ConfabSessionID != "" && s.IsDaemonRunning() {
			active = append(active, s)
		}
	}

	if len(active) == 0 {
		logger.Debug("No active sessions found")
		return ""
	}
	if len(active) > 1 {
		logger.Debug("Multiple active sessions found (%d), using most recent", len(active))
		// Pick the most recently started session
		best := active[0]
		for _, s := range active[1:] {
			if s.StartedAt.After(best.StartedAt) {
				best = s
			}
		}
		return best.ConfabSessionID
	}

	return active[0].ConfabSessionID
}

func runLearn(title, body string, tags []string, sessionOverride string) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Use body flag if provided, otherwise title is both
	if body == "" {
		body = title
	}

	// Detect or override session ID
	var sessionIDs []string
	if sessionOverride != "" {
		sessionIDs = []string{sessionOverride}
	} else if sid := findActiveSessionID(); sid != "" {
		sessionIDs = []string{sid}
		logger.Debug("Auto-detected active session: %s", sid)
	}

	req := &learnRequest{
		Title:      title,
		Body:       body,
		Tags:       tags,
		Source:     "manual_session",
		SessionIDs: sessionIDs,
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}

	var resp learnResponse
	if err := client.Post("/api/v1/learnings", req, &resp); err != nil {
		return fmt.Errorf("failed to save learning: %w", err)
	}

	fmt.Printf("Learning saved: %s\n", resp.Title)
	fmt.Printf("  ID: %s | Status: %s\n", resp.ID, resp.Status)
	if len(sessionIDs) > 0 {
		fmt.Printf("  Linked to session: %s\n", sessionIDs[0])
	}

	return nil
}

func init() {
	learnCmd.Flags().StringArrayVar(&learnTags, "tag", nil,
		"Add tags to the learning (can be used multiple times)")
	learnCmd.Flags().StringVar(&learnBody, "body", "",
		"Detailed description (defaults to title if not set)")
	learnCmd.Flags().StringVar(&learnSession, "session", "",
		"Override session ID (auto-detected by default)")
	rootCmd.AddCommand(learnCmd)
}
