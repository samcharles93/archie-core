package sampling

import (
	"context"
	"slices"
)

// recencySampler is the "recency" strategy: prefers more recently
// created/updated content, i.e. the largest Candidate.At first.
type recencySampler struct{}

// NewRecency builds the "recency" strategy.
func NewRecency() Sampler { return recencySampler{} }

func (recencySampler) Name() string { return "recency" }

func (recencySampler) Sample(_ context.Context, candidates []Candidate, req Request) ([]Candidate, error) {
	sorted := sortByTime(candidates, true)
	return sorted[:effectiveCap(req.Cap, len(sorted))], nil
}

// sortByTime returns a stably-sorted copy of candidates, never mutating the
// input. newestFirst true sorts descending by At (recency); false sorts
// ascending (staleness). A stable sort keeps ties in input order, which is
// what makes both strategies deterministic when candidates share a
// timestamp.
func sortByTime(candidates []Candidate, newestFirst bool) []Candidate {
	sorted := append([]Candidate(nil), candidates...)
	slices.SortStableFunc(sorted, func(a, b Candidate) int {
		if newestFirst {
			return b.At.Compare(a.At)
		}
		return a.At.Compare(b.At)
	})
	return sorted
}
