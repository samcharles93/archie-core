package memory

import (
	"strings"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

// sectionHeaderPrefix matches the markdown store's own format (builtin's
// sectionHeaderPrefix is unexported): a "## " line starts a named section,
// and blocks within it are separated by one blank line. This package only
// reads back what it wrote through builtin.Store.Add, so it only needs that
// one documented format, not the general case builtin.Store itself parses.
//
// Two properties of that format are traps rather than features, and
// builtin.Store owns both, so this engine documents them instead of working
// around them:
//
//   - a blank line ends a block, so a record's content is one paragraph.
//     Content containing a blank line would come back as a second,
//     marker-free block and be lost. Every producer writes a paragraph (the
//     chat tool one note, the curator one short extracted fact), and the
//     alternative -- an escape scheme -- would cost the hand-editability the
//     marker exists to preserve.
//   - a content line starting "## " starts a section instead, and the rest of
//     the block becomes unaddressed text. Same reason.
const sectionHeaderPrefix = "## "

// parsedBlock is one marked block read back out of a rendered document: the
// provenance its marker carried, the section it sat in, and its exact text
// within that document. Update replaces a block by that text, so it is never
// reconstructed from fields and can never disagree with what is on disk.
type parsedBlock struct {
	marker  markerData
	section string
	text    string
}

// content returns the block's content with the marker line stripped.
func (b parsedBlock) content() string {
	_, rest, _ := strings.Cut(b.text, "\n")
	return rest
}

// record renders the block as a record of scope. Kind comes from the marker
// rather than the section: every record's retained states live in one
// "history" section, so the section cannot be the source of a kind.
func (b parsedBlock) record(scope domainmemory.Scope) domainmemory.Record {
	return recordOf(b.marker, scope, b.content())
}

// record renders a marker and its content as a record of scope.
func recordOf(m markerData, scope domainmemory.Scope, content string) domainmemory.Record {
	return domainmemory.Record{
		ID:         domainmemory.RecordID(m.ID),
		Scope:      scope,
		Kind:       m.Kind,
		Content:    content,
		Revision:   m.Revision,
		Author:     m.Author,
		OriginUser: domainmemory.IdentityID(m.OriginUser),
		Source:     m.Source,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

// renderBlock renders one record as the markdown block the store persists:
// the marker line, then the content.
func renderBlock(m markerData, content string) (string, error) {
	line, err := renderMarker(m)
	if err != nil {
		return "", err
	}
	return line + "\n" + content, nil
}

// parseBlocks reads every marked block out of a rendered document.
//
// A block with no marker, or one whose marker does not decode, was not
// written by this engine -- an operator hand-edited the file, or it predates
// the marker -- and is skipped. This engine's Store contract is only ever
// asked about records it assigned an id to, so inventing an id for an
// unmarked block would be a worse answer than not seeing it.
//
// Blocks are returned in document order, which is append order for a
// document this engine wrote, and therefore the order HISTORY.md's retained
// states come back in.
func parseBlocks(rendered string) []parsedBlock {
	var blocks []parsedBlock
	section := ""
	var lines []string

	flush := func() {
		if len(lines) == 0 {
			return
		}
		text := strings.Join(lines, "\n")
		lines = nil
		first, _, _ := strings.Cut(text, "\n")
		marker, ok := parseMarker(first)
		if !ok {
			return
		}
		blocks = append(blocks, parsedBlock{marker: marker, section: section, text: text})
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
		lines = append(lines, line)
	}
	flush()
	return blocks
}
