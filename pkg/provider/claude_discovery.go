package provider

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// claudeUUIDLength is the length of a Claude session UUID (with hyphens).
// Files in ~/.claude/projects with names not exactly this length are
// silently skipped — this is how session JSONLs are distinguished from
// agent sidechain files and stray non-transcript content.
const claudeUUIDLength = 36

// maxMetadataFieldSize is the backend-imposed limit for metadata fields
// like first_user_message. Messages are truncated to half this value
// (4KB) so the truncated string + JSON quoting + ellipsis still fits
// when the chunk envelope serializes.
const maxMetadataFieldSize = 8 * 1024

var htmlTagRegex = regexp.MustCompile(`<[^>]*>`)

// ScanSessions walks ~/.claude/projects/ and returns all user sessions
// sorted oldest first. Permission errors per path are reported to stderr
// and don't fail the scan.
func (p ClaudeCode) ScanSessions() ([]SessionInfo, error) {
	projectsDir, err := p.ProjectsDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get projects directory: %w", err)
	}

	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var sessions []SessionInfo
	var skippedPaths []string

	err = filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			logger.Warn("Failed to access path during scan: %s: %v", path, err)
			skippedPaths = append(skippedPaths, path)
			return nil
		}

		session := parseClaudeSessionFromPath(path, d, projectsDir)
		if session != nil {
			sessions = append(sessions, *session)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk projects directory: %w", err)
	}

	reportSkippedPaths(skippedPaths, "scan")

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.Before(sessions[j].ModTime)
	})

	return sessions, nil
}

// FindSessionByID resolves a full or partial Claude session ID to its
// full ID and transcript path. Walk-up is identity for Claude (no thread
// tree). Returns an error on no-match or ambiguous prefix.
func (p ClaudeCode) FindSessionByID(partialID string) (string, string, error) {
	projectsDir, err := p.ProjectsDir()
	if err != nil {
		return "", "", err
	}

	var matches []SessionInfo
	var skippedPaths []string

	err = filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			logger.Warn("Failed to access path during search: %s: %v", path, walkErr)
			skippedPaths = append(skippedPaths, path)
			return nil
		}

		session := parseClaudeSessionFromPath(path, d, projectsDir)
		if session == nil {
			return nil
		}
		if strings.HasPrefix(session.SessionID, partialID) {
			matches = append(matches, *session)
		}
		return nil
	})
	if err != nil {
		logger.Warn("Failed to walk projects directory: %v", err)
	}

	reportSkippedPaths(skippedPaths, "search")

	if len(matches) == 0 {
		return "", "", fmt.Errorf("session not found: %s", partialID)
	}
	if len(matches) > 1 {
		return "", "", fmt.Errorf("ambiguous session ID '%s' matches %d sessions", partialID, len(matches))
	}
	return matches[0].SessionID, matches[0].TranscriptPath, nil
}

// ExtractMetadata parses summary, first user message, and summary links
// from in-memory Claude transcript lines. Lines beyond
// maxLinesForExtraction are ignored.
//
// For summaries:
//   - Entries with a leafUuid go to SummaryLinks (links to previous sessions).
//   - Entries without leafUuid become the local Summary (last one wins).
//
// For user messages: the first one encountered sets FirstUserMessage.
func (p ClaudeCode) ExtractMetadata(lines []string) SessionMetadata {
	if len(lines) > maxLinesForExtraction {
		lines = lines[:maxLinesForExtraction]
	}
	return extractClaudeMetadata(lines)
}

// extractClaudeSessionMetadataFromFile reads the head of a transcript file
// and returns its summary and first user message. Used by parse-session-
// from-path to populate the SessionInfo title fields during ScanSessions.
func extractClaudeSessionMetadataFromFile(transcriptPath string) SessionMetadata {
	lines, err := readHeadLines(transcriptPath)
	// Open failures (lines==nil) degrade silently; scan errors log a
	// warning but we still process whatever was collected.
	if err != nil && lines != nil {
		logger.Warn("Error reading transcript %s during metadata extraction: %v", transcriptPath, err)
	}
	return extractClaudeMetadata(lines)
}

