package sync

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ConfabulousDev/confab/pkg/discovery"
	"github.com/ConfabulousDev/confab/pkg/git"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/redactor"
)

// TrackedFile represents a file being synced
type TrackedFile struct {
	Path           string    // Full path to the file
	Name           string    // Base name of the file
	Type           string    // "transcript" or "agent"
	LastSyncedLine int       // Last line number synced to backend (1-based)
	ByteOffset     int64     // Byte position after LastSyncedLine (for seeking)
	LastModTime    time.Time // Last modification time (for change detection)
	LastSize       int64     // Last known size (for change detection)
}

// Chunk represents a range of lines read from a file with extracted metadata
type Chunk struct {
	FileName  string         // Base name of the file
	FileType  string         // "transcript" or "agent"
	FirstLine int            // 1-based line number of first line
	Lines     []string       // The lines (redacted if applicable)
	NewOffset int64          // Byte offset after reading these lines
	Metadata  *ChunkMetadata // Metadata to send to backend
	AgentIDs  []string       // Agent IDs discovered (local use only, not sent to backend)
}

// FileTracker tracks files and their sync state for a session
type FileTracker struct {
	transcriptPath string
	transcriptDir  string
	files          map[string]*TrackedFile
	knownAgentIDs  map[string]bool // Agent IDs we've already discovered
}

// NewFileTracker creates a new file tracker for a session
func NewFileTracker(transcriptPath string) *FileTracker {
	return &FileTracker{
		transcriptPath: transcriptPath,
		transcriptDir:  filepath.Dir(transcriptPath),
		files:          make(map[string]*TrackedFile),
		knownAgentIDs:  make(map[string]bool),
	}
}

// InitFromBackendState initializes the tracker with state from the backend.
// This sets up tracking for the transcript and any files the backend knows about.
func (t *FileTracker) InitFromBackendState(backendFiles map[string]FileState) {
	transcriptName := filepath.Base(t.transcriptPath)

	// Add transcript
	transcriptState := backendFiles[transcriptName]
	t.files[transcriptName] = &TrackedFile{
		Path:           t.transcriptPath,
		Name:           transcriptName,
		Type:           "transcript",
		LastSyncedLine: transcriptState.LastSyncedLine,
		ByteOffset:     0, // Will be set on first read
	}

	// Add any other files from backend state (agent files)
	for fileName, state := range backendFiles {
		if fileName == transcriptName {
			continue
		}

		t.files[fileName] = &TrackedFile{
			Path:           filepath.Join(t.transcriptDir, fileName),
			Name:           fileName,
			Type:           "agent",
			LastSyncedLine: state.LastSyncedLine,
			ByteOffset:     0, // Will be set on first read
		}
	}
}

// GetTrackedFiles returns all currently tracked files
func (t *FileTracker) GetTrackedFiles() []*TrackedFile {
	result := make([]*TrackedFile, 0, len(t.files))
	for _, f := range t.files {
		result = append(result, f)
	}
	return result
}

// IsTracked returns true if a file is already being tracked
func (t *FileTracker) IsTracked(fileName string) bool {
	_, ok := t.files[fileName]
	return ok
}

// HasFileChanged checks if a file has more data to sync.
// Returns true if:
// - The file has grown (more bytes than our last known offset)
// - The file has been modified (mod time changed)
// - We haven't read the file yet (no byte offset)
func (t *FileTracker) HasFileChanged(file *TrackedFile) bool {
	info, err := os.Stat(file.Path)
	if err != nil {
		// Can't stat - assume changed to be safe
		return true
	}

	size := info.Size()
	modTime := info.ModTime()

	// If we have a byte offset, check if there's more data beyond it
	if file.ByteOffset > 0 && size > file.ByteOffset {
		return true
	}

	// Check if file was modified since last sync
	if !modTime.Equal(file.LastModTime) || size != file.LastSize {
		return true
	}

	return false
}

