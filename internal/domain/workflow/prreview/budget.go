package prreview

import "time"

// Budget bounds a pr-review run's total cost (tokens, the only spend the
// agent runtime accounts for) and wall-clock, split evenly across every
// budget-tracked phase (docs/prds/pr-review-agent.md, Budget: "A run has a
// cost cap and a wall-clock cap, split into per-phase shares"). Zero
// disables that dimension.
type Budget struct {
	MaxTokens int
	WallClock time.Duration
}

// PhaseExhausted reports whether phase (1-based, counting the phase about to
// start) has already spent its share, given how much of the run's total
// budget every phase up to and including it is allotted out of phases total.
//
// The check is against the cumulative allotment through phase, not phase's
// own isolated slice: an earlier phase's underspend is not wasted, but its
// overspend still eats into a later phase's share. That is what "a phase
// whose share is spent is skipped" means for a phase that has not started
// yet and so has spent nothing of its own -- the run's spend so far is
// compared against how much the whole run should have spent by this point.
func (b Budget) PhaseExhausted(phase, phases, tokensSpent int, elapsed time.Duration) bool {
	if phase <= 0 || phases <= 0 {
		return false
	}
	if b.MaxTokens > 0 && tokensSpent >= b.MaxTokens*phase/phases {
		return true
	}
	if b.WallClock > 0 && elapsed >= b.WallClock*time.Duration(phase)/time.Duration(phases) {
		return true
	}
	return false
}
