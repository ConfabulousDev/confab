package hookconfig

import (
	"fmt"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// This file parses Codex's config.toml into the byte spans the editor
// changes (codexDoc) and locates the hook groups and handlers in it
// (codexEventLayout). See codex_edit.go for the overall design.

// codexSpan is a half-open byte range [start, end) of the config text.
type codexSpan struct{ start, end int }

// codexKeyValue is one `key = value` expression. value is empty when the
// parser does not report the value's range (arrays, inline tables).
type codexKeyValue struct {
	path  []string
	span  codexSpan
	value codexSpan
}

// codexTable is one [table] or [[array table]]. Its span runs from the start
// of the header line through the end of the line holding its last key-value,
// so trailing same-line comments belong to it while standalone comments and
// blank lines after the last key-value do not.
type codexTable struct {
	array     bool
	path      []string
	span      codexSpan
	headerEnd int
	kvs       []codexKeyValue
}

// codexDoc is a config parsed twice: semantically (sem) and into the byte
// spans of its top-level expressions.
type codexDoc struct {
	src     string
	sem     map[string]any
	root    []codexKeyValue // key-values above the first table header
	tables  []codexTable
	markers []codexSpan // whole lines holding a legacy marker comment
}

func parseCodexSem(src string) (map[string]any, error) {
	sem := map[string]any{}
	if err := toml.Unmarshal([]byte(src), &sem); err != nil {
		return nil, err
	}
	return sem, nil
}

func parseCodexDoc(src string) (*codexDoc, error) {
	sem, err := parseCodexSem(src)
	if err != nil {
		return nil, fmt.Errorf("the Codex config is not valid TOML: %w", err)
	}
	doc := &codexDoc{src: src, sem: sem}

	p := unstable.Parser{KeepComments: true}
	p.Reset([]byte(src))
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Comment:
			// Only standalone comments are reported as expressions; a
			// marker inside a multi-line string never reaches here.
			comment := rawSpan(e.Raw)
			line := lineSpan(src, comment.start, comment.end)
			switch strings.TrimSpace(src[line.start:line.end]) {
			case confabCodexHooksStart, confabCodexHooksEnd:
				doc.markers = append(doc.markers, line)
			}
		case unstable.Table, unstable.ArrayTable:
			path, first := codexKeyPath(e)
			line := lineSpan(src, first, first)
			doc.tables = append(doc.tables, codexTable{
				array:     e.Kind == unstable.ArrayTable,
				path:      path,
				span:      line,
				headerEnd: line.end,
			})
		case unstable.KeyValue:
			path, _ := codexKeyPath(e)
			kv := codexKeyValue{path: path, span: rawSpan(e.Raw)}
			if v := e.Value(); v != nil {
				kv.value = rawSpan(v.Raw)
			}
			if len(doc.tables) == 0 {
				doc.root = append(doc.root, kv)
				continue
			}
			t := &doc.tables[len(doc.tables)-1]
			t.kvs = append(t.kvs, kv)
			t.span.end = lineEnd(src, kv.span.end)
		}
	}
	if err := p.Error(); err != nil {
		return nil, fmt.Errorf("the Codex config is not valid TOML: %w", err)
	}
	return doc, nil
}

func rawSpan(r unstable.Range) codexSpan {
	return codexSpan{int(r.Offset), int(r.Offset + r.Length)}
}

// lineSpan widens [from, to) to whole lines, including the final newline.
func lineSpan(src string, from, to int) codexSpan {
	return codexSpan{strings.LastIndexByte(src[:from], '\n') + 1, lineEnd(src, to)}
}

// lineEnd returns the offset just past the newline that ends the line
// holding offset at, or len(s) when that line has no newline.
func lineEnd(s string, at int) int {
	if i := strings.IndexByte(s[at:], '\n'); i >= 0 {
		return at + i + 1
	}
	return len(s)
}

// codexKeyPath returns the decoded key parts of a table header or key-value
// and the byte offset of the first part.
func codexKeyPath(e *unstable.Node) (path []string, first int) {
	first = -1
	it := e.Key()
	for it.Next() {
		k := it.Node()
		path = append(path, string(k.Data))
		if first == -1 {
			first = int(k.Raw.Offset)
		}
	}
	return path, first
}

