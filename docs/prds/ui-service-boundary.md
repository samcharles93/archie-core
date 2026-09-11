# UI Service boundary and configuration ownership

**Status:** Ratified (rev. 1)
**Date:** 2026-09-07
**Beads issue:** `archie-core-8cda.5.1`
**Parent:** [`service-decomposition.md`](service-decomposition.md)
**Prerequisites:** Gateway contract extraction and State Store contract ratification

This document is the authority for Phase 3 UI Service implementation. It fixes
the process boundary, configuration ownership, client contracts, security
posture, readiness behavior, and migration evidence required by
`archie-core-8cda.5.2` and later UI extraction work.

## Decision

The UI Service owns the operator-facing HTTP surface and the compiled SPA. It
serves `ui/dist` and exposes UI-specific JSON DTOs for dashboard views and
actions. It does not own conversations, task state, workflow execution,
channel lifecycle, forge access, or the daemon's runtime configuration.

The UI Service consumes two typed remote capabilities:

- Gateway's `gateway.ChatContract` through the versioned Gateway gRPC
  contract, for chat snapshots, messages, turns, streaming, persona changes,
  cancellation, and Gateway task actions.
- State Store's narrow read/write contracts through the versioned State Store
  gRPC contract, for task views, events, captures, mappings, bindings, and
  configuration data explicitly projected for the dashboard.

The UI process may compose local adapters during the migration seam, but the
adapter implements the same wire-safe contract as the remote client. The
production extraction cutover removes the in-process adapter in the same
change; a dual-live UI authority is not permitted.

## Boundary and ownership

| Concern | UI Service owns | UI Service may consume | UI Service must not own or receive |
|---|---|---|---|
| HTTP and SPA | routes, auth middleware, UI DTOs, assets, HTTP lifecycle | contract results and health snapshots | domain entities or infrastructure handles |
| Chat | request validation and response projection | Gateway `ChatContract` | `gateway.Router`, `SessionStore`, turn runner, model/provider runtime |
| Task dashboard | query/action DTOs and HTTP status mapping | State Store narrow contracts and explicitly ratified task-action contract | `*store.Store`, SQL connections, workflow registry, daemon pointer |
| Configuration page | secret-free view and update request validation | a UI configuration contract owned by the configuration/application boundary | `config.Config`, `config.Holder`, secret registry, reload implementation |
| Readiness | UI process liveness and dependency readiness aggregation | bounded health results from Gateway and State Store | direct probing of private databases or providers |

Every value crossing the process boundary is a versioned DTO or scalar contract
value. `internal/webui` may retain temporary Go-side adapters while the
migration is in progress, but handlers must map Gateway/State Store values to
UI-owned response types at the HTTP edge. A constructor accepting a concrete
Gateway, State Store, daemon, or `config.Holder` is not an approved final
boundary.

The existing UI routes remain behaviorally compatible during extraction,
including task actions, SSE event delivery, config views, chat streaming,
capture intake, mappings, bindings, update reporting, and health endpoints.
Compatibility preserves status codes, authentication requirements, redaction,
pagination/limits, event ordering, and failure degradation unless a separately
ratified contract changes them.

The route families have these target owners. This is the required inventory for
the implementation children; a route without an owner is not eligible for
cutover.

| Route family | Target owner consumed by UI | Migration status |
|---|---|---|
| `/api/chat/*` | Gateway `ChatContract` and its versioned client | contract seam exists; DTO and stream parity remain |
| `/api/tasks/*`, `/api/workflows`, `/api/setup` | State Store read/action contracts plus an explicit execution/admin action contract | State Store adapter exists; UI-facing narrow surface remains |
| `/events` | State Store `EventsSince`, polled by one UI-process pump feeding `Server.Broadcast` | RESOLVED (`archie-core-za9f`): see migration-decisions, "Dashboard live event delivery". No new RPC; replay cursor and drop-recovery are the existing `since` watermark in `sse.go` |
| `/api/logs`, `/api/logs/stream` | daemon diagnostic feed, host-local | unresolved: `logging.Feed` is in-process on the daemon host and has no contract |
| `/api/captures`, `/api/mappings`, `/api/bindings` | State Store contracts, with webhook verification owned by Work Intake/Messaging | RESOLVED (cutover, `archie-core-8cda.5.4`): the UI process composes CaptureStore/MappingStore/BindingStore and mounts the `captureintake.Receiver` from its own State Store client; the daemon's binding-dispatch loop keeps consuming captures from the same store |
| `/api/config` (read) | daemon-published `ConfigView` snapshot, read over the State Store contract | RESOLVED (`archie-core-ymut`): see migration-decisions, "Dashboard configuration page" |
| `/api/config` (write), `/api/config/reset`, `/api/config/repos/*` | none; descoped for Phase 3 | RESOLVED (`archie-core-ymut`): 503 in the UI process, SPA hides the controls, editing is config.toml plus reload |
| `/api/channels`, reload, curators, memory, skills, version/update | owning capability contract or an explicitly removed route | owner and failure semantics remain to define |
| `/`, `/healthz`, `/health`, `/health/detailed` | UI HTTP process; readiness consumes per-service health contracts | current HTTP behavior is the compatibility baseline |

