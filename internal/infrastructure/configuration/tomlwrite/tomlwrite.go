// Package tomlwrite patches specific keys in a TOML document without
// disturbing anything else in it -- including comments, blank lines, and
// key order.
//
// # Why not decode/re-marshal
//
// BurntSushi/toml (this project's TOML library, and every other Go TOML
// encoder evaluated for archie-core-rs9) does not round-trip comments: a
// Decode followed by an Encode reproduces the data but discards every
// comment. config.example.toml carries substantial documentation as inline
// comments that operators are meant to read and hand-edit, and archied
// setup must be able to write into that same file -- and be re-run later
// to change one value -- without deleting it. A full re-marshal was
// rejected for that reason (archie-core-rs9).
//
// # The chosen strategy
//
// Apply treats the document as text and edits only the lines a caller
// names. For each requested key it looks, in order, for: an active
// "key = value" line in the target table (replace the value, keep any
// trailing inline comment); a commented-out "# key = value" line in the
// target table (uncomment the table header and the line, then replace the
// value); or, failing both, a place to add the key (the end of the
// table's existing content, or a brand new table appended at the end of
// the document). Every line Apply was not asked to touch is copied through
// unchanged, byte for byte.
//
// This trades generality for the property archie-core-rs9 requires: a
// second setup run that changes one value leaves every other line --
// comments included -- identical to the first run's output. It only
// understands single-line scalar and inline-table values (strings,
// numbers, booleans, `{ engine = "...", key = "..." }` secret refs), which
// covers everything archied setup writes. Multi-line arrays and
// [[array-of-tables]] entries are not targets for Apply; other code paths
// (e.g. adding a [[repos]] entry) must not use it.
package tomlwrite

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Edit is one key = value change to make within a TOML document.
//
// Table is the dotted path to the table the key lives in ("" for the
// document root, "forge", "providers.openai", "chat.telegram", ...). Value
// is the exact TOML literal to write -- callers are responsible for
// quoting it themselves; see [String] and [Ref] for the two shapes archied
// setup needs.
type Edit struct {
	Table string
	Key   string
	Value string
}

// String renders s as a quoted TOML basic string.
func String(s string) string {
	return strconv.Quote(s)
}

// Ref renders a secret.SecretRef as the inline table archied setup writes
// for it: { engine = "...", key = "..." }. Kept as a string here, rather
// than importing internal/secret, so this package has no dependency on
// the runtime config model -- it edits text, not Go values.
func Ref(engine, key string) string {
	return fmt.Sprintf("{ engine = %s, key = %s }", String(engine), String(key))
}

var (
	activeHeaderRe    = regexp.MustCompile(`^\[([A-Za-z0-9_.]+)\]\s*(#.*)?$`)
	commentedHeaderRe = regexp.MustCompile(`^#\s*\[([A-Za-z0-9_.]+)\]\s*(#.*)?$`)
	activeKeyRe       = regexp.MustCompile(`^(\s*)([A-Za-z0-9_.]+)(\s*=\s*)(.*)$`)
	commentedKeyRe    = regexp.MustCompile(`^(\s*)#\s?([A-Za-z0-9_.]+)(\s*=\s*)(.*)$`)
)

// Generate applies edits to template and returns the result: a config a
// human can read and hand-edit afterwards, since it is the documented
// template with only the requested values filled in. archied setup calls
// this on first run, with configtemplate.Example as template, and calls
// Apply directly against the existing file on every later run.
func Generate(template []byte, edits []Edit) ([]byte, error) {
	return Apply(template, edits)
}

// Apply patches src, setting each edit's key to its value within the
// edit's table, and returns the result. See the package doc for the
// matching and placement rules, and their limitations.
func Apply(src []byte, edits []Edit) ([]byte, error) {
	pending, order, err := collectPending(edits)
	if err != nil {
		return nil, err
	}

	trailingNewline := len(src) == 0 || src[len(src)-1] == '\n'
	text := strings.TrimSuffix(string(src), "\n")
	var lines []string
	if text != "" || len(src) > 0 {
		lines = strings.Split(text, "\n")
	}

	a := &applier{
		lines:       lines,
		pending:     pending,
		order:       order,
		replace:     map[int]string{},
		insertAfter: map[int][]string{},
	}
	a.indexTables()

	tables := make([]string, 0, len(order))
	for t := range order {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		a.resolveTable(t)
	}

	result := a.render()
	if trailingNewline {
		result += "\n"
	}
	return []byte(result), nil
}

// collectPending groups edits by table and key, and records each table's
// keys in the caller's edit order for deterministic appends.
func collectPending(edits []Edit) (map[string]map[string]string, map[string][]string, error) {
	pending := map[string]map[string]string{}
	order := map[string][]string{}
	for _, e := range edits {
		if e.Key == "" {
			return nil, nil, fmt.Errorf("tomlwrite: edit has empty key (table %q)", e.Table)
		}
		t := pending[e.Table]
		if t == nil {
			t = map[string]string{}
			pending[e.Table] = t
		}
		if _, exists := t[e.Key]; !exists {
			order[e.Table] = append(order[e.Table], e.Key)
		}
		t[e.Key] = e.Value
	}
	return pending, order, nil
}

// applier holds the line-indexed state Apply threads through resolving
// each edited table: which table each line belongs to (active or
// commented-out), and the accumulated replace/insert/append operations.
type applier struct {
	lines            []string
	pending          map[string]map[string]string
	order            map[string][]string
	activeTableAt    []string
	commentedTableAt []string
	replace          map[int]string
	insertAfter      map[int][]string
	appendBlocks     [][]string
}

