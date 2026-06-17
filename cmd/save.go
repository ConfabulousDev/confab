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
		cfg, p, err := resolveSaveContext(saveProviderName, saveConfigDir)
		if err != nil {
			return err
		}
		return saveSessionsForProvider(cfg, p, args)
	},
}

var (
	saveProviderName string
	saveConfigDir    string
)

// resolveSaveContext resolves the backend upload config and the discovery
// provider for `confab save`, honoring the per-(provider, config-dir) binding
// (kata hpec / z0rt).
//
// With configDir empty it takes the unchanged default-binding path. With
// configDir set it requires providerName and resolves the binding's backend via
// provider.BindingFor + config.EnsureAuthenticatedFor; local discovery uses
// provider.GetWithDir(name, configDir), which is claude-code-only — other
// providers surface "custom --config-dir is not yet supported".
func resolveSaveContext(providerName, configDir string) (*config.UploadConfig, provider.Provider, error) {
	if configDir == "" {
		p, err := provider.Get(providerName)
		if err != nil {
			return nil, nil, err
		}
		cfg, err := config.EnsureAuthenticated()
		if err != nil {
			return nil, nil, err
		}
		return cfg, p, nil
	}

	if providerName == "" {
		return nil, nil, fmt.Errorf("--config-dir requires --provider (a config dir is provider-specific)")
	}

	// Local discovery against the custom dir (claude-code-only; errors otherwise).
	p, err := provider.GetWithDir(providerName, configDir)
	if err != nil {
		return nil, nil, err
	}

	// Resolve the bound backend. BindingFor needs the DEFAULT provider (not the
	// GetWithDir override, whose StateDir() is the custom dir itself and would
	// always look "default").
	def, err := provider.Get(providerName)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.EnsureAuthenticatedFor(provider.BindingFor(def, configDir))
	if err != nil {
		return nil, nil, withSetupHint(err, def.Name(), configDir)
	}
	return cfg, p, nil
}

// saveSessionsForProvider resolves each session ID via the provider's
// FindSessionByID (which transparently walks Codex subagent UUIDs up to
// their root) and uploads through the sync engine against cfg.
func saveSessionsForProvider(cfg *config.UploadConfig, p provider.Provider, sessionIDs []string) error {
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
	saveCmd.Flags().StringVar(&saveProviderName, "provider", "", "Provider to save sessions from (claude-code, codex, or cursor; opencode is live-sync only)")
	saveCmd.MarkFlagRequired("provider")
	saveCmd.Flags().StringVar(&saveConfigDir, "config-dir", "", "Save into a non-default backend bound to this config dir (requires --provider; claude-code only)")
	rootCmd.AddCommand(saveCmd)
}
