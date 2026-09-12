# Dynamically-defined curators -- decision

**Status:** Decided, to be implemented
**Date:** 2026-09-12
**GitHub epic:** [#789](https://github.com/samcharles93/archie-core/issues/789)
**Parent epic (curator engine):** [#435](https://github.com/samcharles93/archie-core/issues/435) (closed)
**Depends on:** nothing shipping; this resolves the open question that epic #435's
"registered and configured declaratively, not hardcoded" criterion left open.

## §0 Implementation status

Every claim in this document is mapped to what exists in the tree today.

| Claim | Status | Where it stands |
|---|---|---|
| Curator engine family: typed contract, owning registry, start/health/stop, narrow host access | **Implemented** | `internal/domain/curator/{contract,registry,runtime,registrar,activity,wake}.go` |
| Curator declared shape (`Manifest`: interval, cooldown, on_input, tools, skills, memory engine, conversations, model) | **Implemented** | `internal/domain/curator/contract.go`; `Validate()` requires `Interval > 0` |
| Two reference curators run through the registry | **Implemented** | `internal/infrastructure/skillcurator`, `internal/infrastructure/sessioncurator` |
| Curator definitions as persisted, API-editable data | **Aspirational -- zero code** | No table, no entity, no RPC, no config block |
| Generic definition-driven engine that executes a declared tool set + free-form instructions | **Aspirational -- zero code** | Only the two code-registered curators exist |
| `Registrar.Tools` (`ToolBuilder`) resolved and bound at registration | **Partial** | Interface declared (`registrar.go:86`); **no implementation anywhere**; `bootstrap.go` builds the `Registrar` with no `Tools` field |
| A curator can declare tools at all | **Partial** | `Manifest.Tools` exists, but `registry.go:123` fails registration with `"curator manifest: declares tools but the registrar has no ToolBuilder"` |
| Curator passes that actually reach a model with tools | **Cosmetic only** | `curator.ChatRequest` carries `Tools`/`MaxSteps`; `curatorLLMRunner` (`internal/app/archied/main.go:551`) drops both and returns `ChatResult{Text}` only |
| Dynamic registration / removal (no restart) | **Aspirational -- zero code** | `Registry` has `Register`, no `Unregister`; `Runtime` has `Start`/`Nudge`/`Stop`, no `Add`/`Remove`; `Start()` snapshots membership |
| Curator definition CRUD over REST | **Partial** | `GET /api/curators` read-only (`internal/webui/server.go:226`); no write routes |
| Curator definition config | **Aspirational -- zero code** | No `[curator]` block in `internal/config/config.go` |
| Store/API as definition authority | **Aspirational -- zero code** | No curator persistence of any kind |
| No live-path defaults for interval/cooldown/tools/model/memory engine | **Partial** | `skillcurator.DefaultInterval` / `sessioncurator.DefaultInterval` are Go consts read at registration; `curator.NewRuntime(..., RuntimeConfig{})` uses code defaults for pass timeout and concurrency |
| Idle curators are observable in logs | **Partial** | `runtime.go:202-211` logs nothing when `Check` reports not-due; the session-memory curator's hourly pass is the only curator activity ever logged |
| Sampler family | **Implemented** | `internal/domain/sampling`, shipped; no curator consumes a `Sampler` yet |

> **Target state, not current state.** Everything below marked aspirational is
> design, not a description of the daemon as it runs today.

## Problem

A curator exists today only if a Go package implements `curator.CuratorEngine`
and `bootstrap.go:1022/1025` names it in a `Register` call. Nothing about a
curator is definable as data. Worse, no curator can declare a tool set at all:
the `ToolBuilder` interface has no implementation in the repo, so the registry
refuses (correctly, fail-closed) any curator that declares tools -- and the
daemon's model adapter drops the tools even if it were bound. The curator family
therefore has exactly one shape (cheap, tool-less maintenance pass) and its only
extension point is compiling Go.

Sam's requirement is the opposite: *"Curators should be dynamically created and
defined, in addition to being able to be defined in code/config/webui"* and *"No
configuration should ever be hard-coded."* The motivating consumer is a
project/orchestrator curator -- declared as data, given forge issue tools, and
pointed at a repo to review, refine, and propose issues.

## Decision

**A curator definition is data: a persisted entity whose declared shape is
exactly `curator.Manifest` plus identity, free-form instructions, and an enabled
flag. One generic engine interprets any definition.** The shape is not a second
invention -- `Manifest` already encodes interval, cooldown, on-input, tools,
skills, memory engine, conversations, and model, and `Validate()` already
enforces what a valid shape means. The definition adds only what `Manifest`
cannot express:

| Definition field | Relationship to `Manifest` |
|---|---|
| `name`, `enabled` | Identity and lifecycle; the registry's key is the name |
| `instructions` | **Free-form** system prompt for the generic engine -- the piece that makes a definition more than a knob set |
| interval, cooldown, on_input, tools, skills, memory_engine, conversations, model | The existing `Manifest` fields verbatim; the definition **is** a `Manifest` |

Code-registered engines keep `CuratorEngine` unchanged. A data-defined curator is
served by one registered engine whose `Manifest()` is the stored definition and
whose `Pass` composes `instructions` with the built tool set.

This is the curator domain's missing half, not a standalone tool: it lands in
`internal/domain/curator` (entity + engine), `internal/store` (persistence),
`internal/webui` (CRUD), and `internal/app/archied` (seed + apply wiring).

## Definition sources and the merge rule

Three sources, per Sam's requirement. **The store is authoritative.**

| Source | Role | Collision behaviour |
|---|---|---|
| **Code** (a registered Go engine) | The escape hatch: a curator whose strategy is Go, not tools+instructions | Wins over data for its own name -- a data definition cannot silently replace a compiled engine; the store refuses the collision and logs it |
| **Config** (`[[curators]]`) | **Seed data only**, merged at startup | Insert-if-absent. A store row of the same name is never overwritten, and deleting a store row is not undone by config on the next boot |
| **WebUI/API** (the store) | **Authoritative** | Wins over config on a name collision; the only source of truth after first boot |

Config existing at all is what makes a definition reproducible on a fresh host;
making it authoritative would make the API a lie, so it is seed data with a
one-way merge. The alternative -- config authoritative, store a cache -- would
mean an API edit silently reverts on restart. Rejected.

## How the generic engine executes a definition

A pass is: resolve the declared tools, compose the prompt, run the model loop,
record actions.

1. **Tool resolution.** `Registrar.Tools.Build(ctx, declared)` returns exactly
   the declared set, never a broader registry. A declared name with no
   implementation fails the pass rather than silently running with fewer tools.
   This is the enforcement point, and it is why the `ToolBuilder` binding is
   load-bearing rather than a convenience: without an implementation, no curator
   can declare tools at all (today's `registry.go:123` guard).
2. **Tool-carrying model call.** `curator.ChatRequest` already carries
   `Tools []tools.ToolEntry` and `MaxSteps`; `curatorLLMRunner` must pass them
   into `core.GenerateOptions` (which has `Tools`, `ToolChoice`, `MaxSteps`, and
   already executes the tool loop) instead of dropping them. `ChatResult` is
   widened only as far as attributing the pass's actions requires.
3. **Prompt.** The system prompt is the definition's `instructions` plus the
   declared tool summaries -- rendered from the same set that was built, never
   from a wider list. Each pass records `Action`s so activity stays attributable
   through the existing registry `activity` surface and events.

A bespoke non-tool strategy (the skill curator's structural validation, the
session-memory curator's own extraction) remains a registered Go engine. That is
the escape hatch, not the mode: the mode is tools + instructions as data.

## Dynamic registration and mutation

A definition change must apply without a daemon restart, which the runtime
cannot do today (`Start()` snapshots membership into one goroutine per curator;
no `Unregister`, no `Add`/`Remove`).

- `Registry.Unregister(name)` -- valid while running, same failure isolation as
  `Register` (a rejected removal affects only that curator).
- `Runtime.Add(engine)` / `Runtime.Remove(name)` -- `Remove` cancels the removed
  curator's in-flight pass context and stops its goroutine within a bounded
  wait; it never holds the registry mutex across a pass.
- The app layer owns the apply sequence (stop old, register new, start) so the
  registry stays the membership authority and the runtime the scheduler.

Removal must not be able to orphan a loop, and a pass that ignores cancellation
must not block removal forever.

## REST / WebUI surface

Mirror `internal/webui/api_binding.go`, not a new pattern:

| Route | Handler shape |
|---|---|
| `GET /api/curators` | Existing read-only list, fields unchanged (dashboard keeps working) |
| `POST /api/curators` | `handleCuratorCreate` |
| `GET /api/curators/{id}` / `PATCH` / `DELETE` | `handleCuratorGet` / `Update` / `Delete` |

All mutations behind `authorizeTaskMutation`, same as bindings and mappings. A
successful mutation goes through the runtime apply path above, so it is live
without a restart.

## Sequencing: persistence and the State Store

The State Store is a standalone process that solely owns `archie.db`; the daemon
and Gateway never open it (`TestOpenStoresNeverOwnsTaskDB` guards this), and
`proto/state/v1/state.proto` is one gRPC service fronting every ratified store
contract. AGENTS.md forbids adding a Go interface method without a matching RPC.
That fixes the order:

| Phase | Ships | Proto work |
|---|---|---|
| 1. Engine + config source | `ToolBuilder`, tool-carrying passes, generic engine, `[[curators]]`, idle logging | None -- nothing persisted yet |
| 2. Runtime mutation | `Unregister`, `Add`/`Remove`, apply path | None -- in-memory membership |
| 3. Persistence | definition entity + store schema, config seed (store authoritative), REST CRUD | **Same change**: request/response messages + RPCs, `staterpc.Client` façade, `mapError`/`unmapError`, and an update to `docs/prds/state-store-contract.md` |

Phases 1 and 2 are genuinely independent of the wire boundary, so they are not
blocked on it. Phase 3 is where the boundary is unavoidable: a definition the
WebUI can edit must be reachable from the WebUI, and the WebUI reaches the store
through the State Store, not by opening `archie.db`. Splitting the RPCs into a
later change would leave a table nothing can read -- rejected. Deferring the
whole phase is the same rejection, later.

## No live-path constants

No default may be a live-path constant. Interval, cooldown, tools, model, memory
engine, pass timeout, and concurrency are definition or config values. A
definition missing `interval` is refused with a clear error, never defaulted at
pass time; `skillcurator.DefaultInterval` and `sessioncurator.DefaultInterval`
stop being read at registration (the value becomes seed data), and
`curator.NewRuntime(..., RuntimeConfig{})` stops relying on code defaults.

## What this deliberately does NOT do

- **No fixed catalogue of curator "types".** A definition is arbitrary tools +
  instructions, not a union of known kinds.
- **No chat tool that mints curators.** A definition grants an LLM a tool set; a
  curator able to create curators is privilege escalation. Mutation is REST with
  the same authority as bindings -- not exposed on the chat tool surface.
- **No dashboard SPA editor page.** The REST surface is the WebUI definition
  source; the dashboard can adopt it separately.
- **No forge issue tools here.** The orchestrator curator's motivating tools
  (list/create/comment issues for a repo) belong to the existing tools registry
  work (#161/#140); this design only ensures a curator can declare them.
- **No second authority.** No file-per-curator definition store, no parallel
  config reader.
