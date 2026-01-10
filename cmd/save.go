package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/discovery"
	"github.com/ConfabulousDev/confab/pkg/sync"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var saveCmd = &cobra.Command{
	Use:   "save <session-id> [session-id...]",
	Short: "Save session data to cloud",
	Long: `Upload session(s) by ID.

Use 'confab list' to see available sessions and their IDs.

Examples:
  confab save abc123de           # Upload specific session
  confab save abc123de f9e8d7c6  # Upload multiple sessions`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return saveSessionsByID(args)
	},
}

// saveSessionsByID uploads specific sessions by their IDs
func saveSessionsByID(sessionIDs []string) error {
	// Check authentication
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	for _, sessionID := range sessionIDs {
		// Handle partial session IDs (first 8 chars)
		fullSessionID, transcriptPath, err := discovery.FindSessionByID(sessionID)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Printf("Uploading session %s...\n", utils.TruncateSecret(fullSessionID, 8, 0))

		result := uploadSingleSession(cfg, fullSessionID, transcriptPath)
		if result.Error != nil {
			fmt.Printf("  Error uploading: %v\n", result.Error)
			continue
		}

		fmt.Printf("  ✓ Uploaded (%d files)\n", result.FilesUploaded)
	}

	return nil
}

// UploadResult contains the result of uploading a single session
type UploadResult struct {
	SessionID     string
	InternalID    string
	FilesUploaded int
	Error         error
}

// uploadSingleSession uploads a session using the sync engine.
func uploadSingleSession(cfg *config.UploadConfig, sessionID, transcriptPath string) UploadResult {
	result := UploadResult{SessionID: sessionID}

	// Derive CWD from transcript path
	cwd := filepath.Dir(transcriptPath)

	// Create sync engine
	engine, err := sync.New(cfg, sync.EngineConfig{
		ExternalID:     sessionID,
		TranscriptPath: transcriptPath,
		CWD:            cwd,
	})
	if err != nil {
		result.Error = err
		return result
	}

	// Initialize sync session with backend
	if err := engine.Init(); err != nil {
		result.Error = err
		return result
	}

	result.InternalID = engine.SessionID()

	// Sync all files (transcript + discovered agents)
	chunks, err := engine.SyncAll()
	if err != nil {
		result.Error = err
		return result
	}

	result.FilesUploaded = chunks
	return result
}

func init() {
	rootCmd.AddCommand(saveCmd)
}
