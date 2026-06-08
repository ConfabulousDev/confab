package sync

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/git"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/ConfabulousDev/confab/pkg/redactor"
	"github.com/ConfabulousDev/confab/pkg/types"
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

	// CodexRollout, if non-nil, marks this tracked file as a Codex rollout
	// for which the engine should emit `codex_rollout` chunk metadata on
	// the FIRST chunk uploaded for this file. "First chunk" is detected
	// via chunk.FirstLine == 1; no separate state field is required.
	// Roots and descendants both carry this; only the engine's emission
	// gate (FirstLine==1) determines when it goes on the wire.
	CodexRollout *CodexRolloutMetadata
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
	subagentsDir   string // <session-id>/subagents/ directory for agent files
	files          map[string]*TrackedFile
	knownAgentIDs  map[string]bool // Agent IDs we've already discovered
}

// NewFileTracker creates a new file tracker for a session
func NewFileTracker(transcriptPath string) *FileTracker {
	// Derive subagents directory from transcript path:
	// transcript: <project>/<session-id>.jsonl
	// subagents:  <project>/<session-id>/subagents/
	base := strings.TrimSuffix(transcriptPath, filepath.Ext(transcriptPath))
	return &FileTracker{
		transcriptPath: transcriptPath,
		subagentsDir:   filepath.Join(base, "subagents"),
		files:          make(map[string]*TrackedFile),
		knownAgentIDs:  make(map[string]bool),
	}
}

// InitFromBackendState initializes the tracker with state from the backend.
// This sets up tracking for the transcript and any files the backend knows about.
//
// Called from both Engine.Init() (first time) and Engine.refreshStateFromBackend()
// (after a chunk-upload failure). On refresh, any per-file metadata that the
// engine has already set on a tracked file (notably Codex rollout metadata)
// must survive — otherwise a retried first chunk would lose its codex_rollout
// payload. We preserve CodexRollout for existing entries and only refresh the
// fields that can legitimately drift (sync position).
func (t *FileTracker) InitFromBackendState(backendFiles map[string]FileState) {
	transcriptName := filepath.Base(t.transcriptPath)

	// Add transcript
	transcriptState := backendFiles[transcriptName]
	t.files[transcriptName] = t.buildTrackedFromState(TrackedFile{
		Path:           t.transcriptPath,
		Name:           transcriptName,
		Type:           "transcript",
		LastSyncedLine: transcriptState.LastSyncedLine,
		ByteOffset:     0, // Will be set on first read
	})

	// Add any other files from backend state (agent files)
	for fileName, state := range backendFiles {
		if fileName == transcriptName {
			continue
		}

		path := ""
		if prev, ok := t.files[fileName]; ok {
			path = prev.Path
		}
		if path == "" {
			// First time we've seen this file; default to subagents dir.
			// Codex children, when present in the tracker, already have an
			// absolute rollout path from AddCodexRollout, so this branch is
			// only taken for genuinely-new Claude agent files.
			path = filepath.Join(t.subagentsDir, fileName)
		}
		t.files[fileName] = t.buildTrackedFromState(TrackedFile{
			Path:           path,
			Name:           fileName,
			Type:           "agent",
			LastSyncedLine: state.LastSyncedLine,
			ByteOffset:     0, // Will be set on first read
		})
	}
}

func (t *FileTracker) buildTrackedFromState(next TrackedFile) *TrackedFile {
	if prev, ok := t.files[next.Name]; ok {
		next.CodexRollout = prev.CodexRollout
	}
	return &next
}

// SetCodexRollout assigns Codex rollout metadata to this tracked file, used
// by provider.Codex.InitTranscript via the provider.TranscriptRegistrar
// interface. Separated from direct field assignment so the engine and
// providers don't need to share struct internals.
func (f *TrackedFile) SetCodexRollout(meta *provider.CodexRolloutMetadata) {
	f.CodexRollout = meta
}

// RegisterCodexRollout is the provider.DescendantRegistrar entry point:
// thin wrapper around AddCodexRollout that drops the *TrackedFile return
// (kept on AddCodexRollout for in-package callers that want it).
func (t *FileTracker) RegisterCodexRollout(path, fileName string, isRoot bool, meta provider.CodexRolloutMetadata) {
	t.AddCodexRollout(path, fileName, isRoot, meta)
}

