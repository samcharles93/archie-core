# Curator sampler wave 1: `Sampler` interface + four cheap strategies -- decision

**Status:** Approved for implementation
**Date:** 2026-09-05
**Beads issue:** none yet at time of writing (bead `archie-core-1786637500776-416-600a66e5`
/ GitHub #437 covers the embedding-backed follow-on strategy only; this PRD
covers the prerequisite interface + cheap strategies, tracked by the new
issue this change files, see `new-issues.jsonl`)
**Parent epic:** `archie-core-1786637500725` (curator engine)
**Depends on:** nothing shipped (this is itself a prerequisite)
**Blocks:** the embedding-backed surprisal strategy (GitHub #437), which
implements this same interface once the embeddings capability
(`internal/domain/embedding`, this change) lands

## Problem

A future curator that reviews a large body of content (memories, sessions,
skills) cannot always afford to run its agentic budget over every candidate
every pass. It needs to pick a bounded subset first. GitHub #437's acceptance
criteria requires that subset-picking to sit "behind a `Sampler` interface
that wave 1 of the curator epic ships (cheap strategies: recency, random,
all, staleness proxy)" and that the embedding-backed strategy implement "the
same interface" later. No such interface, and none of the four cheap
strategies, exist anywhere in the codebase today (bead 434e5bb9's closure
without a linked PR was the tracker being wrong, not the code existing) --
this PRD is the one-page settled design for it, per this repo's Scope
Discipline rule.

## Decision

A `Sampler` is a pure, call-scoped selection function: given a fixed slice of
candidates and a request, it returns a subset. No lifecycle, no host access,
no I/O for the four wave-1 strategies -- they are computation only, so they
live directly in `internal/domain/sampling` (a domain package, not
`internal/infrastructure/*`) alongside the contract, the same way
`curator/activity.go`'s pure ring buffer sits next to `curator/contract.go`
rather than in an infrastructure package.

```go
// Package sampling is the curator sampler family: given a bounded set of
// candidates, pick a subset to spend a curator pass's agentic budget on.
// See docs/prds/curator-sampler-wave1.md.
package sampling

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
// deterministic seed. Both are optional -- Cap 0 means unlimited (the "all"
// strategy relies on this), Seed 0 is a valid seed, not "unset".
type Request struct {
    Cap  int
    Seed int64
}

// Sampler selects a subset of candidates to spend a curator pass's agentic
// budget on. Every implementation -- the four here and the future
// embedding-backed surprisal strategy (GitHub #437) -- must be
// deterministic given the same candidates and Request: same inputs, same
// output, every time. That is what makes a pass reproducible and testable,
// and is GitHub #437's stated acceptance criterion for the strategy it
// adds behind this same interface.
type Sampler interface {
    // Name identifies the strategy for logging/attribution (e.g.
    // "recency", "random", "all", "staleness", later "surprisal").
    Name() string
    // Sample selects from candidates per req. Implementations must not
    // mutate candidates or rely on anything but candidates and req --
    // determinism depends on that.
    Sample(ctx context.Context, candidates []Candidate, req Request) ([]Candidate, error)
}
```

### The four wave-1 strategies

All four are `internal/domain/sampling` types constructed with `NewX(...)`,
each implementing `Sampler`:

- **`recency`** (`NewRecency()`): sorts candidates newest-`At`-first (stable
  sort, ties keep input order so output is deterministic even when two
  candidates share a timestamp), then truncates to `req.Cap` (0 = all).
- **`staleness`** (`NewStaleness()`): the mirror of recency -- sorts oldest-
  `At`-first, then truncates to `req.Cap`. This is explicitly a *proxy*:
  "hasn't been touched in the longest time" using whatever timestamp the
  caller populated `Candidate.At` with (created, updated, or
  last-reviewed -- the caller's choice, not this package's), never the
  embedding-based surprisal calculation GitHub #437 owns.
- **`all`** (`NewAll()`): no filtering. Returns candidates in input order,
  truncated to `req.Cap` (0 = every candidate). Input order is the only
  order this strategy has an opinion about, and preserving it is what makes
  it deterministic.
- **`random`** (`NewRandom()`): a uniform-random subset, sized to
  `req.Cap` (0 = every candidate, order-shuffled). Determinism comes from
  `req.Seed`: `math/rand/v2`'s `rand.New(rand.NewPCG(seed, seed))` seeded
  per call (not a package-level or struct-level generator), so the same
  `(candidates, req)` pair always produces the same permutation regardless
  of call order or concurrent callers -- required for GitHub #437's
  determinism criterion and for this package's own tests.

### Why one shared `Request` and not one options struct per strategy

The embedding-backed strategy (GitHub #437) is deferred, but its stated
shape -- "sample up to a candidate cap," a seed for reproducible tie-
breaking in the power-iteration -- already fits `Request{Cap, Seed}`
without modification. A per-strategy options type would mean GitHub #437
either reuses this one (proving it was unnecessary to split) or grows its
own incompatible shape that the `Sampler` interface can't express without a
breaking change later. One shared, small `Request` is the smallest thing
that already accommodates the known future consumer.

### Why `Candidate.Metadata map[string]any` instead of a typed field per strategy

Only `ID` and `At` are used by any wave-1 strategy. `Metadata` exists solely
so GitHub #437 can carry an embedding vector (or whatever else the k-NN/
power-iteration step needs) through the *same* `Candidate` type without this
package importing `internal/domain/embedding` or growing a `Vector` field
that every non-surprisal strategy ignores. Untyped and optional, exactly
like `image.GenerateRequest.Options` and `curator.Action.Detail`'s existing
"provider-specific overflow" convention elsewhere in this codebase.

## What we deliberately do NOT do

- **No curator-family wiring.** `curator.Registrar` gains no `Sampler`
  field and no curator declares one in this change. Nothing in
  `internal/domain/curator` consumes `sampling.Sampler` yet -- there is no
  curator that samples anything today. Wiring a sampler into a real
  curator's `Pass` is that future curator's job, not this prerequisite's.
- **No embedding-backed strategy.** That is GitHub #437, explicitly
  deferred behind the embeddings capability this same change also adds
  (`internal/domain/embedding`) -- see that issue for the k-NN graph /
  power-iteration / surprisal-scoring algorithm. This package only
  guarantees the interface shape GitHub #437 must implement.
- **No persistence, no candidate discovery.** `Sampler.Sample` takes
  `candidates` as a plain slice; where they come from (a memory store, a
  session list, a skill directory) is entirely the caller's concern, same
  boundary `curator.Registrar`'s `SkillStore`/`ConversationSource` already
  draw between "the curator finds candidates" and "the curator engine acts
  on them."
- **No configuration surface.** Strategy selection (which `Sampler` a
  curator uses) is a future curator's constructor argument, not a new
  `[curator]` config section -- no curator exists yet to configure.

## Acceptance criteria

1. `Sampler` interface plus `Candidate`/`Request` types exist in
   `internal/domain/sampling`, importing nothing from
   `internal/infrastructure/*` or `internal/app/*` (domain-layer rule).
2. Four `Sampler` implementations (`recency`, `random`, `all`, `staleness`)
   with table-driven tests proving: correct selection per strategy,
   `Cap` truncation (including `Cap` larger than the candidate count, and
   `Cap` 0 meaning unlimited), and -- for `random` -- that the same
   `(candidates, req)` produces the same output across repeated calls
   (determinism), while a different `req.Seed` can produce a different
   permutation.
3. Every strategy's `Sample` leaves the input `candidates` slice
   unmodified (verified by a shared test helper across all four).
4. The interface shape is reviewed against GitHub #437's stated
   acceptance criteria ("a `Sampler` given fixed inputs producing
   deterministic selection," "sample up to a candidate cap") and
   accommodates them without a breaking change -- this document's
   "Why one shared `Request`" section is that review.

## Files this change adds

- `internal/domain/sampling/contract.go` -- `Sampler`, `Candidate`,
  `Request`
- `internal/domain/sampling/recency.go`, `staleness.go`, `all.go`,
  `random.go` -- the four strategies
- `internal/domain/sampling/*_test.go` -- table-driven tests per strategy
  plus the shared non-mutation test helper
