package confabpath

import (
	"path/filepath"
	"testing"
)

// setHome redirects HOME to a temp dir for the duration of the test so
// Dir/Subpath resolve against a sandbox instead of the real home.
// Returns the temp dir for use in expected-path assertions.
func setHome(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	return tmpDir
}

// Spec: Dir() returns ~/.confab using os.UserHomeDir under the hood.
func TestDir(t *testing.T) {
	tmpDir := setHome(t)

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	want := filepath.Join(tmpDir, ".confab")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

// Spec: Subpath joins ~/.confab with the given segments. Empty trailing
// segments are dropped by filepath.Join (documented stdlib behavior).
func TestSubpath(t *testing.T) {
	tmpDir := setHome(t)
	confabDir := filepath.Join(tmpDir, ".confab")

	tests := []struct {
		name  string
		first string
		rest  []string
		want  string
	}{
		{
			name:  "single segment",
			first: "logs",
			want:  filepath.Join(confabDir, "logs"),
		},
		{
			name:  "multiple segments",
			first: "sync",
			rest:  []string{"claude-code", "abc.json"},
			want:  filepath.Join(confabDir, "sync", "claude-code", "abc.json"),
		},
		{
			name:  "empty trailing segment is dropped",
			first: "logs",
			rest:  []string{""},
			want:  filepath.Join(confabDir, "logs"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Subpath(tt.first, tt.rest...)
			if err != nil {
				t.Fatalf("Subpath(%q, %v) error = %v", tt.first, tt.rest, err)
			}
			if got != tt.want {
				t.Errorf("Subpath(%q, %v) = %q, want %q", tt.first, tt.rest, got, tt.want)
			}
		})
	}
}