## Configuration ownership

The UI Service owns only process-local settings:

- HTTP listen address and read/header/shutdown timeouts;
- UI asset source or build mode;
- UI authentication mode and token reference;
- Gateway endpoint and credential reference;
- State Store endpoint and credential reference;
- readiness endpoint and dependency timeout policy.

These settings are decoded into a UI-owned input DTO by the UI composition root.
The DTO contains endpoint references and secret references, never resolved
secret values or complete daemon configuration. The UI process resolves its
own credentials and builds its own clients. It does not read or mutate the
daemon's configuration file, runtime overlay, forge credentials, model catalog,
workflow routing, NATS settings, filesystem jail, or agent/container settings.

The dashboard configuration API is a separate capability. Its secret-free,
allowlisted field projections remain governed by
[`config-schema.md`](config-schema.md). During the transition the daemon may
continue to perform validate-persist-publish for those updates through a
narrow application contract. The UI process must never implement that policy
by holding or mutating a shared `config.Holder`.

The current daemon's `config.Holder` sharing is migration residue: `buildDaemon`
currently assigns `b.web.Cfg = b.d.Cfg`. It is removed only when the UI has a
configuration read/update contract and tests prove that the daemon and UI no
longer share a mutable holder.

For Phase 3, configuration ownership is split deliberately. The UI process
owns its listener, browser-auth, asset, dependency-target, and readiness
settings. The daemon/application configuration owner remains authoritative for
the dashboard's `/api/config` read, validation, persistence, overlay, reload,
and audit behavior. The UI does not become the writer merely because it
renders the configuration page.

Amended 2026-09-09 by `archie-core-ymut`: this section previously assumed one
narrow admin contract carrying both the read and the write. It does not. The
read crosses as a daemon-published `ConfigView` snapshot over the State Store
contract, and the write is descoped for Phase 3 rather than contracted, on the
reasoning recorded in migration-decisions under "Dashboard configuration
page". A future dedicated Configuration Service is the natural owner of a
write contract, and designing one is that feature's work, not this migration's.

## Listen, authentication, and readiness

The extracted UI follows the current web UI security posture:

- loopback listeners may omit a dashboard token;
- non-loopback listeners require a non-empty token and fail closed when it is
  absent;
- `/healthz` and `/health` are unauthenticated liveness probes;
- `/health/detailed` is authenticated and reports dependency readiness;
- capture webhooks retain their separately ratified source authentication and
  rate-limit rules; they are not admitted by the dashboard token;
- Gateway and State Store credentials are service-to-service credentials, never
  browser-visible dashboard tokens.

The UI is ready only when its HTTP server is serving and each mandatory remote
contract has passed its bounded readiness check. Each dependency must expose a
versioned health result or health endpoint that the UI can consume; the UI must
not inspect another service's concrete manager, registry, or database. Optional
dashboard sections may remain degraded with the current explicit
`503`/unavailable behavior. A successful liveness response must never imply
dependency readiness.

## Migration sequence and evidence

The implementation must complete these gates in order:

1. Define the UI input DTO, Gateway client surface, State Store client surface,
   and UI response DTOs. Add compile-time contract assertions and mapping
   tests. No handler may depend on a concrete remote implementation.
2. Make the current in-process web UI consume the same narrow adapters and
   preserve the current route, auth, redaction, stream, SSE, and degradation
   behavior. Add a composition test proving the selected adapter is the one
   used by production wiring.
3. Remove direct `internal/gateway` session/router/runtime access from web UI
   handlers. Remove direct `internal/store` implementation access, SQL access,
   daemon pointers, and broad runtime callback bundles from the UI constructor.
4. Replace the shared `config.Holder` with the UI-owned configuration DTO and
   narrow configuration application contract. Prove reload/update atomicity
   remains owned by the configuration/application boundary.
5. Add the standalone UI process and deployment wiring. It must obtain Gateway
   and State Store endpoints from its own configuration, authenticate each
   client independently, start liveness/readiness endpoints, and shut down
   clients and HTTP listeners in bounded order.
6. Run dual-adapter contract tests and an HTTP parity suite covering every
   retained route. Test remote failures, missing credentials, malformed DTOs,
   stream cancellation, stale State Store data, and readiness degradation.
7. Cut over the supported composition to the UI process. The old in-process
   `webui.Run` path is deleted in the same change; no fallback or second HTTP
   authority remains.

