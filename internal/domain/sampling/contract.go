// Package sampling is the curator sampler family: given a bounded set of
// candidates, pick a subset to spend a curator pass's agentic budget on.
// See docs/prds/curator-sampler-wave1.md for the design and the four
// wave-1 strategies (recency, random, all, staleness) this package ships.
// The embedding-backed surprisal strategy (GitHub #437) is a separate,
// deferred consumer of this same interface -- it does not live here.
package sampling

import (
	"context"
	"time"
)

// Candidate is one item eligible for sampling. It is deliberately narrow:
// an opaque identifier, the timestamp the timestamp-based strategies score
// against, and an open Metadata bag for whatever a future strategy needs
// (e.g. the embedding-backed surprisal strategy's vector) without widening
// this struct for every strategy that comes after wave 1.
type Candidate struct {
	ID       string
	At       time.Time
	Metadata map[string]any
}

// Request carries the knobs every strategy shares: a selection cap and a
// deterministic seed. Both are optional -- Cap 0 (or negative) means
// unlimited (the "all" strategy relies on this), Seed 0 is a valid seed,
// not "unset".
type Request struct {
	Cap  int
	Seed int64
}

// Sampler selects a subset of candidates to spend a curator pass's agentic
// budget on. Every implementation -- the four in this package and the
// future embedding-backed surprisal strategy (GitHub #437) -- must be
// deterministic given the same candidates and Request: same inputs, same
// output, every time. That is what makes a pass reproducible and testable.
type Sampler interface {
	// Name identifies the strategy for logging/attribution (e.g.
	// "recency", "random", "all", "staleness", later "surprisal").
	Name() string
	// Sample selects from candidates per req. Implementations must not
	// mutate candidates or rely on anything but candidates and req --
	// determinism depends on that.
	Sample(ctx context.Context, candidates []Candidate, req Request) ([]Candidate, error)
}

// effectiveCap resolves req.Cap against n candidates: zero, negative, or
// larger than n means unlimited (every candidate); otherwise req.Cap.
func effectiveCap(reqCap, n int) int {
	if reqCap <= 0 || reqCap > n {
		return n
	}
	return reqCap
}
