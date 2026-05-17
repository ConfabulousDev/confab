package cmd

import (
	"fmt"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/spf13/cobra"
)

var setupProviderName string

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up confab (login + install hooks)",
	Long: `Complete setup for confab in one command.

This command:
1. Authenticates with the backend (if not already logged in)
2. Detects installed provider CLIs (claude, codex) and installs hooks for each
3. Installs provider-specific skills (Claude only; no-op for Codex)

If --provider is set, only that provider is configured. If unset, hooks
are installed for every provider whose CLI is on PATH.

If you're already authenticated with a valid API key, the login step is
skipped. Use --api-key to provide an API key directly (bypasses device
auth flow).`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	logger.Info("Starting setup (provider=%q)", setupProviderName)

	backendURL, needsLogin, err := runSetupAuth(cmd)
	if err != nil {
		return err
	}

	if setupProviderName != "" {
		return runSetupSingle(backendURL, needsLogin)
	}
	return runSetupAutoDetect(backendURL, needsLogin)
}

// runSetupSingle installs hooks/skills for exactly the provider named
// in --provider.
func runSetupSingle(backendURL string, needsLogin bool) error {
	providerName, err := provider.NormalizeName(setupProviderName)
	if err != nil {
		return err
	}
	p, err := provider.Get(providerName)
	if err != nil {
		return err
	}

	if needsLogin {
		fmt.Println("Step 2/2: Installing hooks")
	}
	fmt.Println()

	if err := installForProvider(p); err != nil {
		return fmt.Errorf("failed to install %s hooks: %w", p.Name(), err)
	}

	fmt.Println()
	fmt.Printf("✅ Setup complete. %s sessions will sync to %s\n", p.Name(), backendURL)
	return nil
}

// runSetupAutoDetect probes PATH for installed provider CLIs and runs
// the full setup (hooks + skills) for each. If none are detected, auth
// stays in place, a terse warning prints, and the process exits 0. On
// per-provider failure mid-loop, every detected provider is still
// attempted and the process exits non-zero if any failed.
func runSetupAutoDetect(backendURL string, needsLogin bool) error {
	detected := provider.DetectInstalled()
	if len(detected) == 0 {
		fmt.Println("Detected providers: (none)")
		fmt.Println()
		fmt.Println("⚠️  No supported CLIs (claude, codex) found on PATH.")
		fmt.Println("   Auth saved, but no hooks were installed.")
		return nil
	}

	fmt.Printf("Detected providers: %s\n", strings.Join(detected, ", "))
	fmt.Println()

	if needsLogin {
		fmt.Println("Step 2/2: Installing hooks")
		fmt.Println()
	}

	results := make(map[string]error, len(detected))
	for _, name := range detected {
		p, err := provider.Get(name)
		if err != nil {
			results[name] = err
			logger.Error("auto-detect: %v", err)
			continue
		}
		results[name] = installForProvider(p)
	}

	var failed int
	fmt.Println()
	fmt.Println("Summary:")
	for _, name := range detected {
		if err := results[name]; err != nil {
			failed++
			fmt.Printf("  %s: failed (%v)\n", name, err)
		} else {
			fmt.Printf("  %s: installed\n", name)
		}
	}

	fmt.Println()
	if failed == 0 {
		fmt.Printf("✅ Setup complete. %s sessions will sync to %s\n",
			strings.Join(detected, ", "), backendURL)
		return nil
	}
	fmt.Printf("❌ Setup complete with errors. %d of %d providers failed (see above).\n",
		failed, len(detected))
	return fmt.Errorf("%d of %d providers failed to install", failed, len(detected))
}

// installForProvider prints the per-provider sub-header, then installs
// hooks (skipping if already present) and skills. Returns the first
// failure encountered.
func installForProvider(p provider.Provider) error {
	fmt.Printf("▶ %s\n", p.Name())

	already, err := p.IsHooksInstalled()
	if err != nil {
		fmt.Printf("  ✗ failed to check hook status: %v\n", err)
		return err
	}
	if already {
		fmt.Println("  ✓ hooks already installed (no changes)")
	} else {
		if _, err := p.InstallHooks(); err != nil {
			fmt.Printf("  ✗ failed: %v\n", err)
			return err
		}
		fmt.Println("  ✓ hooks installed")
	}

	if err := p.InstallSkills(); err != nil {
		fmt.Printf("  ✗ skills install failed: %v\n", err)
		return err
	}

	return nil
}

func runSetupAuth(cmd *cobra.Command) (backendURL string, needsLogin bool, err error) {
	backendURL, err = cmd.Flags().GetString("backend-url")
	if err != nil {
		return "", false, fmt.Errorf("failed to get backend-url flag: %w", err)
	}
	apiKey, err := cmd.Flags().GetString("api-key")
	if err != nil {
		return "", false, fmt.Errorf("failed to get api-key flag: %w", err)
	}

	fmt.Printf("Backend URL: %s\n", backendURL)
	fmt.Println()

	needsLogin = true
	if apiKey != "" {
		if err := loginWithAPIKey(backendURL, apiKey); err != nil {
			return "", false, err
		}
		fmt.Println()
		needsLogin = false
	} else {
		cfg, err := config.GetUploadConfig()
		if err == nil && cfg.APIKey != "" {
			if cfg.BackendURL == backendURL {
				fmt.Println("Checking existing authentication...")
				if err := verifyAPIKey(cfg); err == nil {
					logger.Info("Existing API key is valid, skipping login")
					fmt.Println("Already authenticated")
					fmt.Println()
					needsLogin = false
				} else {
					logger.Info("Existing API key is invalid: %v", err)
					fmt.Println("❌ Existing credentials invalid, need to re-authenticate")
					fmt.Println()
				}
			} else {
				logger.Info("Backend URL changed from %s to %s, need to re-login", cfg.BackendURL, backendURL)
				fmt.Println("Backend URL changed, need to re-authenticate")
				fmt.Println()
			}
		}

		if needsLogin {
			fmt.Println("Step 1/2: Authentication")
			fmt.Println()
			if err := doDeviceLogin(backendURL, defaultKeyName()); err != nil {
				return "", false, err
			}
			fmt.Println()
		}
	}

	if added, err := config.EnsureDefaultRedaction(); err != nil {
		logger.Warn("Failed to initialize redaction config: %v", err)
	} else if added {
		logger.Info("Initialized default redaction config")
		fmt.Println("Redaction enabled (default patterns)")
	}

	return backendURL, needsLogin, nil
}

func init() {
	rootCmd.AddCommand(setupCmd)

	setupCmd.Flags().StringVar(&setupProviderName, "provider", "", "Provider to set up (claude-code or codex); auto-detects if unset")
	setupCmd.Flags().String("backend-url", "", "Backend API URL (required)")
	setupCmd.MarkFlagRequired("backend-url")
	setupCmd.Flags().String("api-key", "", "API key (bypasses device auth flow)")
}
