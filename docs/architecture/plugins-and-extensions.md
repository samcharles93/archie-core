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

## Plugin engine rule (strict)

“Engine” is reserved for a typed, lifecycle-managed capability family. It is not
a synonym for a generic plugin hook. Every new plugin engine must satisfy all of
these invariants:

1. **Typed domain contract.** The engine interface exposes real
   capability-specific operations in addition to identity metadata. Examples are
   resolving a secret, delivering a message, or running an agent. A plugin that
   only exposes `Name` and `Version` is metadata, not an engine.
2. **Owning family manager.** The capability package owns a `Registry` or
   `Manager` that controls provider registration and discovery, initialization,
   or access. Providers do not register arbitrary callbacks directly on the
   daemon.
3. **Lifecycle and health.** When an implementation owns resources or background
   work, its typed family contract must expose start, health, and stop
   semantics. The owning manager invokes them and defines failure isolation and
   shutdown ordering. Stateless providers may make these operations explicit
   no-ops.
4. **Narrow host access.** Plugins receive the typed family registrar and only
   the host services their declared capability requires. They never receive
   `*daemon.Daemon`, a service locator, or an untyped hook map.
5. **Metadata-only generic plugin contract.** `plugin.Plugin` remains limited to
   `Name()` and `Version()`. Domain hooks belong on typed engine interfaces;
   adding capability methods to the generic interface is forbidden.
6. **Trust boundary stays explicit.** Explicitly trusted, operator-installed
   plugins—including third-party secret-engine plugins the operator chooses to
   trust—may run in-process and have daemon privileges. Repository-supplied code
   runs in the task container. Integrations that are not trusted as daemon code
   run out of process through MCP or a versioned protocol and, when a
   confidentiality or integrity boundary is required, inside a real container
   sandbox. A subprocess boundary alone is not a security sandbox, and calling
   Yaegi-loaded code a plugin does not isolate it.

`internal/plugin/architecture_test.go` enforces the mechanically testable parts
of this rule: the generic plugin method set, the shared agent-instruction
source, and the requirement that engine interfaces have domain behavior plus an
owning registry or manager.

### Memory engine family

One engine, addressed by four typed scopes -- `ScopeGlobal`, `ScopeAgent`,
`ScopeUser`, `ScopeAgentUser` (`internal/domain/memory`, `Scope`/`ScopeKind`).
A caller names exactly the scopes it may read or write; the engine applies no
access policy of its own, so isolation falls out mechanically because
`ScopeAgent{a}` and `ScopeAgent{b}` are different storage keys. See
`docs/prds/memory-engine-unification.md` for the decision this contract
implements; it superseded `internal/memory` (`MemoryProvider`/`Manager`),
deleted in that PRD's slice 5.

`MemoryEngine` (`internal/domain/memory/contract.go`) is the typed family
contract: `Create`, `Get`, `Query`, `List`, `Update`, `Forget`, `Revisions` --
CRUD with revisions retained, not just create/read/delete. `Update` supersedes
rather than overwrites: the superseded state is appended to the scope's
`HISTORY.md` before the live block is replaced, and `Update` carries no
`Scope` field, so a record can never change scope. The family's content
scanner (prompt-injection / sensitive-data patterns) is applied inside
`Create` and `Update`, the single choke point every producer crosses.

`internal/domain/memory.Registry` is the owning family registry: registration,
start/health/stop, shutdown ordering, and failure isolation, following the
plugin engine rule above. `internal/infrastructure/memory.NewBuiltinEngine` is
the only registered engine today; it persists through
`internal/infrastructure/memory/builtin`, a markdown store (`MEMORY.md`-shaped
per-scope files) that owns its on-disk format end-to-end.

Two per-turn seams in `internal/gateway` connect a chat turn to the engine,
each narrowed to exactly the operations it needs:

- **Read** (`turn_memory.go`, `MemoryStore`): `prepareTurn` runs a
  scope-only `Query` over the resolved `Subject`'s readable scopes,
  synchronously and before the prompt is built. It never returns an error --
  a panicking or failing engine degrades to an empty `<memory>` block rather
  than failing the turn -- and is bounded by a per-scope record limit and a
  byte cap on the rendered block.
- **Write** (`turn_memory_tool.go`, `MemoryWriteStore`): four tools --
  `memory_create`, `memory_update`, `memory_delete`, `memory_list` -- built
  per turn onto the resolved `Subject`'s writable scopes, not registered once
  at boot. The model chooses the scope kind; it never chooses the agent or
  user id, which come from the turn's resolved `Subject` and have no schema
  field to override. `ScopeGlobal` is never model-writable.

`boot.setupMemoryEngine` (`internal/app/archied/bootstrap.go`) registers and
starts the engine and must run before `setupGatewayChat`, which constructs the
only chat turn runner in production and captures the engine at construction
time (`internal/app/archied/composition_order_test.go` pins the ordering), and
before `setupCurators`, whose `Registrar` captures the engine registry. The
session curator (`internal/infrastructure/sessioncurator`) writes derived
records onto `ScopeAgentUser`, making it the engine's other live producer
besides the chat write path.

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
receiver (`internal/forge/webhook`; see "Intake provenance" in
`docs/architecture/messaging-and-work-intake.md`) and the EDA playbook engine's
Module action position
(`docs/prds/eda-playbook-engine.md`, `internal/domain/eda/module`) are the
shipped instances of that decision -- each is a narrow typed contract with an
owning registry, not a generic event hook. The playbook engine's Channel and
Forge action positions are designed but not yet implemented (open
investigation `archie-core-t2db.19`); when they land they follow the same
model, not a new one.

## Curator surprisal sampling

**Decided 2026-08-05, wave 1 landed 2026-09-05** — the curator engine family
(epic [#435](https://github.com/samcharles93/archie-core/issues/435)) selects which memories deserve agentic attention via
surprisal-based sampling: score memories by how surprising they are and
spend the sampled reasoner's agentic budget on the most surprising items.

The prerequisites are built: the embeddings capability
([#436](https://github.com/samcharles93/archie-core/issues/436), `internal/domain/embedding` contract +
`internal/infrastructure/embedding` implementation, config-driven via
`models.embedding`) and the wave-1 `Sampler` strategy seam
(`internal/domain/sampling` -- not `internal/curator`, see
`docs/prds/curator-sampler-wave1.md` for the settled design and tracking)
with four
cheap, embedding-free strategies (recency, random, all, staleness proxy).
No curator consumes a `Sampler` yet, and the embeddings client is likewise
deliberately inert: `setupEmbeddings` constructs it at boot and holds it on
`b.embeddings`, but no production code reads that field yet, so a resolved
`models.embedding` role currently produces a client whose only observable
effect is a boot log line. Both are prerequisites waiting on #437, not
unwired defects.

Still deferred: [#437](https://github.com/samcharles93/archie-core/issues/437), the embedding-backed surprisal strategy, which
depends on the above and on [#407](https://github.com/samcharles93/archie-core/issues/407). Requirements for that deferred work,
so it can be picked up without re-deriving the design:

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
