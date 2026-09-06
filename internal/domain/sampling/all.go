package sampling

import "context"

// allSampler is the "all" strategy: no filtering, returns every candidate
// in input order, truncated to Request.Cap.
type allSampler struct{}

// NewAll builds the "all" strategy.
func NewAll() Sampler { return allSampler{} }

func (allSampler) Name() string { return "all" }

func (allSampler) Sample(_ context.Context, candidates []Candidate, req Request) ([]Candidate, error) {
	n := effectiveCap(req.Cap, len(candidates))
	out := make([]Candidate, n)
	copy(out, candidates[:n])
	return out, nil
}