// reportSkippedPaths prints a user-friendly warning about paths that couldn't be accessed.
func reportSkippedPaths(skippedPaths []string, operation string) {
	if len(skippedPaths) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n⚠ Warning: Could not access %d path(s) during %s:\n", len(skippedPaths), operation)
	for _, p := range skippedPaths {
		fmt.Fprintf(os.Stderr, "  - %s\n", p)
	}
	fmt.Fprintf(os.Stderr, "Check permissions or see logs at ~/.confab/logs/confab.log\n\n")
}

// parseClaudeSessionFromPath checks if a path is a valid session
// transcript and returns SessionInfo with summary + first user message
// extracted. Returns nil for directories, agent files, or non-UUID names.
func parseClaudeSessionFromPath(path string, d os.DirEntry, projectsDir string) *SessionInfo {
	if d.IsDir() {
		return nil
	}
	if !strings.HasSuffix(path, ".jsonl") {
		return nil
	}
	name := d.Name()
	if strings.HasPrefix(name, "agent-") {
		return nil
	}

	sessionID := strings.TrimSuffix(name, ".jsonl")
	if len(sessionID) != claudeUUIDLength {
		return nil
	}

	info, err := d.Info()
	if err != nil {
		return nil
	}

	relPath, _ := filepath.Rel(projectsDir, filepath.Dir(path))
	meta := extractClaudeSessionMetadataFromFile(path)
	return &SessionInfo{
		SessionID:        sessionID,
		TranscriptPath:   path,
		ProjectPath:      relPath,
		ModTime:          info.ModTime(),
		SizeBytes:        info.Size(),
		Summary:          meta.Summary,
		FirstUserMessage: meta.FirstUserMessage,
	}
}

// extractClaudeMetadata is the in-memory extraction primitive shared by
// ExtractMetadata (chunk-time) and extractClaudeSessionMetadataFromFile
// (scan-time).
func extractClaudeMetadata(lines []string) SessionMetadata {
	var result SessionMetadata

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		msgType, _ := entry["type"].(string)

		if result.FirstUserMessage == "" && msgType == "user" {
			if text := extractTextFromMessage(entry); text != "" {
				result.FirstUserMessage = truncateString(sanitizeText(text), maxMetadataFieldSize/2)
			}
		}

		if msgType == "summary" {
			summary, _ := entry["summary"].(string)
			leafUUID, _ := entry["leafUuid"].(string)

			if summary != "" {
				if leafUUID != "" {
					result.SummaryLinks = append(result.SummaryLinks, SummaryLink{
						Summary:  sanitizeText(summary),
						LeafUUID: leafUUID,
					})
				} else {
					result.Summary = sanitizeText(summary)
				}
			}
		}
	}

	return result
}

// extractTextFromMessage extracts the first text content from a message
// entry. Handles both string content and array content (multimodal
// messages with images + text).
func extractTextFromMessage(entry map[string]interface{}) string {
	message, ok := entry["message"].(map[string]interface{})
	if !ok {
		return ""
	}

	content := message["content"]
	if content == nil {
		return ""
	}

	if str, ok := content.(string); ok {
		return str
	}

	if arr, ok := content.([]interface{}); ok {
		for _, block := range arr {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockType, _ := blockMap["type"].(string); blockType == "text" {
					if text, ok := blockMap["text"].(string); ok && text != "" {
						return text
					}
				}
			}
		}
	}

	return ""
}

// truncateString truncates a string to maxBytes, respecting UTF-8 rune
// boundaries. If truncated, appends "..." to indicate continuation.
func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	maxBytes -= 3
	if maxBytes <= 0 {
		return "..."
	}
	truncated := s[:maxBytes]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "..."
}

// sanitizeText removes HTML tags, decodes HTML entities, and normalizes whitespace.
func sanitizeText(input string) string {
	cleaned := htmlTagRegex.ReplaceAllString(input, "")
	decoded := html.UnescapeString(cleaned)
	decoded = strings.Join(strings.Fields(decoded), " ")
	return strings.TrimSpace(decoded)
}
