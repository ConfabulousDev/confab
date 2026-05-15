package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/discovery"
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
		providerName, err := provider.NormalizeName(saveProviderName)
		if err != nil {
			return err
		}
		return saveSessionsByIDForProvider(providerName, args)
	},
}

var saveProviderName string

// saveSessionsByID uploads Claude Code sessions by ID.
func saveSessionsByID(sessionIDs []string) error {
	return saveSessionsByIDForProvider(provider.NameClaudeCode, sessionIDs)
}

func saveSessionsByIDForProvider(providerName string, sessionIDs []string) error {
	if providerName == provider.NameCodex {
		return saveCodexSessionsByID(sessionIDs)
	}

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

func saveCodexSessionsByID(sessionIDs []string) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	codex := provider.Codex{}
	for _, sessionID := range sessionIDs {
		// Use FindRolloutByID (not FindSessionByID) so subagent UUIDs resolve
		// too — we walk up to the root next.
		fullSessionID, rolloutPath, err := codex.FindRolloutByID(sessionID)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		// If the user passed a subagent UUID, transparently switch to syncing
		// the root tree. The engine + tracker discover and upload every
		// descendant as a sidechain file under the root's session, so the
		// caller gets the whole tree from one save invocation.
		rootUUID, rootRolloutPath, _ := codex.WalkUpToRoot(fullSessionID)
		if rootUUID != "" && rootUUID != fullSessionID {
			fmt.Printf("Resolved subagent %s → root %s\n",
				utils.TruncateSecret(fullSessionID, 8, 0),
				utils.TruncateSecret(rootUUID, 8, 0))
			fullSessionID = rootUUID
			if rootRolloutPath != "" {
				rolloutPath = rootRolloutPath
			}
		}

		info, err := codex.ReadSessionInfo(rolloutPath)
		cwd := filepath.Dir(rolloutPath)
		if err == nil && info.CWD != "" {
			cwd = info.CWD
		}

		fmt.Printf("Uploading Codex session %s...\n", utils.TruncateSecret(fullSessionID, 8, 0))
		result := uploadSingleCodexSession(cfg, fullSessionID, rolloutPath, cwd)
		if result.Error != nil {
			fmt.Printf("  Error syncing: %v\n", result.Error)
			continue
		}
		fmt.Printf("  ✓ Uploaded (%d chunks)\n", result.FilesUploaded)
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

func uploadSingleCodexSession(cfg *config.UploadConfig, sessionID, rolloutPath, cwd string) UploadResult {
	result := UploadResult{SessionID: sessionID}
	engine, err := sync.New(cfg, sync.EngineConfig{
		Provider:       provider.NameCodex,
		ExternalID:     sessionID,
		TranscriptPath: rolloutPath,
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

// uploadSingleSession uploads a session using the sync engine.
func uploadSingleSession(cfg *config.UploadConfig, sessionID, transcriptPath string) UploadResult {
	result := UploadResult{SessionID: sessionID}

	// Derive CWD from transcript path
	cwd := filepath.Dir(transcriptPath)

	// Create sync engine
	engine, err := sync.New(cfg, sync.EngineConfig{
		Provider:       provider.NameClaudeCode,
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
	saveCmd.Flags().StringVar(&saveProviderName, "provider", provider.NameClaudeCode, "Provider to save sessions from (claude-code or codex)")
	rootCmd.AddCommand(saveCmd)
}
