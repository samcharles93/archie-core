package scrubber

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// ── Basic functionality ─────────────────────────────────────────────────

func TestScrubber_NoMemoryBlock(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("Hello, world!")
	if result != "Hello, world!" {
		t.Errorf("got %q, want %q", result, "Hello, world!")
	}
	if s.BlocksStripped() != 0 {
		t.Errorf("BlocksStripped = %d, want 0", s.BlocksStripped())
	}
}

func TestScrubber_SingleMemoryBlock(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("Before <memory>secret</memory> After")
	if result != "Before  After" {
		t.Errorf("got %q, want %q", result, "Before  After")
	}
	if s.BlocksStripped() != 1 {
		t.Errorf("BlocksStripped = %d, want 1", s.BlocksStripped())
	}
}

func TestScrubber_MultipleMemoryBlocks(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("A <memory>x</memory> B <memory>y</memory> C")
	if result != "A  B  C" {
		t.Errorf("got %q, want %q", result, "A  B  C")
	}
	if s.BlocksStripped() != 2 {
		t.Errorf("BlocksStripped = %d, want 2", s.BlocksStripped())
	}
}

func TestScrubber_EmptyContent(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("")
	if result != "" {
		t.Errorf("got %q, want %q", result, "")
	}
}

func TestScrubber_OnlyOpenTag(t *testing.T) {
	// Missing close tag  --  block is implicitly closed at end of stream.
	s := New(nil)
	result := s.ProcessString("Before <memory>stuff")
	if result != "Before " {
		t.Errorf("got %q, want %q", result, "Before ")
	}
}

func TestScrubber_OnlyCloseTag(t *testing.T) {
	// Stray close tag with no open  --  treated as literal text.
	s := New(nil)
	result := s.ProcessString("text </memory> more")
	if result != "text </memory> more" {
		t.Errorf("got %q, want %q", result, "text </memory> more")
	}
}

func TestScrubber_EmptyMemoryBlock(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("before<memory></memory>after")
	if result != "beforeafter" {
		t.Errorf("got %q, want %q", result, "beforeafter")
	}
}

func TestScrubber_OnlyMemoryBlock(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("<memory>everything</memory>")
	if result != "" {
		t.Errorf("got %q, want %q", result, "")
	}
}

// ── Nested tags ─────────────────────────────────────────────────────────

func TestScrubber_NestedMemoryBlocks(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("A <memory>outer <memory>inner</memory> back</memory> B")
	if result != "A  B" {
		t.Errorf("got %q, want %q", result, "A  B")
	}
	if s.BlocksStripped() != 2 {
		t.Errorf("BlocksStripped = %d, want 2", s.BlocksStripped())
	}
}

func TestScrubber_DeeplyNestedMemoryBlocks(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("<memory>1<memory>2<memory>3</memory>2</memory>1</memory>")
	if result != "" {
		t.Errorf("got %q, want %q", result, "")
	}
	if s.BlocksStripped() != 3 {
		t.Errorf("BlocksStripped = %d, want 3", s.BlocksStripped())
	}
}

func TestScrubber_UnbalancedNested_MoreOpens(t *testing.T) {
	// More open tags than close tags  --  remaining blocks implicitly closed
	// at end of stream. Everything after the first <memory> is stripped
	// because the outer block never closes.
	s := New(nil)
	result := s.ProcessString("A <memory>outer <memory>inner</memory> B")
	if result != "A " {
		t.Errorf("got %q, want %q", result, "A ")
	}
}

func TestScrubber_UnbalancedNested_MoreCloses(t *testing.T) {
	// Extra close tags are literal text.
	s := New(nil)
	result := s.ProcessString("<memory>a</memory></memory>b")
	if result != "</memory>b" {
		t.Errorf("got %q, want %q", result, "</memory>b")
	}
}

// ── Partial chunk handling ──────────────────────────────────────────────

func TestScrubber_PartialOpenTag_SplitAtAngle(t *testing.T) {
	// Open tag split right after '<'
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("Hello <"))
	s.Write([]byte("memory>secret</memory> World"))

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	result := buf.String()
	if result != "Hello  World" {
		t.Errorf("got %q, want %q", result, "Hello  World")
	}
}