// indexTables computes, for every line, which active or commented-out
// table it belongs to.
func (a *applier) indexTables() {
	a.activeTableAt = make([]string, len(a.lines))
	a.commentedTableAt = make([]string, len(a.lines))
	activeTable, commentedTable := "", ""
	for i, line := range a.lines {
		switch {
		case activeHeaderRe.MatchString(line):
			activeTable = activeHeaderRe.FindStringSubmatch(line)[1]
			commentedTable = ""
		case commentedHeaderRe.MatchString(line):
			commentedTable = commentedHeaderRe.FindStringSubmatch(line)[1]
		case strings.TrimSpace(line) == "":
			commentedTable = ""
		}
		a.activeTableAt[i] = activeTable
		a.commentedTableAt[i] = commentedTable
	}
}

// resolveTable satisfies every pending edit for one table, in priority
// order: rewrite an active "key = value" line, else uncomment a commented
// one (and its header), else append a new key.
func (a *applier) resolveTable(table string) {
	keys := a.order[table]
	if len(keys) == 0 {
		return
	}
	remaining := map[string]bool{}
	for _, k := range keys {
		remaining[k] = true
	}

	a.matchActiveKeys(table, remaining)
	if len(remaining) == 0 {
		return
	}
	headerLine := a.uncommentKeys(table, remaining)
	if len(remaining) == 0 {
		return
	}
	a.appendMissingKeys(table, keys, remaining, headerLine)
}

// matchActiveKeys rewrites every remaining key that already has an active
// "key = value" line in table, removing it from remaining.
func (a *applier) matchActiveKeys(table string, remaining map[string]bool) {
	for i, line := range a.lines {
		if a.activeTableAt[i] != table {
			continue
		}
		m := activeKeyRe.FindStringSubmatch(line)
		if m == nil || !remaining[m[2]] {
			continue
		}
		a.replace[i] = m[1] + m[2] + m[3] + a.pending[table][m[2]] + trailingComment(m[4])
		delete(remaining, m[2])
	}
}

// uncommentKeys rewrites every remaining key that has a commented-out
// "# key = value" line in table, uncommenting the table header (once) and
// the key line, removing matched keys from remaining. Returns the header
// line index, or -1 if table has no commented header.
func (a *applier) uncommentKeys(table string, remaining map[string]bool) int {
	headerLine := -1
	for i, line := range a.lines {
		if a.commentedTableAt[i] == table && commentedHeaderRe.MatchString(line) {
			headerLine = i // last matching header wins; there should only be one
		}
	}
	for i, line := range a.lines {
		if a.commentedTableAt[i] != table {
			continue
		}
		if commentedHeaderRe.MatchString(line) {
			continue
		}
		m := commentedKeyRe.FindStringSubmatch(line)
		if m == nil || !remaining[m[2]] {
			continue
		}
		if headerLine >= 0 {
			if _, done := a.replace[headerLine]; !done {
				a.replace[headerLine] = "[" + table + "]"
			}
		}
		a.replace[i] = m[1] + m[2] + m[3] + a.pending[table][m[2]] + trailingComment(m[4])
		delete(remaining, m[2])
	}
	return headerLine
}

// appendMissingKeys adds every key in remaining that no line in table
// satisfied: after the table's last active line, after its header if it
// only exists commented-out, or as a brand-new table block.
func (a *applier) appendMissingKeys(table string, keys []string, remaining map[string]bool, headerLine int) {
	var toAdd []string
	for _, k := range keys {
		if remaining[k] {
			toAdd = append(toAdd, k)
		}
	}
	sort.Strings(toAdd)
	var newLines []string
	for _, k := range toAdd {
		newLines = append(newLines, k+" = "+a.pending[table][k])
	}

	lastActiveLine := -1
	for i := range a.lines {
		if a.activeTableAt[i] == table {
			lastActiveLine = i
		}
	}
	switch {
	case lastActiveLine >= 0:
		a.insertAfter[lastActiveLine] = append(a.insertAfter[lastActiveLine], newLines...)
	case headerLine >= 0:
		if _, done := a.replace[headerLine]; !done {
			a.replace[headerLine] = "[" + table + "]"
		}
		a.insertAfter[headerLine] = append(a.insertAfter[headerLine], newLines...)
	default:
		block := append([]string{"", "[" + table + "]"}, newLines...)
		a.appendBlocks = append(a.appendBlocks, block)
	}
}

// render assembles the final text from the original lines plus every
// replace/insert/append operation resolveTable recorded.
func (a *applier) render() string {
	out := make([]string, 0, len(a.lines)+8)
	for i, line := range a.lines {
		if r, ok := a.replace[i]; ok {
			out = append(out, r)
		} else {
			out = append(out, line)
		}
		out = append(out, a.insertAfter[i]...)
	}
	for _, block := range a.appendBlocks {
		out = append(out, block...)
	}
	return strings.Join(out, "\n")
}

// trailingComment returns the " # ..." suffix of rest, if rest carries an
// inline comment outside of a quoted string, and "" otherwise. rest is
// everything after "key = " on an original line; the value portion itself
// is discarded by the caller, which substitutes its own.
func trailingComment(rest string) string {
	inString := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '"':
			if i == 0 || rest[i-1] != '\\' {
				inString = !inString
			}
		case '#':
			if !inString {
				return " " + strings.TrimRight(rest[i:], " \t")
			}
		}
	}
	return ""
}
