# Plugins and Extensions

**Status:** Approved foundation  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

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

## Event sources and reactions

`archie-core-7d5u.1` asked whether extensions should react to events
in-process through typed capability families (this document's model) or
out-of-process like a generic `DispatchEvent` dispatcher. Decided 2026-08-22
in `docs/prds/event-sources-and-reactions.md`: in-process typed families,
confirming this document's model rather than amending it. The forge webhook
receiver (`internal/forge/webhook`, see `ARCHITECTURE.md`'s "Webhook intake"
section) and the EDA playbook engine's Module action position
(`docs/prds/eda-playbook-engine.md`, `internal/domain/eda/module`) are the
shipped instances of that decision -- each is a narrow typed contract with an
owning registry, not a generic event hook. The playbook engine's Channel and
Forge action positions are designed but not yet implemented (open
investigation `archie-core-t2db.19`); when they land they follow the same
model, not a new one.

## Curator surprisal sampling (deferred)

**Deferred by decision, 2026-08-05** — Archie has no embedding capability, and
wave 1 ships the strategy seam without one. Pick the deferred work up in order:
[#436](https://github.com/samcharles93/archie-core/issues/436) first, then
[#437](https://github.com/samcharles93/archie-core/issues/437) which depends on it
and on [#407](https://github.com/samcharles93/archie-core/issues/407).

The curator engine family (epic [#435](https://github.com/samcharles93/archie-core/issues/435)) selects which memories
deserve agentic attention via surprisal-based sampling: score memories by how
surprising they are and spend the sampled reasoner's agentic budget on the
most surprising items. Wave 1 of the epic ships the `Sampler` strategy seam in
`internal/curator` with cheap, embedding-free strategies (recency, random,
all, and a staleness proxy). The embedding-backed strategy is a documented
extension point and is gated behind an embedding capability Archie does not
yet have — **any surprisal-style sampling is gated behind adding one**.

Requirements for the deferred work, so it can be picked up without
re-deriving the design (tracked as [#436](https://github.com/samcharles93/archie-core/issues/436) embeddings capability,
then [#437](https://github.com/samcharles93/archie-core/issues/437) surprisal strategies):

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
