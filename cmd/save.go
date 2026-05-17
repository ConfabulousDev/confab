package cmd

import (
	"fmt"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/sync"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var saveCmd = &cobra.Command{
	Use:   "save <session-id> [session-id...]",
	Short: "Save session data to the backend",
	Long: `Upload session(s) by ID.

Use 'confab list' to see available sessions and their IDs.

Examples:
  confab save abc123de           # Upload specific session
  confab save abc123de f9e8d7c6  # Upload multiple sessions`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		p, err := provider.Get(saveProviderName)
		if err != nil {
			return err
		}
		return saveSessionsForProvider(p, args)
	},
}

var saveProviderName string

// saveSessionsByID uploads Claude Code sessions by ID. Retained as a
// convenience wrapper for legacy callers; new code should use
// saveSessionsForProvider with an explicit Provider.
func saveSessionsByID(sessionIDs []string) error {
	return saveSessionsForProvider(provider.ClaudeCode{}, sessionIDs)
}

// saveSessionsForProvider resolves each session ID via the provider's
// FindSessionByID (which transparently walks Codex subagent UUIDs up to
// their root) and uploads through the sync engine.
func saveSessionsForProvider(p provider.Provider, sessionIDs []string) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	for _, sessionID := range sessionIDs {
		fullID, transcriptPath, err := p.FindSessionByID(sessionID)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		cwd := p.DefaultCWD(transcriptPath)
		fmt.Printf("Uploading session %s...\n", utils.TruncateSecret(fullID, 8, 0))

		result := uploadSingleSession(cfg, p.Name(), fullID, transcriptPath, cwd)
		if result.Error != nil {
			fmt.Printf("  Error uploading: %v\n", result.Error)
			continue
		}
		fmt.Printf("  ✓ Uploaded (%d chunks)\n", result.FilesUploaded)
	}
	return nil
}

// UploadResult contains the result of uploading a single session.
type UploadResult struct {
	SessionID     string
	InternalID    string
	FilesUploaded int
	Error         error
}

// uploadSingleSession runs the sync engine for one session.
func uploadSingleSession(cfg *config.UploadConfig, providerName, sessionID, transcriptPath, cwd string) UploadResult {
	result := UploadResult{SessionID: sessionID}

	engine, err := sync.New(cfg, sync.EngineConfig{
		Provider:       providerName,
		ExternalID:     sessionID,
		TranscriptPath: transcriptPath,
		CWD:            cwd,
	})
	if err != nil {
		result.Error = err
		return result
	}

	if err := engine.Init(); err != nil {
		result.Error = err
		return result
	}

	result.InternalID = engine.SessionID()

	chunks, err := engine.SyncAll()
	if err != nil {
		result.Error = err
		return result
	}
	result.FilesUploaded = chunks
	return result
}

func init() {
	saveCmd.Flags().StringVar(&saveProviderName, "provider", provider.NameClaudeCode, "Provider to save sessions from (claude-code or codex)")
	rootCmd.AddCommand(saveCmd)
}
