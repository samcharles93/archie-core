package sampling

import "context"

// stalenessSampler is the "staleness proxy" strategy: prefers content that
// hasn't been touched/reviewed in the longest time, i.e. the smallest
// Candidate.At first. It is a cheap proxy only -- callers decide what
// timestamp At holds (created, updated, or last-reviewed); this is never
// the embedding-based surprisal calculation (see GitHub #437).
type stalenessSampler struct{}

// NewStaleness builds the "staleness" strategy.
func NewStaleness() Sampler { return stalenessSampler{} }

func (stalenessSampler) Name() string { return "staleness" }

func (stalenessSampler) Sample(_ context.Context, candidates []Candidate, req Request) ([]Candidate, error) {
	sorted := sortByTime(candidates, false)
	return sorted[:effectiveCap(req.Cap, len(sorted))], nil
}
