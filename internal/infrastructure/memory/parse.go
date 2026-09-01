package memory

import (
	"strings"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

// sectionHeaderPrefix matches internal/memory/builtin's own format (its
// sectionHeaderPrefix constant is unexported): a "## " line starts a named
// section, and blocks within it are separated by one blank line. This
// package only ever reads what it wrote through builtin.Store.Add, so it
// only needs to read that one documented format back, not the general
// case builtin.Store itself parses.
const sectionHeaderPrefix = "## "

// parseRecords reads every marked block out of rendered and returns it as
// a Record scoped to identity. A block with no marker was not written by
// this engine (e.g. hand-edited) and is skipped: this engine's contract is
// only ever asked about records it assigned an ID to.
func parseRecords(identity, rendered string) []domainmemory.Record {
	var records []domainmemory.Record
	section := ""
	var blockLines []string

	flush := func() {
		if len(blockLines) == 0 {
			return
		}
		if rec, ok := parseBlock(identity, section, strings.Join(blockLines, "\n")); ok {
			records = append(records, rec)
		}
		blockLines = nil
	}

	for line := range strings.SplitSeq(rendered, "\n") {
		if name, ok := strings.CutPrefix(line, sectionHeaderPrefix); ok {
			flush()
			section = strings.TrimSpace(name)
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		blockLines = append(blockLines, line)
	}
	flush()
	return records
}

// parseBlock splits one block back into its marker-carried ID and content.
// ok is false for a block with no recognizable marker.
func parseBlock(identity, section, block string) (domainmemory.Record, bool) {
	first, rest, _ := strings.Cut(block, "\n")
	id, ok := strings.CutPrefix(first, markerPrefix)
	if !ok {
		return domainmemory.Record{}, false
	}
	id, ok = strings.CutSuffix(id, markerSuffix)
	if !ok {
		return domainmemory.Record{}, false
	}
	return domainmemory.Record{
		ID:       id,
		Identity: identity,
		Kind:     section,
		Content:  rest,
	}, true
}
