package hookconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCodexHooksWritesManagedBlock(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	got, err := InstallCodexHooks(configPath)
	if err != nil {
		t.Fatalf("InstallCodexHooks() error = %v", err)
	}
	if got != configPath {
		t.Fatalf("InstallCodexHooks() = %q, want %q", got, configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"[[hooks.SessionStart]]",
		"hook session-start --provider codex",
		"# >>> confab codex hooks >>>",
		"# <<< confab codex hooks <<<",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config.toml missing %q after InstallCodexHooks()\n%s", want, out)
		}
	}
}

func TestUninstallCodexHooksRemovesManagedBlock(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	if _, err := InstallCodexHooks(configPath); err != nil {
		t.Fatalf("InstallCodexHooks() error = %v", err)
	}
	if _, err := UninstallCodexHooks(configPath); err != nil {
		t.Fatalf("UninstallCodexHooks() error = %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml missing after uninstall: %v", err)
	}
	for _, notWant := range []string{
		"# >>> confab codex hooks >>>",
		"hook session-start --provider codex",
	} {
		if strings.Contains(string(data), notWant) {
			t.Errorf("config.toml still contains %q after UninstallCodexHooks()\n%s", notWant, string(data))
		}
	}
}

func TestIsCodexHooksInstalled(t *testing.T) {
	const confabBlock = `[features]
hooks = true

[[hooks.SessionStart]]
matcher = "startup|resume|clear"
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/local/bin/confab hook session-start --provider codex"
`
	const otherBlock = `[features]
hooks = true

[[hooks.SessionStart]]
matcher = "startup"
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/bin/something-else"
`
	tests := []struct {
		name    string
		content string // "" = no file
		want    bool
	}{
		{"missing config", "", false},
		{"empty config", "# nothing here\n", false},
		{"confab block present", confabBlock, true},
		{"only non-confab hook", otherBlock, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.toml")
			if tt.content != "" {
				if err := os.WriteFile(configPath, []byte(tt.content), 0600); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}
			got, err := IsCodexHooksInstalled(configPath)
			if err != nil {
				t.Fatalf("IsCodexHooksInstalled() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("IsCodexHooksInstalled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInstallCodexHooksIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	if _, err := InstallCodexHooks(configPath); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := InstallCodexHooks(configPath); err != nil {
		t.Fatalf("second install: %v", err)
	}
	data, _ := os.ReadFile(configPath)
	count := strings.Count(string(data), "# >>> confab codex hooks >>>")
	if count != 1 {
		t.Fatalf("expected exactly one managed block after repeated install, got %d\n%s", count, string(data))
	}
}
