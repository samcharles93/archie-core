package sampling

import (
	"context"
	"math/rand/v2"
)

// randomSampler is the "random" strategy: a uniform random subset, seeded
// per call from Request.Seed so the same (candidates, req) pair always
// produces the same selection -- a package-level or struct-level generator
// would not give that guarantee under concurrent or repeated calls.
type randomSampler struct{}

// NewRandom builds the "random" strategy.
func NewRandom() Sampler { return randomSampler{} }

func (randomSampler) Name() string { return "random" }

func (randomSampler) Sample(_ context.Context, candidates []Candidate, req Request) ([]Candidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	n := effectiveCap(req.Cap, len(candidates))
	seed := uint64(req.Seed) //nolint:gosec // deterministic seeding, not a security boundary
	rng := rand.New(rand.NewPCG(seed, seed))
	order := rng.Perm(len(candidates))
	out := make([]Candidate, n)
	for i := range n {
		out[i] = candidates[order[i]]
	}
	return out, nil
}
