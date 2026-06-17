package hookconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ConfabulousDev/confab/pkg/config"
)

// Cursor hook events Confab drives. sessionStart spawns the sync daemon;
// sessionEnd signals a clean shutdown; preToolUse/postToolUse drive
// bidirectional GitHub commit/PR linking (65aq). We deliberately do NOT
// install stop (fires per turn — same hazard as Codex's Stop).
const (
	cursorSessionStartEvent = "sessionStart"
	cursorSessionEndEvent   = "sessionEnd"
	cursorPreToolUseEvent   = "preToolUse"
	cursorPostToolUseEvent  = "postToolUse"

	// cursorShellMatcher scopes the tool-use hooks to Cursor's Shell tool
	// (matcher filters by tool type for preToolUse/postToolUse). The handler
	// also checks tool_name=="Shell" defensively.
	cursorShellMatcher = "Shell"

	// cursorProviderFlag is the --provider value Confab passes to its Cursor
	// hook subcommands. Kept as a literal here (not imported from pkg/provider)
	// because pkg/provider imports this package — importing it back would cycle.
	cursorProviderFlag = "cursor"
)

// cursorHookEntry is one command-hook entry in a Cursor hooks.json event
// array: {"command": "...", "type": "command"}. Cursor's schema for a hook
// entry is exactly these two keys (kata 6kys §1), so a struct round-trips
// our own entries faithfully. User-authored entries are preserved verbatim
// via cursorHooksRaw.hooks (raw JSON), never re-encoded through this struct,
// so any extra keys they carry survive untouched.
type cursorHookEntry struct {
	Command string `json:"command"`
	Type    string `json:"type"`
	// Matcher scopes a preToolUse/postToolUse hook to a tool type (e.g.
	// "Shell"). Omitted (and round-tripped absent) for the lifecycle events.
	Matcher string `json:"matcher,omitempty"`
}

// cursorHooksFile is the typed view of ~/.cursor/hooks.json used by the
// install/uninstall/check logic and tests. It is NOT used to rewrite the
// file (that path preserves unknown keys via raw JSON); it is the read model.
type cursorHooksFile struct {
	Version int                          `json:"version"`
	Hooks   map[string][]cursorHookEntry `json:"hooks"`
}

// confabCursorEntry returns the managed hook entry for a Cursor event,
// invoking the confab binary with the given subcommand. The command string
// is the idempotency + clean-uninstall key (we match on a confab command for
// the event), so it stays stable across installs. A non-empty matcher scopes
// the entry to a tool type (used for the tool-use events).
func confabCursorEntry(binPath, subcommand, matcher string) cursorHookEntry {
	return cursorHookEntry{
		Command: binPath + " hook " + subcommand + " --provider " + cursorProviderFlag,
		Type:    "command",
		Matcher: matcher,
	}
}

// InstallCursorHooks writes Confab's sessionStart + sessionEnd + preToolUse +
// postToolUse command hooks into Cursor's hooks.json at hooksPath, preserving
// any user-authored hooks (and unknown top-level keys), backing the file up
// first, and writing atomically. The tool-use events carry matcher "Shell"
// (commit/PR linking; 65aq). Re-installing is idempotent: an existing confab
// command for an event is not duplicated. Returns the hooksPath written.
func InstallCursorHooks(hooksPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0700); err != nil {
		return "", fmt.Errorf("failed to create Cursor state directory: %w", err)
	}

	top, err := readCursorHooksRaw(hooksPath)
	if err != nil {
		return "", err
	}
	if err := backupCursorHooks(hooksPath); err != nil {
		return "", err
	}

	binPath, err := config.GetBinaryPath()
	if err != nil {
		return "", err
	}

	managed := []struct {
		event, subcommand, matcher string
	}{
		{cursorSessionStartEvent, "session-start", ""},
		{cursorSessionEndEvent, "session-end", ""},
		{cursorPreToolUseEvent, "pre-tool-use", cursorShellMatcher},
		{cursorPostToolUseEvent, "post-tool-use", cursorShellMatcher},
	}
	for _, m := range managed {
		if err := top.ensureEntry(m.event, confabCursorEntry(binPath, m.subcommand, m.matcher)); err != nil {
			return "", err
		}
	}
	top.ensureVersion()

	return writeCursorHooks(hooksPath, top)
}

// UninstallCursorHooks removes Confab's managed hook entries from Cursor's
// hooks.json, preserving every other hook and top-level key. A missing file
// is a no-op. Returns the hooksPath.
func UninstallCursorHooks(hooksPath string) (string, error) {
	top, err := readCursorHooksRaw(hooksPath)
	if err != nil {
		return "", err
	}
	if top.fileMissing {
		return hooksPath, nil
	}
	if err := backupCursorHooks(hooksPath); err != nil {
		return "", err
	}
	top.removeConfabEntries(cursorSessionStartEvent)
	top.removeConfabEntries(cursorSessionEndEvent)
	top.removeConfabEntries(cursorPreToolUseEvent)
	top.removeConfabEntries(cursorPostToolUseEvent)
	return writeCursorHooks(hooksPath, top)
}

