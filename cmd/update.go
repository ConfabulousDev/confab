package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

const (
	latestVersionURL = "https://confabulous.dev/cli/latest_version"
	installScriptURL = "https://confabulous.dev/install"
)

var checkOnly bool

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update confab to the latest version",
	Long: `Checks for a newer version of confab and installs it if available.

Use --check to only check for updates without installing.`,
	RunE: runUpdate,
}

func runUpdate(cmd *cobra.Command, args []string) error {
	logger.Info("Running update command (check=%v)", checkOnly)

	// Fetch latest version
	latest, err := fetchLatestVersion()
	if err != nil {
		logger.Error("Failed to fetch latest version: %v", err)
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	current := version
	latestClean := strings.TrimPrefix(latest, "v")
	currentClean := strings.TrimPrefix(current, "v")

	logger.Info("Current version: %s, Latest version: %s", current, latest)

	// Check if update is needed
	needsUpdate := isNewerVersion(currentClean, latestClean)

	if !needsUpdate {
		fmt.Printf("confab is up to date (v%s)\n", latestClean)
		return nil
	}

	// Show version info
	fmt.Printf("Current version: %s\n", current)
	fmt.Printf("Latest version:  %s\n", latest)
	fmt.Println()

	if checkOnly {
		fmt.Println("Update available! Run 'confab update' to install.")
		return nil
	}

	// Perform update
	fmt.Println("Updating confab...")
	fmt.Println()

	if err := runInstallScript(); err != nil {
		logger.Error("Failed to run install script: %v", err)
		return fmt.Errorf("update failed: %w", err)
	}

	logger.Info("Update complete")
	return nil
}

// fetchLatestVersion fetches the latest version string from the server
func fetchLatestVersion() (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(latestVersionURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}

// isNewerVersion returns true if latest is newer than current
func isNewerVersion(current, latest string) bool {
	// Dev builds always need update
	if current == "dev" || current == "none" || current == "" {
		return true
	}

	currentParts := parseVersion(current)
	latestParts := parseVersion(latest)

	for i := 0; i < 3; i++ {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}

	return false
}

// parseVersion parses a version string into [major, minor, patch]
func parseVersion(v string) [3]int {
	var parts [3]int
	segments := strings.Split(v, ".")

	for i := 0; i < len(segments) && i < 3; i++ {
		// Strip any suffix (e.g., "1.0.0-beta" -> "1.0.0")
		numStr := strings.Split(segments[i], "-")[0]
		num, _ := strconv.Atoi(numStr)
		parts[i] = num
	}

	return parts
}

// runInstallScript downloads and executes the install script
func runInstallScript() error {
	// Fetch install script
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(installScriptURL)
	if err != nil {
		return fmt.Errorf("failed to download install script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "confab-install-*.sh")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write install script: %w", err)
	}
	tmpFile.Close()

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to make script executable: %w", err)
	}

	// Execute
	cmd := exec.Command(tmpPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install script failed: %w", err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVar(&checkOnly, "check", false, "only check for updates, don't install")
}

// AutoUpdateIfNeeded checks for updates and if available, downloads the new version
// and re-execs into it with the same arguments. Only returns if no update is needed
// or if update fails.
func AutoUpdateIfNeeded() {
	if !shouldCheckForUpdate() {
		return
	}

	latest, err := fetchLatestVersion()
	if err != nil {
		logger.Debug("Auto-update check failed: %v", err)
		return
	}

	current := version
	latestClean := strings.TrimPrefix(latest, "v")
	currentClean := strings.TrimPrefix(current, "v")

	if !isNewerVersion(currentClean, latestClean) {
		logger.Debug("No update needed (current=%s, latest=%s)", current, latest)
		writeLastCheckTime()
		return
	}

	logger.Info("Update available: %s -> %s", current, latest)
	fmt.Fprintf(os.Stderr, "Updating confab (%s -> %s)...\n", current, latest)

	// Download and install new version
	newBinary, err := downloadNewBinary()
	if err != nil {
		logger.Error("Auto-update failed: %v", err)
		fmt.Fprintf(os.Stderr, "Auto-update failed: %v\n", err)
		return
	}

	writeLastCheckTime()

	// Re-exec into new binary with same arguments
	fmt.Fprintf(os.Stderr, "Update complete, restarting...\n\n")
	logger.Info("Re-execing into new binary: %s", newBinary)

	if err := syscall.Exec(newBinary, os.Args, os.Environ()); err != nil {
		logger.Error("Failed to exec new binary: %v", err)
		fmt.Fprintf(os.Stderr, "Failed to restart: %v\n", err)
	}
}

// shouldCheckForUpdate returns true if enough time has passed since last check
func shouldCheckForUpdate() bool {
	// Don't auto-update dev builds
	if version == "dev" {
		return false
	}

	lastCheck := readLastCheckTime()
	if lastCheck.IsZero() {
		return true
	}

	// Check at most once per hour
	return time.Since(lastCheck) > time.Hour
}

func getCheckTimePath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".confab", "last_update_check")
}

func readLastCheckTime() time.Time {
	data, err := os.ReadFile(getCheckTimePath())
	if err != nil {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}

	return t
}

func writeLastCheckTime() {
	path := getCheckTimePath()
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)), 0644)
}

// NotifyIfUpdateAvailable checks for updates and prints a notice if available.
// Does not install - just informs the user.
func NotifyIfUpdateAvailable() {
	if !shouldCheckForUpdate() {
		return
	}

	latest, err := fetchLatestVersion()
	if err != nil {
		logger.Debug("Update check failed: %v", err)
		return
	}

	writeLastCheckTime()

	current := version
	latestClean := strings.TrimPrefix(latest, "v")
	currentClean := strings.TrimPrefix(current, "v")

	if !isNewerVersion(currentClean, latestClean) {
		return
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Update available: %s -> %s (run 'confab update' to install)\n", current, latest)
}

// downloadNewBinary downloads the install script, runs it, and returns the path
// to the new binary
func downloadNewBinary() (string, error) {
	if err := runInstallScript(); err != nil {
		return "", err
	}

	// The install script puts the binary at ~/.local/bin/confab
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	newBinary := filepath.Join(homeDir, ".local", "bin", "confab")
	if _, err := os.Stat(newBinary); err != nil {
		return "", fmt.Errorf("new binary not found at %s", newBinary)
	}

	return newBinary, nil
}
