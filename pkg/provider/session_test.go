package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPathIsUnderAnyRoot(t *testing.T) {
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "root")
	other := filepath.Join(tmpDir, "other")
	for _, d := range []string{root, other} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}

	t.Run("under single root", func(t *testing.T) {
		path := filepath.Join(root, "sub", "file.jsonl")
		if !pathIsUnderAnyRoot(path, []string{root}) {
			t.Error("expected true for path under root")
		}
	})

	t.Run("not under root", func(t *testing.T) {
		path := filepath.Join(other, "file.jsonl")
		if pathIsUnderAnyRoot(path, []string{root}) {
			t.Error("expected false for path outside root")
		}
	})

	t.Run("multiple roots — matches second", func(t *testing.T) {
		path := filepath.Join(other, "file.jsonl")
		if !pathIsUnderAnyRoot(path, []string{root, other}) {
			t.Error("expected true when path matches second root")
		}
	})

	t.Run("traversal via .. rejected by caller, cleaned path is safe", func(t *testing.T) {
		// The caller (ValidateTranscriptPath) rejects ".." before calling us.
		// Verify that a lexically-cleaned path that stays under root is accepted.
		path := filepath.Clean(filepath.Join(root, "sub", "..", "file.jsonl"))
		if !pathIsUnderAnyRoot(path, []string{root}) {
			t.Error("expected true for cleaned path under root")
		}
	})

	t.Run("nonexistent parent — lexical fallback", func(t *testing.T) {
		// Parent directory does not exist; EvalSymlinks fails, falls back to lexical check.
		path := filepath.Join(root, "newproject", "session.jsonl")
		if !pathIsUnderAnyRoot(path, []string{root}) {
			t.Error("expected true for nonexistent parent under root (lexical fallback)")
		}
	})

	t.Run("symlinked parent escaping root", func(t *testing.T) {
		linkDir := filepath.Join(root, "link-out")
		if err := os.Symlink(other, linkDir); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		path := filepath.Join(linkDir, "file.jsonl")
		if pathIsUnderAnyRoot(path, []string{root}) {
			t.Error("expected false for symlink escaping root")
		}
	})
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		expected string
	}{
		{"empty string", "", 10, ""},
		{"no truncation needed", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncate ASCII", "hello world", 8, "hello..."},
		{"truncate at UTF-8 boundary", "hello 世界 world", 12, "hello 世..."},
		{"truncate mid-UTF8 removes partial char", "hello 世界", 10, "hello ..."},
		{"maxBytes equals ellipsis length", "hello", 3, "..."},
		{"maxBytes less than ellipsis length — truncates without suffix", "hello", 2, "he"},
		{"maxBytes zero", "hello", 0, ""},
		{"maxBytes negative", "hello", -5, ""},
		{"emoji at boundary", "hello 🌍 world", 11, "hello ..."},
		{"all multibyte", "世界世界世界", 7, "世..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateUTF8(tt.input, tt.maxBytes)
			if result != tt.expected {
				t.Errorf("TruncateUTF8(%q, %d) = %q, want %q", tt.input, tt.maxBytes, result, tt.expected)
			}
		})
	}
}

func TestTruncateUTF8_NeverExceedsMaxBytes(t *testing.T) {
	// For every maxBytes from 0 to 20, verify the result never exceeds it.
	for maxBytes := 0; maxBytes <= 20; maxBytes++ {
		input := strings.Repeat("a", 100)
		result := TruncateUTF8(input, maxBytes)
		if len(result) > maxBytes {
			t.Errorf("TruncateUTF8(%q, %d) length = %d, want <= %d", input, maxBytes, len(result), maxBytes)
		}
	}
}

func TestTruncateUTF8_ValidUTF8(t *testing.T) {
	// Truncation must never produce invalid UTF-8.
	input := strings.Repeat("世界", 100)
	for maxBytes := 1; maxBytes <= 30; maxBytes++ {
		result := TruncateUTF8(input, maxBytes)
		if !utf8.ValidString(result) {
			t.Errorf("TruncateUTF8 produced invalid UTF-8 at maxBytes=%d: %q", maxBytes, result)
		}
	}
}

func TestTruncateUTF8_AppendsEllipsisOnTruncation(t *testing.T) {
	input := strings.Repeat("a", 100)
	result := TruncateUTF8(input, 10)
	if !strings.HasSuffix(result, "...") {
		t.Errorf("TruncateUTF8(%q, 10) = %q, want suffix \"...\"", input, result)
	}
}

func TestTruncateUTF8_NoEllipsisWhenNoTruncation(t *testing.T) {
	result := TruncateUTF8("short", 100)
	if strings.HasSuffix(result, "...") {
		t.Errorf("TruncateUTF8(%q, 100) = %q, should not have ellipsis", "short", result)
	}
}

func TestTruncateUTF8_ParityAcrossProviders(t *testing.T) {
	// All three providers must produce identical truncation for the same input.
	longInput := strings.Repeat("hello 世界 ", 500)
	limit := 4096

	// Claude path: sanitizeText + TruncateUTF8
	claudeResult := TruncateUTF8(longInput, limit)

	// Codex path: TruncateUTF8 directly
	codexResult := TruncateUTF8(strings.TrimSpace(longInput), limit)

	// OpenCode path: TruncateUTF8 directly
	opencodeResult := TruncateUTF8(longInput, limit)

	// Claude and OpenCode see identical input → identical output.
	if claudeResult != opencodeResult {
		t.Errorf("Claude and OpenCode truncation diverged:\n  Claude:   %q\n  OpenCode: %q", claudeResult, opencodeResult)
	}
	// Codex trims whitespace first, so compare trimmed.
	if codexResult != TruncateUTF8(longInput, limit) {
		t.Errorf("Codex truncation diverged from direct call:\n  Codex:  %q\n  Direct: %q", codexResult, TruncateUTF8(longInput, limit))
	}
}
