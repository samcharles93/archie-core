package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultTurnBudgetChars is the default per-turn aggregate output budget
// in characters (200,000 chars ≈ 50K tokens at ~4 chars/token).
const DefaultTurnBudgetChars = 200_000

// SpillRef is a reference to a tool result that was spilled to disk because
// it exceeded the turn budget.
type SpillRef struct {
	// Path is the absolute path to the spill file on disk.
	Path string

	// SizeChars is the size of the spilled result in characters.
	SizeChars int
}

// TurnBudget tracks cumulative tool result sizes within a single turn.
// When the budget is exceeded the largest results are spilled to disk
// and replaced with spill references. A hard stop prevents further
// tool calls once the budget is exhausted.
//
// All methods are safe for concurrent use.
type TurnBudget struct {
	MaxChars int

	mu   sync.Mutex
	used int

	// spillDir is where oversized results are written. Empty means
	// spill-to-disk is disabled (results are truncated instead).
	spillDir string

	// spilled tracks all results that have been spilled to disk.
	spilled []SpillRef

	// exceeded is set to true when used >= MaxChars.
	exceeded bool
}

// NewTurnBudget creates a [TurnBudget] with the given character limit.
// If spillDir is non-empty, oversized results are written to that
// directory instead of being truncated inline.
func NewTurnBudget(maxChars int, spillDir string) *TurnBudget {
	return &TurnBudget{
		MaxChars: maxChars,
		spillDir: spillDir,
	}
}

// Consume attempts to allocate n characters against the budget.
// It returns true if the allocation succeeded (budget not yet exceeded).
// When the budget is exceeded on this call, exceeded is set and
// subsequent calls always return false.
func (b *TurnBudget) Consume(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.exceeded {
		return false
	}

	b.used += n
	if b.used > b.MaxChars {
		b.exceeded = true
		return false
	}
	return true
}

// Spill writes the given result content to disk and returns a [SpillRef].
// When spillDir is empty, the content is discarded and a SpillRef with an
// empty Path is returned (callers should treat this as "result truncated").
//
// The spill consumes len(content) characters from the budget regardless
// of whether a file was actually written.
func (b *TurnBudget) Spill(toolName string, content []byte) SpillRef {
	b.mu.Lock()
	defer b.mu.Unlock()

	sizeChars := len(content)
	ref := SpillRef{SizeChars: sizeChars}

	if b.spillDir != "" {
		path := filepath.Join(b.spillDir, fmt.Sprintf("spill-%s-%d", toolName, b.used))
		if err := os.WriteFile(path, content, 0o644); err == nil {
			ref.Path = path
		}
	}

	b.spilled = append(b.spilled, ref)
	b.used += sizeChars
	if b.used > b.MaxChars {
		b.exceeded = true
	}

	return ref
}

// Used reports the total characters consumed so far.
func (b *TurnBudget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Remaining reports how many characters are left in the budget.
// Returns 0 when the budget is exceeded.
func (b *TurnBudget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exceeded {
		return 0
	}
	remaining := b.MaxChars - b.used
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Exceeded reports whether the budget has been exceeded.
func (b *TurnBudget) Exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exceeded
}

// Spilled returns a snapshot of all spill references.
func (b *TurnBudget) Spilled() []SpillRef {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]SpillRef, len(b.spilled))
	copy(out, b.spilled)
	return out
}

// Reset clears all counters and spill tracking for the next turn.
func (b *TurnBudget) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used = 0
	b.spilled = nil
	b.exceeded = false
}