func pathEquals(path []string, want ...string) bool {
	return slices.Equal(path, want)
}

// findKeyValue returns the key-value whose full path (table path + key path)
// is want. Array tables are skipped: their keys belong to an array element.
func (d *codexDoc) findKeyValue(want ...string) (codexKeyValue, bool) {
	for _, kv := range d.root {
		if pathEquals(kv.path, want...) {
			return kv, true
		}
	}
	for _, t := range d.tables {
		if t.array {
			continue
		}
		for _, kv := range t.kvs {
			if pathEquals(slices.Concat(t.path, kv.path), want...) {
				return kv, true
			}
		}
	}
	return codexKeyValue{}, false
}

// findTable returns the index of the first non-array table at path.
func (d *codexDoc) findTable(path ...string) (int, bool) {
	for i, t := range d.tables {
		if !t.array && pathEquals(t.path, path...) {
			return i, true
		}
	}
	return 0, false
}

// codexHandler is one [[hooks.<Event>.hooks]] table.
type codexHandler struct {
	table  int // index into codexDoc.tables
	confab bool
}

// codexGroup is one [[hooks.<Event>]] table with its handlers.
type codexGroup struct {
	table    int // index into codexDoc.tables
	handlers []codexHandler
	sem      map[string]any
}

// codexEventLayout is an event's groups in source order, which is the order
// Codex numbers them in for its trust-state keys. When the groups are not
// written as array tables the editor cannot address them: groups is nil and
// count still says how many there are.
type codexEventLayout struct {
	groups []codexGroup
	count  int
}

// semHookGroups returns hooks.<event> as the generic group list, or nil.
func semHookGroups(sem map[string]any, event string) []any {
	hooks, _ := sem["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	return groups
}

// semHookState returns the hooks.state table, or nil.
func semHookState(sem map[string]any) map[string]any {
	hooks, _ := sem["hooks"].(map[string]any)
	state, _ := hooks["state"].(map[string]any)
	return state
}

// semHandlers returns a group's hooks list, or nil.
func semHandlers(group any) []any {
	g, _ := group.(map[string]any)
	handlers, _ := g["hooks"].([]any)
	return handlers
}

// codexOwns reports whether a handler belongs to Confabulous Code.
type codexOwns func(handler any) bool

// eventLayout matches the i-th [[hooks.<event>]] header to the i-th parsed
// group, and each [[hooks.<event>.hooks]] header to a handler. It refuses
// when a confab handler exists that is not written as array tables (inline
// arrays, dotted keys): such a handler cannot be edited by span.
func (d *codexDoc) eventLayout(event string, owns codexOwns) (codexEventLayout, error) {
	var groups []codexGroup
	located := true
	for i, t := range d.tables {
		switch {
		case !t.array:
		case pathEquals(t.path, "hooks", event):
			groups = append(groups, codexGroup{table: i})
		case pathEquals(t.path, "hooks", event, "hooks"):
			if len(groups) == 0 {
				located = false
				continue
			}
			g := &groups[len(groups)-1]
			g.handlers = append(g.handlers, codexHandler{table: i})
		}
	}

	semGroups := semHookGroups(d.sem, event)
	located = located && len(groups) == len(semGroups)
	for i := 0; located && i < len(groups); i++ {
		handlers := semHandlers(semGroups[i])
		if len(handlers) != len(groups[i].handlers) {
			located = false
			break
		}
		groups[i].sem, _ = semGroups[i].(map[string]any)
		for j, h := range handlers {
			groups[i].handlers[j].confab = owns(h)
		}
	}
	if located {
		return codexEventLayout{groups: groups, count: len(groups)}, nil
	}

	for _, g := range semGroups {
		for _, h := range semHandlers(g) {
			if owns(h) {
				return codexEventLayout{}, fmt.Errorf(
					"the Confabulous Code hook under hooks.%s is not written as [[hooks.%s]] tables, so it cannot be edited safely",
					event, event)
			}
		}
	}
	return codexEventLayout{count: len(semGroups)}, nil
}
