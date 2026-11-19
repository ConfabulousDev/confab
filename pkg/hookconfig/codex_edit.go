package hookconfig

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// These files edit Codex's config.toml by ownership, not by position (kata
// cd4q). Codex's own config writer puts brand-new top-level tables after the
// last table and before a trailing comment, so anything "between two marker
// comments" can belong to Codex. The editor therefore locates the exact byte
// spans of the tables Confabulous Code owns and changes only those; every
// other byte of the file is left as it was.
//
//   - codex_edit_parse.go: parsing into spans and locating hook groups.
//   - codex_edit.go:       the pure text transform (canonical hooks, plan, apply).
//   - codex_edit_write.go: verification and the file-level write path.

// codexEditMode selects what the transform does with Confabulous Code's
// hook tables.
type codexEditMode int

const (
	// codexModeInstall writes the hooks, sets [features] hooks = true and
	// resets confab's trust-state tables. Used by `confab hooks add` and by
	// `confab setup` when the hooks are not installed.
	codexModeInstall codexEditMode = iota
	// codexModeRefresh updates an installed set in place. It leaves an
	// explicit [features] hooks = false and any extra keys Codex keeps in
	// confab's trust-state tables. Used by `confab setup`.
	codexModeRefresh
	// codexModeUninstall removes confab's hook and trust-state tables.
	codexModeUninstall
)

// codexHookEvent pairs a hook event's TOML name with the snake_case label
// Codex uses in its positional trust-state key.
type codexHookEvent struct {
	name  string
	label string
}

// codexHookEvents are the events Confabulous Code installs, in the order
// codexHooksTOML emits them.
var codexHookEvents = []codexHookEvent{
	{"SessionStart", "session_start"},
	{"PreToolUse", "pre_tool_use"},
	{"PostToolUse", "post_tool_use"},
}

// codexStateKey is Codex's positional trust-state key for the handler at
// (group, handler) of an event: "<config path>:<event label>:<g>:<h>".
func codexStateKey(configPath, label string, group, handler int) string {
	return fmt.Sprintf("%s:%s:%d:%d", configPath, label, group, handler)
}

// codexCanonicalHook is what this binary writes for one event.
type codexCanonicalHook struct {
	group     map[string]any // parsed [[hooks.<Event>]] group
	groupTOML string         // its text: group header through its handler
	command   string
	hash      string // trusted_hash for the handler
}

// codexCanonical is the full canonical hook set by event name, derived from
// codexHooksTOML so that function stays the single source of what gets
// installed.
type codexCanonical map[string]codexCanonicalHook

func newCodexCanonical(configPath, binPath string) (codexCanonical, error) {
	doc, err := parseCodexDoc(codexHooksTOML(configPath, binPath, codexHookGroupIndices{}))
	if err != nil {
		return nil, fmt.Errorf("internal error: canonical Codex hooks: %w", err)
	}
	state := semHookState(doc.sem)
	canon := codexCanonical{}
	for _, ev := range codexHookEvents {
		layout, err := doc.eventLayout(ev.name, func(any) bool { return false })
		if err != nil || len(layout.groups) != 1 || len(layout.groups[0].handlers) != 1 {
			return nil, fmt.Errorf("internal error: canonical Codex hooks do not hold one %s group with one handler", ev.name)
		}
		g := layout.groups[0]
		handler, _ := semHandlers(g.sem)[0].(map[string]any)
		command, _ := handler["command"].(string)
		entry, _ := state[codexStateKey(configPath, ev.label, 0, 0)].(map[string]any)
		hash, _ := entry["trusted_hash"].(string)
		if command == "" || hash == "" {
			return nil, fmt.Errorf("internal error: canonical Codex %s hook has no command or trusted hash", ev.name)
		}
		canon[ev.name] = codexCanonicalHook{
			group:     g.sem,
			groupTOML: doc.src[doc.tables[g.table].span.start:doc.tables[g.handlers[0].table].span.end],
			command:   command,
			hash:      hash,
		}
	}
	return canon, nil
}

