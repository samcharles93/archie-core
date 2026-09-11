package telegram

import (
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

// blockText extracts the plain text of a single block, for assertions.
func blockText(b models.InputRichBlock) string {
	switch b.Type {
	case models.RichBlockTypeParagraph:
		if b.InputRichBlockParagraph != nil {
			return b.InputRichBlockParagraph.Text.PlainText
		}
	case models.RichBlockTypeSectionHeading:
		if b.InputRichBlockSectionHeading != nil {
			return b.InputRichBlockSectionHeading.Text.PlainText
		}
	case models.RichBlockTypePreformatted:
		if b.InputRichBlockPreformatted != nil {
			return b.InputRichBlockPreformatted.Text.PlainText
		}
	}
	return ""
}

func TestMarkdownToBlocksHeadings(t *testing.T) {
	// A spacer paragraph precedes the heading: Telegram adds no gap between
	// adjacent blocks on its own, so the parser inserts one explicitly
	// before every non-heading-following block. See appendBlock.
	blocks := markdownToBlocks("Intro prose.\n### Danger\nMore prose.")
	if len(blocks) != 4 {
		t.Fatalf("blocks = %d, want 4 (%v)", len(blocks), blockTypes(blocks))
	}
	heading := blocks[2]
	if heading.Type != models.RichBlockTypeSectionHeading {
		t.Fatalf("second block type = %q, want heading", heading.Type)
	}
	if got := heading.InputRichBlockSectionHeading.Size; got != 3 {
		t.Errorf("heading size = %d, want 3 (for ###)", got)
	}
	if got := blockText(heading); got != "Danger" {
		t.Errorf("heading text = %q, want Danger", got)
	}
}

func TestMarkdownToBlocksHeadingsMapDepthToSize(t *testing.T) {
	for _, tc := range []struct {
		line string
		size int
	}{
		{"# One", 1},
		{"## Two", 2},
		{"### Three", 3},
		{"#### Four", 4},
		{"##### Five", 5},
		{"###### Six", 6},
	} {
		blocks := markdownToBlocks(tc.line)
		if len(blocks) != 1 {
			t.Fatalf("%q: blocks = %d, want 1", tc.line, len(blocks))
		}
		if got := blocks[0].InputRichBlockSectionHeading.Size; got != tc.size {
			t.Errorf("%q: size = %d, want %d", tc.line, got, tc.size)
		}
	}
}

func TestMarkdownToBlocksFencedCode(t *testing.T) {
	// Non-heading blocks get an explicit spacer paragraph on both sides; see
	// appendBlock.
	in := "before\n```go\nfunc main() {}\n```\nafter"
	blocks := markdownToBlocks(in)
	if len(blocks) != 5 {
		t.Fatalf("blocks = %d, want 5 (%v)", len(blocks), blockTypes(blocks))
	}
	pre := blocks[2]
	if pre.Type != models.RichBlockTypePreformatted {
		t.Fatalf("second block type = %q, want pre", pre.Type)
	}
	if got := pre.InputRichBlockPreformatted.Language; got != "go" {
		t.Errorf("language = %q, want go", got)
	}
	if got := blockText(pre); got != "func main() {}" {
		t.Errorf("code = %q, want func main() {}", got)
	}
}

func TestMarkdownToBlocksBulletList(t *testing.T) {
	// Non-heading blocks get an explicit spacer paragraph on both sides; see
	// appendBlock.
	in := "Text before\n- one\n- two\nText after"
	blocks := markdownToBlocks(in)
	if len(blocks) != 5 {
		t.Fatalf("blocks = %d, want 5 (%v)", len(blocks), blockTypes(blocks))
	}
	list := blocks[2]
	if list.Type != models.RichBlockTypeList {
		t.Fatalf("second block type = %q, want list", list.Type)
	}
	items := list.InputRichBlockList.Items
	if len(items) != 2 {
		t.Fatalf("list items = %d, want 2", len(items))
	}
	if got := blockText(items[0].Blocks[0]); got != "one" {
		t.Errorf("item 0 = %q, want one", got)
	}
}

func TestMarkdownToBlocksBlockQuotation(t *testing.T) {
	// Non-heading blocks get an explicit spacer paragraph on both sides; see
	// appendBlock.
	in := "prose\n> important note\nafter"
	blocks := markdownToBlocks(in)
	if len(blocks) != 5 {
		t.Fatalf("blocks = %d, want 5 (%v)", len(blocks), blockTypes(blocks))
	}
	quote := blocks[2]
	if quote.Type != models.RichBlockTypeBlockQuotation {
		t.Fatalf("second block type = %q, want blockquote", quote.Type)
	}
	if got := blockText(quote.InputRichBlockBlockQuotation.Blocks[0]); got != "important note" {
		t.Errorf("quote text = %q, want 'important note'", got)
	}
}

func TestMarkdownToBlocksStripsInlineMarkers(t *testing.T) {
	blocks := markdownToBlocks("**bold** and `code` and [a link](https://example.com)")
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	got := blockText(blocks[0])
	for _, marker := range []string{"**", "`", "](https://example.com)"} {
		if strings.Contains(got, marker) {
			t.Errorf("text %q leaked marker %q", got, marker)
		}
	}
	if !strings.Contains(got, "a link (https://example.com)") {
		t.Errorf("text = %q, want link rendered as 'label (url)'", got)
	}
	if !strings.Contains(got, "bold") {
		t.Errorf("text = %q, lost bold content", got)
	}
}

func TestMarkdownToBlocksToolBlockBecomesParagraphAndCode(t *testing.T) {
	// The tool progress block composes a label line then a ```text fence.
	// Non-heading blocks get an explicit spacer paragraph on both sides; see
	// appendBlock.
	in := "🔧 shell — done\n```text\nexit 0\n```\ndone"
	blocks := markdownToBlocks(in)
	if len(blocks) != 5 {
		t.Fatalf("blocks = %d, want 5 (%v)", len(blocks), blockTypes(blocks))
	}
	if got := blockText(blocks[0]); got != "🔧 shell — done" {
		t.Errorf("label block = %q", got)
	}
	if blocks[2].Type != models.RichBlockTypePreformatted {
		t.Fatalf("third block type = %q, want pre", blocks[2].Type)
	}
	if got := blockText(blocks[2]); got != "exit 0" {
		t.Errorf("code = %q, want exit 0", got)
	}
}

func TestMarkdownToBlocksEmptyAndResets(t *testing.T) {
	if blocks := markdownToBlocks(""); len(blocks) != 0 {
		t.Fatalf("empty input produced %d blocks", len(blocks))
	}
	// Consecutive headings must not collapse into one block.
	blocks := markdownToBlocks("### One\n### Two")
	if len(blocks) != 2 {
		t.Fatalf("two headings produced %d blocks", len(blocks))
	}
}

func TestBlocksToPlainText(t *testing.T) {
	blocks := markdownToBlocks("## Head\n\npara one\n- item\n```\ncode\n```")
	out := strings.Join(blocksToPlainText(blocks), "\n\n")
	if strings.Contains(out, "##") || strings.Contains(out, "```") {
		t.Fatalf("plain text leaked markdown markers: %q", out)
	}
	if !strings.Contains(out, "Head") || !strings.Contains(out, "para one") ||
		!strings.Contains(out, "item") || !strings.Contains(out, "code") {
		t.Fatalf("plain text lost content: %q", out)
	}
}

func blockTypes(blocks []models.InputRichBlock) []string {
	types := make([]string, len(blocks))
	for i, b := range blocks {
		types[i] = string(b.Type)
	}
	return types
}

func TestSplitBlocksFitsInOneChunk(t *testing.T) {
	blocks := markdownToBlocks("one\n\n## Two\n\n- a\n- b")
	chunks := splitBlocks(blocks, 10_000)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if len(chunks[0]) != len(blocks) {
		t.Fatalf("chunk has %d blocks, want %d", len(chunks[0]), len(blocks))
	}
}

func TestSplitBlocksBreaksOnlyAtBlockBoundaries(t *testing.T) {
	// A large code block followed by prose must not be split mid-block.
	big := strings.Repeat("x", 5000)
	blocks := markdownToBlocks("```text\n" + big + "\n```\n\nafter")
	chunks := splitBlocks(blocks, 1000)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	// First chunk is the preformatted block, whole.
	if chunks[0][0].Type != models.RichBlockTypePreformatted {
		t.Fatalf("first block type = %q, want pre", chunks[0][0].Type)
	}
	if got := blockText(chunks[0][0]); got != big {
		t.Fatalf("code block was truncated (len %d)", len(got))
	}
	// Second chunk carries the trailing paragraph.
	if chunks[1][0].Type != models.RichBlockTypeParagraph {
		t.Fatalf("second chunk block type = %q, want paragraph", chunks[1][0].Type)
	}
}

func TestSplitBlocksPreservesOrderAndContent(t *testing.T) {
	blocks := markdownToBlocks("para one\n\npara two\n\npara three")
	chunks := splitBlocks(blocks, 4)
	var all []models.InputRichBlock
	for _, chunk := range chunks {
		all = append(all, chunk...)
	}
	if len(all) != len(blocks) {
		t.Fatalf("total blocks %d, want %d", len(all), len(blocks))
	}
	// Spacer paragraphs (see appendBlock) are empty and intentionally
	// dropped here, same as blocksToPlainText does for the real send path.
	var texts []string
	for _, b := range all {
		if text := blockText(b); text != "" {
			texts = append(texts, text)
		}
	}
	got := strings.Join(texts, " ")
	want := "para one para two para three"
	if got != want {
		t.Fatalf("order/content = %q, want %q", got, want)
	}
}

func TestSplitBlocksEmptyAndOversizedBlock(t *testing.T) {
	if chunks := splitBlocks(nil, 1000); len(chunks) != 1 || len(chunks[0]) != 0 {
		t.Fatalf("empty input chunks = %v, want a single empty chunk", chunks)
	}
	// A single block larger than the bound is kept whole.
	blocks := markdownToBlocks(strings.Repeat("z", 5000))
	chunks := splitBlocks(blocks, 100)
	if len(chunks) != 1 {
		t.Fatalf("oversized single block produced %d chunks, want 1", len(chunks))
	}
}

// Identifiers carrying Markdown's emphasis characters are ordinary prose in
// this project: snake_case config keys, dotted file paths and Go pointer
// types all reach Telegram inside agent reports. Emphasis stripping must
// leave them byte-for-byte intact, or the relay hands operators citations to
// files that do not exist (archie-core-8x1r).
func TestStripInlineMarkdownKeepsIdentifiers(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			name: "two snake_case keys on one line",
			in:   `"allow_concurrent": true, "max_retries": true`,
			want: `"allow_concurrent": true, "max_retries": true`,
		},
		{
			name: "snake_case file path",
			in:   "the internal/webui/config_schema_test.go file",
			want: "the internal/webui/config_schema_test.go file",
		},
		{
			name: "two Go pointer types on one line",
			in:   "RecordDispatch takes no *sql.Tx and returns *Result",
			want: "RecordDispatch takes no *sql.Tx and returns *Result",
		},
		{
			name: "leading-underscore identifiers",
			in:   "reply on _INBOX.foo then _INBOX.bar",
			want: "reply on _INBOX.foo then _INBOX.bar",
		},
		{
			name: "glob patterns",
			in:   "glob the *.go files under cmd/*",
			want: "glob the *.go files under cmd/*",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripInlineMarkdown(tc.in); got != tc.want {
				t.Errorf("stripInlineMarkdown(%q)\n = %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// The flanking rules must not cost us actual emphasis stripping: a marker
// that really does delimit emphasis still has to be reduced to plain text,
// because Telegram's renderer would otherwise show the raw marker.
func TestStripInlineMarkdownStillStripsRealEmphasis(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"italic underscore", "an _emphasised_ word", "an emphasised word"},
		{"italic asterisk", "an *emphasised* word", "an emphasised word"},
		{"bold", "a **strong** word", "a strong word"},
		{"underline", "an __underlined__ word", "an underlined word"},
		{"strikethrough", "a ~~struck~~ word", "a struck word"},
		{"inline code", "call `doThing()` now", "call doThing() now"},
		{"link", "see [docs](http://x/y) here", "see docs (http://x/y) here"},
		{"bold around identifier", "the **max_retries** field", "the max_retries field"},
		{"emphasis next to punctuation", "(_emphasised_)", "(emphasised)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripInlineMarkdown(tc.in); got != tc.want {
				t.Errorf("stripInlineMarkdown(%q)\n = %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// An indented code block is CommonMark's other code form, and models emit it
// constantly. Without support it falls through to the paragraph path, which
// space-joins its lines into one run-on line (archie-core-cvu6).
func TestMarkdownToBlocksIndentedCode(t *testing.T) {
	blocks := markdownToBlocks("Report:\n\n    type D struct {\n        Key string\n        Label string\n    }\n")
	var pre *models.InputRichBlock
	for i := range blocks {
		if blocks[i].Type == models.RichBlockTypePreformatted {
			pre = &blocks[i]
		}
	}
	if pre == nil {
		t.Fatalf("no preformatted block; got %v", blockTypes(blocks))
	}
	want := "type D struct {\n    Key string\n    Label string\n}"
	if got := blockText(*pre); got != want {
		t.Errorf("indented code text =\n%q\nwant\n%q", got, want)
	}
}

// An indented line cannot interrupt an open paragraph: CommonMark says so,
// and treating a wrapped prose line as code would be a worse failure than
// the one being fixed.
func TestMarkdownToBlocksIndentedLineDoesNotInterruptParagraph(t *testing.T) {
	blocks := markdownToBlocks("Some prose that wraps\n    onto an indented line.\n")
	for _, b := range blocks {
		if b.Type == models.RichBlockTypePreformatted {
			t.Fatalf("indented continuation became code; blocks = %v", blockTypes(blocks))
		}
	}
	if got := blockText(blocks[0]); got != "Some prose that wraps onto an indented line." {
		t.Errorf("paragraph = %q", got)
	}
}

// Code is verbatim by definition. Running the emphasis stripper over a fenced
// block corrupted pointer types and same-line snake_case pairs alike.
func TestMarkdownToBlocksFencedCodeIsVerbatim(t *testing.T) {
	body := "m := map[string]bool{\"allow_concurrent\": true, \"max_retries\": true}\nfunc f(tx *sql.Tx) *Result"
	blocks := markdownToBlocks("```go\n" + body + "\n```\n")
	var pre *models.InputRichBlock
	for i := range blocks {
		if blocks[i].Type == models.RichBlockTypePreformatted {
			pre = &blocks[i]
		}
	}
	if pre == nil {
		t.Fatalf("no preformatted block; got %v", blockTypes(blocks))
	}
	if got := blockText(*pre); got != body {
		t.Errorf("fenced code was altered:\n got %q\nwant %q", got, body)
	}
}