// IsCursorHooksInstalled reports true only when all four managed events
// (sessionStart, sessionEnd, preToolUse, postToolUse) carry a confab command.
// A legacy two-event install (pre-65aq, lifecycle only) reads as "not
// installed" so `confab setup` re-emits the managed block and transparently
// upgrades it. A missing or unparseable file reads as not installed.
func IsCursorHooksInstalled(hooksPath string) (bool, error) {
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read Cursor hooks: %w", err)
	}
	var f cursorHooksFile
	if err := json.Unmarshal(data, &f); err != nil {
		return false, fmt.Errorf("failed to parse Cursor hooks: %w", err)
	}
	return cursorEventHasConfab(f, cursorSessionStartEvent) &&
		cursorEventHasConfab(f, cursorSessionEndEvent) &&
		cursorEventHasConfab(f, cursorPreToolUseEvent) &&
		cursorEventHasConfab(f, cursorPostToolUseEvent), nil
}

// cursorEventHasConfab reports whether any entry in the named event invokes
// a confab Cursor hook command.
func cursorEventHasConfab(f cursorHooksFile, event string) bool {
	for _, h := range f.Hooks[event] {
		if h.Type == "command" && isConfabCursorCommand(h.Command) {
			return true
		}
	}
	return false
}

// isConfabCursorCommand reports whether a hooks.json command string is one of
// Confab's managed Cursor hooks. We key off the command signature (" hook "
// plus "--provider cursor") rather than the binary basename so detection is
// independent of where the confab binary lives (and so tests, whose
// os.Executable() is the test binary, still match). This signature is what
// makes re-install idempotent and uninstall surgical, and it covers all four
// managed events (session-start/end + pre-/post-tool-use).
func isConfabCursorCommand(command string) bool {
	return strings.Contains(command, " hook ") &&
		strings.Contains(command, "--provider "+cursorProviderFlag)
}

// cursorHooksRaw is the rewrite model: top-level keys are kept as raw JSON so
// unknown user keys survive byte-faithfully, and each event's hook array is
// kept as raw entries so user-authored entries (which may carry keys beyond
// command/type) are never lossily re-encoded. Confab edits only its own
// entries within the two managed events.
type cursorHooksRaw struct {
	fileMissing bool
	top         map[string]json.RawMessage
	hooks       map[string][]json.RawMessage
}

// readCursorHooksRaw loads hooksPath into the rewrite model. A missing file
// yields an empty model with fileMissing=true. An empty file is treated like
// an empty JSON object.
func readCursorHooksRaw(hooksPath string) (*cursorHooksRaw, error) {
	out := &cursorHooksRaw{
		top:   map[string]json.RawMessage{},
		hooks: map[string][]json.RawMessage{},
	}
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		if os.IsNotExist(err) {
			out.fileMissing = true
			return out, nil
		}
		return nil, fmt.Errorf("failed to read Cursor hooks: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out.top); err != nil {
		return nil, fmt.Errorf("failed to parse Cursor hooks: %w", err)
	}
	if rawHooks, ok := out.top["hooks"]; ok {
		if err := json.Unmarshal(rawHooks, &out.hooks); err != nil {
			return nil, fmt.Errorf("failed to parse Cursor hooks.hooks: %w", err)
		}
	}
	return out, nil
}

// ensureVersion sets version=1 only when the file did not already declare one,
// so a user's explicit version is never overwritten.
func (r *cursorHooksRaw) ensureVersion() {
	if _, ok := r.top["version"]; !ok {
		r.top["version"] = json.RawMessage("1")
	}
}

// ensureEntry appends the confab entry to the event array iff no confab
// command is already present for that event (idempotent re-install).
func (r *cursorHooksRaw) ensureEntry(event string, entry cursorHookEntry) error {
	for _, raw := range r.hooks[event] {
		if rawIsConfabEntry(raw) {
			return nil
		}
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to encode Cursor hook entry: %w", err)
	}
	r.hooks[event] = append(r.hooks[event], json.RawMessage(encoded))
	return nil
}

// removeConfabEntries drops every confab command entry from the event array,
// pruning the event key entirely when it becomes empty so uninstall restores
// a hand-authored file as closely as possible.
func (r *cursorHooksRaw) removeConfabEntries(event string) {
	kept := r.hooks[event][:0]
	for _, raw := range r.hooks[event] {
		if rawIsConfabEntry(raw) {
			continue
		}
		kept = append(kept, raw)
	}
	if len(kept) == 0 {
		delete(r.hooks, event)
		return
	}
	r.hooks[event] = kept
}

// rawIsConfabEntry reports whether a raw hook entry is a confab command hook.
func rawIsConfabEntry(raw json.RawMessage) bool {
	var e cursorHookEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return false
	}
	return e.Type == "command" && isConfabCursorCommand(e.Command)
}

// writeCursorHooks serializes the rewrite model back into hooks.json. The
// hooks object is re-attached under the "hooks" top-level key; when it is
// empty an empty object is written so the schema stays valid.
func writeCursorHooks(hooksPath string, r *cursorHooksRaw) (string, error) {
	hooksEncoded, err := json.Marshal(r.hooks)
	if err != nil {
		return "", fmt.Errorf("failed to encode Cursor hooks: %w", err)
	}
	r.top["hooks"] = json.RawMessage(hooksEncoded)

	out, err := json.MarshalIndent(r.top, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode Cursor hooks file: %w", err)
	}
	out = append(out, '\n')
	if err := writeFileAtomic(hooksPath, out, 0600); err != nil {
		return "", fmt.Errorf("failed to write Cursor hooks: %w", err)
	}
	return hooksPath, nil
}

// backupCursorHooks copies an existing hooks.json to a timestamped backup
// before it is rewritten. A missing file is a no-op.
func backupCursorHooks(hooksPath string) error {
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read Cursor hooks for backup: %w", err)
	}
	backupPath := fmt.Sprintf("%s.confab-backup-%s", hooksPath, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}
	return nil
}