// owns is the ownership rule for a handler: a command hook whose binary is
// named confab (isConfabCommand), or whose command is exactly one this
// binary writes. The second clause keeps a binary with another name (a dev
// build) from stacking duplicate groups it cannot recognise as its own.
func (c codexCanonical) owns(handler any) bool {
	h, _ := handler.(map[string]any)
	if h["type"] != "command" {
		return false
	}
	command, _ := h["command"].(string)
	if isConfabCommand(command) {
		return true
	}
	for _, hook := range c {
		if hook.command == command {
			return true
		}
	}
	return false
}

func codexStateTOML(key, hash string) string {
	return "[hooks.state." + tomlQuoteString(key) + "]\n" + codexTrustedHashLine(hash) + "\n"
}

func codexTrustedHashLine(hash string) string {
	return "trusted_hash = " + tomlQuoteString(hash)
}

// codexEdit replaces span with text. An empty text is a deletion.
type codexEdit struct {
	span codexSpan
	text string
}

// codexPlan collects the edits one transform makes.
type codexPlan struct {
	edits    []codexEdit
	appended []string // blocks added at end of file, each ending in "\n"
	notices  []string
}

func (p *codexPlan) remove(span codexSpan) { p.replace(span, "") }

func (p *codexPlan) replace(span codexSpan, text string) {
	p.edits = append(p.edits, codexEdit{span, text})
}

func (p *codexPlan) notice(format string, args ...any) {
	p.notices = append(p.notices, fmt.Sprintf(format, args...))
}

// insertLine inserts line at offset at, which is a line start or the end of
// a src that does not end in a newline.
func (p *codexPlan) insertLine(src string, at int, line string) {
	text := line + "\n"
	if at > 0 && src[at-1] != '\n' {
		text = "\n" + line
	}
	p.replace(codexSpan{at, at}, text)
}

// codexTransform holds what every planning step of one transform shares.
type codexTransform struct {
	doc        *codexDoc
	canon      codexCanonical
	mode       codexEditMode
	configPath string
	plan       codexPlan
}

// codexStateTarget is the trust-state key confab's handler for an event has
// after the edit.
type codexStateTarget struct {
	key   string
	event string
}

// transformCodexConfig is the pure text transform behind install, refresh
// and uninstall. It returns the edited config and notices for the user. The
// output equals the input when nothing needs to change.
func transformCodexConfig(input, configPath, binPath string, mode codexEditMode) (string, []string, error) {
	doc, err := parseCodexDoc(input)
	if err != nil {
		return "", nil, err
	}
	canon, err := newCodexCanonical(configPath, binPath)
	if err != nil {
		return "", nil, err
	}
	t := &codexTransform{doc: doc, canon: canon, mode: mode, configPath: configPath}

	for _, marker := range doc.markers {
		t.plan.remove(marker)
	}
	if mode != codexModeUninstall {
		t.planFeatureFlags()
	}

	// previous holds the trust-state key of every confab handler before the
	// edit; final holds the keys confab's handlers have after it.
	previous := map[string]bool{}
	var final []codexStateTarget
	var appendedGroups []string
	for _, ev := range codexHookEvents {
		layout, err := doc.eventLayout(ev.name, canon.owns)
		if err != nil {
			return "", nil, err
		}
		group, inPlace := t.planEventGroups(ev, layout, previous)
		if mode == codexModeUninstall {
			continue
		}
		if !inPlace {
			appendedGroups = append(appendedGroups, canon[ev.name].groupTOML)
		}
		final = append(final, codexStateTarget{codexStateKey(configPath, ev.label, group, 0), ev.name})
	}

	appendedStates := t.planStateTables(previous, final)
	t.plan.appended = append(t.plan.appended, appendedGroups...)
	t.plan.appended = append(t.plan.appended, appendedStates...)

	out, err := applyCodexPlan(input, &t.plan)
	if err != nil {
		return "", nil, err
	}
	return out, t.plan.notices, nil
}

