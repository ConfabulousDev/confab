package hookconfig

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/ConfabulousDev/confab/pkg/logger"
)

// verifyCodexEdit is the gate before any write. It parses the config before
// and after the edit, removes everything Confabulous Code owns from both,
// and requires what is left to be identical; then it checks that the edit
// did what the mode promises. Any failure means the file must not be written.
func verifyCodexEdit(input, output, configPath, binPath string, mode codexEditMode) error {
	before, err := parseCodexSem(input)
	if err != nil {
		return fmt.Errorf("the Codex config is not valid TOML: %w", err)
	}
	after, err := parseCodexSem(output)
	if err != nil {
		return fmt.Errorf("the edited Codex config would not be valid TOML: %w", err)
	}
	canon, err := newCodexCanonical(configPath, binPath)
	if err != nil {
		return err
	}

	if err := verifyCodexHookSet(after, canon, configPath, mode); err != nil {
		return err
	}
	if mode != codexModeUninstall {
		featuresBefore, _ := before["features"].(map[string]any)
		featuresAfter, _ := after["features"].(map[string]any)
		_, explicit := featuresBefore["hooks"]
		if featuresAfter["hooks"] != true && !(mode == codexModeRefresh && explicit) {
			return fmt.Errorf("the edit would not set [features] hooks = true")
		}
	}

	// A state entry is confab's when its key addresses a confab handler in
	// either layout: a leftover table at the key confab moves to is taken
	// over, so it must not count as someone else's.
	keys := codexConfabStateKeys(before, canon, configPath)
	for key := range codexConfabStateKeys(after, canon, configPath) {
		keys[key] = true
	}
	stripCodexConfab(before, canon, keys)
	stripCodexConfab(after, canon, keys)
	if path := firstDifference(before, after, ""); path != "" {
		return fmt.Errorf("the edit would change %q, which does not belong to Confabulous Code", path)
	}
	return nil
}

// verifyCodexHookSet checks the mode's post-condition on the edited config:
// after install or refresh each event holds exactly one confab handler, in
// the canonical group, with the expected trusted hash at its positional key;
// after uninstall none remains.
func verifyCodexHookSet(after map[string]any, canon codexCanonical, configPath string, mode codexEditMode) error {
	state := semHookState(after)
	for _, ev := range codexHookEvents {
		hook := canon[ev.name]
		found, group := 0, -1
		for gi, g := range semHookGroups(after, ev.name) {
			for _, h := range semHandlers(g) {
				if canon.owns(h) {
					found++
					if reflect.DeepEqual(g, hook.group) {
						group = gi
					}
				}
			}
		}
		if mode == codexModeUninstall {
			if found != 0 {
				return fmt.Errorf("the edit would leave a Confabulous Code hook under hooks.%s", ev.name)
			}
			continue
		}
		if found != 1 || group == -1 {
			return fmt.Errorf("the edit would not leave exactly one up-to-date Confabulous Code hook under hooks.%s", ev.name)
		}
		key := codexStateKey(configPath, ev.label, group, 0)
		entry, _ := state[key].(map[string]any)
		if entry["trusted_hash"] != hook.hash || (mode == codexModeInstall && len(entry) != 1) {
			return fmt.Errorf("the edit would not write the expected trusted_hash at hooks.state.%q", key)
		}
	}
	return nil
}

// codexConfabStateKeys returns the trust-state key of every confab handler
// in sem's layout.
func codexConfabStateKeys(sem map[string]any, canon codexCanonical, configPath string) map[string]bool {
	keys := map[string]bool{}
	for _, ev := range codexHookEvents {
		for gi, g := range semHookGroups(sem, ev.name) {
			for hi, h := range semHandlers(g) {
				if canon.owns(h) {
					keys[codexStateKey(configPath, ev.label, gi, hi)] = true
				}
			}
		}
	}
	return keys
}

// stripCodexConfab removes from sem everything the editor may change:
// confab handlers (and groups left without a handler), the given trust-state
// entries, and the two [features] flags.
func stripCodexConfab(sem map[string]any, canon codexCanonical, stateKeys map[string]bool) {
	if hooks, ok := sem["hooks"].(map[string]any); ok {
		for _, ev := range codexHookEvents {
			groups, ok := hooks[ev.name].([]any)
			if !ok {
				continue
			}
			var keptGroups []any
			for _, g := range groups {
				var keptHandlers []any
				handlers := semHandlers(g)
				for _, h := range handlers {
					if !canon.owns(h) {
						keptHandlers = append(keptHandlers, h)
					}
				}
				switch {
				case len(keptHandlers) == len(handlers):
				case len(keptHandlers) == 0:
					continue
				default:
					g.(map[string]any)["hooks"] = keptHandlers
				}
				keptGroups = append(keptGroups, g)
			}
			if len(keptGroups) == 0 {
				delete(hooks, ev.name)
			} else {
				hooks[ev.name] = keptGroups
			}
		}
		if state, ok := hooks["state"].(map[string]any); ok {
			for key := range stateKeys {
				delete(state, key)
			}
			if len(state) == 0 {
				delete(hooks, "state")
			}
		}
		if len(hooks) == 0 {
			delete(sem, "hooks")
		}
	}
	if features, ok := sem["features"].(map[string]any); ok {
		delete(features, "hooks")
		delete(features, "codex_hooks")
		if len(features) == 0 {
			delete(sem, "features")
		}
	}
}

