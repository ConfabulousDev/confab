// ABOUTME: CLI command to download raw JSONL transcript files from the backend.
// ABOUTME: Streams main transcript to stdout by default, or downloads all files to a directory with --output-dir.
package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
	confabhttp "github.com/ConfabulousDev/confab/pkg/http"
	"github.com/ConfabulousDev/confab/pkg/utils"
	"github.com/spf13/cobra"
)

var sessionDownloadOutputDir string

var sessionDownloadCmd = &cobra.Command{
	Use:   "download <id>",
	Short: "Download raw JSONL transcript files",
	Long: `Download raw JSONL transcript files from the backend.

By default, streams the main transcript file to stdout.
With --output-dir, downloads all files (transcript + agent) to a directory.

Examples:
  # Stream main transcript to stdout
  confab session download abc123-uuid-here

  # Download all files to a directory
  confab session download --output-dir ./transcripts abc123-uuid-here

  # Pipe to jq for analysis
  confab session download abc123-uuid-here | jq '.type'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer NotifyIfUpdateAvailable()
		return runSessionDownload(args[0], sessionDownloadOutputDir)
	},
}

func init() {
	sessionDownloadCmd.Flags().StringVar(&sessionDownloadOutputDir, "output-dir", "", "Download all files (transcript + agents) to this directory")
	sessionCmd.AddCommand(sessionDownloadCmd)
}

// sessionFile represents a file in the session files list response.
type sessionFile struct {
	FileName       string    `json:"file_name"`
	FileType       string    `json:"file_type"`
	LastSyncedLine int       `json:"last_synced_line"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// sessionFilesResponse is the response from the session files list endpoint.
type sessionFilesResponse struct {
	Files []sessionFile `json:"files"`
}

// buildSessionFilesPath constructs the API path for the session files list endpoint.
func buildSessionFilesPath(id string) string {
	return "/api/v1/sessions/" + url.PathEscape(id) + "/files"
}

// buildSessionFileDownloadPath constructs the API path for downloading a single file.
func buildSessionFileDownloadPath(id string, fileName string) string {
	params := url.Values{}
	params.Set("file_name", fileName)
	return "/api/v1/sessions/" + url.PathEscape(id) + "/files/download?" + params.Encode()
}

func runSessionDownload(id string, outputDir string) error {
	cfg, err := config.EnsureAuthenticated()
	if err != nil {
		return err
	}

	client, err := confabhttp.NewClient(cfg, utils.DefaultHTTPTimeout)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Fetch file list
	var filesResp sessionFilesResponse
	if err := client.Get(buildSessionFilesPath(id), &filesResp); err != nil {
		if errors.Is(err, confabhttp.ErrSessionNotFound) {
			return fmt.Errorf("session not found")
		}
		return fmt.Errorf("failed to list session files: %w", err)
	}

	if len(filesResp.Files) == 0 {
		return fmt.Errorf("no files found for session")
	}

	if outputDir != "" {
		return downloadAllFiles(client, id, outputDir, filesResp.Files)
	}
	return downloadMainTranscript(client, id, filesResp.Files)
}

// downloadMainTranscript finds the main transcript file and streams it to stdout.
func downloadMainTranscript(client *confabhttp.Client, id string, files []sessionFile) error {
	// Find first transcript file
	var transcriptFile *sessionFile
	for i := range files {
		if files[i].FileType == "transcript" {
			transcriptFile = &files[i]
			break
		}
	}
	if transcriptFile == nil {
		return fmt.Errorf("no transcript file found for session")
	}

	path := buildSessionFileDownloadPath(id, transcriptFile.FileName)
	return client.GetRawToWriter(path, os.Stdout)
}

// downloadAllFiles downloads every file to the output directory.
func downloadAllFiles(client *confabhttp.Client, id string, outputDir string, files []sessionFile) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve output directory: %w", err)
	}

	var failedFiles []string
	for _, f := range files {
		path := buildSessionFileDownloadPath(id, f.FileName)
		destPath := filepath.Join(outputDir, filepath.Base(f.FileName))

		// Prevent path traversal
		absDest, err := filepath.Abs(destPath)
		if err != nil || !strings.HasPrefix(absDest, absOutputDir+string(filepath.Separator)) {
			failedFiles = append(failedFiles, f.FileName)
			continue
		}

		outFile, err := os.Create(destPath)
		if err != nil {
			failedFiles = append(failedFiles, f.FileName)
			continue
		}

		if err := client.GetRawToWriter(path, outFile); err != nil {
			outFile.Close()
			failedFiles = append(failedFiles, f.FileName)
			continue
		}
		outFile.Close()
	}

	if len(failedFiles) > 0 {
		return fmt.Errorf("failed to download: %v", failedFiles)
	}

	fmt.Fprintf(os.Stderr, "Downloaded %d files to %s\n", len(files), outputDir)
	return nil
}
