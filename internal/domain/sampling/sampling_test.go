package sampling

import (
	"context"
	"testing"
	"time"
)

// assertIDs checks got's IDs match want, in order.
func assertIDs(t *testing.T, got []Candidate, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d (%v)", len(got), len(want), want)
	}
	for i, c := range got {
		if c.ID != want[i] {
			t.Fatalf("candidate[%d].ID = %q, want %q", i, c.ID, want[i])
		}
	}
}

// assertInputNotMutated builds a candidate slice, samples it through s,
// then verifies the original slice's order and values are untouched --
// the non-mutation guarantee every Sampler implementation must uphold.
func assertInputNotMutated(t *testing.T, s Sampler) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{ID: "a", At: base.Add(3 * time.Hour)},
		{ID: "b", At: base.Add(time.Hour)},
		{ID: "c", At: base.Add(2 * time.Hour)},
		{ID: "d", At: base},
	}
	original := append([]Candidate(nil), candidates...)

	if _, err := s.Sample(context.Background(), candidates, Request{Cap: 2, Seed: 7}); err != nil {
		t.Fatalf("Sample() error = %v", err)
	}

	if len(candidates) != len(original) {
		t.Fatalf("input slice length changed: got %d, want %d", len(candidates), len(original))
	}
	for i := range candidates {
		if candidates[i].ID != original[i].ID || !candidates[i].At.Equal(original[i].At) {
			t.Fatalf("input slice mutated at index %d: got %+v, want %+v", i, candidates[i], original[i])
		}
	}
}