// SubagentsDir returns the <session>/subagents directory for this tracker.
// Part of provider.WorkflowRegistrar: the Claude provider scans
// subagents/workflows/<runId>/ beneath it to discover workflow files.
func (t *FileTracker) SubagentsDir() string {
	return t.subagentsDir
}

// RegisterSidechainFile is the entry point for registering path-encoded
// sidechain files. CF-533 uses it for Claude workflow subagent transcripts +
// run journals; CF-538 uses it for OpenCode subagent messages. name is the
// forward-slash path-encoded backend file_name; path is its absolute
// location on disk; fileType is "agent" or provider.FileTypeWorkflowJournal.
//
// Returns true when a new file is tracked. When the name is already tracked
// (e.g. a stale entry rebuilt from backend state on daemon restart), it
// overwrites Path+Type in place and returns false, preserving the sync
// position (LastSyncedLine/ByteOffset) so the file resumes incrementally.
func (t *FileTracker) RegisterSidechainFile(path, name, fileType string) bool {
	if existing, ok := t.files[name]; ok {
		existing.Path = path
		existing.Type = fileType
		return false
	}
	t.files[name] = &TrackedFile{
		Path: path,
		Name: name,
		Type: fileType,
	}
	return true
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

// gitInfoFromClaudeMessage extracts per-chunk git info from a Claude
// transcript message (inline `gitBranch` + `cwd`). Returns nil for any
// other shape (including agent files, where Type != "transcript").
func gitInfoFromClaudeMessage(file *TrackedFile, msg map[string]interface{}) *git.GitInfo {
	if file.Type != "transcript" {
		return nil
	}
	branch, _ := msg["gitBranch"].(string)
	if branch == "" {
		return nil
	}
	info := &git.GitInfo{Branch: branch}
	if cwd, _ := msg["cwd"].(string); cwd != "" {
		info.RepoURL, _ = git.GetRepoURL(cwd)
		info.Remotes, _ = git.DetectRemotes(cwd)
		info.TrackingRemote = git.DetectTrackingRemote(cwd, branch)
	}
	return info
}

// gitInfoFromCodexSessionMeta extracts git info from a Codex
// `session_meta` line (root rollouts and descendant agent files both
// begin with one). Returns nil if msg isn't a session_meta, has no cwd,
// or the cwd has no remotes — keying on type=="session_meta" is safe
// since Claude transcripts never use that literal.
func gitInfoFromCodexSessionMeta(msg map[string]interface{}) *git.GitInfo {
	if t, _ := msg["type"].(string); t != "session_meta" {
		return nil
	}
	payload, ok := msg["payload"].(map[string]interface{})
	if !ok {
		return nil
	}
	cwd, _ := payload["cwd"].(string)
	if cwd == "" {
		return nil
	}
	remotes, _ := git.DetectRemotes(cwd)
	if len(remotes) == 0 {
		return nil
	}
	repoURL, _ := git.GetRepoURL(cwd)
	branch := git.DetectBranch(cwd)
	return &git.GitInfo{
		RepoURL:        repoURL,
		Remotes:        remotes,
		Branch:         branch,
		TrackingRemote: git.DetectTrackingRemote(cwd, branch),
	}
}

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

	// Set up scanner with buffer larger than maxBytes so we can detect when a single
	// line exceeds the chunk limit. This intentionally doesn't use types.NewJSONLScanner
	// because the buffer must exceed DefaultMaxChunkBytes (14MB) + headroom = ~24MB,
	// which is larger than the standard 10MB JSONL scanner buffer.
	scanner := bufio.NewScanner(f)
	maxLineSize := maxBytes + types.MaxJSONLLineSize // maxBytes + 10MB headroom
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
				// Extract agent IDs (agents can spawn other agents).
				// Agent-ID extraction is Claude-only — Codex tracks
				// subagents via its SQLite thread tree, not via inline
				// IDs in rollout JSONL. Hard-binding to ClaudeCode here
				// is the explicit "this is Claude's transcript schema"
				// signal; see pkg/provider/claude_agentids.go.
				for _, agentID := range (provider.ClaudeCode{}).ExtractAgentIDsFromMessage(msg) {
					if !seenAgents[agentID] {
						seenAgents[agentID] = true
						agentIDs = append(agentIDs, agentID)
					}
				}

				// Extract git info — first message wins. Two provider-
				// agnostic paths, keyed by message shape:
				//   - Claude: any message with inline `gitBranch` + `cwd`
				//     (transcript files only — agent JSONL has its own
				//     branch-less shape).
				//   - Codex: the leading `session_meta` line (both root
				//     transcripts and descendant agent files) whose
				//     payload carries `cwd`.
				// Both fan out to the same git.Detect* helpers so the
				// wire shape stays identical across providers.
				if gitInfo == nil {
					gitInfo = gitInfoFromClaudeMessage(file, msg)
				}
				if gitInfo == nil {
					gitInfo = gitInfoFromCodexSessionMeta(msg)
				}
			}
		}

		// Apply redaction if enabled. Provider-agnostic: RedactJSONLine
		// walks any JSON shape, so Claude transcripts, Claude agent JSONL,
		// and Codex rollouts all flow through the same pattern set. The
		// backend's per-provider Redactions analytics cards depend on
		// this being the sole place lines are scrubbed before upload.
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
// discovered in previous chunk reads, and also scans the subagents
// directory for any agent files not already tracked.
// Returns newly discovered files.
func (t *FileTracker) DiscoverNewFiles(newAgentIDs []string) []*TrackedFile {
	var newFiles []*TrackedFile

	// Add new agent IDs to known set
	for _, agentID := range newAgentIDs {
		t.knownAgentIDs[agentID] = true
	}

	// Check all known agent IDs for files that now exist
	for agentID := range t.knownAgentIDs {
		agentFileName := fmt.Sprintf("agent-%s.jsonl", agentID)
		if t.IsTracked(agentFileName) {
			continue
		}
		if tracked := t.trackAgentFile(agentFileName); tracked != nil {
			newFiles = append(newFiles, tracked)
		}
	}

	// Scan the subagents directory for any agent files not already tracked.
	// This catches files that we missed because agent IDs from already-synced
	// transcript lines are not in memory (e.g., after daemon restart).
	entries, err := os.ReadDir(t.subagentsDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			if t.IsTracked(name) {
				continue
			}
			if tracked := t.trackAgentFile(name); tracked != nil {
				newFiles = append(newFiles, tracked)
			}
		}
	}

	return newFiles
}

