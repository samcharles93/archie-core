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
// Those two delimiters are the format's structure, and builtin.Store owns
// them: a blank line ends a block and a "## " line starts a section, so
// content holding either would be read back as structure -- cut at the
// delimiter, the remainder unaddressable. Content is therefore escaped on
// the way out and unescaped on the way back (see encodeBlockContent), which
// keeps a record's content whole without giving the store a second format to
// parse.
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

// content returns the block's content with the marker line stripped and the
// format's escaping undone.
func (b parsedBlock) content() string {
	_, rest, _ := strings.Cut(b.text, "\n")
	return decodeBlockContent(rest)
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
	return line + "\n" + encodeBlockContent(content), nil
}

// contentEscape is the backslash that marks a content line the format would
// otherwise read as structure.
const contentEscape = `\`

// encodeBlockContent escapes the content lines the store's format would
// otherwise read as its own structure -- a blank line ends a block, and a
// "## " line starts a section -- so that a record's content survives the
// write whole (see sectionHeaderPrefix). An empty line becomes one backslash,
// and a line already starting with a backslash or with "## " gains one, so
// the two are never ambiguous: everything is written verbatim except lines
// that start with a backslash, and every content line can therefore be
// recovered by dropping the backslash it starts with (decodeBlockContent).
// The common record -- one paragraph, no delimiter in sight -- is written
// exactly as the caller wrote it, so the document stays hand-editable.
func encodeBlockContent(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		switch {
		case line == "":
			lines[i] = contentEscape
		case strings.HasPrefix(line, contentEscape), strings.HasPrefix(line, sectionHeaderPrefix):
			lines[i] = contentEscape + line
		}
	}
	return strings.Join(lines, "\n")
}

// decodeBlockContent is encodeBlockContent's inverse: it returns the content
// the block's lines were rendered from.
func decodeBlockContent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == contentEscape {
			lines[i] = ""
			continue
		}
		lines[i] = strings.TrimPrefix(line, contentEscape)
	}
	return strings.Join(lines, "\n")
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
