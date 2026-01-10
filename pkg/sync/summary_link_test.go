package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSessionByLeafUUID(t *testing.T) {
	// Create temp directory with test transcripts
	tmpDir := t.TempDir()

	// Transcript 1: has the target uuid in last lines
	transcript1 := filepath.Join(tmpDir, "aaaaaaaa-1111-1111-1111-111111111111.jsonl")
	transcript1Content := `{"type":"user","message":{"content":"Hello"}}
{"type":"assistant","message":{"content":"Hi there"}}
{"type":"user","message":{"content":"Thanks"},"uuid":"target-uuid-123"}`
	os.WriteFile(transcript1, []byte(transcript1Content), 0644)

	// Transcript 2: does NOT have the target uuid
	transcript2 := filepath.Join(tmpDir, "bbbbbbbb-2222-2222-2222-222222222222.jsonl")
	transcript2Content := `{"type":"user","message":{"content":"Other session"}}
{"type":"assistant","message":{"content":"Response"},"uuid":"different-uuid"}`
	os.WriteFile(transcript2, []byte(transcript2Content), 0644)

	// Transcript 3: current session (should be excluded)
	transcript3 := filepath.Join(tmpDir, "cccccccc-3333-3333-3333-333333333333.jsonl")
	transcript3Content := `{"type":"summary","summary":"Test","leafUuid":"target-uuid-123"}`
	os.WriteFile(transcript3, []byte(transcript3Content), 0644)

	// Agent file (should be skipped)
	agentFile := filepath.Join(tmpDir, "agent-12345678.jsonl")
	agentContent := `{"type":"agent","uuid":"target-uuid-123"}`
	os.WriteFile(agentFile, []byte(agentContent), 0644)

	tests := []struct {
		name        string
		leafUUID    string
		excludeFile string
		wantSession string
	}{
		{
			name:        "finds matching session",
			leafUUID:    "target-uuid-123",
			excludeFile: "cccccccc-3333-3333-3333-333333333333.jsonl",
			wantSession: "aaaaaaaa-1111-1111-1111-111111111111",
		},
		{
			name:        "no match found",
			leafUUID:    "nonexistent-uuid",
			excludeFile: "cccccccc-3333-3333-3333-333333333333.jsonl",
			wantSession: "",
		},
		{
			name:        "excludes current file",
			leafUUID:    "target-uuid-123",
			excludeFile: "aaaaaaaa-1111-1111-1111-111111111111.jsonl",
			wantSession: "", // transcript1 is excluded, transcript2 doesn't match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindSessionByLeafUUID(tmpDir, tt.leafUUID, tt.excludeFile)
			if result != tt.wantSession {
				t.Errorf("FindSessionByLeafUUID() = %q, want %q", result, tt.wantSession)
			}
		})
	}
}

func TestFindSessionByLeafUUID_LastNLines(t *testing.T) {
	// Test that only last N lines are searched
	tmpDir := t.TempDir()

	// Create transcript with uuid NOT in last 10 lines
	var content string
	content += `{"type":"user","uuid":"old-uuid-at-start"}` + "\n"
	// Add 15 more lines without the uuid
	for i := 0; i < 15; i++ {
		content += `{"type":"assistant","message":{"content":"filler"}}` + "\n"
	}

	transcript := filepath.Join(tmpDir, "test-session.jsonl")
	os.WriteFile(transcript, []byte(content), 0644)

	// Should NOT find the uuid since it's beyond last 10 lines
	result := FindSessionByLeafUUID(tmpDir, "old-uuid-at-start", "other.jsonl")
	if result != "" {
		t.Errorf("Expected empty result for uuid beyond last 10 lines, got %q", result)
	}
}

func TestFindSessionByLeafUUID_UUIDInLast10Lines(t *testing.T) {
	// Test that uuid in last 10 lines IS found
	tmpDir := t.TempDir()

	// Create transcript with uuid in last 10 lines
	var content string
	// Add 5 filler lines
	for i := 0; i < 5; i++ {
		content += `{"type":"assistant","message":{"content":"filler"}}` + "\n"
	}
	// Add line with uuid (will be within last 10)
	content += `{"type":"user","uuid":"recent-uuid"}` + "\n"
	// Add 4 more lines (uuid is now 5th from end, within last 10)
	for i := 0; i < 4; i++ {
		content += `{"type":"assistant","message":{"content":"more filler"}}` + "\n"
	}

	transcript := filepath.Join(tmpDir, "aaaaaaaa-1111-1111-1111-111111111111.jsonl")
	os.WriteFile(transcript, []byte(content), 0644)

	result := FindSessionByLeafUUID(tmpDir, "recent-uuid", "other.jsonl")
	if result != "aaaaaaaa-1111-1111-1111-111111111111" {
		t.Errorf("Expected to find session, got %q", result)
	}
}

func TestHasUUIDInLastLines_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "empty.jsonl")
	os.WriteFile(emptyFile, []byte(""), 0644)

	result := hasUUIDInLastLines(emptyFile, "any-uuid")
	if result {
		t.Error("Expected false for empty file")
	}
}

func TestHasUUIDInLastLines_NonexistentFile(t *testing.T) {
	result := hasUUIDInLastLines("/nonexistent/path.jsonl", "any-uuid")
	if result {
		t.Error("Expected false for nonexistent file")
	}
}