// DefaultMaxChunkBytes is the default maximum size of a chunk in bytes.
// This is a backend-imposed limit: the server rejects chunks larger than 16MB.
// We use 14MB to leave headroom for JSON encoding overhead and compression.
// If the backend limit changes, this constant must be updated accordingly.
const DefaultMaxChunkBytes = 14 * 1024 * 1024 // 14MB

// ReadChunk reads new lines from a file starting after LastSyncedLine.
// Uses ByteOffset to seek directly to the right position if available.
// Applies redaction if a redactor is provided.
// Stops reading when accumulated bytes would exceed maxBytes (aligned to line boundary).
// Returns nil if there are no new lines.
func (t *FileTracker) ReadChunk(file *TrackedFile, r *redactor.Redactor, maxBytes int) (*Chunk, error) {
	f, err := os.Open(file.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	var lines []string
	var metadata *ChunkMetadata
	var newOffset int64
	var totalBytes int
	var currentOffset int64
	var readingFromStart bool // true if we're reading from start (offset 0)

	// If we have a byte offset from a previous read, try to seek to it
	if file.ByteOffset > 0 && file.LastSyncedLine > 0 {
		// Seek to the saved offset
		if _, err := f.Seek(file.ByteOffset, io.SeekStart); err != nil {
			// Seek failed, fall back to reading from start.
			// Use local state rather than mutating file.ByteOffset.
			logger.Debug("Seek to offset %d failed, falling back to start: %v", file.ByteOffset, err)
			readingFromStart = true
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return nil, fmt.Errorf("failed to seek to start: %w", err)
			}
		} else {
			currentOffset = file.ByteOffset
		}
	} else {
		readingFromStart = true
	}

	// Set up scanner with large buffer for transcripts with big tool results
	// Buffer must be larger than maxBytes to detect oversized lines
	scanner := bufio.NewScanner(f)
	maxLineSize := maxBytes + 10*1024*1024 // maxBytes + 10MB headroom
	scanner.Buffer(make([]byte, bufio.MaxScanTokenSize), maxLineSize)

	lineNum := file.LastSyncedLine // Start counting from where we left off
	if readingFromStart {
		lineNum = 0 // Reading from start, so start at line 0
	}

	// Extract metadata from transcript and agent files (for transitive agent discovery)
	extractMetadata := file.Type == "transcript" || file.Type == "agent"
	var agentIDs []string
	var gitInfo *git.GitInfo
	seenAgents := make(map[string]bool)

	// Copy known agent IDs to seen set so we don't re-report them
	for id := range t.knownAgentIDs {
		seenAgents[id] = true
	}

	for scanner.Scan() {
		lineNum++
		lineWithNewline := len(scanner.Bytes()) + 1 // +1 for newline

		// If we're reading from start and need to skip already-synced lines
		if readingFromStart && lineNum <= file.LastSyncedLine {
			currentOffset += int64(lineWithNewline)
			continue
		}

		line := scanner.Text()

		// Check if adding this line would exceed the chunk size limit
		// Account for JSON array overhead: quotes, comma, etc. (~4 bytes per line)
		lineBytes := len(line) + 4

		if totalBytes+lineBytes > maxBytes {
			if totalBytes == 0 {
				// First line of chunk exceeds limit - cannot proceed past this line
				return nil, fmt.Errorf("line %d exceeds max chunk size (%d bytes > %d bytes)", lineNum, lineBytes, maxBytes)
			}
			// Would exceed limit - stop here, this line will be read next time
			// newOffset stays at current position (before this line)
			newOffset = currentOffset
			break
		}
		totalBytes += lineBytes
		currentOffset += int64(lineWithNewline)

		// Extract metadata from transcript and agent lines
		if extractMetadata {
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(line), &msg); err == nil {
				// Extract agent IDs (agents can spawn other agents)
				for _, agentID := range discovery.ExtractAgentIDsFromMessage(msg) {
					if !seenAgents[agentID] {
						seenAgents[agentID] = true
						agentIDs = append(agentIDs, agentID)
					}
				}

				// Extract git info (transcript only, first one wins)
				if file.Type == "transcript" && gitInfo == nil {
					if branch, ok := msg["gitBranch"].(string); ok && branch != "" {
						gitInfo = &git.GitInfo{Branch: branch}
						if cwd, ok := msg["cwd"].(string); ok {
							gitInfo.RepoURL, _ = git.GetRepoURL(cwd)
						}
					}
				}
			}
		}

		// Apply redaction if enabled
		if r != nil {
			line = r.RedactJSONLine(line)
		}

		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan file: %w", err)
	}

	if len(lines) == 0 {
		return nil, nil // No new lines
	}

	// Get the current file position as the new offset (if not already set by early break).
	//
	// Note: Using Seek after a bufio.Scanner relies on the scanner having consumed
	// all buffered data, which holds true for complete reads of JSONL files where
	// every line ends with a newline (as Claude Code transcripts do). For malformed
	// files without trailing newlines, Seek and the tracked currentOffset could differ.
	// This is acceptable since Claude Code always writes properly formatted JSONL.
	if newOffset == 0 {
		seekOffset, _ := f.Seek(0, io.SeekCurrent)
		// Detect offset discrepancy that could indicate a malformed file
		if seekOffset != currentOffset {
			logger.Debug("Offset discrepancy in %s: tracked=%d, seek=%d (possible missing trailing newline)",
				file.Path, currentOffset, seekOffset)
		}
		newOffset = seekOffset
	}

	// Build metadata for backend (git info only)
	if gitInfo != nil {
		metadata = &ChunkMetadata{
			GitInfo: gitInfo,
		}
	}

	return &Chunk{
		FileName:  file.Name,
		FileType:  file.Type,
		FirstLine: file.LastSyncedLine + 1,
		Lines:     lines,
		NewOffset: newOffset,
		Metadata:  metadata,
		AgentIDs:  agentIDs, // Local use only, not sent to backend
	}, nil
}

