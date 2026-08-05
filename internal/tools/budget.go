package tools

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
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

	ref := b.writeSpillLocked(toolName, content)
	b.used += ref.SizeChars
	if b.used > b.MaxChars {
		b.exceeded = true
	}
	return ref
}

// WriteSpill writes content to the spill directory and records the reference
// without charging the budget.
//
// It exists so that displacing a result to disk and billing for it are separate
// decisions. A caller that hands the model a short spill reference has shown it
// only those few characters and should be charged for those; charging the full
// displaced size as well bills a turn twice for one result and exhausts it
// early. [CapPayload] uses this; the batch-dispatch path uses [TurnBudget.Spill],
// which still charges, because it accounts for the result separately.
func (b *TurnBudget) WriteSpill(toolName string, content []byte) SpillRef {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeSpillLocked(toolName, content)
}

// writeSpillLocked performs the write. Must be called with b.mu held.
func (b *TurnBudget) writeSpillLocked(toolName string, content []byte) SpillRef {
	ref := SpillRef{SizeChars: len(content)}

	if b.spillDir != "" {
		ref.Path = writeSpillFile(b.spillDir, toolName, content)
	}

	b.spilled = append(b.spilled, ref)
	return ref
}

// writeSpillFile writes content to a fresh file in dir, returning its path or
// "" when the write fails. A failure is not an error here: the caller falls
// back to inline truncation, which is degraded but correct.
//
// The name comes from CreateTemp rather than from budget state. A budget is
// minted per turn and every turn shares one directory, so any within-budget
// counter restarts at zero each turn and the second turn would overwrite the
// first turn's file, handing one session a reference that resolves to another
// session's output.
//
// 0600 because this is verbatim tool output -- shell stdout, file contents, MCP
// responses. It is the first place archie persists that material and it can
// hold anything the tools could read.
func writeSpillFile(dir, toolName string, content []byte) string {
	f, err := os.CreateTemp(dir, "spill-"+sanitiseToolName(toolName)+"-*")
	if err != nil {
		return ""
	}
	defer f.Close()

	if err := f.Chmod(0o600); err != nil {
		return ""
	}
	if _, err := f.Write(content); err != nil {
		return ""
	}
	return f.Name()
}

// sanitiseToolName strips path separators and other characters that must not
// reach a filename. MCP tool names are supplied by a third-party server, so a
// name like "../../etc/passwd" is reachable input rather than a hypothetical.
func sanitiseToolName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

// CapPayload bounds one tool result to max bytes before it reaches the model.
// It is the single definition of that rule: [enforcePerToolLimit] applies it on
// the batch-dispatch path, and the agent tool set applies it to the marshalled
// payload it hands back to the model.
//
// When budget has a spill directory the whole result is written there and the
// model is given the path, so an oversized result is displaced rather than
// lost. Without one -- or when the write fails -- the result is truncated
// inline on a rune boundary.
//
// Either way the return value carries a notice naming the tool and the full
// size. Shortening a result silently is how a model concludes a file is empty
// or a search found nothing, so the fact of truncation is never implicit.
//
// max <= 0 disables capping.
func CapPayload(toolName, payload string, limit int, budget *TurnBudget) string {
	if limit <= 0 || len(payload) <= limit {
		return payload
	}

	if budget != nil {
		// WriteSpill, not Spill: the caller charges for what it actually
		// hands the model, which is the short reference below, not the
		// displaced payload.
		if ref := budget.WriteSpill(toolName, []byte(payload)); ref.Path != "" {
			return fmt.Sprintf(
				"[%s: result spilled to %s (%d characters); too large to inline]",
				toolName, ref.Path, ref.SizeChars,
			)
		}
		// Spill was disabled or failed -- fall through to inline truncation.
	}

	return truncateAtRuneBoundary(payload, limit) + fmt.Sprintf(
		"\n\n[%s: result truncated, showing the first %d of %d characters]",
		toolName, limit, len(payload),
	)
}

// truncateAtRuneBoundary cuts s to at most max bytes without splitting a
// multi-byte rune. A half rune is invalid UTF-8: some providers reject the
// request outright and others render a replacement character mid-word, so the
// cut always retreats to a boundary even when that costs a few bytes.
func truncateAtRuneBoundary(s string, limit int) string {
	if limit >= len(s) {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
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