The deletion gate is objective: `go list -deps` from the UI process shows no
daemon, SQL/store implementation, workflow, forge, channel, model, or secret
runtime package; production composition contains one UI HTTP listener; and
architecture tests reject `config.Holder`, concrete `*store.Store`, concrete
Gateway runtime types, and direct SQLite imports in the UI service.

Rollback is a deployment composition change that points the operator at the
last validated UI binary and restores the previous endpoint configuration. It
must not restore a second writer or reintroduce shared-holder mutation. UI
state is either remote contract state or existing durable state owned by its
current service; no UI-specific database migration is introduced by this
boundary decision.

## Current evidence and known delta

Current code proves the following migration inputs:

- `internal/app/archied/bootstrap.go:setupObservability` constructs
  `webui.Server` and wires store, channels, events, logs, and config-related
  callbacks.
- `internal/app/archied/bootstrap.go:buildDaemon` replaces the initial holder
  with `b.d.Cfg`, explicitly sharing the daemon's live configuration.
- `internal/webui/api_chat.go` calls the Gateway contract but still imports
  Gateway-owned request/value types; handlers must finish the DTO projection.
- `internal/webui/server.go:Handler` owns the route set, SPA fallback, health
  split, capture bypass, and token wrapper.
- `internal/app/archied/state_store.go:RunStateStore` owns the task SQLite
  file and serves the State Store gRPC contract; the UI must use that contract
  rather than opening the file.
- `internal/app/archied/gateway.go:RunGateway` serves Gateway gRPC and owns
  Gateway session persistence; the UI must use `ChatContract` rather than
  Gateway internals.

The current code therefore satisfies the upstream prerequisites but does not
yet satisfy this boundary. This document is the ratified target; the
implementation child must prove each migration gate before claiming Phase 3
complete.

### Deletion gate: SATISFIED (archie-core-8cda.5.6, rev. 2)

The objective gate now holds. `go list -deps ./cmd/archie-ui` links **zero**
banned packages: no `modernc.org/sqlite`, no `internal/store`, `internal/gateway`,
`internal/channels`, `internal/memory`, `internal/secret`, `internal/agentexec`,
`internal/tools`, `internal/skill`, `internal/yaegiutil`, `internal/domain/curator`,
or the workflow engine (`internal/domain/workflow`). How the runtime handles were
severed, each at the producer:

- **Store contracts** moved to `internal/domain/storecontract` (producer-owned
  read contracts, state-store-contract rev. 2d); `internal/store` keeps aliases.
- **Chat contracts** moved to `internal/domain/messaging`; `internal/gateway`
  keeps aliases. The UI process holds `ChatContract`, `SessionStore`, turn and
  stream types without linking the gateway runtime or its SQLite session store.
- **Webui-owned views** replaced concrete runtime handles on `webui.Server`:
  channel lifecycle via `internal/channels/status`, memory via a `MemoryStatus`
  interface, curators via a `CuratorStatus` interface, and the skill catalogue
  via a `SkillCatalog` interface — each supplied by a daemon-side adapter at
  bootstrap, each degrading to an empty page when unwired.
- **`config.SecretRef`** moved into `internal/config` (it is config vocabulary);
  `internal/secret` keeps an alias and resolves through `Registry.Resolve`.
- **Task vocabulary** (`Task`, `Store`, `Definition`, status constants) split
  into `internal/domain/workflow/task`; the pipeline engine stays in package
  `workflow`, which keeps aliases. The UI process links the vocabulary only.

The gate is enforced, not just measured. `cmd/archie-ui/architecture_test.go`
re-runs `go list -deps` on every `task check` and fails on any package in a
banned category, so a single reference to a type sitting beside a runtime
cannot quietly relink it. Its exception list (`internal/channels/status`,
`internal/domain/workflow/task`) is checked in both directions: an exception
the UI stops needing is reported, so the allowlist cannot decay into an open
prefix. `internal/app/archieui`'s `TestComposeUIServerHoldsNoDaemonState`
covers the composition clauses, failing on a non-nil `config.Holder`, a
concrete `*store.Store`, or a Gateway that is not the gRPC client.

One piece of residue remains visible: `webui.Server` still declares
`Cfg *config.Holder`, because the daemon keeps a `webui.Server` as the renderer
that builds the published `ConfigView` snapshot. The UI process never receives
a holder, and the composition test fails if it ever does, but removing the
field outright means moving that renderer and the readiness probes off
`webui.Server` onto the daemon's own configuration owner.

Remaining for Phase 3 closure: the end-to-end smoke suite over real processes.

## Non-goals and open work

This ratification does not define a new domain model, move the dashboard's
allowlisted configuration schema, redesign Gateway or State Store RPCs, choose
Kubernetes versus local discovery, or authorize unrelated package migration.
Those decisions remain in their existing authority documents. Any new UI
capability that requires a wider Gateway or State Store surface must amend that
service's contract before implementation.