// planFeatureFlags makes the only edits allowed outside confab's own
// tables: removing the deprecated features.codex_hooks and enabling
// features.hooks. Both are matched by full key path, so same-named keys in
// other tables are never touched.
func (t *codexTransform) planFeatureFlags() {
	doc := t.doc
	if kv, ok := doc.findKeyValue("features", "codex_hooks"); ok {
		t.plan.remove(lineSpan(doc.src, kv.span.start, kv.span.end))
	}

	features, _ := doc.sem["features"].(map[string]any)
	enabled, present := features["hooks"]
	switch {
	case enabled == true:
	case present && t.mode == codexModeRefresh:
		t.plan.notice("[features] hooks = false is set in %s, so Codex does not run Confabulous Code hooks; set it to true to turn them on", t.configPath)
	case present:
		// When the value cannot be located (an inline table), nothing is
		// planned and verification refuses the write.
		if kv, ok := doc.findKeyValue("features", "hooks"); ok && kv.value.end > kv.value.start {
			t.plan.replace(kv.value, "true")
		}
	default:
		if i, ok := doc.findTable("features"); ok {
			t.plan.insertLine(doc.src, doc.tables[i].headerEnd, "hooks = true")
		} else {
			t.plan.appended = append(t.plan.appended, "[features]\nhooks = true\n")
		}
	}
}

// planEventGroups plans the edits to one event's groups and adds to previous
// the trust-state key of every confab handler it finds. Outside uninstall it
// returns the index confab's group has after the edit, and whether that
// group stays in the file (false: the canonical group must be appended).
func (t *codexTransform) planEventGroups(ev codexHookEvent, layout codexEventLayout, previous map[string]bool) (group int, inPlace bool) {
	hook := t.canon[ev.name]
	kept := -1     // final index of the confab group that stays
	removed := 0   // confab groups removed so far
	moved := false // a user handler's (group, handler) index changed
	for gi, g := range layout.groups {
		confab := 0
		for hi, h := range g.handlers {
			if h.confab {
				confab++
				previous[codexStateKey(t.configPath, ev.label, gi, hi)] = true
			}
		}
		owned := confab > 0 && confab == len(g.handlers)
		switch {
		case owned && t.mode != codexModeUninstall && kept == -1:
			// The first confab group is replaced in place so the positions
			// of the user's own groups never change.
			kept = gi - removed
			if !reflect.DeepEqual(g.sem, hook.group) {
				t.replaceGroup(g, hook.groupTOML)
			}
		case owned:
			t.replaceGroup(g, "")
			removed++
		default:
			// A user group, or a mixed one: only confab's handler tables go.
			gone := 0
			for _, h := range g.handlers {
				switch {
				case h.confab:
					t.plan.remove(t.doc.tables[h.table].span)
					gone++
				case removed > 0 || gone > 0:
					moved = true
				}
			}
		}
	}
	if moved {
		t.plan.notice("your own %s hooks in %s changed position; Codex will ask you to trust them again", ev.name, t.configPath)
	}
	if kept == -1 {
		return layout.count - removed, false
	}
	return kept, true
}

// replaceGroup replaces group g, header and handlers, with text ("" removes
// it).
func (t *codexTransform) replaceGroup(g codexGroup, text string) {
	t.plan.replace(t.doc.tables[g.table].span, text)
	for _, h := range g.handlers {
		t.plan.remove(t.doc.tables[h.table].span)
	}
}

