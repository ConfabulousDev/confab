package provider

import "strings"

// shellProcessNames are the shells an agent may wrap a hook command in
// (`/bin/sh -c "<command>"`). Matched against the basename from getProcName.
var shellProcessNames = map[string]bool{
	"sh": true, "dash": true, "bash": true, "zsh": true,
	"ksh": true, "mksh": true, "ash": true, "fish": true,
}

// isShellProcessName reports whether a process name (ps comm basename) is a
// shell. A leading "-" (login shell, as ps reports on macOS) is ignored.
func isShellProcessName(name string) bool {
	return shellProcessNames[strings.TrimPrefix(name, "-")]
}

// rejectShells wraps an agent matcher so it never accepts a shell process.
//
// Agents run hooks via `/bin/sh -c "<command>"`. Where /bin/sh is dash
// (Debian/Ubuntu) that wrapper stays alive as the hook's parent, and its
// cmdline contains the hook command itself — e.g. `--provider claude-code`
// matches `\bclaude\b`. The wrapper exits as soon as the hook returns, so
// monitoring it made the sync daemon shut down seconds after SessionStart.
// Rejecting shells lets findParentOrGrandparent fall through to the real
// agent one level up. The shell check only runs for processes the matcher
// already accepted, so the common path costs no extra ps call.
func rejectShells(isProcess func(int) bool) func(int) bool {
	return func(pid int) bool {
		return isProcess(pid) && !isShellProcessName(getProcName(pid))
	}
}