// trackAgentFile attempts to start tracking an agent file by name.
// Returns the TrackedFile if the file exists on disk, nil otherwise.
func (t *FileTracker) trackAgentFile(fileName string) *TrackedFile {
	agentPath := filepath.Join(t.subagentsDir, fileName)
	if _, err := os.Stat(agentPath); err != nil {
		return nil
	}
	tracked := &TrackedFile{
		Path: agentPath,
		Name: fileName,
		Type: "agent",
	}
	t.files[fileName] = tracked
	return tracked
}

// GetTranscriptFile returns the transcript file being tracked
func (t *FileTracker) GetTranscriptFile() *TrackedFile {
	transcriptName := filepath.Base(t.transcriptPath)
	return t.files[transcriptName]
}

// AddCodexRollout registers a Codex rollout file in the tracker.
//
// isRoot=true → file type "transcript" (the Codex root's primary rollout).
// isRoot=false → file type "agent" (every descendant, at any depth).
//
// All descendants sync as sidechain files under the root's backend session
// — the same primitive Claude Code uses for its `agent-*.jsonl` files —
// while the Codex thread tree is preserved separately in the backend's
// `codex_rollouts` table via `meta.ParentThreadUUID`.
//
// Idempotent: a second call for an already-tracked path returns the
// existing TrackedFile without modifying it. The caller can use this to
// avoid maintaining a separate "already added" set.
func (t *FileTracker) AddCodexRollout(path, fileName string, isRoot bool, meta CodexRolloutMetadata) *TrackedFile {
	if existing, ok := t.files[fileName]; ok {
		return existing
	}
	fileType := "agent"
	if isRoot {
		fileType = "transcript"
	}
	tracked := &TrackedFile{
		Path:         path,
		Name:         fileName,
		Type:         fileType,
		CodexRollout: &meta,
	}
	t.files[fileName] = tracked
	return tracked
}
