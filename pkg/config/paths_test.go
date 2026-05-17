package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsLinkFromGitHubDisabled covers paths.go:20 (0% before). If the
// env var name silently changes, the link-from-GitHub feature would
// stop honoring user-disable intent without any test failure.
func TestIsLinkFromGitHubDisabled(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		val  string
		want bool
	}{
		{"unset", false, "", false},
		{"set empty string", true, "", false},
		{"set to 1", true, "1", true},
		{"set to true", true, "true", true},
		{"set to anything non-empty", true, "yes please", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(DisableLinkFromGitHubEnv, tt.val)
			} else {
				// t.Setenv with "" still sets it; explicitly unset.
				orig := os.Getenv(DisableLinkFromGitHubEnv)
				os.Unsetenv(DisableLinkFromGitHubEnv)
				t.Cleanup(func() {
					if orig != "" {
						os.Setenv(DisableLinkFromGitHubEnv, orig)
					}
				})
			}
			if got := IsLinkFromGitHubDisabled(); got != tt.want {
				t.Errorf("IsLinkFromGitHubDisabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetClaudeStateDir(t *testing.T) {
	originalEnv := os.Getenv(ClaudeStateDirEnv)
	defer os.Setenv(ClaudeStateDirEnv, originalEnv)

	home, _ := os.UserHomeDir()

	tests := []struct {
		name    string
		envVal  string
		want    string
		wantErr bool
	}{
		{
			name:    "default to ~/.claude",
			envVal:  "",
			want:    filepath.Join(home, ".claude"),
			wantErr: false,
		},
		{
			name:    "override with env var",
			envVal:  "/tmp/custom-claude",
			want:    "/tmp/custom-claude",
			wantErr: false,
		},
		{
			name:    "override with relative path",
			envVal:  "my-claude-dir",
			want:    "my-claude-dir",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal == "" {
				os.Unsetenv(ClaudeStateDirEnv)
			} else {
				os.Setenv(ClaudeStateDirEnv, tt.envVal)
			}

			got, err := GetClaudeStateDir()
			if (err != nil) != tt.wantErr {
				t.Errorf("GetClaudeStateDir() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetClaudeStateDir() = %v, want %v", got, tt.want)
			}
		})
	}
}
