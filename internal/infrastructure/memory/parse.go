package memory

import (
	"strings"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

// sectionHeaderPrefix starts a section in the store's format. Blank lines
// and "## " lines in content are escaped (see encodeBlockContent).
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

// encodeBlockContent escapes blank lines and lines starting with "## " or a
// backslash by prefixing a backslash. decodeBlockContent reverses it.
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

// parseBlocks returns the marked blocks in a document, in order. Unmarked or
// undecodable blocks are skipped.
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
