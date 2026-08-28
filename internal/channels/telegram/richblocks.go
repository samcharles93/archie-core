package telegram

import (
	"regexp"
	"strings"

	"github.com/go-telegram/bot/models"
)

// Telegram's rich-message renderer has no ATX heading support and its
// MarkdownV2 dialect needs escaping, so rather than translating Markdown into a
// format it only partially implements we emit structured blocks the renderer
// supports natively. Inline emphasis/code/links are reduced to plain text so no
// Markdown marker can survive into the rendered message.

var (
	inlineLinkHTML     = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
	strikeBlockMark    = regexp.MustCompile(`~~(.+?)~~`)
	boldBlockMark      = regexp.MustCompile(`\*\*(.+?)\*\*`)
	underlineBlockMark = regexp.MustCompile(`__(.+?)__`)
	italicBlockMark    = regexp.MustCompile(`\*(.+?)\*`)
	italicBlockUndersc = regexp.MustCompile(`_(.+?)_`)
	codeBlockMark      = regexp.MustCompile("`([^`]*)`")
)

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
	s = strikeBlockMark.ReplaceAllString(s, "$1")
	s = boldBlockMark.ReplaceAllString(s, "$1")
	s = underlineBlockMark.ReplaceAllString(s, "$1")
	s = italicBlockMark.ReplaceAllString(s, "$1")
	s = italicBlockUndersc.ReplaceAllString(s, "$1")
	s = codeBlockMark.ReplaceAllString(s, "$1")
	return s
}

// markdownToBlocks converts a Markdown document into Telegram rich-message
// blocks. It is intentionally conservative: unknown constructs fall back to a
// plain paragraph rather than risking literal markers in the rendered output.
func markdownToBlocks(md string) []models.InputRichBlock {
	if strings.TrimSpace(md) == "" {
		return nil
	}
	lines := strings.Split(md, "\n")
	var blocks []models.InputRichBlock
	var paragraph strings.Builder
	var codeLines []string
	var codeLang string
	inCode := false
	var listItems [][]models.InputRichBlock
	var quoteLines []string

	flushParagraph := func() {
		if paragraph.Len() == 0 {
			return
		}
		if text := strings.TrimSpace(paragraph.String()); text != "" {
			blocks = append(blocks, paragraphBlock(stripInlineMarkdown(text)))
		}
		paragraph.Reset()
	}
	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		items := make([]models.InputRichBlockListItem, 0, len(listItems))
		for _, item := range listItems {
			items = append(items, models.InputRichBlockListItem{Blocks: item})
		}
		blocks = append(blocks, models.InputRichBlock{
			Type: models.RichBlockTypeList,
			InputRichBlockList: &models.InputRichBlockList{
				Type:  models.RichBlockTypeList,
				Items: items,
			},
		})
		listItems = nil
	}
	flushQuote := func() {
		if len(quoteLines) == 0 {
			return
		}
		var inner []models.InputRichBlock
		for _, line := range quoteLines {
			body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
			if body == "" {
				continue
			}
			inner = append(inner, paragraphBlock(stripInlineMarkdown(body)))
		}
		if len(inner) > 0 {
			blocks = append(blocks, models.InputRichBlock{
				Type: models.RichBlockTypeBlockQuotation,
				InputRichBlockBlockQuotation: &models.InputRichBlockBlockQuotation{
					Type:   models.RichBlockTypeBlockQuotation,
					Blocks: inner,
				},
			})
		}
		quoteLines = nil
	}
	closeCode := func() {
		if len(codeLines) > 0 || codeLang != "" {
			blocks = append(blocks, preformattedBlock(stripInlineMarkdown(strings.Join(codeLines, "\n")), codeLang))
		}
		codeLines, codeLang, inCode = nil, "", false
	}
	flush := func() {
		flushList()
		flushQuote()
		flushParagraph()
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		if inCode {
			if strings.HasPrefix(trimmed, "```") {
				closeCode()
			} else {
				codeLines = append(codeLines, line)
			}
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "```"):
			flush()
			codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			inCode = true
			codeLines = nil
		case headingDepth(trimmed) > 0:
			flush()
			blocks = append(blocks, headingBlock(trimmed))
		case isListItem(trimmed):
			flushParagraph()
			flushQuote()
			if len(listItems) == 0 {
				flushList()
			}
			listItems = append(listItems, []models.InputRichBlock{paragraphBlock(listItemText(trimmed))})
		case len(trimmed) > 1 && trimmed[0] == '>':
			flushParagraph()
			flushList()
			quoteLines = append(quoteLines, line)
		case trimmed == "":
			flush()
		default:
			// A normal line ends a list or quote block and continues a paragraph.
			flushList()
			flushQuote()
			if paragraph.Len() > 0 {
				paragraph.WriteString("\n")
			}
			paragraph.WriteString(line)
		}
	}
	closeCode()
	flush()
	return blocks
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
			if block.InputRichBlockList == nil {
				continue
			}
			var items []string
			for _, item := range block.InputRichBlockList.Items {
				for _, inner := range item.Blocks {
					if text := blockPlainText(inner); text != "" {
						items = append(items, text)
					}
				}
			}
			if len(items) > 0 {
				out = append(out, strings.Join(items, "\n"))
			}
		case models.RichBlockTypeBlockQuotation:
			if block.InputRichBlockBlockQuotation == nil {
				continue
			}
			var inner []string
			for _, b := range block.InputRichBlockBlockQuotation.Blocks {
				if text := blockPlainText(b); text != "" {
					inner = append(inner, text)
				}
			}
			if len(inner) > 0 {
				out = append(out, strings.Join(inner, "\n"))
			}
		case models.RichBlockTypeDivider:
			out = append(out, "———")
		}
	}
	return out
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
