// ABOUTME: CLI command for capturing learnings from the current session.
// ABOUTME: Posts a learning artifact to the backend API via confab learn <message>.
package cmd

import (
	"fmt"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var learnTags []string

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
	Use:   "learn <message>",
	Short: "Capture a learning from the current session",
	Long: `Record a reusable insight, pattern, or gotcha as a learning artifact.

The learning is saved as a draft linked to the current session.

Examples:
  confab learn "Proxy blocks OCP signature verification"
  confab learn --tag openshift --tag upgrade "Must inject configmap manually"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		message := strings.Join(args, " ")
		return runLearn(message, learnTags)
	},
}

func buildLearnRequest(message string, tags []string) *learnRequest {
	req := &learnRequest{
		Title:  message,
		Body:   message,
		Tags:   tags,
		Source: "manual_session",
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}
	return req
}

func runLearn(message string, tags []string) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	req := buildLearnRequest(message, tags)

	var resp learnResponse
	if err := client.Post("/api/v1/learnings", req, &resp); err != nil {
		return fmt.Errorf("failed to save learning: %w", err)
	}

	logger.Info("Learning saved: %s (id: %s, status: %s)", resp.Title, resp.ID, resp.Status)
	fmt.Printf("Learning saved: %s\n", resp.Title)
	fmt.Printf("  ID: %s | Status: %s\n", resp.ID, resp.Status)

	return nil
}

func init() {
	learnCmd.Flags().StringArrayVar(&learnTags, "tag", nil,
		"Add tags to the learning (can be used multiple times)")
	rootCmd.AddCommand(learnCmd)
}