// UpdateAfterSync updates the tracked file state after a successful sync.
// This updates both the sync position and the cached file stats (modtime/size)
// so HasFileChanged won't re-trigger until the file actually changes again.
func (t *FileTracker) UpdateAfterSync(file *TrackedFile, lastLine int, newOffset int64) {
	file.LastSyncedLine = lastLine
	file.ByteOffset = newOffset

	// Update cached file stats so HasFileChanged returns false until file changes again
	if info, err := os.Stat(file.Path); err == nil {
		file.LastModTime = info.ModTime()
		file.LastSize = info.Size()
	}
}

// DiscoverNewFiles checks for new agent files based on agent IDs
// discovered in previous chunk reads. Returns newly discovered files.
// Also re-checks known agent IDs in case their files now exist on disk.
func (t *FileTracker) DiscoverNewFiles(newAgentIDs []string) []*TrackedFile {
	var newFiles []*TrackedFile

	// Add new agent IDs to known set
	for _, agentID := range newAgentIDs {
		t.knownAgentIDs[agentID] = true
	}

	// Check all known agent IDs for files that now exist
	for agentID := range t.knownAgentIDs {
		agentFileName := fmt.Sprintf("agent-%s.jsonl", agentID)

		// Skip if already tracked
		if t.IsTracked(agentFileName) {
			continue
		}

		agentPath := filepath.Join(t.transcriptDir, agentFileName)

		// Check if file exists on disk
		if _, err := os.Stat(agentPath); err != nil {
			continue // Agent file doesn't exist yet
		}

		// Add to tracked files
		tracked := &TrackedFile{
			Path:           agentPath,
			Name:           agentFileName,
			Type:           "agent",
			LastSyncedLine: 0,
			ByteOffset:     0,
		}
		t.files[agentFileName] = tracked
		newFiles = append(newFiles, tracked)
	}

	return newFiles
}

// GetTranscriptFile returns the transcript file being tracked
func (t *FileTracker) GetTranscriptFile() *TrackedFile {
	transcriptName := filepath.Base(t.transcriptPath)
	return t.files[transcriptName]
}
