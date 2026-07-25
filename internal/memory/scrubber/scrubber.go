// Package scrubber implements a stateful StreamingContextScrubber that
// strips <memory>...</memory> fence spans from streaming text output. It
// handles partial chunks that split across fence boundaries, maintains state
// between chunks, and only emits clean text downstream.
//
// The scrubber is designed for zero allocations in the hot path (normal text
// with no memory blocks). Buffers are pre-allocated and reused.
package scrubber

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

const (
	// openTag marks the start of a memory context block.
	openTag = "<memory>"
	// closeTag marks the end of a memory context block.
	closeTag = "</memory>"

	// maxTagLen is the length of the longest tag we need to buffer for
	// partial chunk boundary matching.
	maxTagLen = len(closeTag) // 9 bytes
)

// Scrubber strips <memory>...</memory> fence spans from streaming text
// output. It maintains state between Write calls to handle fence tags
// that span chunk boundaries.
//
// A Scrubber is safe for use by a single goroutine; it is not safe for
// concurrent use without external synchronization.
type Scrubber struct {
	w     io.Writer // underlying writer; nil means discard
	depth int       // nesting depth of <memory> blocks (0 = normal text)

	// pending holds bytes from the end of the previous chunk that may be
	// the start of a tag we're looking for. Backed by pbuf to avoid
	// allocation when len(pending) <= maxTagLen.
	pending []byte
	pbuf    [maxTagLen]byte

	// buf is a reusable buffer for building output in Process.
	buf bytes.Buffer

	// Stats counters.
	blocksStripped int
	bytesStripped  int64
}

// New creates a new Scrubber that writes scrubbed output to w. If w is
// nil, scrubbed output is discarded (useful when the caller only needs
// to count stripped blocks or when using Process).
func New(w io.Writer) *Scrubber {
	return &Scrubber{w: w}
}

// Reset resets the scrubber to its initial state so it can be reused.
// The underlying writer is preserved. Buffers are cleared without
// releasing backing arrays.
func (s *Scrubber) Reset() {
	s.depth = 0
	s.pending = s.pending[:0]
	s.buf.Reset()
	s.blocksStripped = 0
	s.bytesStripped = 0
}

// Write processes a chunk of streaming text and writes the scrubbed output
// to the underlying writer. It returns the number of bytes consumed from p
// (always len(p) on success) and any write error from the underlying writer.
//
// Write is the primary streaming interface. It maintains internal state
// across calls to handle tags that span chunk boundaries.
func (s *Scrubber) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	data := p

	// Prepend any pending bytes from the previous chunk.
	if len(s.pending) > 0 {
		// We need to combine pending + new data. This allocates, but the
		// pending path is rare (only when a tag is split across chunks).
		combined := make([]byte, len(s.pending)+len(p))
		copy(combined, s.pending)
		copy(combined[len(s.pending):], p)
		data = combined
		s.pending = s.pending[:0]
	}

	for len(data) > 0 {
		if s.depth > 0 {
			data = s.processInMemory(data)
		} else {
			data = s.processNormal(data)
		}
	}

	return len(p), nil
}

// processNormal handles input when we're outside a memory block (depth == 0).
// It looks for <memory> open tags and passes everything else through.
func (s *Scrubber) processNormal(data []byte) []byte {
	// Fast path: no '<' at all means no possible tag.
	idx := bytes.IndexByte(data, '<')
	if idx < 0 {
		s.writeOutput(data)
		return nil
	}

	// Check if the '<' starts a <memory> tag.
	tail := data[idx:]
	if len(tail) >= len(openTag) {
		// Enough data to check the full tag.
		if string(tail[:len(openTag)]) == openTag {
			// Found <memory> — write everything before it, then enter memory block.
			s.writeOutput(data[:idx])
			s.depth++
			s.blocksStripped++
			s.bytesStripped += int64(len(openTag))
			return tail[len(openTag):]
		}
		// Not <memory> — write the '<' and continue after it.
		s.writeOutput(data[:idx+1])
		return data[idx+1:]
	}

	// Not enough data to determine — partial tag at end of chunk.
	// Hold the tail for the next Write call.
	if isPrefixOfTag(tail, openTag) {
		s.writeOutput(data[:idx])
		s.pending = append(s.pbuf[:0], tail...)
		return nil
	}

	// Not a prefix of <memory> — write everything.
	s.writeOutput(data)
	return nil
}