func TestScrubber_PartialOpenTag_SplitMidTag(t *testing.T) {
	tests := []struct {
		name     string
		chunk1   string
		chunk2   string
		expected string
	}{
		{name: "split after <m", chunk1: "Hi <m", chunk2: "emory>secret</memory> Bye", expected: "Hi  Bye"},
		{name: "split after <me", chunk1: "Hi <me", chunk2: "mory>secret</memory> Bye", expected: "Hi  Bye"},
		{name: "split after <mem", chunk1: "Hi <mem", chunk2: "ory>secret</memory> Bye", expected: "Hi  Bye"},
		{name: "split after <memo", chunk1: "Hi <memo", chunk2: "ry>secret</memory> Bye", expected: "Hi  Bye"},
		{name: "split after <memor", chunk1: "Hi <memor", chunk2: "y>secret</memory> Bye", expected: "Hi  Bye"},
		{name: "split after <memory", chunk1: "Hi <memory", chunk2: ">secret</memory> Bye", expected: "Hi  Bye"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := New(&buf)

			s.Write([]byte(tt.chunk1))
			s.Write([]byte(tt.chunk2))

			if err := s.Flush(); err != nil {
				t.Fatalf("Flush error: %v", err)
			}

			if got := buf.String(); got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestScrubber_PartialCloseTag_SplitMidTag(t *testing.T) {
	tests := []struct {
		name     string
		chunk1   string
		chunk2   string
		expected string
	}{
		{name: "close split after <", chunk1: "AB <memory>secret <", chunk2: "/memory> CD", expected: "AB  CD"},
		{name: "close split after </", chunk1: "AB <memory>secret </", chunk2: "memory> CD", expected: "AB  CD"},
		{name: "close split after </m", chunk1: "AB <memory>secret </m", chunk2: "emory> CD", expected: "AB  CD"},
		{name: "close split after </me", chunk1: "AB <memory>secret </me", chunk2: "mory> CD", expected: "AB  CD"},
		{name: "close split after </mem", chunk1: "AB <memory>secret </mem", chunk2: "ory> CD", expected: "AB  CD"},
		{name: "close split after </memo", chunk1: "AB <memory>secret </memo", chunk2: "ry> CD", expected: "AB  CD"},
		{name: "close split after </memor", chunk1: "AB <memory>secret </memor", chunk2: "y> CD", expected: "AB  CD"},
		{name: "close split after </memory", chunk1: "AB <memory>secret </memory", chunk2: "> CD", expected: "AB  CD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := New(&buf)

			s.Write([]byte(tt.chunk1))
			s.Write([]byte(tt.chunk2))

			if err := s.Flush(); err != nil {
				t.Fatalf("Flush error: %v", err)
			}

			if got := buf.String(); got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestScrubber_PartialTag_NotActuallyATag(t *testing.T) {
	tests := []struct {
		name     string
		chunk1   string
		chunk2   string
		expected string
	}{
		{
			name: "literal < followed by x", chunk1: "abc <", chunk2: "x not a tag> def",
			expected: "abc <x not a tag> def",
		},
		{
			name: "literal <m followed by x", chunk1: "abc <m", chunk2: "x not a tag> def",
			expected: "abc <mx not a tag> def",
		},
		{
			name: "literal </ followed by x", chunk1: "abc </", chunk2: "x not a tag> def",
			expected: "abc </x not a tag> def",
		},
		{
			name:   "partial in memory block not a close tag",
			chunk1: "<memory>content </", chunk2: "notmemory> more</memory>",
			// "</notmemory>" is not "</memory>"  --  it doesn't close the block.
			// Everything inside the memory block is stripped until the real
			// </memory> is found.
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := New(&buf)

			s.Write([]byte(tt.chunk1))
			s.Write([]byte(tt.chunk2))

			if err := s.Flush(); err != nil {
				t.Fatalf("Flush error: %v", err)
			}

			if got := buf.String(); got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestScrubber_LiteralAngleBrackets(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("if (x < 5 && y > 3) { doStuff(); }")
	if result != "if (x < 5 && y > 3) { doStuff(); }" {
		t.Errorf("got %q, want %q", result, "if (x < 5 && y > 3) { doStuff(); }")
	}
}

func TestScrubber_LiteralLtInMemoryBlock(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("before <memory>if (x < 5) { }</memory> after")
	if result != "before  after" {
		t.Errorf("got %q, want %q", result, "before  after")
	}
}

// ── Chunk boundary edge cases ──────────────────────────────────────────

func TestScrubber_OpenTagAtExactChunkBoundary(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("Hello "))
	s.Write([]byte("<memory>secret</memory>"))
	s.Write([]byte(" World"))

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	if got := buf.String(); got != "Hello  World" {
		t.Errorf("got %q, want %q", got, "Hello  World")
	}
}

func TestScrubber_CloseTagAtExactChunkBoundary(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("Hello <memory>secret"))
	s.Write([]byte("</memory> World"))

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	if got := buf.String(); got != "Hello  World" {
		t.Errorf("got %q, want %q", got, "Hello  World")
	}
}

func TestScrubber_SingleByteChunks(t *testing.T) {
	input := "abc<memory>def</memory>ghi"
	var buf bytes.Buffer
	s := New(&buf)

	for i := 0; i < len(input); i++ {
		s.Write([]byte{input[i]})
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	if got := buf.String(); got != "abcghi" {
		t.Errorf("got %q, want %q", got, "abcghi")
	}
}

func TestScrubber_TwoByteChunks(t *testing.T) {
	input := "a<memory>b</memory>c"
	var buf bytes.Buffer
	s := New(&buf)

	for i := 0; i < len(input); i += 2 {
		end := min(i+2, len(input))
		s.Write([]byte(input[i:end]))
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	if got := buf.String(); got != "ac" {
		t.Errorf("got %q, want %q", got, "ac")
	}
}

func TestScrubber_ChunkEndingWithOpenTagStart(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("text <"))
	s.Write([]byte("notmemory> more"))

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	if got := buf.String(); got != "text <notmemory> more" {
		t.Errorf("got %q, want %q", got, "text <notmemory> more")
	}
}

// ── Streaming Write interface ──────────────────────────────────────────

func TestScrubber_Write_ReturnsBytesConsumed(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	input := []byte("hello")
	n, err := s.Write(input)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write returned n=%d, want %d", n, len(input))
	}
}

func TestScrubber_Write_EmptyChunk(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	n, err := s.Write(nil)
	if err != nil {
		t.Fatalf("Write(nil) error: %v", err)
	}
	if n != 0 {
		t.Errorf("Write(nil) returned n=%d, want 0", n)
	}
}

func TestScrubber_Flush_EmptyState(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	// Flush with no writes should be a no-op.
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("buf has %d bytes after empty flush, want 0", buf.Len())
	}
}

// ── Process / ProcessString ────────────────────────────────────────────

func TestScrubber_Process_StreamingEquivalence(t *testing.T) {
	input := "A <memory>x</memory> B <memory>y</memory> C"

	// ProcessString (one-shot)
	s1 := New(nil)
	oneShot := s1.ProcessString(input)

	// Process with chunking (streaming)
	s2 := New(nil)
	var streamingResult strings.Builder
	chunks := []string{"A <mem", "ory>x</mem", "ory> B <memory>", "y</memory> C"}
	for _, ch := range chunks {
		streamingResult.WriteString(s2.Process(ch))
	}
	s2.Flush()
	streamingResult.WriteString(s2.Process("")) // drain any final output

	if streamingResult.String() != oneShot {
		t.Errorf("streaming result %q != one-shot %q", streamingResult.String(), oneShot)
	}
}

func TestScrubber_Process_ReturnsOnlyNewOutput(t *testing.T) {
	s := New(nil)
	r1 := s.Process("hello <memo")
	r2 := s.Process("ry>secret</memory> world")
	r3 := s.Process("")

	if r1 != "hello " {
		t.Errorf("first Process = %q, want %q", r1, "hello ")
	}
	if r2 != " world" {
		t.Errorf("second Process = %q, want %q", r2, " world")
	}
	if r3 != "" {
		t.Errorf("third Process = %q, want %q", r3, "")
	}
}

func TestScrubber_ProcessString_CompleteInput(t *testing.T) {
	s := New(nil)

	r1 := s.ProcessString("A <memory>x</memory> B")
	r2 := s.ProcessString("C <memory>y</memory> D")

	if r1 != "A  B" {
		t.Errorf("first = %q, want %q", r1, "A  B")
	}
	if r2 != "C  D" {
		t.Errorf("second = %q, want %q", r2, "C  D")
	}
}

// ── Reset ──────────────────────────────────────────────────────────────

func TestScrubber_Reset_MidStream(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("A <memory>secret"))
	if s.InMemoryBlock() == false {
		t.Error("expected to be in memory block")
	}

	s.Reset()
	buf.Reset()

	if s.InMemoryBlock() {
		t.Error("after Reset, should not be in memory block")
	}
	if s.BlocksStripped() != 0 {
		t.Errorf("BlocksStripped = %d after Reset, want 0", s.BlocksStripped())
	}

	s.Write([]byte("clean <memory>gone</memory> text"))
	s.Flush()

	if got := buf.String(); got != "clean  text" {
		t.Errorf("after Reset: got %q, want %q", got, "clean  text")
	}
}

// ── Stats tracking ─────────────────────────────────────────────────────

func TestScrubber_Stats(t *testing.T) {
	s := New(nil)
	s.ProcessString("x <memory>abcdef</memory> y <memory>ghijk</memory> z")

	if s.BlocksStripped() != 2 {
		t.Errorf("BlocksStripped = %d, want 2", s.BlocksStripped())
	}
	// Tags: <memory> = 8, </memory> = 9 each → 17*2 = 34
	// Content: "abcdef" = 6, "ghijk" = 5 → 11
	// Total stripped: 45
	expectedBytes := int64(45)
	if s.BytesStripped() != expectedBytes {
		t.Errorf("BytesStripped = %d, want %d", s.BytesStripped(), expectedBytes)
	}
}

func TestScrubber_Stats_Reset(t *testing.T) {
	s := New(nil)
	s.ProcessString("<memory>a</memory>")

	if s.BlocksStripped() == 0 {
		t.Fatal("expected blocks to be stripped")
	}

	s.Reset()

	if s.BlocksStripped() != 0 {
		t.Errorf("BlocksStripped = %d after Reset, want 0", s.BlocksStripped())
	}
	if s.BytesStripped() != 0 {
		t.Errorf("BytesStripped = %d after Reset, want 0", s.BytesStripped())
	}
}

// ── InMemoryBlock / Depth ──────────────────────────────────────────────

func TestScrubber_InMemoryBlock(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	if s.InMemoryBlock() {
		t.Error("InMemoryBlock should be false initially")
	}

	s.Write([]byte("before <memory>"))
	if !s.InMemoryBlock() {
		t.Error("InMemoryBlock should be true after open tag")
	}

	s.Write([]byte("content</memory> after"))
	if s.InMemoryBlock() {
		t.Error("InMemoryBlock should be false after close tag")
	}
}

func TestScrubber_Depth(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	if s.Depth() != 0 {
		t.Errorf("Depth = %d initially, want 0", s.Depth())
	}

	s.Write([]byte("<memory>"))
	if s.Depth() != 1 {
		t.Errorf("Depth = %d after first open, want 1", s.Depth())
	}

	s.Write([]byte("<memory>"))
	if s.Depth() != 2 {
		t.Errorf("Depth = %d after second open, want 2", s.Depth())
	}

	s.Write([]byte("</memory>"))
	if s.Depth() != 1 {
		t.Errorf("Depth = %d after first close, want 1", s.Depth())
	}

	s.Write([]byte("</memory>"))
	if s.Depth() != 0 {
		t.Errorf("Depth = %d after second close, want 0", s.Depth())
	}
}

// ── Nil writer ─────────────────────────────────────────────────────────

func TestScrubber_NilWriter(t *testing.T) {
	s := New(nil)

	input := []byte("some <memory>hidden</memory> text")
	n, err := s.Write(input)
	if err != nil {
		t.Fatalf("Write error with nil writer: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write returned n=%d, want %d", n, len(input))
	}

	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error with nil writer: %v", err)
	}
}

// ── IO Writer integration ──────────────────────────────────────────────

func TestScrubber_IOWriter_Chain(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	_, err := io.WriteString(s, "Hello <memory>hidden</memory> World")
	if err != nil {
		t.Fatalf("io.WriteString error: %v", err)
	}
	s.Flush()

	if got := buf.String(); got != "Hello  World" {
		t.Errorf("got %q, want %q", got, "Hello  World")
	}
}

// ── Complex scenarios ──────────────────────────────────────────────────

func TestScrubber_MemoryBlockAcrossManyChunks(t *testing.T) {
	content := "This is a very long memory block with lots of content " +
		"that should be stripped. " + strings.Repeat("blah ", 200)
	input := "visible_start<memory>" + content + "</memory>visible_end"

	var buf bytes.Buffer
	s := New(&buf)

	for i := 0; i < len(input); i += 3 {
		end := min(i+3, len(input))
		s.Write([]byte(input[i:end]))
	}
	s.Flush()

	expected := "visible_startvisible_end"
	if got := buf.String(); got != expected {
		t.Errorf("got %q (len=%d), want %q (len=%d)", got, len(got), expected, len(expected))
	}
}

func TestScrubber_ConsecutivePartialTags(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("a <"))    // partial
	s.Write([]byte("m"))      // still partial
	s.Write([]byte("emory>")) // complete!
	s.Write([]byte("hidden"))
	s.Write([]byte("</"))      // partial close
	s.Write([]byte("memory>")) // complete close
	s.Write([]byte("b"))
	s.Flush()

	if got := buf.String(); got != "a b" {
		t.Errorf("got %q, want %q", got, "a b")
	}
}

func TestScrubber_MemoryLikeButNotExactly(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "memory without angle brackets", input: "memory and /memory are not tags", want: "memory and /memory are not tags"},
		{name: "angle bracket but wrong tag name", input: "use <section>content</section> here", want: "use <section>content</section> here"},
		{name: "unclosed angle bracket", input: "the <memory tag without close", want: "the <memory tag without close"},
		{name: "close tag without open in normal mode", input: "text </memory> more text", want: "text </memory> more text"},
		{name: "back-to-back memory blocks", input: "A<memory>x</memory><memory>y</memory>B", want: "AB"},
		{name: "memory block at start", input: "<memory>x</memory>B", want: "B"},
		{name: "memory block at end", input: "A<memory>x</memory>", want: "A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(nil)
			got := s.ProcessString(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScrubber_MalformedNested(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "interleaved close tags", input: "<memory>a</memory></memory>b", want: "</memory>b"},
		{name: "open after close", input: "</memory><memory>b</memory>", want: "</memory>"},
		{name: "deeply nested with missing close", input: "A<memory>B<memory>C<memory>D", want: "A"},
		{name: "partial nest resolution", input: "A<memory>B<memory>C</memory>D</memory>E", want: "AE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(nil)
			got := s.ProcessString(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ── UTF-8 / multibyte handling ─────────────────────────────────────────

func TestScrubber_UnicodeContent(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("BEFORE <memory>こんにちは世界</memory> AFTER")
	if result != "BEFORE  AFTER" {
		t.Errorf("got %q, want %q", result, "BEFORE  AFTER")
	}
}

func TestScrubber_UnicodeOutsideMemoryBlock(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("👋 hello <memory>secret</memory> 🌍 world")
	if result != "👋 hello  🌍 world" {
		t.Errorf("got %q, want %q", result, "👋 hello  🌍 world")
	}
}

func TestScrubber_UnicodeSplitAcrossChunks(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("Hi <m"))
	s.Write([]byte("emory>")) // completes the open tag
	s.Write([]byte("世界</memory> Bye"))

	s.Flush()

	if got := buf.String(); got != "Hi  Bye" {
		t.Errorf("got %q, want %q", got, "Hi  Bye")
	}
}

// ── Large input ─────────────────────────────────────────────────────────

func TestScrubber_LargeInput(t *testing.T) {
	var input strings.Builder
	var expected strings.Builder

	input.WriteString("START ")
	expected.WriteString("START ")

	for i := range 100 {
		fmt.Fprintf(&input, " visible_%d ", i)
		fmt.Fprintf(&expected, " visible_%d ", i)

		input.WriteString("<memory>")
		fmt.Fprintf(&input, " hidden_block_%d ", i)
		input.WriteString("</memory>")
	}

	input.WriteString(" END")
	expected.WriteString(" END")

	s := New(nil)
	got := s.ProcessString(input.String())
	want := expected.String()

	if got != want {
		t.Errorf("large output mismatch (got len=%d, want len=%d)", len(got), len(want))
	}
}

// ── Regression: edge cases ─────────────────────────────────────────────

func TestScrubber_Regression_DoubleChevron(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("a <<memory>hidden</memory> b")
	if result != "a < b" {
		t.Errorf("got %q, want %q", result, "a < b")
	}
}

func TestScrubber_Regression_TagInTag(t *testing.T) {
	s := New(nil)
	result := s.ProcessString("x <memory<memory>>test</memory> y")
	if result != "x <memory y" {
		t.Errorf("got %q, want %q", result, "x <memory y")
	}
}

// ── Adversarial review findings ────────────────────────────────────────

func TestScrubber_Flush_PendingInMemoryBlock_NotEmitted(t *testing.T) {
	// Finding 1: Flush must NOT emit pending bytes when inside a memory block.
	// Pending bytes inside a memory block are content that should be stripped.
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "pending < inside memory block", input: "<memory>abc<", expected: ""},
		{name: "pending < only inside memory block", input: "<memory><", expected: ""},
		{name: "pending </ inside memory block", input: "<memory>abc</", expected: ""},
		{name: "pending </m inside memory block", input: "<memory>abc</m", expected: ""},
		{name: "text before block and pending inside", input: "hi<memory>abc<", expected: "hi"},
		{name: "nested with pending inside", input: "A<memory>B<memory>C<", expected: "A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := New(&buf)
			s.Write([]byte(tt.input))
			if err := s.Flush(); err != nil {
				t.Fatalf("Flush error: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestScrubber_Flush_PendingInMemoryBlock_StatsCorrect(t *testing.T) {
	// Finding 3: BytesStripped should count pending bytes that are stripped
	// on Flush inside a memory block.
	var buf bytes.Buffer
	s := New(&buf)
	s.Write([]byte("<memory>abc<"))
	// At this point: bytesStripped = 8 (<memory>) + 3 (abc) = 11, pending = "<"
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	// After Flush: pending "<" should be counted as stripped
	// Total: 8 + 3 + 1 = 12
	expectedBytes := int64(12)
	if s.BytesStripped() != expectedBytes {
		t.Errorf("BytesStripped = %d, want %d", s.BytesStripped(), expectedBytes)
	}
	// No output should be emitted
	if buf.Len() != 0 {
		t.Errorf("output = %q, want empty", buf.String())
	}
}

func TestScrubber_Flush_PendingInMemoryBlock_TwoChunks(t *testing.T) {
	// Finding 1 two-chunk variant: first chunk ends with pending inside
	// memory block, second chunk is empty.
	var buf bytes.Buffer
	s := New(&buf)
	s.Write([]byte("<memory>xyz<"))
	s.Write([]byte("")) // empty chunk
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("got %q, want %q", got, "")
	}
	if s.BytesStripped() == 0 {
		t.Error("BytesStripped should include both the open tag and the stripped content")
	}
}

func TestScrubber_Flush_PendingOutsideMemoryBlock(t *testing.T) {
	// Pending bytes OUTSIDE a memory block should still be emitted.
	// This is the normal case for a partial tag that turns out not to be a tag.
	var buf bytes.Buffer
	s := New(&buf)
	s.Write([]byte("hello <m"))
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if got := buf.String(); got != "hello <m" {
		t.Errorf("got %q, want %q", got, "hello <m")
	}
}

func TestScrubber_Flush_SingleByteChunkIntoMemoryBlock(t *testing.T) {
	// Test a specific sequence: open tag crosses a chunk boundary,
	// then content appears, then chunk ends with pending.
	var buf bytes.Buffer
	s := New(&buf)

	s.Write([]byte("A <mem"))
	s.Write([]byte("ory>xyz<"))
	s.Flush()

	if got := buf.String(); got != "A " {
		t.Errorf("got %q, want %q", got, "A ")
	}
	// bytesStripped: open tag (8) + "xyz" (3) + pending "<" (1) = 12
	if s.BytesStripped() != 12 {
		t.Errorf("BytesStripped = %d, want 12", s.BytesStripped())
	}
}

func TestScrubber_Write_ZeroLengthChunkWithPending(t *testing.T) {
	// Zero-length write while pending is non-empty should leave pending
	// untouched (no crash, no side effects).
	var buf bytes.Buffer
	s := New(&buf)
	// Write text ending with a partial tag prefix.
	s.Write([]byte("data <m"))
	if len(s.pending) == 0 {
		t.Error("pending should not be empty after partial tag")
	}
	s.Write([]byte("")) // zero-length write  --  no-op
	if len(s.pending) == 0 {
		t.Error("pending should not be cleared by zero-length Write")
	}
	// Now write text that does NOT complete the tag: "<m" + "x..." = "<mx..."
	// which is NOT "<memory>" so it should pass through.
	s.Write([]byte("x abc"))
	s.Flush()
	if got := buf.String(); got != "data <mx abc" {
		t.Errorf("got %q, want %q", got, "data <mx abc")
	}
}

// ── Compile-time checks ────────────────────────────────────────────────

func TestScrubber_SatisfiesIOWriter(t *testing.T) {
	var _ io.Writer = New(nil)
}

// ── DebugString ────────────────────────────────────────────────────────

func TestScrubber_DebugString(t *testing.T) {
	s := New(nil)
	s.Write([]byte("<memory>test"))

	ds := s.DebugString()
	if !strings.Contains(ds, "depth=1") {
		t.Errorf("DebugString should contain depth=1: %s", ds)
	}
}

// ── Benchmarks ─────────────────────────────────────────────────────────

func BenchmarkScrubber_NoMemoryBlock(b *testing.B) {
	s := New(io.Discard)
	input := []byte("This is normal text without any memory blocks at all.")
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s.Write(input)
	}
	s.Flush()
}

func BenchmarkScrubber_SingleMemoryBlock(b *testing.B) {
	s := New(io.Discard)
	input := []byte("Before <memory>hidden content here</memory> After")
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s.Reset()
		s.Write(input)
		s.Flush()
	}
}

func BenchmarkScrubber_ManySmallChunks(b *testing.B) {
	text := "This is a long text <memory>with some hidden content that should be stripped</memory> and more text here"
	var chunks [][]byte
	for i := 0; i < len(text); i += 3 {
		end := min(i+3, len(text))
		chunks = append(chunks, []byte(text[i:end]))
	}

	s := New(io.Discard)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s.Reset()
		for _, ch := range chunks {
			s.Write(ch)
		}
		s.Flush()
	}
}

func BenchmarkScrubber_ProcessString(b *testing.B) {
	s := New(nil)
	input := "Before <memory>hidden content here</memory> After"
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s.ProcessString(input)
	}
}

func BenchmarkScrubber_LargeInputManyBlocks(b *testing.B) {
	var input strings.Builder
	for i := range 100 {
		fmt.Fprintf(&input, "visible_%d ", i)
		input.WriteString("<memory>")
		fmt.Fprintf(&input, "hidden_%d ", i)
		input.WriteString("</memory>")
	}
	data := []byte(input.String())

	s := New(io.Discard)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s.Reset()
		s.Write(data)
		s.Flush()
	}
}

func BenchmarkScrubber_NestedBlocks(b *testing.B) {
	s := New(io.Discard)
	input := []byte("A <memory>B <memory>C <memory>D</memory>E</memory>F</memory>G")
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		s.Reset()
		s.Write(input)
		s.Flush()
	}
}
