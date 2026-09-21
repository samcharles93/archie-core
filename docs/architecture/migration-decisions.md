# Architecture Migration Decisions

**Status:** Active migration inventory  
**Date:** 2026-07-28  
**Tracking issue:** [#73](https://github.com/samcharles93/archie-core/issues/73)

## Purpose

This document records what remains to be decided before Archie's current
implementation can be migrated into the approved domain-oriented structure.

The product and core domain model are not being redesigned. Existing Archie
behaviour and the existing session implementation are the migration
baseline. Remaining decisions MUST be grounded in current packages, consumers,
tests, persistence, and runtime wiring.

## Confirmed domain packages

```text
internal/domain/
  identity/    (approved, not yet materialised as a package)
  agent/       (approved, not yet materialised as a package)
  workflow/
  messaging/   (canonical Message/Conversation types only; gateway migration pending, see section 2)
  workintake/
  plugin/      (approved, not yet materialised as a package)
  curator/
  mapping/
  scheduling/
```

- `identity` owns persistent actors, lifecycle, ownership, attribution, and
  audit identity.
- `agent` owns persistent assistants, specialisation, instructions,
  model-independent continuity, capability context, and associations with
  identity and memory.
- `workflow` is a separate domain. It owns reusable, versioned Workflow
  definitions, WorkflowSteps, WorkflowExecutions, StepExecutions, Attempts,
  outcomes, and typed Workflow plugin contracts.
- `messaging` owns canonical Messages, Conversations, branches, channel and
  source correlation, replies, acknowledgements, delivery attempts, events, and
  projections.
- `workintake` owns admission, validation, deterministic routing, and accepted
  work requests for the optional transition into durable workflow-backed
  execution.
- `plugin` owns generic plugin identity, discovery, compatibility, and lifecycle
  mechanics. It remains metadata-only; capability semantics and typed
  registration belong to the owning domain.
- `curator` owns the curator engine family: long-running agent loops that
  reason over memory and perform maintenance tasks on a declared shape
  (check-in interval, tool set, attached memory engine). See epic
  archie-core-yp9.
- `mapping` owns the payload field-mapping vocabulary: binding named fields to
  JSON paths inside a captured webhook payload, and detecting drift. See
  `docs/prds/payload-field-mapping.md`.
- `scheduling` owns the ticker engine: deciding when a scheduled job runs and
  with what concurrency. It declares `JobSource`/`Runner` contracts and owns
  no storage or delivery. See epic archie-core-1786637496320-249-be6d9a20.

**Reconciliation (2026-08-31):** `curator`, `mapping`, and `scheduling` existed
in the tree without being added to this list when their epics landed. They are
confirmed as legitimate, narrowly-scoped domains under the same rules as the
original six — each owns one capability family, declares typed contracts, and
has no cross-domain reach. This list was out of date, not the tree.

Conversely, `identity`, `agent`, `messaging`, and `plugin` are approved target
domains that do not yet exist as packages. Their behaviour currently lives in
pre-migration packages (see "Complete package destination map" below for the
full inventory). Do **not** create empty stub packages for them ahead of an
actual migration PR — an unused stub is dead code and gives no test coverage.
Each materialises when its corresponding migration decision below is resolved
and implemented in the same change.

Memory's final package placement remains undecided pending focused review of the
existing implementation.

The following are approved shared or composition packages, not domains:

```text
internal/eventbus/
internal/policy/
internal/infrastructure/
internal/app/archied/
internal/app/agentworker/
```

## Remaining migration decisions

### 1. Complete package destination map

Every current package MUST receive one explicit disposition:

- move into a confirmed domain;
- move under infrastructure as an implementation of a domain-owned contract;
- move into application composition;
- merge with an identified duplicate;
- remain as an approved shared application contract;
- move to a plugin, extra, deployment, example, or generated artifact;
- be deleted after its replacement is proven.

The largest unreviewed areas include memory, tools and capabilities, workspace
and worktree behaviour, gates, storage, events, web UI, containers, RPC
packages, release management, and scheduling.

The destination matrix MUST name the behaviour owner, target path, dependencies
to remove, state and contracts to preserve, migration prerequisites, and
deletion criteria.

**`internal/webui` / UI Service — RATIFIED Phase 3 disposition (2026-09-07).**
The current owner is the in-process HTTP/SSE dashboard: SPA hosting, operator
authentication, UI response projection, configuration reads and mutations,
task/control APIs, and direct adapters to State Store, channel, event/log,
memory, curator, workflow, forge, and Gateway runtime values. The target owner
is a standalone UI application process; `internal/webui` remains its HTTP
transport adapter, while Gateway, State Store, configuration, Work Intake,
Messaging, and other capability owners expose the contracts behind those
routes. The migration preserves retained route schemas, auth/CSRF behavior,
task/capture/mapping/binding/session continuity, SSE replay semantics, and
committed generated SPA assets. Prerequisites are the ratified UI boundary,
route-owner inventory, narrow remote adapters, configuration/auth/readiness
contracts, and process/conformance tests. Remove the shared live
`config.Holder`, direct event/log bus, daemon stopper, forge client, channel
manager, capability registries, composite store access, and in-process UI
listener only after the focused criteria in
[`docs/prds/ui-service-boundary.md`](../prds/ui-service-boundary.md) pass in
production composition. The old construction and listener are deleted in the
same cutover change; no second UI authority remains.

### 2. Session and Messaging migration

The existing session implementation is the migration baseline. The migration must
decide:

- which session and message components are retained or adapted;
- how Agent, user, channel, Conversation, and branch ownership are added;
- how Archie's parallel gateway session implementation is replaced;
- how canonical Messages become immutable persisted records;
- how existing sessions and messages are migrated;
- how outbound delivery, retry, acknowledgement, deduplication, and failure
  semantics are represented;
- how current Telegram, email, webhook, forge, and Jira integrations migrate to
  Messaging infrastructure adapters.

The following are already decided and MUST NOT be reopened without contradictory
implementation evidence:

- the composite Conversation identity;
- immutable MessageIDs;
- typed tool-call and tool-result messages;
- exact branch lineage through ParentConversationID and ForkMessageID;
- parent and child transcript isolation;
- normal scoped-memory behaviour regardless of the originating Conversation;
- Messaging and Work Intake ownership.

**Decision (2026-08-31), resolving the open questions above:**

`internal/gateway/` is the "parallel gateway session implementation" this
section names. It is retained as-is for now — a big-bang rewrite of live
session/compression/approval/branch logic across Telegram, web UI, email, and
webhook channels in one change is exactly the "convoluted to satisfy edge
cases" failure mode CLAUDE.md's Revert-on-Flail rule warns about, and would
risk silent behavioural regressions in the one path every user-facing message
travels through.

Instead, `internal/domain/messaging/` is materialised now with the pieces
that are already decided and don't require touching gateway: the canonical
`Message` and `Conversation` types (composite Conversation identity, immutable
`MessageID`, typed tool-call/tool-result message variants, branch lineage via
`ParentConversationID`/`ForkMessageID`). These are pure domain types with no
gateway dependency — a new package, not a rewrite of an existing one.

Migrating `internal/gateway/`'s session, compression, approval, and branch
logic onto these types is real, cross-channel-risk work and is **not** done in
this pass. It is tracked as separate, appropriately-scoped bd issues (see
`bd list --label messaging-migration`), sequenced channel-by-channel so each
lands with its own behavioural verification instead of one large diff no one
can safely review.

**Amended 2026-09-15.** `internal/infrastructure/gatewayrpc.StoreClient` is the
adapter this migration will need: it implements `messaging.SessionStore` over
the chat service's session-store RPCs and is proven by
`TestStoreClientPreservesCanonicalRecords`. It is deliberately unwired.
Production `archied` dials the standalone gateway for `ChatContract` only
(`composeChatContract` → `gatewayrpc.Dial` returns a `*Client`, not a
`*StoreClient`), and the daemon's session store is in-process SQLite via
`makeTelegramSessionStore`. The test comment that says it mirrors production
describes the target, not today's tree. Kept rather than deleted because the
migration it serves is deferred by this very section, and re-deriving a tested
wire adapter once that lands is pure cost.

**Phase 4 Messaging Service extraction (2026-09-19, `archie-core-8cda.6.1`).**
The process boundary for external channels (Telegram, email, webhook) is ratified
in [`docs/prds/messaging-service-boundary.md`](../prds/messaging-service-boundary.md).
Messaging is extracted into `cmd/archie-messaging` / `internal/app/archiemessaging`,
calling Gateway's `messaging.ChatContract` exclusively over gRPC. Channel frontends
hold no direct database handles, daemon state, or model runtimes.

**Telegram operator surface after extraction (2026-09-19, `archie-core-8cda.6.6`).**
`setupTelegramGateway` wired eight `telegram.Gateway` seams the daemon could
satisfy from its own process. Each is resolved by where the fact it needs is
actually owned, not by where it used to be built:

| Seam | Resolution |
|---|---|
| `Version` | Sourced from the Gateway via `ChatSnapshot.Version`. The Messaging Service never stamps or infers a build version; `cmd/archie-messaging` deliberately takes no version ldflags, so it cannot report a partial upgrade as a matched one. |
| `Updates` (`/update`) | Built in the Messaging Service from `[chat.telegram].update_check_command` / `update_install_command` — its own configuration. `Enrich` is dropped: `componentInstallTypeEnricher` reads `[nats]`, which this service must not decode, so the check command's own reported install type stands unmodified. |
| `UpdateReportPath` | A local state file under `work_dir`, keyed by the same `sha256(bot_user)[:8]` scheme the daemon used, so a pending report is found rather than written afresh. |
| `ReleaseAnnouncements` | **Deliberately left nil**, for the same reason as `RunningVersions`. `releaseannounce.Announcer` announces nothing unless each `Component` carries a parseable version, and the only versions this service can obtain are the Gateway's own build stamps, not archied's. Wiring the announcer without them yields a no-op that reads as configured, so it stays unwired until component self-reporting exists. |
| `SetShowToolCalls` | Projected from `[chat].show_tool_calls`. |
| `Reload` | Local: re-resolves the token and allowlist from this service's own config file and overlay. Reload was never a daemon fact. |
| `RunningVersions` | **Deliberately left nil.** It exists to turn an installer's claim into a checked one, and only a component's own compiled-in build can vouch for it. The Messaging Service knows neither archied's build nor the observed archie-agent version (`daemon.AgentStatus`), and the Gateway's own `gatewayVersion` is a different binary's stamp — reporting it as the daemon's would manufacture exactly the false success the check exists to catch. Update reports therefore relay as unverified claims until a component self-report contract exists. |
| `Dangerous` | Stays nil. No daemon composition ever set it; `/rollback` and `/stop` reported "not configured" before the extraction and still do. |

The Messaging Service consequently reads `work_dir`, `bot_user`, `[chat]` and
`[health]` in addition to the PRD's `[chat.*]` / `[services.gateway]` list.
`[health]` is the daemon's health URL, which `releaseupdate.CommandInstaller`
polls from outside to confirm an update came back up; the rest of the PRD's
prohibition list is untouched.

**Phase 4 messaging severance: what must move, and the decision the PRD does
not cover (2026-09-22, `archie-core-1ng1`).** The PRD's deletion gate cannot
pass today. `cmd/archie-messaging/architecture_test.go` does not exist, and the
check that section specifies -- `go list -deps ./cmd/archie-messaging` links
zero banned runtime packages -- reports four:

| banned package | PRD category |
| --- | --- |
| `modernc.org/sqlite` | State Store |
| `internal/store` | State Store |
| `internal/domain/workflow` | Workflow engine |
| `internal/agentexec` | Workflow engine, via `internal/domain/workflow/agent.go` |

All four arrive through one import: `internal/app/archiemessaging/run.go`
imports `internal/app/controlplane`, whose own dependency set is exactly those
four. The Messaging Service uses three symbols from that package
(`NewRPCClient`, `RuntimeChatConfig`, `ChannelSettingsKind`); the package also
carries the store-backed `ResourceStore` server, the `Definition` registry and
the workflow step vocabulary. Sever the package. Amending the gate to tolerate
`modernc.org/sqlite` and `internal/store` is rejected: a boundary you whitelist
around is no longer a boundary.

**What moves.**

- Every kind constant. They are strings with no dependency, and the client names
  them.
- The channel-settings vocabulary and its decode: `channelSettings`,
  `telegramSettings`, `webhookChannelSettings`, `emailSettings`,
  `rateLimitSettings`, `channelDuration`, and `decodeRenamingLegacyKeys`.
- The generic client: `Client`, `NewRPCClient`, `RuntimeConfig` with
  `runtimeConfigFrom`, `runtimeChatConfigFrom`, the other `runtime*From`
  projections, and the `resourceReader` interface.
- The persona watch (`WatchPersonas`, `AppliedPersonas`), which needs only
  `internal/domain/agent`.
- `executionSettingsDocument`, the document alone.

**What stays, and why.**

- The store-backed server: `Server`, `NewServer`, `ResourceStore`,
  `ImportConfig`, `SeedSkip`, the `Definition` registry, and every other kind's
  seed, validator and `Normalize` hook.
- `WorkflowDefinitionsClient` and `stepRegistry`, which decode through
  `internal/domain/workflow`.
- `WatchWorkflowExecutionSettings` and `decodeSettings`: `decodeSettings`
  returns and validates `workflow.ExecutionSettings`, so any package holding it
  links the workflow domain and therefore `internal/agentexec`. This is why the
  client cannot move as one package -- see open question 2.
- The schedules document, whose type is `cronstore.JobSpec` in
  `internal/infrastructure`. A domain vocabulary package may not import
  infrastructure.
- The schema derivation, which describes the documents the server advertises.

**Placement as landed (2026-09-22, `archie-core-1ng1`).** One new package,
`internal/infrastructure/controlplanerpc`, holding both the vocabulary (kinds, the
channel-settings document and its decode) and the client
(`NewRPCClient`, `Query`, `RuntimeChatConfig`, the exported `ResourceReader`, the
wire sentinels and `ClientError`). A second `internal/domain/controlplane` package
for the vocabulary was planned and then measured out of the design: the vocabulary
has exactly one consumer, the client that projects it, and the store-backed side
reads the same definitions through type aliases rather than a copy -- so a separate
domain package would have been a package with one consumer and a second name for
one thing. What made that safe is a measurement, not a preference:
`internal/app/controlplane` was the ONLY direct import of the Messaging Service
that dragged a banned runtime (`gatewayrpc`, `staterpc`, `applystatus`, `messaging`,
`configuration`, `readiness` and `health` were all clean), so severing that one
dependency is sufficient, and the severance needed no third package. Promoting the
vocabulary to a domain package stays open for the day a second consumer appears.

**Settled by the implementation.** Two of the four open questions this decision
originally left are answered, in both cases by measuring rather than by preference:

- The client did NOT become two packages. `WatchWorkflowExecutionSettings` and
  `WatchPersonas` stayed on `internal/app/controlplane`'s `Client`, which now
  *embeds* the new one, so the daemon's validated watch is still defined once and
  the read path still has one implementation -- which is what the second package
  was for -- with one fewer package to keep in step.
- The schedules and workflow documents stayed put, and
  `internal/domain/workflow/task` is linked deliberately: it carries the Task
  status and view vocabulary, its own dependency set holds none of the banned
  runtimes, and `cmd/archie-messaging/architecture_test.go` carries it as an
  exception checked in both directions (the same entry, for the same reason, as
  `cmd/archie-ui/architecture_test.go`).

**Still open.**

1. Does the server also leave `internal/app/`? `organisation.md` reserves
   `internal/app/` for wiring and composition, and a store-backed gRPC server is
   infrastructure -- but the gate does not require the move, and it would multiply
   the diff across archied's wiring and the State Store binary. Tracked as
   `archie-core-8f4n`.
2. Does `internal/domain/workflow`'s contract/engine split happen, and when? A
   contract type (`WorkflowDefinitionCollection`) drags `internal/agentexec` only
   because `agent.go` sits beside it. Independent of this severance, tracked as
   `archie-core-inh6`, and the shape `archie-core-8cda.7` will meet again.
3. Does the vocabulary move to a domain package when a second consumer appears?

**Both consumers keep compiling.** archied imports the new vocabulary for the
kinds it names and the new client package for `RuntimeConfig` and the persona
watch, and keeps `internal/app/controlplane` for the server, the registry,
`ErrVersionConflict` and the workflow-aware pieces. The State Store binary
continues to call `NewServer` and `ResourceStore` unchanged. The Messaging
Service then imports only `internal/infrastructure/controlplanerpc`,
`gatewayrpc` and `staterpc`, so `go list -deps ./cmd/archie-messaging` loses all
four banned packages and the gate can land green.

### 3. Identity data migration

The migration must define:

- the durable identity schema and repository implementation;
- bootstrap of the permanent system identity;
- stable IdentityIDs for existing configured Agents;
- migration from configured identity names, empty-string ownership, and bot
  usernames;
- preservation of historical attribution;
- identity-aware uniqueness and transport deduplication;
- correction of container and RPC capability resolution so execution uses the
  owning identity's bindings;
- removal of identity-string equality from access-control behaviour.

Exact schema and method signatures are implementation decisions. The approved
Identity lifecycle and attribution semantics are fixed migration constraints.

### 4. Workflow migration

The current implementation must be transformed deliberately:

```text
store.Task            -> WorkflowExecution
mutable Stage         -> StepExecution history
RetryCount / Attempt  -> Attempt records
workflow.Registry     -> versioned Workflow definitions
current stages        -> WorkflowStep definitions or Workflow plugins
```

The migration must decide:

- Workflow definition and version persistence;
- the smallest accepted-work-request contract consumed from Work Intake;
- the enforced WorkflowExecution state machine;
- atomic state and domain-event persistence;
- migration of existing task rows and transition history;
- disposition of current skill, Yaegi, and stage-plugin implementations;
- the versioned worker-handoff contract;
- scheduler, claim, retry, crash-recovery, and waiting-human cutover.

Workflow remains a separate domain from Agent. A user or Agent may define or
invoke a Workflow, but Workflow semantics and plugins remain Workflow-owned.

### 5. Memory placement and storage

The four required scopes are fixed:

- global shared;
- Agent-wide;
- user-wide;
- Agent-user relationship.

A focused review of `internal/memory`, its providers, consumers, persistence,
and tests had these decisions:

- `internal/domain/memory` versus ownership within the Agent domain — settled
  (2026-09-02, below);
- the authoritative memory record and revision model — settled by the PRD
  (2026-09-16, below);
- provider and infrastructure boundaries — settled by the PRD (2026-09-16,
  below);
- retrieval and access enforcement — settled by the PRD (2026-09-16, below);
  ranked retrieval remains open (below);
- migration of existing memory data — open: existing data is not migrated;
- provenance representation — settled by the PRD (2026-09-16, below).

Conversation branches do not create another memory scope. A memory action has
the same scoped effect regardless of which Conversation originated it.

**`internal/domain/memory` ownership — DECIDED (2026-09-02).** The typed
engine family lives at `internal/domain/memory`
(archie-core-1786637490069-11-e245c170), mirroring `internal/domain/curator`'s
contract/registrar/registry split. Not owned within the Agent domain: memory
is consumed by more than agent turns (curators, per archie-core-1786637499636)
and the plugin engine rule requires its own family, not a capability bolted
onto another domain's contract.

**Authoritative record and revision model for the builtin engine — DECIDED
(2026-09-02),** scoped to unblock archie-core-1786637499480 (ship the
file-writing store as the default engine). This decides only the builtin
engine's mapping onto the new `Record{ID}`/`Forget(id)` contract, not the
memory model in general or a future non-file engine's model:

- `internal/memory/builtin.Store` is unchanged. It has no concept of a
  stable per-block ID — blocks are addressed by content substring — and nothing
  in this decision requires changing that; existing `internal/memory` tests
  keep passing unmodified, per that issue's acceptance criteria.
- A new infrastructure adapter (`internal/infrastructure/memory`, per
  `organisation.md`'s "infrastructure implements domain contracts" rule — not
  under `internal/domain/memory`, which stays contract-only) wraps one
  `builtin.Store` per identity.
- **Identity → directory:** `hex(sha256(identity))`, not the identity string
  itself. Closes the path-traversal question without an allowlist regex, and
  sidesteps collisions from identities that differ only in characters a
  filesystem treats as equivalent.
- **Record ID:** assigned once at `Write`, independent of content, so a later
  edit elsewhere in the file can never orphan a `Forget`. Realized as a
  leading marker line (`<!--mem:<ulid>-->`) on the block `builtin.Store.Add`
  writes; `Query`/`List` strip it back out of `Record.Content` before
  returning, and `Forget(id)` locates the block by that unique marker via the
  store's existing substring-based `Remove`. The marker is a pragmatic,
  reversible choice — plain-text records stay human-readable and
  human-editable, at the cost of one marker line an operator editing the file
  by hand should leave alone. Revisit if/when provenance representation
  (still open, below) settles on something richer.
- **Kind → section:** `Observation.Kind` (default `"general"` when empty) is
  the section name, reusing `builtin.ValidateSectionName` — so an
  invalid `Kind` fails `Write` with the same validation error `memory_edit`
  already returns for an invalid section today, not a new error shape.

**Memory engine: scopes, CRUD with retained revisions, and store placement —
DECIDED (2026-09-16).** Supersedes the 2026-09-02 "Authoritative record and
revision model for the builtin engine" entry above, whose `<!--mem:<ulid>-->`
live marker cannot carry provenance. Authority:
[`docs/prds/memory-engine-unification.md`](../prds/memory-engine-unification.md)
(Approved); the contract is `internal/domain/memory/contract.go`. This register
supersedes; it does not erase.

- **Four typed scopes, caller-named.** `internal/domain/memory` is addressed by
  `Scope{Kind, Agent, User}` over `global`, `agent`, `user` and `agent-user`,
  with `Validate` rejecting a component present where it is not required. A
  caller names the read and write sets (`Subject.Scopes()` /
  `WritableScopes()`; global is never model-writable) and the engine applies no
  policy of its own: **access control is naming a scope, and nothing else.** An
  engine that enforced policy would need to know about channels, sessions and
  bindings, and every backend would reimplement it. `Scope.Key()` length-prefixes
  each component, so two scopes can never share a key even when an opaque
  channel-native id embeds the separator.
- **The caller resolves identity; the engine never does.** Resolution happens in
  the gateway turn path, where the native sender id and the session are both in
  hand, and is supplied per channel by the composition. A webhook's sender id is
  a route path, not a person, and treating it as one would give a URL an
  identity. An absent user id yields global and agent scopes only: "show none of
  it", never a wider fallback.
- **CRUD supersedes, never overwrites.** `Store` is `Create`/`Get`/`Query`/
  `List`/`Update`/`Forget`/`Revisions` over the four scopes. `Update` appends the
  superseded state to the scope's `HISTORY.md` before replacing the live block;
  `Update.Expected` names the revision being replaced and returns
  `ErrStaleRevision` on mismatch, making a lost update between two processes
  sharing one scope file detectable. `Forget` records a deleted revision before
  removing the live block, so a record's provenance outlives its content; `Get`
  and `Forget` take a scope, because an id alone does not locate one.
- **Provenance representation.** The live marker becomes JSON, since a
  `<!--mem:<ulid>-->` line cannot carry provenance, and `Record` retains the
  provenance `agent-system.md` requires — scope, author, originating user,
  source, and revision history. `Metadata` is dropped: it never round-tripped.
- **Store and scanner placement.** The markdown store relocates to
  `internal/infrastructure/memory/builtin`, its correct home under
  `organisation.md`'s "a package owns its on-disk format end-to-end". The content
  scanner moves into `internal/domain/memory` and is applied in `Create` and
  `Update` — the single choke point every producer crosses, so curator-extracted
  content is covered too.

**Open in this section.** Ranked retrieval is deferred: `Query.Text` stays in
the contract, unused by the prompt path. Existing memory data is not migrated:
`<workDir>/memory` and `<workDir>/memory-engine` are left on disk, unreferenced.
The PRD leaves these undetermined and they remain open here: group-chat
extraction, where the curator fails closed on mixed-sender sessions; two
processes (`archied` and `archie-gateway`) holding one scope file, where
`Update.Expected` detects but does not prevent a lost block; unbounded
`HISTORY.md` with no compaction policy; whether the per-scope size bound is
right; and whether the dashboard should carry a user identity.

### 6. Runtime and process boundaries — DECIDED

`archied` is the native resident orchestration and interactive-chat process.
Every autonomous repository workflow crosses one full-task core-NATS handoff
into a task-scoped `archie-agent` container. Embedded versus external NATS
changes broker deployment only; it never selects an executor. The former host
in-process runner, per-stage NATS protocol, and subprocess runner are removed.

Forge, store, and worktree authority remains in `archied` and is exposed to the
task container through scoped RPC services. The worker runs the workflow and
its worker-local ai-sdk loop; it does not receive forge credentials or direct
store access. See `docs/prds/embedded-nats.md`.

**UI Service boundary — RATIFIED (2026-09-07).** Phase 3 has a focused
authority record in [`docs/prds/ui-service-boundary.md`](../prds/ui-service-boundary.md).
The UI Service owns HTTP/SPA delivery, UI DTOs, browser authentication, and
its own endpoint/credential/readiness settings. It consumes Gateway and State
Store contracts and does not receive `config.Holder`, daemon pointers, SQL or
store implementations, workflow registries, forge/channel/model runtimes, or
resolved secrets. The current `internal/webui` implementation remains inside
`archied` until the ratified migration gates prove route parity, adapter
selection, failure/readiness behavior, and deletion of the shared-holder and
direct-access paths. The extraction cutover removes the in-process UI
listener in the same change; there is no dual-live UI authority.

**Dashboard live event delivery — DECIDED (2026-09-09, `archie-core-za9f`).**
The extracted UI receives live activity by polling the State Store's existing
`EventsSince` cursor, not by a new push-subscription RPC. No proto change, no
contract amendment beyond this record.

The UI process runs one event pump. It primes its watermark by paging
`EventsSince` to the end at startup without delivering, then polls
`EventsSince(watermark, limit)` on an interval and hands each new event to
`webui.Server.Broadcast`, exactly where `internal/app/archied/main.go`'s
`persistAndBroadcastEvents` hands events to it today. One pump serves every
connected browser, so cost is one indexed query per interval regardless of
viewer count, and `Broadcast` is a no-op when nobody is watching.

Three properties make the cursor sufficient, and a push hub unnecessary:

- **The cursor cannot skip.** `internal/store/store.go` opens the database with
  `SetMaxOpenConns(1)`, so writes serialise and a row's `id` is assigned in
  commit order. There is no window in which a lower id becomes visible after a
  higher one, which is the failure mode that normally rules out polling an
  autoincrement cursor. SSE catch-up already depends on this same property.
- **A broadcast is a wakeup, not the payload.** `sseStream.drain` treats each
  broadcast event as a signal to fill the persisted gap ahead of it via
  `catchUp`, then delivers the event only if catch-up did not already cover
  it, deduplicating on the `since` watermark. Substituting a poller for the
  in-process bus therefore changes no SSE semantics, and needs no change to
  `internal/webui/sse.go`. It also means a broadcast dropped to a stalled
  client self-heals on the next one.
- **The table is the only complete fan-out point.** Events reach the store
  through several paths that never touch the daemon's bus, including the
  audit event `store.ArchiveTask` writes inside its transaction
  (`internal/store/store.go`) and the binding-dispatch failure
  `internal/daemon/daemon.go` inserts directly. A push hub would have to be
  hooked at every write site and at post-commit, and would silently miss any
  site added later that forgets to notify. Reading the table misses nothing.

The cost is one poll interval of latency on the activity feed, default 1s and
operator-tunable. That is accepted: the feed reports task lifecycle history,
and an operator action already gets its own synchronous HTTP response.

This resolves the `/events` row of the route-owner inventory in
[`ui-service-boundary.md`](../prds/ui-service-boundary.md). `/api/logs` and
`/api/logs/stream` are not resolved by it, they read the daemon's in-process
diagnostic feed and remain host-local (see `archie-core-8cda.5.4`).

Revisit only if sub-second feed latency becomes a requirement, or if a second
consumer needs the same stream, at which point the fan-out belongs in the State
Store process behind a `SubscribeEvents` RPC and this record is superseded.

**Dashboard configuration page — DECIDED (2026-09-09, `archie-core-ymut`).**
The configuration page becomes read-only in the UI process for Phase 3. Reads
cross the boundary as a published snapshot; writes are descoped, not
contracted. This amends the ratified boundary, which assumed a single admin
contract carrying both.

**Reads.** `webui.ConfigView` is already the whole answer. It is a UI-owned,
secret-free value projection built in `handleConfig`: `ProviderView` carries
the API key's environment variable name and a `Configured` boolean, never a
value, and `RepoView` is repository metadata only. It is already the exact
shape the browser receives. So the daemon publishes that projection, plus
provenance, reload status and the overridden-key list, as a snapshot the UI
reads; nothing new has to be designed, only moved. Two State Store RPCs carry
it, a `PutConfigSnapshot` restricted to the administrative token and a
`GetConfigSnapshot` the UI calls. The daemon republishes on boot and after
each successful reload, which is precisely when the projection changes.

The State Store is the right holder because a published configuration snapshot
is shared state, it is already dialled by the UI, and it costs no new listener,
port, token or interceptor. The alternative, a config admin service on
`archied`, would make the daemon a gRPC server for the first time. Today it is
purely a client of Gateway and State Store; `grpc.NewServer` appears only in
`RunGateway` and `RunStateStore`, which are the standalone binaries' entry
points, not the daemon's.

**Writes.** `PATCH /api/config`, `POST /api/config/reset` and
`PATCH /api/config/repos/{owner}/{name}` answer 503 in the UI process, and the
SPA hides those controls rather than offering a button that fails. Editing is
`config.toml` plus reload, which is already the only path for every setting
outside the runtime-tunable allowlist.

This is a deliberate feature reduction and worth naming as one. The write path
is not hard because of the transport; it is hard because the policy is
daemon-local and stays that way: `installUpdateConfigHandler` denies
non-runtime-tunable keys, applies a dotted overlay to a clone of the published
config, validates the materialised result, writes overlay rows, and rebuilds
provenance. Contracting that means either moving the policy, which the
boundary forbids, or exposing it verbatim as remote procedure calls, which
builds a Configuration Service inside a migration bead. Neither is work this
phase should be doing to keep one page's buttons alive.

Revisit when dashboard configuration editing is picked up as a feature in its
own right (`archie-core-1786637498420-327`, still open). At that point it gets
an admin contract designed as a feature, and the Configuration Service this
document already anticipates is its natural owner. Until then the UI renders
configuration and does not write it, which is what the boundary always said
about ownership even when it assumed a wider contract.

Amended 2026-09-11. This descope is a deferral, not a decision that the
dashboard should be read-only. Name it that way in review: the daemon-side
policy was built and tested, then deleted by the cutover (`e037b95` removed
`internal/app/archied/config_update.go` and its tests), so reviving it is
recoverable work, not a rewrite. `archie-core-j28m` carries the remaining
question -- which process serves the policy and what transport carries the
write -- and blocks the feature bead. The handlers, their error sentinels and
the `UpdateConfig`/`ResetConfig`/`UpdateRepoField` seams on `webui.Server` are
kept deliberately for it; `ConfigView.Editable` follows whichever process holds
`UpdateConfig`, so wiring that one field makes the page editable
(`archie-core-ml30`).

Implementation belongs to `archie-core-8cda.5.4`, the cutover that removes the
daemon's own dashboard. Nothing here is built yet.

**Superseded 2026-09-21 by the runtime control plane.** The open question this
descope left -- which process serves the policy and what transport carries the
write -- was answered by `docs/prds/runtime-control-plane.md`: neither a daemon
admin service nor a revival of the daemon-local policy. `ControlPlaneService` on
the State Store's existing gRPC server owns every database-backed setting, and
each feature validates its own resource, so there is no daemon-local policy left
to contract. `archie-core-j28m` is closed.

Consequently the three write seams described above no longer exist.
`PATCH /api/config`, `POST /api/config/reset` and
`PATCH /api/config/repos/{owner}/{name}`, the `UpdateConfig`/`ResetConfig`/
`UpdateRepoField` fields on `webui.Server`, and `ConfigView.Editable` were
deleted in `31622ec5` along with the runtime overlay store they wrote to.
`GET /api/config` remains a read of the published projection, as this decision
always intended.

### 7. Shared mechanics

The following cross-domain mechanics require exact contracts before dependent
packages migrate:

- event-bus delivery and failure semantics;
- policy evaluation API, composition, and precedence;
- configuration candidate preparation, health validation, atomic promotion,
  observation, and rollback;
- service-specific health, bounded remediation, isolation, and escalation;
- changelog association and grouping.

These shared mechanisms do not acquire ownership of domain meaning. Domains
continue to own their commands, events, policies, settings, and consequences.

The Go documentation generator, its output placement, and drift checking are
defined in `generated-documentation.md`. The VitePress site and its Pages
deployment were removed on 2026-09-12 as an unnecessary build step; `docs/` is
repository documentation only. The rendering and publishing surface is an open
decision, and so are the renderer's page templates and any developer commands
or CI sequence that would accompany one. Adapting the generator and implementing
Archie's normalized documentation model remain migration work rather than open
product architecture.

**Amended 2026-09-15.** `internal/infrastructure/servicediscovery/nats`
implements the `servicediscovery.ServiceRegistry` contract (Connect, Register,
Resolve, Watch) over NATS KV and is fully tested, but no production code
constructs it and no config surface exposes it. This is a deliberate deferral,
not an oversight: service-to-service addressing belongs to the runtime and
process boundary work in section 6, which currently pins container addressing
by injecting the daemon's startup NATS URL rather than resolving through a
registry. The scaffold is kept for that work; re-derive it only if the
addressing model changes.

### 8. Cutover sequence

The final migration plan must order:

- compatibility adapters;
- schema migrations and data backfills;
- any required dual-read or transitional paths;
- domain-by-domain package replacement;
- production wiring changes;
- legacy package removal;
- architecture tests preventing dependency regression;
- focused, integration, race, and full quality gates.

Each legacy package requires objective removal criteria. No compatibility path
may become an indefinite second implementation.


## Completion criteria

Migration design is complete when:

- every current package has one approved owner and destination;
- every mutable record has one authoritative owner and migration path;
- all external and cross-process contracts have versioned owners;
- existing behaviour and data have explicit parity requirements;
- process boundaries have evidence-backed justification;
- the cutover order preserves a working application;
- legacy deletion criteria and deterministic architecture tests are defined;
- no unresolved decision requires an implementer to invent domain semantics.

Exact Go APIs, SQL statements, and mechanical refactoring steps MAY be finalized
during implementation when they do not change these semantics.

## Recorded migration position (2026-07-29)

Recovered 2026-08-09 from the pre-migration issue tracker. This section records
decisions the maintainer approved that the repository did not otherwise capture.

**Completed:** `internal/infrastructure/eventbus/nats` (rebuilt, not relocated);
`internal/infrastructure/configuration` (loading moved out of `internal/config`,
which is now types-only and does no I/O); `internal/eventbus` (broker-neutral
contract); `internal/domain/workintake` (the first `internal/domain/` package,
owning `TaskEnvelope`, the routing `Kind`, the label vocabulary, and task
subjects).

**DECISION — private input DTO deferred (approved).**
`configuration.Document.Config` is still `internal/config.Config` rather than the
private per-domain input DTO this document mandates. Building an ~800-line
parallel type tree while every consumer still reads `config.Config` would mean
two shapes kept in sync by hand, with no consumer for the new one. It is named as
a field so the compromise is visible at each use site, with the deletion
condition recorded in that package's `doc.go`. Do this **with** the
`internal/config` dissolution, not ahead of it.

**Agreed next: `internal/app/{archied,agentworker}`.** `run()` in
`cmd/archied/main.go` is ~460 lines, against `organisation.md`'s rule that
`cmd/*` must not contain substantive wiring or act as a service locator. It is
also the last non-infrastructure holder of the NATS SDK, and it is where the
config DTO translation will land, so it unblocks the dissolution above.

**Method constraint.** Do not write a separate recorded package-review document
per area when the decisions are already dictated by
`dependencies-and-contracts.md`. That is the ceremony pattern this project
deliberately avoids.

## OPEN — `[chat.*]` config section naming

`configuration.md:160` already states the position: `ChatConfig` holds "separate
channel instance settings... there is no chat-wide settings owner". The config
section name has not followed, so a settings block named for a product surface
owns what the architecture calls channel state.

Two candidates, neither settled: `[channel.*]` and `[messaging.channel.*]`. The
second reads as a domain path, and no config section in this repo is currently
named after a domain package, so adopting it sets a precedent for every other
section rather than fixing one name.

Blocked on a maintainer decision, not on evidence. This is a rename with an
operator-visible TOML break, so it should land with the `internal/config`
dissolution recorded above rather than on its own.
