package sampling

import "context"

// stalenessSampler prefers the oldest Candidate.At.
type stalenessSampler struct{}

// NewStaleness builds the "staleness" strategy.
func NewStaleness() Sampler { return stalenessSampler{} }

func (stalenessSampler) Name() string { return "staleness" }

func (stalenessSampler) Sample(_ context.Context, candidates []Candidate, req Request) ([]Candidate, error) {
	sorted := sortByTime(candidates, false)
	return sorted[:effectiveCap(req.Cap, len(sorted))], nil
}
