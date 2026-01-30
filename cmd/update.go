package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/spf13/cobra"
)

const (
	// GitHub repository for releases
	githubRepo = "ConfabulousDev/confab"

	// GitHub API URL for latest release
	githubReleasesAPI = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
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

	if err := installLatestRelease(); err != nil {
		logger.Error("Failed to install update: %v", err)
		return fmt.Errorf("update failed: %w", err)
	}

	logger.Info("Update complete")
	return nil
}

// githubRelease represents the relevant fields from GitHub's release API
type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchLatestRelease fetches the latest release info from GitHub
func fetchLatestRelease() (*githubRelease, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", githubReleasesAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse GitHub response: %w", err)
	}

	return &release, nil
}

// fetchLatestVersion fetches the latest version string from GitHub releases
func fetchLatestVersion() (string, error) {
	release, err := fetchLatestRelease()
	if err != nil {
		return "", err
	}
	return release.TagName, nil
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

// getAssetName returns the expected asset name for the current platform
func getAssetName() string {
	return fmt.Sprintf("confab_%s_%s", runtime.GOOS, runtime.GOARCH)
}

// findAssetURL finds the download URL for the current platform from a release
func findAssetURL(release *githubRelease) (string, error) {
	expectedName := getAssetName()

	for _, asset := range release.Assets {
		if asset.Name == expectedName {
			return asset.BrowserDownloadURL, nil
		}
	}

	return "", fmt.Errorf("no binary found for %s/%s (looked for %s)", runtime.GOOS, runtime.GOARCH, expectedName)
}

// downloadBinary downloads a binary from the given URL to the specified path
func downloadBinary(url, destPath string) error {
	client := &http.Client{
		Timeout: 120 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Write to temp file first for atomic replacement
	tmpPath := destPath + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	_, err = io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write binary: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to install binary: %w", err)
	}

	return nil
}

// installLatestRelease downloads and installs the latest release
func installLatestRelease() error {
	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to fetch release info: %w", err)
	}

	assetURL, err := findAssetURL(release)
	if err != nil {
		return err
	}

	// Install to ~/.local/bin/confab
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	binDir := filepath.Join(homeDir, ".local", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("failed to create bin directory: %w", err)
	}

	destPath := filepath.Join(binDir, "confab")

	fmt.Printf("Downloading %s...\n", release.TagName)
	if err := downloadBinary(assetURL, destPath); err != nil {
		return err
	}

	fmt.Printf("Installed to %s\n", destPath)
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

// downloadNewBinary downloads the latest release and returns the path to the new binary
func downloadNewBinary() (string, error) {
	if err := installLatestRelease(); err != nil {
		return "", err
	}

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
