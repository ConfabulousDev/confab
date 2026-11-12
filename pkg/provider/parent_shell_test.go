package provider

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// helperEnv makes the test binary act as a hook process: it runs
// findParentOrGrandparent against the real process table and prints the
// chosen PID. The agent under test is the go test process itself, whose
// PID arrives via helperAgentPIDEnv.
const (
	helperEnv         = "CONFAB_TEST_PARENT_PID_HELPER"
	helperAgentPIDEnv = "CONFAB_TEST_PARENT_PID_AGENT"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		agentPID, _ := strconv.Atoi(os.Getenv(helperAgentPIDEnv))
		// Real Claude matcher, plus "is the agent" for the go test process
		// (its cmdline does not mention claude).
		isProcess := func(pid int) bool {
			return pid == agentPID || ClaudeCode{}.IsProcess(pid)
		}
		os.Stdout.WriteString(strconv.Itoa(findParentOrGrandparent(isProcess, "")))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runHookHelper starts the test binary as a hook would be started and
// returns the PID findParentOrGrandparent picked inside it.
func runHookHelper(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		helperAgentPIDEnv+"="+strconv.Itoa(os.Getpid()))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook helper failed: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("hook helper output %q: %v", out, err)
	}
	return pid
}

// Claude Code runs hook commands via `/bin/sh -c "<command>"`. Where /bin/sh
// is dash (Debian/Ubuntu) that wrapper shell stays alive as the hook's
// parent, and its cmdline carries the hook command — `--provider
// claude-code` matches `\bclaude\b`. The wrapper exits seconds later, so
// picking it made the sync daemon shut down ~5s after SessionStart. The
// trailing `; true` keeps the wrapper alive under every /bin/sh (bash would
// otherwise exec the single command), reproducing the dash process tree.
func TestFindParentOrGrandparent_SkipsHookWrapperShell(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hookCmd := exe + " hook session-start --provider claude-code; true"
	got := runHookHelper(t, exec.Command("/bin/sh", "-c", hookCmd))
	if want := os.Getpid(); got != want {
		t.Errorf("findParentOrGrandparent() = %d (cmdline %q), want agent pid %d",
			got, getProcCmdline(got), want)
	}
}

// Without a wrapper shell (e.g. bash exec'd the hook command), the agent is
// the direct parent and must still be picked.
func TestFindParentOrGrandparent_DirectParent(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := runHookHelper(t, exec.Command(exe, "hook", "session-start", "--provider", "claude-code"))
	if want := os.Getpid(); got != want {
		t.Errorf("findParentOrGrandparent() = %d, want agent pid %d", got, want)
	}
}

func TestIsShellProcessName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"sh", true},
		{"dash", true},
		{"bash", true},
		{"zsh", true},
		{"-zsh", true}, // login shell as reported by ps comm on macOS
		{"ksh", true},
		{"fish", true},
		{"claude", false},
		{"node", false},
		{"codex", false},
		{"cursor-agent", false},
		{"bashful", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isShellProcessName(tt.name); got != tt.want {
			t.Errorf("isShellProcessName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
