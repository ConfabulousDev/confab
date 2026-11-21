package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/hookconfig"
	"github.com/ConfabulousDev/confab/pkg/logger"
	"github.com/ConfabulousDev/confab/pkg/pathcanon"
	"github.com/ConfabulousDev/confab/pkg/provider"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var uninstallYes bool

// stdinIsTerminal reports whether stdin is an interactive terminal. A real
// isatty check: a char-device test would accept /dev/null. A var so tests can
// stub it.
var stdinIsTerminal = func() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop syncing and remove every Confabulous Code hook, plugin and skill",
	Long: `Undoes 'confab setup' on this machine.

This command:
1. Disables sync and stops every running sync daemon (see 'confab disable')
2. Removes Confabulous Code's hooks, plugin and skills from every provider
   (Claude Code, Codex, Cursor, OpenCode), including Claude Code config dirs
   set up with --config-dir. Your own hooks and settings are left untouched.

Kept: ~/.confab (credentials, bindings, redaction settings, logs) and the
confab binary; the command prints how to remove the binary and lists any
config backups. Run 'confab setup' to reinstall.

Asks for confirmation first; --yes skips the prompt (required when stdin is
not a terminal). To remove one provider's hooks only, use
'confab hooks remove --provider <name>'.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger.Info("Running uninstall command")
		return runUninstall(os.Stdin, os.Stdout, uninstallYes)
	},
}

// uninstallTarget is one provider config dir to clean.
type uninstallTarget struct {
	p     provider.Provider
	dir   string
	bound bool // a --config-dir binding rather than the provider default
}

func (t uninstallTarget) label() string {
	if t.bound {
		return fmt.Sprintf("%s (--config-dir %s)", t.p.Name(), t.dir)
	}
	return fmt.Sprintf("%s (%s)", t.p.Name(), t.dir)
}

func runUninstall(in io.Reader, w io.Writer, yes bool) error {
	targets, notes, err := uninstallTargets()
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "This will:")
	fmt.Fprintln(w, "  - disable sync and stop every running sync daemon")
	fmt.Fprintln(w, "  - remove Confabulous Code hooks, plugin and skills from:")
	for _, t := range targets {
		fmt.Fprintf(w, "      %s\n", t.label())
	}
	for _, n := range notes {
		fmt.Fprintf(w, "      %s\n", n)
	}
	fmt.Fprintln(w, "Kept: ~/.confab (credentials, bindings, redaction settings, logs) and the confab binary.")

	if !yes {
		if !stdinIsTerminal() {
			return fmt.Errorf("refusing to uninstall without confirmation: stdin is not a terminal (re-run with --yes)")
		}
		fmt.Fprint(w, "Continue? [y/N] ")
		answer, _ := bufio.NewReader(in).ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(w, "Aborted; nothing was changed.")
			return nil
		}
	}
	fmt.Fprintln(w)

	// Keep going past every failure: once the flag is set, sync has stopped
	// even if a file edit fails, and each failure is reported.
	failures := 0
	if err := runDisable(w, stopAllTimeout); err != nil {
		fmt.Fprintf(w, "✗ %v\n", err)
		failures++
	}

	var backups []string
	for _, t := range targets {
		ok := true
		if path, err := t.p.UninstallHooks(); err != nil {
			ok = false
			fmt.Fprintf(w, "✗ %s: failed to remove hooks: %v\n", t.label(), err)
			failures++
		} else {
			backups = append(backups, hookconfig.ListBackups(path)...)
		}
		if err := t.p.UninstallSkills(); err != nil {
			ok = false
			fmt.Fprintf(w, "✗ %s: failed to remove skills: %v\n", t.label(), err)
			failures++
		}
		if ok {
			fmt.Fprintf(w, "✓ %s: cleaned\n", t.label())
		}
	}

	if len(backups) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Config backups made by earlier installs (kept; delete them if you no longer need them):")
		for _, b := range backups {
			fmt.Fprintf(w, "  %s\n", b)
		}
	}

	fmt.Fprintln(w)
	if bin, err := config.GetBinaryPath(); err == nil {
		fmt.Fprintf(w, "To remove the binary: rm %s\n", bin)
	}
	fmt.Fprintln(w, "Credentials and logs kept in ~/.confab; run 'confab setup' to reinstall.")

	if failures > 0 {
		return fmt.Errorf("uninstall finished with %d error(s)", failures)
	}
	return nil
}

// uninstallTargets returns every provider's default dir plus every bound
// Claude Code custom config dir (kata hpec bindings), deduplicated by
// canonical dir. A bound dir that no longer exists is skipped with a note.
func uninstallTargets() ([]uninstallTarget, []string, error) {
	defaults, err := allOrNamedProviders("")
	if err != nil {
		return nil, nil, err
	}
	var targets []uninstallTarget
	var claudeDefault string
	for _, p := range defaults {
		dir, err := p.StateDir()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve %s config dir: %w", p.Name(), err)
		}
		if p.Name() == provider.NameClaudeCode {
			claudeDefault = pathcanon.CanonicalDir(dir)
		}
		targets = append(targets, uninstallTarget{p: p, dir: dir})
	}

	var notes []string
	cfg, err := config.GetUploadConfig()
	if err != nil {
		// Still clean the default dirs; the disable step reports the error.
		notes = append(notes, fmt.Sprintf("(could not read ~/.confab/config.json to find --config-dir installs: %v)", err))
		return targets, notes, nil
	}
	// Only Claude Code supports --config-dir bindings today (kata hpec).
	name := provider.NameClaudeCode
	dirs := make([]string, 0, len(cfg.Bindings[name]))
	for dir := range cfg.Bindings[name] {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		// Binding keys are already unique canonical dirs; skip only one
		// that is the default dir handled above.
		if pathcanon.CanonicalDir(dir) == claudeDefault {
			continue
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			notes = append(notes, fmt.Sprintf("%s (--config-dir %s): skipped, directory no longer exists", name, dir))
			continue
		}
		p, err := provider.GetWithDir(name, dir)
		if err != nil {
			return nil, nil, err
		}
		targets = append(targets, uninstallTarget{p: p, dir: dir, bound: true})
	}
	return targets, notes, nil
}

func init() {
	uninstallCmd.Flags().BoolVarP(&uninstallYes, "yes", "y", false, "Skip the confirmation prompt")
	rootCmd.AddCommand(uninstallCmd)
}
