package discovery

import (
	"bufio"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxLinesForExtraction limits how many lines we read when extracting metadata.
// Summaries and first user messages typically appear in the first few lines.
const MaxLinesForExtraction = 50

// MaxMetadataFieldSize is the backend-imposed limit for metadata fields like first_user_message.
// The server rejects metadata fields larger than this value.
// Messages are truncated to half this (4KB) to leave headroom in chunk uploads.
// If the backend limit changes, this constant must be updated accordingly.
const MaxMetadataFieldSize = 8 * 1024 // 8KB

// htmlTagRegex matches HTML tags for removal
var htmlTagRegex = regexp.MustCompile(`<[^>]*>`)

// SummaryLink represents a summary that links to a previous session via leafUuid.
type SummaryLink struct {
	Summary  string
	LeafUUID string
}

// ExtractionResult holds extracted metadata from transcript lines.
type ExtractionResult struct {
	Summary          string        // Local summary for current session (no leafUuid)
	FirstUserMessage string        // First user message content
	SummaryLinks     []SummaryLink // Summaries with leafUuid (for linking to previous sessions)
}

// ExtractSessionMetadata reads a transcript file and extracts summary and first user message.
// It reads up to MaxLinesForExtraction lines and delegates to ExtractMetadataFromLines.
func ExtractSessionMetadata(transcriptPath string) ExtractionResult {
	file, err := os.Open(transcriptPath)
	if err != nil {
		return ExtractionResult{}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Increase buffer size to handle long lines (default is 64KB, increase to 1MB)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var lines []string
	for scanner.Scan() && len(lines) < MaxLinesForExtraction {
		lines = append(lines, scanner.Text())
	}

	return ExtractMetadataFromLines(lines)
}

// extractTextFromMessage extracts the first text content from a message entry.
// Handles both string content and array content (multimodal messages with images + text).
func extractTextFromMessage(entry map[string]interface{}) string {
	message, ok := entry["message"].(map[string]interface{})
	if !ok {
		return ""
	}

	content := message["content"]
	if content == nil {
		return ""
	}

	// Case 1: content is a string
	if str, ok := content.(string); ok {
		return str
	}

	// Case 2: content is an array of content blocks (multimodal)
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

// truncateString truncates a string to maxBytes, respecting UTF-8 rune boundaries.
// If truncated, appends "..." to indicate continuation.
func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Leave room for ellipsis
	maxBytes -= 3
	if maxBytes <= 0 {
		return "..."
	}
	truncated := s[:maxBytes]
	// Trim bytes until we have valid UTF-8 (removes partial multi-byte chars)
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "..."
}

// SanitizeText removes HTML tags, decodes HTML entities, and normalizes whitespace.
func SanitizeText(input string) string {
	// Remove all HTML tags
	cleaned := htmlTagRegex.ReplaceAllString(input, "")

	// Decode HTML entities (e.g., &lt; -> <, &gt; -> >)
	decoded := html.UnescapeString(cleaned)

	// Replace newlines and excessive whitespace with single space
	decoded = strings.Join(strings.Fields(decoded), " ")

	// Trim whitespace
	return strings.TrimSpace(decoded)
}

// ExtractMetadataFromLines extracts summary and first user message from transcript lines.
// Unlike ExtractSessionMetadata, this processes lines already in memory (from a chunk).
//
// For summaries:
//   - Summaries with leafUuid are collected in SummaryLinks (for linking to previous sessions)
//   - Summaries without leafUuid are local to current session (last one wins)
//
// For user messages:
//   - First user message encountered sets FirstUserMessage
func ExtractMetadataFromLines(lines []string) ExtractionResult {
	var result ExtractionResult

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		msgType, _ := entry["type"].(string)

		// Capture first user message (truncated to avoid failing chunk uploads)
		if result.FirstUserMessage == "" && msgType == "user" {
			if text := extractTextFromMessage(entry); text != "" {
				result.FirstUserMessage = truncateString(SanitizeText(text), MaxMetadataFieldSize/2)
			}
		}

		// Process summary messages
		if msgType == "summary" {
			summary, _ := entry["summary"].(string)
			leafUUID, _ := entry["leafUuid"].(string)

			if summary != "" {
				if leafUUID != "" {
					// Summary links to a previous session
					result.SummaryLinks = append(result.SummaryLinks, SummaryLink{
						Summary:  SanitizeText(summary),
						LeafUUID: leafUUID,
					})
				} else {
					// Local summary for current session (last one wins)
					result.Summary = SanitizeText(summary)
				}
			}
		}
	}

	return result
}