// planStateTables brings [hooks.state."<key>"] in line with the final
// layout: a table already at a final key is rewritten in place (it is, by
// Codex's positional semantics, the state of confab's handler), a missing
// one is appended, and confab's tables at keys no longer in use are removed.
// It consumes previous and returns the tables to append.
func (t *codexTransform) planStateTables(previous map[string]bool, final []codexStateTarget) (appended []string) {
	doc := t.doc
	state := semHookState(doc.sem)
	for _, target := range final {
		key, hash := target.key, t.canon[target.event].hash
		delete(previous, key)
		entry, _ := state[key].(map[string]any)
		i, ok := doc.findTable("hooks", "state", key)
		switch {
		case !ok:
			appended = append(appended, codexStateTOML(key, hash))
		case t.mode == codexModeInstall:
			if len(entry) != 1 || entry["trusted_hash"] != hash {
				t.plan.replace(doc.tables[i].span, codexStateTOML(key, hash))
			}
		default:
			if entry["trusted_hash"] != hash {
				if kv, ok := doc.findKeyValue("hooks", "state", key, "trusted_hash"); ok {
					t.plan.replace(kv.span, codexTrustedHashLine(hash))
				} else {
					t.plan.insertLine(doc.src, doc.tables[i].headerEnd, codexTrustedHashLine(hash))
				}
			}
			if entry["enabled"] == false {
				t.plan.notice("the Confabulous Code %s hook is disabled in Codex (enabled = false in %s); enable it in Codex to use it", target.event, t.configPath)
			}
		}
	}
	// What is left in previous addresses no confab handler any more.
	for key := range previous {
		if i, ok := doc.findTable("hooks", "state", key); ok {
			t.plan.remove(doc.tables[i].span)
		}
	}
	return appended
}

// applyCodexPlan applies the plan to src. Bytes outside the edited spans are
// copied through unchanged, except that a blank-line run left around a
// deletion is collapsed to one line and, when anything changed, the file is
// made to end with exactly one newline.
func applyCodexPlan(src string, plan *codexPlan) (string, error) {
	if len(plan.edits) == 0 && len(plan.appended) == 0 {
		return src, nil
	}
	nl := "\n"
	if strings.Contains(src, "\r\n") {
		nl = "\r\n"
	}
	native := func(text string) string { return strings.ReplaceAll(text, "\n", nl) }

	edits := plan.edits
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].span.start != edits[j].span.start {
			return edits[i].span.start < edits[j].span.start
		}
		return edits[i].span.end < edits[j].span.end
	})
	var b strings.Builder
	var cuts []int // output offsets where a deletion happened
	pos := 0
	for _, e := range edits {
		if e.span.start < pos {
			// A deletion inside a span that is already gone (a marker
			// comment inside a removed table) needs nothing more.
			if e.text == "" && e.span.end <= pos {
				continue
			}
			return "", fmt.Errorf("internal error: overlapping edits at byte %d of the Codex config", e.span.start)
		}
		b.WriteString(src[pos:e.span.start])
		b.WriteString(native(e.text))
		if e.text == "" {
			cuts = append(cuts, b.Len())
		}
		pos = e.span.end
	}
	b.WriteString(src[pos:])

	out := trimTrailingBlankLines(collapseBlankRuns(b.String(), cuts))
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += nl
	}
	for _, block := range plan.appended {
		if out != "" {
			out += nl
		}
		out += native(block)
	}
	return out, nil
}

func isBlankLine(line string) bool { return strings.TrimSpace(line) == "" }

// collapseBlankRuns reduces the run of blank lines around each cut (a line
// start in s) to one line, or to none at the top of the file. Cuts that are
// not at a line start are left alone.
func collapseBlankRuns(s string, cuts []int) string {
	for i := len(cuts) - 1; i >= 0; i-- {
		c := cuts[i]
		if c > 0 && s[c-1] != '\n' {
			continue
		}
		start, end := c, c
		for end < len(s) && isBlankLine(s[end:lineEnd(s, end)]) {
			end = lineEnd(s, end)
		}
		for start > 0 {
			prev := strings.LastIndexByte(s[:start-1], '\n') + 1
			if !isBlankLine(s[prev:start]) {
				break
			}
			start = prev
		}
		keepEnd := start
		if start > 0 && end > start {
			keepEnd = lineEnd(s, start)
		}
		s = s[:keepEnd] + s[end:]
		// Earlier cuts inside this same run are already handled.
		for i > 0 && cuts[i-1] >= start {
			i--
		}
	}
	return s
}

func trimTrailingBlankLines(s string) string {
	for s != "" {
		lastStart := strings.LastIndexByte(strings.TrimSuffix(s, "\n"), '\n') + 1
		if !isBlankLine(s[lastStart:]) {
			break
		}
		s = s[:lastStart]
	}
	return s
}
