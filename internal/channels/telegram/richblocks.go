package telegram

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"
)

// Telegram's rich-message renderer has no ATX heading support and its
// MarkdownV2 dialect needs escaping, so rather than translating Markdown into a
// format it only partially implements we emit structured blocks the renderer
// supports natively. Inline emphasis/code/links are reduced to plain text so no
// Markdown marker can survive into the rendered message.

var (
	inlineLinkHTML = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	codeBlockMark  = regexp.MustCompile("`([^`]*)`")
)

// emphasisMarkers are the delimiters stripEmphasis pairs, longest first so
// "**" is consumed before the "*" pass can see it as two single markers.
var emphasisMarkers = []string{"~~", "**", "__", "*", "_"}

// richText builds a plain Text; an empty RichText.Type serializes as a JSON
// string, which is exactly what a plain-text rich block wants.
func richText(text string) models.RichText {
	return models.RichText{PlainText: text}
}

func paragraphBlock(text string) models.InputRichBlock {
	return models.InputRichBlock{
		Type: models.RichBlockTypeParagraph,
		InputRichBlockParagraph: &models.InputRichBlockParagraph{
			Type: models.RichBlockTypeParagraph,
			Text: richText(text),
		},
	}
}

func headingBlock(line string) models.InputRichBlock {
	depth := headingDepth(line)
	text := stripInlineMarkdown(strings.TrimSpace(line[depth:]))
	return models.InputRichBlock{
		Type: models.RichBlockTypeSectionHeading,
		InputRichBlockSectionHeading: &models.InputRichBlockSectionHeading{
			Type: models.RichBlockTypeSectionHeading,
			Text: richText(text),
			Size: depth,
		},
	}
}

func preformattedBlock(text, lang string) models.InputRichBlock {
	return models.InputRichBlock{
		Type: models.RichBlockTypePreformatted,
		InputRichBlockPreformatted: &models.InputRichBlockPreformatted{
			Type:     models.RichBlockTypePreformatted,
			Text:     richText(text),
			Language: lang,
		},
	}
}

func headingDepth(line string) int {
	n := 0
	for n < len(line) && n < 6 && line[n] == '#' {
		n++
	}
	if n == 0 {
		return 0
	}
	if n < len(line) && line[n] != ' ' {
		return 0
	}
	return n
}

var (
	bulletListMarkers = []string{"- ", "* ", "+ "}
	orderedListMark   = regexp.MustCompile(`^[0-9]+[.)] `)
)

