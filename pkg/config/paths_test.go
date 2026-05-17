package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
