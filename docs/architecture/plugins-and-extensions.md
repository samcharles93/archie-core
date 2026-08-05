# Plugins and Extensions

**Status:** Approved foundation  
**Date:** 2026-07-28  
**Beads issue:** `archie-core-5d7`

## Plugin framework

The core plugin domain lives under `internal/domain/plugin`. It owns:

- plugin identity and metadata;
- discovery and registration contracts;
- lifecycle rules;
- capability registration;
- validation and compatibility rules.

The generic plugin contract remains metadata-only. Functionality is exposed
through narrow typed capability contracts with an owning registry or manager.

Workflow plugins belong to the Workflow capability family. The Workflow domain
at `internal/domain/workflow` owns their typed registration, validation,
versioning, definition, and execution contracts. The generic plugin domain may
provide plugin identity, discovery, compatibility, and lifecycle mechanics, but
it MUST NOT define Workflow semantics or expose untyped Workflow hooks.

## Plugin implementations

First-party plugins are self-contained vertical modules under
`plugins/<plugin>`.

A plugin:

- owns its implementation, settings, templates, assets, and tests;
- receives only explicitly supplied contracts and services;
- MUST NOT receive daemon internals, a service locator, or unrestricted
  application access;
- MUST NOT import arbitrary infrastructure or application composition code;
- MAY use approved domain and shared application contracts;
- MUST remain removable without changes to unrelated domains.

Plugins may be internally cohesive like small services. They do not become
network services without a concrete security or operational reason.

## Curator surprisal sampling (deferred)

The curator engine family (epic `archie-core-yp9`) selects which memories
deserve agentic attention via surprisal-based sampling: score memories by how
surprising they are and spend the sampled reasoner's agentic budget on the
most surprising items. Wave 1 of the epic ships the `Sampler` strategy seam in
`internal/curator` with cheap, embedding-free strategies (recency, random,
all, and a staleness proxy). The embedding-backed strategy is a documented
extension point and is gated behind an embedding capability Archie does not
yet have — **any surprisal-style sampling is gated behind adding one**.

Requirements for the deferred work, so it can be picked up without
re-deriving the design (tracked as `archie-core-yp9.1` embeddings capability,
then `archie-core-yp9.2` surprisal strategies):

- A `Sampler` implementation behind the same interface as the cheap
  strategies; selection deterministic given fixed inputs.
- Algorithm: sample up to a candidate cap; build a k-NN graph over embedding
  vectors; row-normalize the adjacency with self-loops; power-iterate to the
  stationary distribution; surprisal = −log(stationary probability of the
  nearest point); select the highest-surprisal items.
- Degradation: content without an embedding is skipped; an embedding failure
  aborts sampling and falls back to free exploration — a sampling failure
  must never fail or crash a curator pass.
- Cost bound: the graph build is quadratic in the candidate cap; the cap must
  be a named constant with a documented bound.
- The embedding capability itself: a narrow typed client usable by in-process
  components, config-driven provider/model, credential-missing degrades to
  "capability unavailable" (daemon still starts), timeouts on network calls,
  tests via httptest only.