func isListItem(line string) bool {
	for _, marker := range bulletListMarkers {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	return orderedListMark.MatchString(line)
}

func listItemText(line string) string {
	for _, marker := range bulletListMarkers {
		if strings.HasPrefix(line, marker) {
			return stripInlineMarkdown(strings.TrimSpace(line[len(marker):]))
		}
	}
	if loc := orderedListMark.FindStringIndex(line); loc != nil {
		return stripInlineMarkdown(strings.TrimSpace(line[loc[1]:]))
	}
	return ""
}

// stripInlineMarkdown reduces inline emphasis, inline code and links to plain
// text, dropping the markers so none of them can leak into the rendered block.
func stripInlineMarkdown(s string) string {
	s = inlineLinkHTML.ReplaceAllString(s, "$1 ($2)")
	for _, marker := range emphasisMarkers {
		s = stripEmphasis(s, marker)
	}
	return codeBlockMark.ReplaceAllString(s, "$1")
}

// stripEmphasis removes paired emphasis delimiters and keeps their content.
// Pairing follows CommonMark's flanking rules, so a delimiter that cannot
// open or close is ordinary text and stays put. That is what keeps this
// project's own vocabulary intact over the wire: snake_case config keys,
// dotted paths like config_schema_test.go, and Go pointer types such as
// *sql.Tx all used to lose characters to a non-greedy `_(.+?)_` that paired
// any two markers on a line (archie-core-8x1r).
func stripEmphasis(s, marker string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if !strings.HasPrefix(s[i:], marker) {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := i + len(marker)
		if canOpen, _ := flanking(s, i, end, marker); canOpen {
			if closer := findCloser(s, end, marker); closer >= 0 {
				b.WriteString(s[end:closer])
				i = closer + len(marker)
				continue
			}
		}
		b.WriteString(marker)
		i = end
	}
	return b.String()
}

// findCloser returns the offset of the first delimiter at or after from that
// may close emphasis, or -1 when the run is unpaired.
func findCloser(s string, from int, marker string) int {
	for i := from; i+len(marker) <= len(s); i++ {
		if !strings.HasPrefix(s[i:], marker) {
			continue
		}
		if _, canClose := flanking(s, i, i+len(marker), marker); canClose {
			return i
		}
	}
	return -1
}

// delimiterFlanks carries CommonMark's flanking facts about one delimiter
// run: whether it is left- and/or right-flanking, and whether the characters
// either side are punctuation, which the underscore rules also need.
type delimiterFlanks struct {
	left, right          bool
	prevPunct, nextPunct bool
}

// flanksOf computes the flanking facts for the delimiter run s[i:j].
func flanksOf(s string, i, j int) delimiterFlanks {
	prev := ' '
	if i > 0 {
		prev, _ = utf8.DecodeLastRuneInString(s[:i])
	}
	next := ' '
	if j < len(s) {
		next, _ = utf8.DecodeRuneInString(s[j:])
	}
	prevSpace, nextSpace := unicode.IsSpace(prev), unicode.IsSpace(next)
	f := delimiterFlanks{prevPunct: isPunct(prev), nextPunct: isPunct(next)}
	f.left = !nextSpace && (!f.nextPunct || prevSpace || f.prevPunct)
	f.right = !prevSpace && (!f.prevPunct || nextSpace || f.nextPunct)
	return f
}

// flanking reports whether the delimiter run s[i:j] may open or close
// emphasis. The rules are CommonMark's, including the one that an underscore
// may do neither inside a word.
//
// Single "*" carries one deliberate deviation: it may not open on
// punctuation. CommonMark reads "*.go files in cmd/*" as emphasis, which in a
// relay carrying shell globs and pointer types corrupts far more text than a
// stray marker would. Emphasis opening on a word still works.
func flanking(s string, i, j int, marker string) (canOpen, canClose bool) {
	f := flanksOf(s, i, j)
	switch marker {
	case "_", "__":
		return f.left && (!f.right || f.prevPunct), f.right && (!f.left || f.nextPunct)
	case "*":
		return f.left && !f.nextPunct, f.right
	default:
		return f.left, f.right
	}
}

func isPunct(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// markdownToBlocks converts a Markdown document into Telegram rich-message
// blocks. It is intentionally conservative: unknown constructs fall back to a
// plain paragraph rather than risking literal markers in the rendered output.
func markdownToBlocks(md string) []models.InputRichBlock {
	if strings.TrimSpace(md) == "" {
		return nil
	}
	p := &markdownBlockParser{}
	for raw := range strings.SplitSeq(md, "\n") {
		p.handleLine(strings.TrimRight(raw, "\r"))
	}
	p.closeCode()
	p.flush()
	return p.blocks
}

// markdownBlockParser holds the mutable state markdownToBlocks threads
// through its line-by-line scan: the accumulated blocks, and whichever
// multi-line construct (paragraph, code fence, list, block quote) is
// currently open.
type markdownBlockParser struct {
	blocks     []models.InputRichBlock
	paragraph  strings.Builder
	codeLines  []string
	codeLang   string
	inCode     bool
	indented   bool
	listItems  [][]models.InputRichBlock
	quoteLines []string
}

// handleLine processes one line of input, dispatching to whichever
// construct is open or starting a new one.
func (p *markdownBlockParser) handleLine(line string) {
	trimmed := strings.TrimSpace(line)

	if p.inCode && p.consumeCodeLine(line, trimmed) {
		return
	}

	switch {
	case strings.HasPrefix(trimmed, "```"):
		p.flush()
		p.codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		p.inCode = true
		p.codeLines = nil
	case headingDepth(trimmed) > 0:
		p.flush()
		p.appendBlock(headingBlock(trimmed))
	case isListItem(trimmed):
		p.flushParagraph()
		p.flushQuote()
		if len(p.listItems) == 0 {
			p.flushList()
		}
		p.listItems = append(p.listItems, []models.InputRichBlock{paragraphBlock(listItemText(trimmed))})
	case p.startsIndentedCode(line):
		// CommonMark's other code form. Models emit it constantly, and
		// without this it fell to the paragraph case below, which
		// space-joins its lines into one run-on line (archie-core-cvu6).
		p.flush()
		p.inCode, p.indented, p.codeLang = true, true, ""
		p.codeLines = []string{stripCodeIndent(line)}
	case len(trimmed) > 1 && trimmed[0] == '>':
		p.flushParagraph()
		p.flushList()
		p.quoteLines = append(p.quoteLines, line)
	case trimmed == "":
		p.flush()
	default:
		// A normal line ends a list or quote block and continues a
		// paragraph. Joined with a space, not "\n": this is a CommonMark
		// soft line break, and Telegram's rich-block paragraph text does
		// not render an embedded "\n" as a break at all -- it renders
		// nothing, jamming the two lines together with no separator
		// whatsoever. A space is the correct soft-break rendering anyway
		// and survives regardless of how the client treats a literal "\n".
		p.flushList()
		p.flushQuote()
		if p.paragraph.Len() > 0 {
			p.paragraph.WriteString(" ")
		}
		p.paragraph.WriteString(trimmed)
	}
}

// consumeCodeLine handles one line while a code block is open, reporting
// whether that block claimed it. A fenced block ends at its closing fence; an
// indented block ends at the first non-indented, non-blank line, which is not
// a terminator and must still be parsed normally.
func (p *markdownBlockParser) consumeCodeLine(line, trimmed string) bool {
	if !p.indented {
		if strings.HasPrefix(trimmed, "```") {
			p.closeCode()
		} else {
			p.codeLines = append(p.codeLines, line)
		}
		return true
	}
	if trimmed == "" || indentedCodeLine(line) {
		p.codeLines = append(p.codeLines, stripCodeIndent(line))
		return true
	}
	p.closeCode()
	return false
}

// startsIndentedCode reports whether line opens an indented code block. It may
// not interrupt an open paragraph, list or quote, so a wrapped prose line that
// happens to be indented stays prose.
func (p *markdownBlockParser) startsIndentedCode(line string) bool {
	return indentedCodeLine(line) && p.paragraph.Len() == 0 &&
		len(p.listItems) == 0 && len(p.quoteLines) == 0
}

// flush closes every open construct, in the order that keeps blocks in
// source order.
func (p *markdownBlockParser) flush() {
	p.flushList()
	p.flushQuote()
	p.flushParagraph()
}

// appendBlock adds block to the output. Telegram's rich-block renderer adds
// no vertical gap between adjacent blocks on its own -- only a heading
// carries its own margin -- so two blocks from source lines with no blank
// line between them (an intro line immediately followed by a list, two
// back-to-back paragraphs) render glued together with no visible break at
// all. An empty paragraph spacer before any non-heading-following block
// makes that gap explicit instead of relying on client-side spacing that
// doesn't exist.
func (p *markdownBlockParser) appendBlock(block models.InputRichBlock) {
	if len(p.blocks) > 0 && p.blocks[len(p.blocks)-1].Type != models.RichBlockTypeSectionHeading {
		p.blocks = append(p.blocks, paragraphBlock(""))
	}
	p.blocks = append(p.blocks, block)
}

func (p *markdownBlockParser) flushParagraph() {
	if p.paragraph.Len() == 0 {
		return
	}
	if text := strings.TrimSpace(p.paragraph.String()); text != "" {
		p.appendBlock(paragraphBlock(stripInlineMarkdown(text)))
	}
	p.paragraph.Reset()
}

func (p *markdownBlockParser) flushList() {
	if len(p.listItems) == 0 {
		return
	}
	items := make([]models.InputRichBlockListItem, 0, len(p.listItems))
	for _, item := range p.listItems {
		items = append(items, models.InputRichBlockListItem{Blocks: item})
	}
	p.appendBlock(models.InputRichBlock{
		Type: models.RichBlockTypeList,
		InputRichBlockList: &models.InputRichBlockList{
			Type:  models.RichBlockTypeList,
			Items: items,
		},
	})
	p.listItems = nil
}

func (p *markdownBlockParser) flushQuote() {
	if len(p.quoteLines) == 0 {
		return
	}
	var inner []models.InputRichBlock
	for _, line := range p.quoteLines {
		body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
		if body == "" {
			continue
		}
		inner = append(inner, paragraphBlock(stripInlineMarkdown(body)))
	}
	if len(inner) > 0 {
		p.appendBlock(models.InputRichBlock{
			Type: models.RichBlockTypeBlockQuotation,
			InputRichBlockBlockQuotation: &models.InputRichBlockBlockQuotation{
				Type:   models.RichBlockTypeBlockQuotation,
				Blocks: inner,
			},
		})
	}
	p.quoteLines = nil
}

// closeCode emits the open code block. The text is passed through verbatim:
// code is not prose, and running the emphasis stripper over it turned
// *sql.Tx into sql.Tx and ate same-line snake_case pairs (archie-core-8x1r).
func (p *markdownBlockParser) closeCode() {
	lines := p.codeLines
	if p.indented {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
	}
	if len(lines) > 0 || p.codeLang != "" {
		p.appendBlock(preformattedBlock(strings.Join(lines, "\n"), p.codeLang))
	}
	p.codeLines, p.codeLang, p.inCode, p.indented = nil, "", false, false
}

// indentedCodeLine reports whether line opens or continues an indented code
// block: four spaces or a tab, and something other than whitespace after.
func indentedCodeLine(line string) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
}

// stripCodeIndent removes the one level of indentation that marks the block,
// so the code reads at its own natural margin.
func stripCodeIndent(line string) string {
	if strings.HasPrefix(line, "\t") {
		return line[1:]
	}
	return strings.TrimPrefix(line, "    ")
}

// blocksToPlainText flattens blocks into readable plain text for the fallback
// send, joining each block's text with a blank line. Because it derives from
// the structured blocks there are no Markdown markers left to strip.
func blocksToPlainText(blocks []models.InputRichBlock) []string {
	var out []string
	for _, block := range blocks {
		switch block.Type {
		case models.RichBlockTypeParagraph, models.RichBlockTypeSectionHeading, models.RichBlockTypePreformatted,
			models.RichBlockTypeFooter, models.RichBlockTypePullQuotation:
			if text := blockPlainText(block); text != "" {
				out = append(out, text)
			}
		case models.RichBlockTypeList:
			if text := listBlockPlainText(block); text != "" {
				out = append(out, text)
			}
		case models.RichBlockTypeBlockQuotation:
			if text := quoteBlockPlainText(block); text != "" {
				out = append(out, text)
			}
		case models.RichBlockTypeDivider:
			out = append(out, "———")
		}
	}
	return out
}

// listBlockPlainText renders a list block's items as newline-joined plain
// text, or "" if it has none.
func listBlockPlainText(block models.InputRichBlock) string {
	if block.InputRichBlockList == nil {
		return ""
	}
	var items []string
	for _, item := range block.InputRichBlockList.Items {
		for _, inner := range item.Blocks {
			if text := blockPlainText(inner); text != "" {
				items = append(items, text)
			}
		}
	}
	return strings.Join(items, "\n")
}

// quoteBlockPlainText renders a block quotation's inner blocks as
// newline-joined plain text, or "" if it has none.
func quoteBlockPlainText(block models.InputRichBlock) string {
	if block.InputRichBlockBlockQuotation == nil {
		return ""
	}
	var inner []string
	for _, b := range block.InputRichBlockBlockQuotation.Blocks {
		if text := blockPlainText(b); text != "" {
			inner = append(inner, text)
		}
	}
	return strings.Join(inner, "\n")
}

func blockPlainText(block models.InputRichBlock) string {
	switch block.Type {
	case models.RichBlockTypeParagraph:
		if block.InputRichBlockParagraph != nil {
			return block.InputRichBlockParagraph.Text.PlainText
		}
	case models.RichBlockTypeSectionHeading:
		if block.InputRichBlockSectionHeading != nil {
			return block.InputRichBlockSectionHeading.Text.PlainText
		}
	case models.RichBlockTypePreformatted:
		if block.InputRichBlockPreformatted != nil {
			return block.InputRichBlockPreformatted.Text.PlainText
		}
	case models.RichBlockTypeFooter:
		if block.InputRichBlockFooter != nil {
			return block.InputRichBlockFooter.Text.PlainText
		}
	case models.RichBlockTypePullQuotation:
		if block.InputRichBlockPullQuotation != nil {
			return block.InputRichBlockPullQuotation.Text.PlainText
		}
	}
	return ""
}

// splitBlocks divides blocks into chunks that each fit within maxLen runes,
// ending a chunk only at a block boundary so a code block or heading is never
// cut in half. Every block is retained; ordering is preserved. A single block
// larger than maxLen is returned on its own so it is not silently dropped.
func splitBlocks(blocks []models.InputRichBlock, maxLen int) [][]models.InputRichBlock {
	if len(blocks) == 0 || maxLen <= 0 {
		return [][]models.InputRichBlock{blocks}
	}
	var chunks [][]models.InputRichBlock
	var current []models.InputRichBlock
	var currentLen int
	for _, block := range blocks {
		blockLen := len([]rune(strings.Join(blocksToPlainText([]models.InputRichBlock{block}), "\n")))
		if len(current) > 0 && currentLen+blockLen > maxLen {
			chunks = append(chunks, current)
			current, currentLen = nil, 0
		}
		current = append(current, block)
		currentLen += blockLen
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}