// firstDifference returns the dotted path of the first value that differs
// between a and b, or "" when they are equal.
func firstDifference(a, b any, path string) string {
	join := func(key string) string {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return path
		}
		keys := make([]string, 0, len(av)+len(bv))
		for k := range av {
			keys = append(keys, k)
		}
		for k := range bv {
			if _, dup := av[k]; !dup {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			x, inA := av[k]
			y, inB := bv[k]
			if !inA || !inB {
				return join(k)
			}
			if d := firstDifference(x, y, join(k)); d != "" {
				return d
			}
		}
		return ""
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return path
		}
		for i := range av {
			if d := firstDifference(av[i], bv[i], fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
		return ""
	}
	// NaN never equals itself, but an untouched nan in the config is not
	// a difference.
	if x, ok := a.(float64); ok && math.IsNaN(x) {
		if y, ok := b.(float64); ok && math.IsNaN(y) {
			return ""
		}
	}
	if !reflect.DeepEqual(a, b) {
		return path
	}
	return ""
}

// codexEditAttempts bounds how often an edit is redone because another
// program changed config.toml while it was being computed.
const codexEditAttempts = 3

// codexEditor owns the file-level write path. The fields other than binPath
// exist so tests can force a bad transform, a concurrent writer, and capture
// notices.
type codexEditor struct {
	binPath       string
	transform     func(input, configPath, binPath string, mode codexEditMode) (string, []string, error)
	beforeRecheck func() // runs between verification and the pre-write re-read
	notify        io.Writer
}

func newCodexEditor() (codexEditor, error) {
	binPath, err := BinaryPath()
	if err != nil {
		return codexEditor{}, err
	}
	return codexEditor{binPath: binPath, transform: transformCodexConfig, notify: os.Stderr}, nil
}

// apply reads configPath, transforms it, verifies the result, and writes it
// atomically after a backup. Nothing is written when the transform changes
// nothing or when anything fails.
//
// Codex takes no lock confab could share, so the file is re-read right
// before the write and the edit redone if it changed. A write by another
// program landing between that re-read and the rename is still lost.
func (e codexEditor) apply(configPath string, mode codexEditMode) error {
	for range codexEditAttempts {
		original, exists, err := readCodexConfig(configPath)
		if err != nil {
			return err
		}
		if !exists && mode == codexModeUninstall {
			return nil
		}
		input := string(original)
		output, notices, err := e.transform(input, configPath, e.binPath, mode)
		if err != nil {
			return fmt.Errorf("%w; %s was not modified", err, configPath)
		}
		if output == input {
			e.announce(notices)
			return nil
		}
		if err := verifyCodexEdit(input, output, configPath, e.binPath, mode); err != nil {
			return fmt.Errorf("%w; %s was not modified", err, configPath)
		}

		if e.beforeRecheck != nil {
			e.beforeRecheck()
		}
		current, stillExists, err := readCodexConfig(configPath)
		if err != nil {
			return err
		}
		if stillExists != exists || !bytes.Equal(current, original) {
			continue
		}

		if exists {
			backupPath, err := backupFile(configPath, original)
			if err != nil {
				return err
			}
			notices = append(notices, "the previous Codex config was saved to "+backupPath)
		} else if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
			return fmt.Errorf("failed to create Codex state directory: %w", err)
		}
		if err := writeFileAtomic(configPath, []byte(output), 0600); err != nil {
			return fmt.Errorf("failed to write Codex config: %w", err)
		}
		e.announce(notices)
		return nil
	}
	return fmt.Errorf("%s is being modified by another program; try again", configPath)
}

func (e codexEditor) announce(notices []string) {
	for _, n := range notices {
		logger.Warn("Codex hooks: %s", n)
		fmt.Fprintf(e.notify, "Note: %s\n", n)
	}
}

// readCodexConfig reads configPath; a missing file is not an error but
// reports exists = false.
func readCodexConfig(configPath string) (data []byte, exists bool, err error) {
	data, err = os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to read Codex config: %w", err)
	}
	return data, true, nil
}

// codexHooksOutdated reports whether configPath holds a full confab hook set
// (IsCodexHooksInstalled) that a refresh would change. That is the whole
// definition of outdated: it covers the matcher, status message, trusted
// hash, trust key, binary path, stale markers and duplicates.
func codexHooksOutdated(configPath, binPath string) (bool, error) {
	data, exists, err := readCodexConfig(configPath)
	if err != nil || !exists {
		return false, err
	}
	installed, err := codexHooksInstalledIn(data)
	if err != nil || !installed {
		return false, err
	}
	refreshed, _, err := transformCodexConfig(string(data), configPath, binPath, codexModeRefresh)
	if err != nil {
		return false, err
	}
	return refreshed != string(data), nil
}
