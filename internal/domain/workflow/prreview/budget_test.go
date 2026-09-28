package prreview

import (
	"testing"
	"time"
)

func TestBudgetPhaseExhausted(t *testing.T) {
	cases := []struct {
		name        string
		budget      Budget
		phase       int
		phases      int
		tokensSpent int
		elapsed     time.Duration
		want        bool
	}{
		{
			name:   "zero budget never exhausts",
			budget: Budget{},
			phase:  1, phases: 6,
			tokensSpent: 1_000_000, elapsed: time.Hour,
			want: false,
		},
		{
			name:   "fresh phase under its cumulative token allotment",
			budget: Budget{MaxTokens: 600},
			phase:  1, phases: 6, // allotted 100 tokens by phase 1
			tokensSpent: 50,
			want:        false,
		},
		{
			name:   "tokens spent already meets the cumulative allotment",
			budget: Budget{MaxTokens: 600},
			phase:  1, phases: 6, // allotted 100 tokens by phase 1
			tokensSpent: 100,
			want:        true,
		},
		{
			name:   "later phase has a larger cumulative allotment",
			budget: Budget{MaxTokens: 600},
			phase:  3, phases: 6, // allotted 300 tokens by phase 3
			tokensSpent: 250,
			want:        false,
		},
		{
			name:   "wall clock exhausted independently of tokens",
			budget: Budget{WallClock: 60 * time.Minute},
			phase:  2, phases: 6, // allotted 20 minutes by phase 2
			elapsed: 25 * time.Minute,
			want:    true,
		},
		{
			name:   "wall clock not yet exhausted",
			budget: Budget{WallClock: 60 * time.Minute},
			phase:  2, phases: 6,
			elapsed: 10 * time.Minute,
			want:    false,
		},
		{
			name:        "phase zero is never exhausted (guard)",
			budget:      Budget{MaxTokens: 1},
			phase:       0,
			phases:      6,
			tokensSpent: 1_000_000,
			want:        false,
		},
		{
			name:        "phases zero is never exhausted (guard)",
			budget:      Budget{MaxTokens: 1},
			phase:       1,
			phases:      0,
			tokensSpent: 1_000_000,
			want:        false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.budget.PhaseExhausted(c.phase, c.phases, c.tokensSpent, c.elapsed)
			if got != c.want {
				t.Errorf("PhaseExhausted(%d, %d, %d, %v) = %v, want %v",
					c.phase, c.phases, c.tokensSpent, c.elapsed, got, c.want)
			}
		})
	}
}