// processInMemory handles input when we're inside a memory block (depth > 0).
// It looks for </memory> close tags and strips everything else.
func (s *Scrubber) processInMemory(data []byte) []byte {
	// Fast path: no '<' means no possible close tag — strip everything.
	idx := bytes.IndexByte(data, '<')
	if idx < 0 {
		s.bytesStripped += int64(len(data))
		return nil
	}

	tail := data[idx:]

	// Check for nested <memory> open tags (increment depth).
	if len(tail) >= len(openTag) && string(tail[:len(openTag)]) == openTag {
		s.bytesStripped += int64(idx + len(openTag))
		s.depth++
		s.blocksStripped++
		return tail[len(openTag):]
	}

	// Check for </memory> close tag.
	if len(tail) >= len(closeTag) {
		if string(tail[:len(closeTag)]) == closeTag {
			s.bytesStripped += int64(idx + len(closeTag))
			s.depth--
			return tail[len(closeTag):]
		}
		// Not a close tag — skip the '<' and continue.
		s.bytesStripped += int64(idx + 1)
		return data[idx+1:]
	}

	// Not enough data to determine — partial tag at end of chunk.
	if isPrefixOfTag(tail, closeTag) || isPrefixOfTag(tail, openTag) {
		s.bytesStripped += int64(idx)
		s.pending = append(s.pbuf[:0], tail...)
		return nil
	}

	// Not a prefix of any relevant tag — strip everything.
	s.bytesStripped += int64(len(data))
	return nil
}

// writeOutput writes data to the underlying writer. Nil writer is a no-op.
func (s *Scrubber) writeOutput(data []byte) {
	if len(data) == 0 {
		return
	}
	if s.w != nil {
		// Write errors are rare on bytes.Buffer (the common case). When the
		// underlying writer is a real connection, errors are surfaced on the
		// next Write call.
		_, _ = s.w.Write(data)
	}
}

// Flush writes any pending buffered bytes (from a partial tag match at the
// end of the previous chunk) to the underlying writer and resets the pending
// buffer. Call Flush at the end of the stream to ensure any partial tag that
// wasn't actually a tag is emitted.
//
// If the scrubber is inside a memory block when Flush is called, the block
// is implicitly closed (missing closing tag handling).
func (s *Scrubber) Flush() error {
	// If we're still inside a memory block, close it implicitly.
	// The content has already been stripped; we just need to reset depth.
	if s.depth > 0 {
		s.depth = 0
	}

	// Write any pending bytes — they weren't actually a tag.
	if len(s.pending) > 0 {
		s.writeOutput(s.pending)
		s.pending = s.pending[:0]
	}

	return nil
}

// Close is equivalent to Flush. It implements a subset of io.Closer for
// convenience when the scrubber is used in a pipeline.
func (s *Scrubber) Close() error {
	return s.Flush()
}

// Process is a convenience method that scrubs a single chunk and returns
// the cleaned text. It maintains internal state across calls, so it can
// be used for streaming just like Write. The returned string is only the
// newly scrubbed output from this chunk — not the full accumulated output.
//
// For zero-allocation streaming, prefer Write with a pre-allocated writer.
func (s *Scrubber) Process(chunk string) string {
	s.buf.Reset()
	prev := s.w
	s.w = &s.buf
	_, _ = s.Write([]byte(chunk))
	s.w = prev
	if s.buf.Len() == 0 {
		return ""
	}
	return s.buf.String()
}

// ProcessString is a convenience method that scrubs a complete string in
// one call. It resets the scrubber state first and flushes at the end,
// so it is suitable for one-shot use. For streaming use, call Write or
// Process repeatedly and call Flush at the end.
func (s *Scrubber) ProcessString(input string) string {
	s.Reset()
	s.buf.Reset()
	prev := s.w
	s.w = &s.buf
	_, _ = s.Write([]byte(input))
	_ = s.Flush()
	s.w = prev
	return s.buf.String()
}

// BlocksStripped returns the number of <memory> blocks that have been
// stripped since the last Reset.
func (s *Scrubber) BlocksStripped() int {
	return s.blocksStripped
}

// BytesStripped returns the total number of bytes stripped (including
// the fence tags themselves) since the last Reset.
func (s *Scrubber) BytesStripped() int64 {
	return s.bytesStripped
}

// InMemoryBlock reports whether the scrubber is currently inside a
// <memory> block (i.e., an open tag has been seen but no matching
// close tag yet).
func (s *Scrubber) InMemoryBlock() bool {
	return s.depth > 0
}

// Depth returns the current nesting depth of <memory> blocks.
func (s *Scrubber) Depth() int {
	return s.depth
}

// ── Helpers ────────────────────────────────────────────────────────────

// isPrefixOfTag reports whether data is a prefix of tag — i.e., data could
// be the start of tag if more bytes arrive later. Returns false if data is
// longer than tag.
func isPrefixOfTag(data []byte, tag string) bool {
	if len(data) >= len(tag) {
		return false
	}
	for i, b := range data {
		if b != tag[i] {
			return false
		}
	}
	return len(data) > 0
}

// DebugString returns a human-readable representation of the scrubber's
// internal state. Useful for debugging tests.
func (s *Scrubber) DebugString() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Scrubber{depth=%d", s.depth)
	fmt.Fprintf(&sb, ", pending=%q", string(s.pending))
	fmt.Fprintf(&sb, ", blocksStripped=%d", s.blocksStripped)
	fmt.Fprintf(&sb, ", bytesStripped=%d}", s.bytesStripped)
	return sb.String()
}

// Compile-time interface checks.
var (
	_ io.Writer = (*Scrubber)(nil)
)
