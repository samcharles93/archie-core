package prreview

import "time"

// Budget bounds a pr-review run's total cost (tokens, the only spend the agent
// runtime accounts for) and wall-clock, split evenly across every
// budget-tracked phase. Zero disables that dimension.
type Budget struct {
	MaxTokens int
	WallClock time.Duration
}

// PhaseExhausted reports whether the run's spend exceeds the cumulative share
// allotted through phase (1-based) of phases.
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
