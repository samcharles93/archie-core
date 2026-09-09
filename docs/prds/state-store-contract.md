# State Store — contract boundary & transport (ratification)

**Status:** Ratified (rev. 2c). Rev. 1 was marked *conditionally ratified*; the three
reviewer conditions it raised are resolved (rev. 2), and the two third-pass findings are
resolved here: (a) the stale Q3 migration wording in `service-decomposition.md`
(`store.WorkflowStore`, `mode = "inproc" | "remote"`) now matches the ratified ownership split
and presence-based `[services.<name>].target`; (b) the State Store listener topology is a
single explicit rule (bridge address + token when agent containers consume it, loopback-only
for a daemon-local-only consumer) instead of the contradictory loopback-vs-bridge wording.
This is the pre-implementation design document that Phase 2 subtasks `.4.2` (generalize
`storerpc` as the State Store gRPC contract) and `.4.3` (stand up `archie-state-store`)
implement against.
**Date:** 2026-09-06 (rev. 2c)
**Beads milestone:** archie-core-8cda.4.1
**Parent:** `docs/prds/service-decomposition.md` (open-question Q4 **RESOLVED**)

This document fixes the **contract boundary** and **transport** for the State Store service.
It does not redesign Archie's domain model, does not invent a second store, and does **not**
split `archie.db`. It records method ownership, DTO/wire representation, error semantics,
limits, mode/config shape, local-adapter compatibility, and cutover/deletion rules so that
`.4.2` and `.4.3` have exactly one authoritative surface to build against.

> **Current state — read before `.4.2` starts.** The §12 step 2 relocation (`archie-core-8cda.
> 4.8`) has landed: `Task`/`Status*`/`Source*` and the 3-method `workflow.Store` now live in
> `internal/domain/workflow`, the six production workflow files import no `internal/store`
> symbol, and `storerpc`/the NATS transport assert against `workflow.Store` instead of
> `store.WorkflowStore`. The broader Phase 2 acceptance criterion (State Store as its own
> service behind gRPC) is still open — that is what `.4.2`/`.4.3` build next.

> **Relationship to migration-decisions §4 — not replaced.** `docs/architecture/
> migration-decisions.md` §4 (`store.Task → WorkflowExecution`, `mutable Stage →
> StepExecution history`, `RetryCount/Attempt → Attempt records`, versioned Workflow defs)
> **remains the authoritative, open document for the long-term Workflow migration.** This PRD
> does **not** replace it. It only pulls the minimal `Task`/`Status`/`Source` type + interface
> relocation forward as a Phase 2 prerequisite (because the acceptance criterion requires it);
> the rest of §4 stays its own migration and stays open.

---

## 1. Decision (the invariant everything follows)

Archie is composed such that a single **`archie-state-store`** service owns the one existing
SQLite file (`archie.db`) and exposes it over **gRPC behind narrow typed contracts**. There is
exactly one store, one file, one service. No database-per-service, no distributed
transactions, no second store abstraction.

Two adapter families satisfy the contract interfaces:

- **Local adapter** — `*store.Store` (the in-process SQLite implementation).
- **Remote adapter** — `*staterpc.Client` (wraps the generated gRPC client).

Each adapter carries an explicit compile-time assertion against the contract interfaces it
serves. Composition picks local vs. remote by the presence of `[services.state].target`
(empty = local; set = dial the gRPC service). Default is **local** (the State Store is not yet
extracted), which is the PRD §3 posture; it differs from the gateway's remote-default only
because the gateway is already a separate deployment and the State Store is not.

> **Contract-ownership fork, decided (rev. 2c — resolves the domain contradiction).**
> Ownership splits by the consumer's *layer*, per the dependency contract's rule #2
> ("a domain defines the smallest interfaces required to perform its work") and rule #7
> ("database rows … MUST NOT leak into domain models"):
>
> - The **workflow domain** (`internal/domain/workflow`) is the only domain-layer consumer of
>   store state. It defines its own **consumer-owned** narrow `Store` interface
>   (Update/Transition/InsertEvent) and its own **domain-owned** `Task` type plus
>   `Status`/`Source` vocabulary. It imports **zero** `internal/store` symbols — satisfying the
>   Phase 2 acceptance criterion. This is the minimal bound of migration-decisions §4
>   (`store.Task → WorkflowExecution`), pulled forward **because** the acceptance criterion
>   requires it.
> - The **daemon / webui / intake** store surfaces (`TaskStore`, `CaptureStore`,
>   `MappingStore`, `BindingStore`, `BindingDispatcher`, `BindingTaskCreator`) stay
>   **producer-owned** in `internal/store`, because those consumers are application and
>   infrastructure layers, not domains — rule #2 does not apply to them.

So the State Store contract is **not** uniformly producer-owned: the domain-facing workflow
contract is consumer-owned (in the domain), while the daemon/webui store surfaces remain in
`internal/store`. `internal/store` imports `internal/domain/workflow` for the `Task`/`Status`/
`Source` types it persists (persistence → domain, the correct direction), and its `Store`
implementation satisfies `workflow.Store`. This is the single decision that makes the whole
Phase 2 acceptance criterion and the architecture's rules simultaneously satisfiable.

> **`internal/store` is a TRANSITIONAL compatibility location (rev. 2c).** The repository's target
> architecture (`docs/architecture/dependencies-and-contracts.md`, `organisation.md`) places
> persistence implementations under `internal/infrastructure/<capability>`. `internal/store`
> holding the task/capture/mapping/binding implementation and ALSO being the home of the
> producer-owned daemon/webui interfaces is acceptable **only as a transitional location**. The
> long-term home for the store implementation is `internal/infrastructure/store`; the
> producer-owned interfaces may move to the same infrastructure package (or an approved
> contract location) when the store implementation relocates. Agents must not treat
> `internal/store` as the final infrastructure boundary.

---

## 2. Contract boundary

**Ownership split** (see the fork in §1): the **workflow-domain consumer contract** is
`workflow.Store` (consumer-owned, in `internal/domain/workflow`); the **daemon/webui/intake
store surfaces** are the producer-owned interfaces in `internal/store/interface.go`.
`store.WorkflowStore` is **superseded** — it is replaced by the domain-owned `workflow.Store`,
and `internal/store`'s `Store` implementation satisfies `workflow.Store` directly. `TaskStore`
remains the only composite; every other interface is flat. Narrow slices are split along
consumer-capability lines so no consumer acquires a surface it does not need
(`interfacebloat` cap = 8).

| Interface (owner) | Embeds | Methods | Local impl | Remote impl |
|---|---|---|---|---|
| `TaskStore` (store) | `TaskLifecycle`, `TaskEvents`, `TaskQueries`, `TaskArchiver`, `TaskRetryer` | 24 (union) | `*Store` | `*staterpc.Client` |
| `TaskLifecycle` (store) | — | 8 | `*Store` | `*staterpc.Client` |
| `TaskEvents` (store) | — | 7 | `*Store` | `*staterpc.Client` |
| `TaskQueries` (store) | — | 7 | `*Store` | `*staterpc.Client` |
| `TaskArchiver` (store) | — | 1 | `*Store` | `*staterpc.Client` |
| `TaskRetryer` (store) | — | 1 | `*Store` | `*staterpc.Client` |
| `workflow.Store` (workflow domain) | — | 3 | `*Store` | `*staterpc.Client` |
| `CaptureStore` (store) | — | 2 | `*Store` | `*staterpc.Client` |
| `MappingStore` (store) | — | 5 | `*Store` | `*staterpc.Client` |
| `BindingStore` (store) | — | 6 | `*Store` | `*staterpc.Client` |
| `BindingDispatcher` (store) | — | 3 | `*Store` | `*staterpc.Client` |
| `BindingTaskCreator` (store) | — | 1 | `*Store` | `*staterpc.Client` |

**Key property of the remote client:** `*staterpc.Client` is a *single* adapter that wraps one
`StateStoreClient` and is asserted against **whichever narrow Go interfaces its caller needs**
(exactly as `gatewayrpc.Client` asserts both `gateway.ChatContract` and
`gateway.SessionStore`). The Go contracts stay narrow (≤8, the `interfacebloat` cap); the wire
is one service. `workflow.Store` introduces **no new RPCs** — it is a narrow consumer face over
the same `Update`/`Transition`/`InsertEvent` RPCs.

---

## 3. Method ownership

`*Store` remains the single implementation of the daemon/webui store surfaces and of
`workflow.Store`. Ownership below is about which **process** is the primary consumer of each
method today, and therefore which methods are genuinely cross-process on the wire today vs.
daemon-internal.

Process legend: **DAEMON** = `archied` (incl. in-process webui + gateway runtime +
`storerpc.Server`); **AGENT** = `archie-agent` worker (no DB connection); **GATEWAY** = store
access through daemon-side narrow adapter interfaces.

| Contract | Primary consumers (process) | Remote on the wire? |
|---|---|---|
| `TaskLifecycle` (8) | DAEMON (dispatch, claim, action) + **AGENT** (`Transition`, `Update` via storerpc) | `Transition`/`Update` = **YES** (agent); rest DAEMON |
| `TaskArchiver` (1) | DAEMON (`taskactions` archive) | not yet |
| `TaskRetryer` (1) | DAEMON (`taskactions` retry) | not yet |
| `TaskQueries` (7) | DAEMON + GATEWAY (status/list adapters) | not yet (`OpenPRs`/`Tasks`/`StatusCounts` cross to gateway adapters in-process) |
| `TaskEvents` (7) | DAEMON + **AGENT** (`InsertEvent` via storerpc) | `InsertEvent` = **YES** (agent); rest DAEMON |
| `workflow.Store` (3) | **AGENT** only | **YES** (the sole genuinely remote consumer today) |
| `CaptureStore` (2) | DAEMON (webhook intake + mapping editor) | not yet |
| `MappingStore` (5) | DAEMON (dashboard editor + dispatch loop) | not yet |
| `BindingStore` (6) | DAEMON (dashboard editor + dispatch loop) | not yet |
| `BindingDispatcher` (3) | DAEMON (dispatch loop) | not yet |
| `BindingTaskCreator` (1) | DAEMON (dispatch loop) | not yet |

**Consequence for migration order:** the **agent's `workflow.Store` is already cross-process
today** (via the NATS `storerpc` surrogate), so it is the first contract to move to gRPC. The
daemon-internal surfaces (`Capture`/`Mapping`/`Binding`) stay in-process until the daemon
itself points at `archie-state-store`. `TaskStore` (the large composite) moves last.

**Two orphan methods** — `TaskQueries.ClearTerminalTasks` and
`TaskQueries.IncrementRetryCount` have **no production consumer** (only `*Store` tests). They
remain part of the Go `TaskQueries`/`TaskStore` surface, and therefore part of the wire
contract, but are flagged for deletion: they must **not** gain dedicated consumers or wire
methods in this work. Do not add the rest of the codebase to them.

---

## 4. Domain-facing types & consumer-owned contract — resolves the contradiction

The Phase 2 acceptance criterion ("`internal/domain/workflow` … no longer import
`internal/store` directly — they call the State Store contract") **cannot be met while**
`store.Task`, `store.Status*`, `store.Source*`, and `store.WorkflowStore` live in
`internal/store`. This section fixes where they move.

**The relocation (a prerequisite that must land before or with `.4.2`):**

| Current symbol (`internal/store`) | Moves to | Notes |
|---|---|---|
| `Task` type | `internal/domain/workflow.Task` | The task-execution record the workflow domain operates on. `internal/store` persists this type (persistence → domain). |
| `Status` + `Status*` constants | `internal/domain/workflow` | e.g. `StatusClosedWontDo`, `StatusMerged`, `StatusPROpen`, `StatusWaitingHuman`, `StatusRunning` — task-lifecycle vocabulary. |
| `Source` + `Source*` constants | `internal/domain/workflow` | e.g. `SourceChat` — provenance vocabulary, follows `Task`. |
| `WorkflowStore` interface (3 methods) | `internal/domain/workflow.Store` | **Consumer-owned** (dependency rule #2). Supersedes `store.WorkflowStore`. |

**Resulting dependency direction (correct per the architecture):**

- `internal/domain/workflow` imports **no** `internal/store` symbols → the acceptance
  criterion is satisfied. It consumes `store.Task`'s replacement as `workflow.Task`, uses
  `workflow.Status*`/`workflow.Source*`, and depends on its own `workflow.Store` interface.
- `internal/store` imports `internal/domain/workflow` for the `Task`/`Status`/`Source` types it
  reads and writes (infrastructure → domain). **NOTE: `internal/store` is a transitional
  compatibility location only** — its long-term home is `internal/infrastructure/store` (see
  the callout in §1). Do not treat `internal/store` as the final infrastructure boundary.
- Every store test that constructs `store.Task{…}` is updated to the relocated type as part of
  this prerequisite. This is a mechanical rename across ~6 workflow production files
  (`workflow.go`, `steps.go`, `triage.go`, `review.go`, `feasibility.go`, `implement.go`) and
  their tests plus the store tests. `TestRecordDispatchViaExplicitTx` is removed rather than
  updated — its non-nil `*sql.Tx` path has no production consumer (see §5's `RecordDispatch`
  blocker).

**What this does NOT do:** it does not implement the full §4 Workflow migration
(`mutable Stage → StepExecution history`, `RetryCount/Attempt → Attempt records`,
`workflow.Registry → versioned Workflow definitions`, etc.). It lands only the minimal
task-type + `Status`/`Source` + consumer-owned-interface relocation that the Phase 2
domain-decoupling criterion requires. The residue of §4 remains its own migration.

**Forward-compatibility:** because the wire DTOs are derived from the domain `Task`/`Status`
(§5), when the full §4 migration later renames `workflow.Task` (or refines it into
`WorkflowExecution`), only `values.go` and the proto field mapping change — the RPC surface and
consumer interfaces are unaffected. The wire contract is deliberately type-agnostic at this
level.

---

## 5. DTO / wire representation

### Proto layout

- **Service:** `proto/state/v1/state.proto`, `package state.v1`, **one** `StateStore` service
  (mirrors the gateway's single `ChatService`; proto bypasses the Go `interfacebloat` cap, as
  `ChatContract` does via `//nolint:interfacebloat`).
- **Codegen:** the existing buf v2 pipeline. `proto/buf.yaml` (module, lint STANDARD, breaking
  FILE against `main`), `proto/buf.gen.yaml` **managed mode** with
  `go_package_prefix → github.com/samcharles93/archie-core/internal/contracts`, plugins
  `protoc-gen-go` + `protoc-gen-go-grpc`, `out: ../internal/contracts`,
  `opt: paths=source_relative`. **No `go_package` in source** — managed mode owns it. Generated
  Go lands in `internal/contracts/state/v1/` (package `statev1`), committed (`proto:check`
  fails on uncommitted/unmodified generated files).
- **Adapter package:** `internal/infrastructure/staterpc/` with `server.go` (registers the
  generated server over the store), `client.go` (wraps the generated client into `store.*` and
  `workflow.Store` facades), `values.go` (proto ⇄ domain mappers), and `conformance_test.go`
  (bufconn local-vs-grpc dual-adapter suite), mirroring `internal/infrastructure/gatewayrpc/`.
- **Binary:** `cmd/archie-state-store/main.go`, mirroring `cmd/archie-gateway/main.go` and the
  serve pattern in `internal/app/archied/gateway.go` (`serveGateway`).

### The `StateStore` RPC set (union of the contracts, grouped)

Methods are named after the store methods so the mapping is unambiguous. `workflow.Store` is a
*view*, not new RPCs — `Update`/`Transition`/`InsertEvent` are shared with
`TaskLifecycle`/`TaskEvents` and appear once.

| Group | RPCs |
|---|---|
| Lifecycle | `EnqueueIssue`, `EnqueueChatTask`, `ClaimNext`, `ClaimByIssue`, `Transition`, `Update`, `Requeue`, `RecoverStale` |
| Archive / Retry | `ArchiveTask`, `RetryTask` |
| Queries | `TaskByIssue`, `TaskByID`, `OpenPRs`, `ClearTerminalTasks`, `Tasks`, `StatusCounts`, `IncrementRetryCount` |
| Events | `InsertEvent`, `EventsSince`, `TaskEvents`, `WorkflowStats`, `StageStats`, `TokensByDay` |
| Capture | `InsertCapture`, `ListCaptures` |
| Mapping | `InsertMapping`, `GetMapping`, `ListMappings`, `UpdateMapping`, `DeleteMapping` |
| Binding | `InsertBinding`, `GetBinding`, `ListBindings`, `UpdateBinding`, `DeleteBinding`, `ApproveBinding` |
| Dispatch | `ArmedBindingsForSource`, `RecordDispatch`, `ListUndispatchedCaptures` |
| BindingTaskCreator | `EnqueueBindingTask` |
| Config snapshot | `PutConfigSnapshot`, `GetConfigSnapshot` |

That is **42 unique RPCs** across one service (the `TaskEvents.Close` method is dropped).
`Close()` is **excluded** from the wire (it is server lifecycle, not a client call) — see §11.

The config-snapshot pair was added by `archie-core-ymut` (see
`docs/architecture/migration-decisions.md`, "Dashboard configuration page"). It is the one
surface whose writer is the daemon and whose reader is the UI process, and both RPCs are
administrative: `authorizesTaskScopedCall` is deny-by-default, so a task-scoped grant reaches
neither. `ConfigSnapshot.document` is opaque to this contract — the store neither parses nor
validates it, and `schema` names the projection so a reader can refuse a shape it does not
understand.

### Domain types and `values.go` mapping

`values.go` maps proto ⇄ these types. The task model for the **agent** path is the domain-owned
`workflow.Task` (§4); the daemon/webui store surfaces return the same `workflow.Task` (the store
persists the domain type).

| Domain type | Package | Notes for `values.go` |
|---|---|---|
| `workflow.Task` | `internal/domain/workflow` | time fields stored as layout strings; convert to/from `google.protobuf.Timestamp` |
| `events.Event` | `internal/events` | `Timestamp` → `google.protobuf.Timestamp` |
| `store.CapturedEvent` | `internal/store` | headers/body/remote_addr; body capped 256 KiB |
| `mapping.Mapping` | `internal/domain/mapping` | JSON-path field bindings |
| `binding.Binding` | `internal/domain/binding` | secret is **already decrypted** before returning; keep decrypted form out of any durable/logged representation |
| `workflow.Status` / `workflow.Source` | `internal/domain/workflow` | enum-like; map to proto enum or string, keeping the names stable for compatibility |
| `store.WorkflowStat` | `internal/store` | aggregate row |
| `store.StageStat` | `internal/store` | aggregate row |
| `store.DayTokens` | `internal/store` | aggregate row |

### Wire-contract blocker fixed here: `RecordDispatch`

`store.BindingDispatcher.RecordDispatch(ctx, tx *sql.Tx, bindingID, bindingVersion int64,
captureID int64, taskID int64) error` takes a **`*sql.Tx`** that cannot cross a gRPC boundary.

**Evidence that the `*sql.Tx` is removable:** the dispatch loop calls `RecordDispatch(ctx, nil,
…)` (`internal/daemon/daemon.go:591`). The implementation falls back to the store's own
`*sql.DB` when `tx == nil` ("best-effort, not atomic"; the comment and the
`TestRecordDispatchViaExplicitTx` test are the only consumers of the non-nil `tx` path).
Production never opens the explicit transaction.

**Decision:** the contract drops `*sql.Tx` from `RecordDispatch` (the method becomes
`RecordDispatch(ctx, bindingID, bindingVersion, captureID, taskID) error`). The store service
owns any transaction boundary internally when atomicity is wanted; today the at-most-once
guarantee is the `INSERT OR IGNORE` dedup row, which is already transactionless in production.
`TestRecordDispatchViaExplicitTx` is superseded by the store service's own internal-transaction
test. **This is a required interface migration that must land before or with `.4.2`** (the
current `interface.go` signature and test still use the transaction-bearing form).

---

## 6. Agent State Store client handoff

Moving the agent from NATS `storerpc` to gRPC `workflow.Store` requires the agent process to
receive a State Store endpoint. The agent already receives its NATS connection settings via
boot-time env/flags (see `cmd/archie-agent/main.go`: `-nats-url` / `NATS_URL`, `NATS_TOKEN`
env), and the daemon injects them into the task container. The State Store handoff follows the
same seam.

**Boot handoff contract (additive, not replacing the NATS settings):**

| Field | Where | Source | Notes |
|---|---|---|---|
| `StateStoreTarget` | `agentworker.Settings` | env `STATE_STORE_URL` (or `-state-store-url` flag), set by the daemon in the container env | `host:port` of the State Store gRPC server. Loopback/Docker-bridge address — the agent reaches it the same way it reaches the daemon's NATS. |
| `StateStoreToken` | `agentworker.Settings` | env `STATE_STORE_TOKEN` (or `-state-store-token` flag) | Per-task bearer token carried in gRPC metadata by a client interceptor (see §9). |
| `StoreTimeout` | `agentworker.Settings` | a bounded per-call timeout | Mirrors `natsrpc.Client.Timeout` / the existing `rpcTimeout`; bounds each State Store call when the caller `ctx` has no deadline. |

**Wiring change in the transport façade:**

- `internal/infrastructure/agenttransport/nats.Transport` — `Store(timeout) store.WorkflowStore`
  currently returns `&storerpc.Client{Conn: t.conn, Timeout: timeout}`. It becomes
  `Store(timeout) workflow.Store` and returns a *single long-lived* `staterpc.Client` dialed to
  `settings.StateStoreTarget` (with the token interceptor and per-call timeout).
- `internal/app/agentworker/worker.go` `Settings` gains the three fields above; `taskServiceTransport`
  and `taskDependencies` change their `store` type from `store.WorkflowStore` to
  `workflow.Store` (via `domain/workflow.TaskContext{Store: …}`).
- Reconnect/deadline: `grpc.NewClient` (or the existing dial path) provides transparent reconnect;
  each RPC is bounded by the per-call context deadline (`StoreTimeout`), matching the
  request/reply timeout the NATS path used. No streaming is involved, so no server-sent
  reconnect semantics apply.

**Deletion rule for the NATS path:** the NATS `storerpc` (Server, Client, subjects
`archie.store.update|transition|insert_event`) is deleted **only after** the agent has fully
moved to gRPC and the container env carries `STATE_STORE_URL`. Until then both paths may coexist
(agent flips when the daemon starts injecting `STATE_STORE_URL`); there is no dual-live window
across a single agent process, which is either NATS-backed or gRPC-backed, never both.

### Operational specifics (in-process listener → address → per-task injection)

- **Who binds the in-process State Store listener:** the daemon (`archied`), at bootstrap, when
  the State Store is served in-process/multiplexed (mirroring `serveGateway` in
  `internal/app/archied/gateway.go`): a `grpc.NewServer()` + `RegisterServer` + graceful stop on
  `ctx.Done()`.
- **Address selection (Docker-bridge reachable):** reuse the host-gateway resolution already used
  for embedded NATS (`internal/container/network.go` `RequireHostGateway` /
  `resolveHostGateway`). Per §9's single listener-topology rule, the listener binds the
  **host-gateway bridge address** when agent containers consume it (the Phase 2 default), or
  loopback-only when the daemon is the sole consumer. The chosen address is stored on the daemon
  as `d.ConnectedStateStore.URL` (analogous to `d.ConnectedNATS.URL`).
- **Per-task injection:** `containerEnv(task)` (`internal/daemon/daemon.go`) appends
  `STATE_STORE_URL=<d.ConnectedStateStore.URL>` and `STATE_STORE_TOKEN=<token>` alongside the
  existing `NATS_URL`/`NATS_TOKEN`. The agent reads them via env (`-state-store-url` /
  `STATE_STORE_URL`, `-state-store-token` / `STATE_STORE_TOKEN`), the same seam as `NATS_URL`. A
  reloaded `[services.state]` must not point new containers at a store the daemon is not serving
  — the same immutability rule as NATS.

---

## 7. Error semantics

### Current state (both boundaries lose error identity)

- **NATS `natsrpc.Envelope`** (`internal/natsrpc/natsrpc.go`) serialises any error to a single
  `err.Error()` string and rehydrates a fresh `errors.New(string)`. The original type, `%w`
  chain, and sentinel identity are lost → `errors.Is(err, store.ErrStaleTransition)` is
  **false** across the boundary.
- **gRPC `gatewayrpc`** currently returns raw `err` from handlers; gRPC-Go converts to
  `codes.Unknown` with only the message. The **only** explicit code in the repo is
  `codes.Unavailable` for a missing session store (`server.go:31`). No status-code mapping for
  store errors exists.

### Decision — structured mapping so `errors.Is` survives

The `StateStore` service must map the store's sentinel errors to canonical gRPC codes and
rehydrate them to the *same* sentinels on the client, so consumer `errors.Is` checks keep
working — an improvement over today's flattening, not a regression.

| Store sentinel / outcome | gRPC code | Client rehydrates to |
|---|---|---|
| `store.ErrStaleTransition` | `FailedPrecondition` | `store.ErrStaleTransition` |
| `store.ErrBindingNotFound` | `NotFound` | `store.ErrBindingNotFound` |
| `store.ErrMappingNotFound` | `NotFound` | `store.ErrMappingNotFound` |
| `store.ErrBindingOverlap` | `FailedPrecondition` | `store.ErrBindingOverlap` |
| `store.ErrBindingTransition` | `FailedPrecondition` | `store.ErrBindingTransition` |
| `store.ErrAlreadyDispatched` | `AlreadyExists` | `store.ErrAlreadyDispatched` |
| not-found-as-`(nil,nil)` (`TaskByID`, `GetBinding`, `GetMapping`, `ClaimNext`, `ClaimByIssue`, …) | `NotFound` with a `found=false` field (**not** an error) | `(nil, nil)` |
| infra / wrapped error | `Internal` (**sanitised** message, see below) | wrapped `fmt.Errorf(...%w)` |
| capability absent | `Unavailable` | context/typed error |

The `found=false`-not-error convention matters: the repo deliberately returns `(nil, nil)` for
not-found in reads and a sentinel for not-found in writes. The wire must preserve **both**
conventions (a `GoogleRPCStatus` with `NotFound` is an *error*, which would wrongly surface to
callers that expect `(nil, nil)`). Implement `found` as an explicit boolean field on read
responses, and map `(nil,nil)` ⇄ `found=false` + `OK`.

### Error-detail sanitisation (rev. 2c)

Full internal error messages must **not** be placed in gRPC `details` for remote callers. SQL
paths, provider/forge details, secrets, and stack traces are sensitive internals. The rule:

- **Sentinel errors** — the client rehydrates the sentinel from the code; the message string in
  `status.Error` is a short, public, stable phrase (e.g. `"stale transition"`), not the raw
  wrapped chain.
- **Infra errors** — the wire carries a **sanitised** public message + the `Internal` code.
  The **full** error (with `%w` chain, SQL, provider detail) is logged **server-side only** by
  the store service. The client rehydrates a generic wrapped error and must not surface the raw
  text to users.

---

## 8. Limits

**Interface bloat (`interfacebloat` cap = 8)** — `.golangci.yml:89` `interfacebloat: max: 8`,
which overrides the upstream default of 10. The Go consumer facades must stay ≤8 methods.
`TaskLifecycle` is exactly 8 (at the cap); `TaskStore` is the 24-method composite that the cap
is why it is decomposed; proto services bypass the cap (like `ChatContract`).

**Payload caps:**
- Capture body: `defaultCaptureMaxBodyBytes = 256 KiB` (may reduce further; the wire message
  must not exceed gRPC's default 4 MiB receive limit for a single capture). A single capture
  fits, but a *batch* of them did not: the caller-side cap of up to 100 captures at 256 KiB each
  could exceed 4 MiB in one unary `ListCaptures`/`ListUndispatchedCaptures` response. `.4.7` adds
  `StreamCaptures`/`StreamUndispatchedCaptures` (server-streaming, one capture per message) as
  the path both `staterpc.Client` methods actually call; the original unary RPCs stay defined,
  unmodified and deprecated, only so `buf breaking` protects an in-flight rolling deploy.
- Task `detail`/`park_reason`/event `Detail`: 4000-char hard cap (`clip(s, 4000)`).

**Retention (stays server-side in the store; the wire does not carry pruning decisions):**
- `events` table: **no prune, unbounded growth** — a known risk to revisit, not changed here.
- `captured_events`: prune-on-write, default 7 days / 5000 events.

**Pagination/arity:** store read methods take a raw `int limit` (no store-side cap); the
caller-side caps (task list 100, log 2000, captures 100, capture scan window 5000, binding
dispatch batch 100, session list 20) remain caller-side. The wire passes `limit int` and returns
`[]*` slices, mirroring the existing Go signatures — **no cursor/paging protocol is introduced**
in this work (the current `EventsSince(sinceID, limit)` offset cursor is the only one, and it
stays as-is).

---

## 9. Transport security boundary

The gateway precedent is loopback-only, unauthenticated gRPC. The State Store holds the
crown-jewel `archie.db` (task/event/capture/mapping/binding data), so its boundary must be
stated explicitly rather than left implicit.

**The State Store's equivalent boundary (rev. 2c):**

- **Listener topology — single rule (rev. 2c).** One in-process listener; its bind address is
  chosen by the consumer set, never both at once:
  - *Agent containers consume it* (the Phase 2 driver during multiplexed serving) → bind to the
    **host-gateway bridge address** so containers can reach it, and **token auth is mandatory**
    (a bridge host is reachable by any process on that bridge). A container **cannot reach the
    host's `127.0.0.1`**, so loopback-only is NOT usable for the agent-consumed path.
  - *Only the daemon consumes it* (no agent) → bind **loopback-only** (`--listen 127.0.0.1`),
    `insecure` is fine because it is not network-reachable; no token required.
  - There is **no dual-listener** design. The practical default during `.4.2` (the reason
    in-process serving exists is to give the agent a gRPC target) is the **bridge address +
    token**. `127.0.0.1` is reserved for the daemon-local-only case.
- **Agent access → the container bridge.** The agent runs in a task-scoped container on the
  host and reaches the State Store over the Docker bridge gateway (the bind address from the
  topology rule above), exactly as it reaches the daemon's NATS today. To close the "any host
  process could reach the store" gap, the agent **authenticates with a per-task bearer token**
  carried in gRPC metadata by a client interceptor (`StateStoreToken`, §6). Network
  reachability + the token is the trust boundary for the default bridge topology.
- **The daemon** uses the local adapter (`*store.Store`) in-process by default, so no network
  boundary exists until `[services.state].target` is set; when it is set, the daemon client
  authenticates with a token supplied alongside the target.
- **Non-loopback / k8s:** TLS/mTLS is an **operator decision**, not a default. Any non-loopback
  exposure (e.g. `--listen 0.0.0.0`, a CDN, a cross-host service) requires an explicit decision
  and mTLS or token auth — mirroring the gateway's `--listen`/`--enable-cors-header`
  confirmation caveat. The NATS-KV discovery fallback supplies endpoints but does not add
  transport security; that stays the operator's responsibility.

### Token lifecycle (rev. 2c; scoping added in `.4.7`)

- **Generation:** the daemon generates a per-task bearer token when it acquires the container
  (or per incumbence) and injects it via `containerEnv` as `STATE_STORE_TOKEN`. A single token
  may cover a task's container lifetime.
- **Registration (`.4.7`, post-standalone-cutover):** the State Store is now its own process
  (`.4.3`/`.4.6`), so the daemon can no longer just hold an in-process issued-token set for the
  server to check — it registers the token at the remote authority via the
  `RegisterTaskGrant`/`RevokeTaskGrant` RPCs (`staterpc.GrantIssuer`, `daemon.StateStoreGrantIssuer`),
  admin-token-authenticated calls a task-scoped token itself can never make.
- **Lifetime:** tied to the task + container grace period; rotated on a new container acquisition
  for the same task; a token for a released container is invalidated (`RevokeTaskGrant`, deferred
  alongside `ContainerPool.Release`). An unrevoked grant still expires server-side (bounded by
  `RegisterTaskGrantRequest.lifetime_seconds`, capped at 7 days) so a daemon crash before revoke
  cannot leave a grant valid indefinitely.
- **Validation and scope (`.4.7`):** a gRPC interceptor (`staterpc.TaskGrants.UnaryInterceptor`)
  validates the token against the State Store's own issued-grant set; missing/unknown/expired →
  `codes.Unauthenticated`. Unlike the daemon's own administrative token (full access to all ~40
  RPCs), a task-scoped grant additionally authorizes only `Update`/`Transition`/`InsertEvent` on
  its own task ID — every other RPC, including the streaming capture surface and
  `RegisterTaskGrant`/`RevokeTaskGrant` themselves, is `codes.PermissionDenied` for a task-scoped
  caller. The token is carried in gRPC metadata, never in a URL.

### Fail-closed on non-loopback (rev. 2c)

If `[services.state].target` points to a non-loopback address and **neither** TLS **nor** a token
is configured, the State Store client (daemon or agent) **fails closed** — it refuses to
start/dial rather than fall back to a plaintext, unauthenticated remote connection. This mirrors
the gateway's `--listen`/`--enable-cors-header` confinement caveat and prevents a silent
unauthenticated remote store. The loopback + token default is the only path that starts without
an explicit operator decision.

---

## 10. Mode / config shape

**Follow the gateway's presence-based `target` seam, not the NATS `mode` enum.**

- `internal/config/services.go` gains a `State ServiceConnection` field:
  ```go
  type Services struct {
      Gateway ServiceConnection `toml:"gateway" yaml:"gateway"`
      State   ServiceConnection `toml:"state"   yaml:"state"`
  }
  type ServiceConnection struct {
      Target string `toml:"target" yaml:"target"`
  }
  ```
- **Empty `Target` → local** `*store.Store` adapter (the default; State Store not yet
  extracted). **Set `Target` → dial gRPC** and use `*staterpc.Client`. This differs from the
  gateway, where `Target` is always defaulted to `127.0.0.1:8585` because the gateway is already
  a separate process.
- Because `[services.state]` is **not** in `config.example.toml` today (the gateway's own
  section is also missing — documentation debt), **add `[services.state]`** with a commented
  `target` to `config.example.toml` in the same change that introduces the config field, so the
  seam is visible to operators.
- Composition root: `internal/app/archied/bootstrap.go`. Base path = the local `*store.Store`
  (already opened by `openProductionTaskStore`); if `cfg.Services.State.Target != ""`, dial gRPC
  and assign `b.stateStore = staterpc.NewClient(conn)` (with the token from config/secret).

> **Mode fork, decided.** The gateway uses a single `target` address (no enum). The State Store
> boundary uses the same seam for symmetry, but with the opposite default (local, not remote)
> because the State Store is not yet extracted. No `mode` field is added. A companion
> `STATE_STORE_TOKEN` secret (or a `[services.state] target_token` key) carries authentication.

---

## 11. Local-adapter compatibility + conformance

- `*store.Store` (local) and `*staterpc.Client` (remote) satisfy the **same** contract
  interfaces. Use **explicit** `var _` assertions on **both** adapters (the gateway's local
  adapters use only structural/return-type assertions; the store precedent's explicit `var _`
  style is stronger and should be adopted on both sides).
  ```go
  var _ workflow.Store = (*staterpc.Client)(nil)   // first contract to move (agent)
  var _ store.TaskStore   = (*staterpc.Client)(nil) // daemon path, added progressively
  var _ store.CaptureStore   = (*staterpc.Client)(nil)
  var _ store.MappingStore   = (*staterpc.Client)(nil)
  var _ store.BindingStore   = (*staterpc.Client)(nil)
  var _ store.BindingDispatcher  = (*staterpc.Client)(nil)
  var _ store.BindingTaskCreator = (*staterpc.Client)(nil)
  // local side (in store), already present:
  var _ workflow.Store = (*Store)(nil)
  ```
- **Conformance suite:** a `staterpc/conformance_test.go` bufconn suite runs the **same**
  contract through both `"local"` and `"grpc"` adapters (mirroring
  `gatewayrpc/conformance_test.go`), asserting error-sentinel fidelity and the
  not-found-as-`(nil,nil)` convention both ways.
- **`Close()`:** stays on the Go store interfaces (so `TaskStore`/`TaskEvents` remain fully
  satisfiable) but is **not** a `StateStore` RPC. The remote `staterpc.Client` implements
  `Close()` as a **no-op**; the store service owns its own DB lifecycle. When the daemon hands
  the store to `archie-state-store`, `b.cleanup()` no longer calls `store.Close()` on a remote
  client (it is the daemon's own lifecycle only when it owns the store in-process).

---

## 12. Cutover / deletion rules

1. **Ratify the contract (this document).** No code.
2. **Domain type + interface relocation (prerequisite, coupled with §4).** Move `Task`/`Status`/
   `Source` and the `WorkflowStore` interface into `internal/domain/workflow` (as
   `workflow.Task`/`workflow.Status`/`workflow.Source`/`workflow.Store`), so
   `internal/domain/workflow` imports no `internal/store`. Update the store tests and the 6
   workflow production files. **This must land before or with `.4.2`, because `.4.2` cannot move
   the agent's store contract off `store.WorkflowStore` while the domain still imports it.**
3. **`.4.2` — generalize `storerpc` to the gRPC contract.** Move the agent's `workflow.Store`
   (`Update`/`Transition`/`InsertEvent`) from NATS JSON `storerpc` to `staterpc.Client` over
   gRPC, using the §6 handoff. The daemon serves the gRPC `StateStore` server **in-process
   (multiplexed)** first so the agent has a gRPC target before the standalone binary exists
   (PRD §3 "multiplexed in-process is the migration default"). Bind the in-process listener and
   inject `STATE_STORE_URL`/`STATE_STORE_TOKEN` per task (see §6 "Operational specifics"). Also
   land the `RecordDispatch` `*sql.Tx` drop (see §5) in the same span. Flip the agent's
   `Transport.Store` to a gRPC client once `STATE_STORE_URL` is injected.
4. **Delete NATS `storerpc` only after the agent is fully on gRPC** — delete `internal/storerpc`
   (Server, Client, subjects `archie.store.update|transition|insert_event`), the
   `natsrpc.RegisterAll` registration, and the now-unused `store.WorkflowStore` NATS path in
   `internal/infrastructure/agenttransport/nats`, in the same commit.
5. **`[services.state].target` stays empty (inproc)** through `.4.2`; the `archie-state-store`
   `cmd/` binary is **`.4.3`** and hosts the same `StateStore` gRPC service.
6. **Swap daemon-internal contracts one at a time** (only once the daemon points at
   `archie-state-store`): `Capture → Mapping → Binding → TaskStore`. Each swap is independent —
   add the remote-client composition path, run the per-contract conformance suite, flip
   `[services.state].target`, delete the local use. A consumer mixes in-process and remote
   contracts for a contract-code (no dual-live *window*; each contract is either local or remote
   at a time).
7. **Final flip / deletion of the in-process serving path:** when the daemon no longer owns the
   store (i.e. `TaskStore` has moved), delete the in-process `openProductionTaskStore` /
   `wireWebStoreSurfaces` path in the daemon in a **same-commit** deletion — no dual-store
   ownership. The single SQLite file remains owned by `archie-state-store`.
8. **Never** split `archie.db` or add a second store. The gateway keeps its own already-separate
   session SQLite; that is unchanged and out of state-store scope.

---

## 13. Decisions recorded (for the review trail)

- **`internal/store` is a transitional compatibility location** (target
  `internal/infrastructure/store`), not the final infrastructure boundary. The producer-owned
  daemon/webui store surfaces live there now but may relocate with the implementation.
- **In-process listener + token lifecycle:** the daemon binds the in-process State Store
  listener to the **host-gateway bridge address** when agent containers consume it (the Phase 2
  default) or loopback-only when the daemon is the sole consumer — one listener, one address
  rule, never both (rev. 2c). It selects the bridge address by reusing the embedded-NATS
  host-gateway resolution and injects `STATE_STORE_URL`/`STATE_STORE_TOKEN` per task via
  `containerEnv`. Token is per-incumbence (task + container grace period) and validated by a
  gRPC interceptor → `codes.Unauthenticated` on missing/unknown/expired. Non-loopback targets
  fail closed without TLS or a token.
- **§12 step 2 (domain type + interface relocation) is done** — `internal/domain/workflow`
  imports no `internal/store` symbol and `store.WorkflowStore` is gone from `TaskContext`. The
  broader Phase 2 acceptance criterion (State Store as its own gRPC service) is still open;
  `.4.2` picks up from here.
- **Ownership split:** domain-facing workflow contract is **consumer-owned** in the domain
  (`workflow.Store` + `workflow.Task`/`Status`/`Source`, per dependency rules #2 and #7);
  daemon/webui store surfaces stay **producer-owned** in `internal/store`. `store.WorkflowStore`
  is superseded by `workflow.Store`.
- **One `StateStore` gRPC service** (42 RPCs, grouped by contract) + narrow Go consumer facades
  (≤8) on `staterpc.Client` — mirrors the single-`ChatService` precedent.
- **Domain type relocation** (`Task`/`Status`/`Source` → `internal/domain/workflow`) is a
  Phase 2 prerequisite, pulled forward from migration-decisions §4 (minimal bound,
  `Task → WorkflowExecution`); the rest of §4 stays a separate migration.
- **`RecordDispatch` drops `*sql.Tx`** (wire-blocker; production passes `nil` today) — a
  required interface migration landing before/with `.4.2`.
- **`Close()` not on the wire**; remote client `Close()` is a no-op.
- **Structured gRPC error codes** with client rehydration to store sentinels (preserves
  `errors.Is`), a `found=false` + `OK` convention for read not-found, and **sanitised public
  error details** (full detail logged server-side only).
- **Agent handoff:** `agentworker.Settings` gains `StateStoreTarget`/`StateStoreToken`/
  `StoreTimeout`, injected as `STATE_STORE_URL`/`STATE_STORE_TOKEN` env (mirrors the NATS
  handoff); `Transport.Store` returns a long-lived gRPC `workflow.Store` client.
- **Transport security + listener topology (rev. 2c):** one in-process listener, bound to the
  host-gateway bridge address when agent containers consume it (the Phase 2 default) or
  loopback-only for a daemon-local-only consumer. `insecure` only on loopback; mandatory
  per-task bearer-token auth on the bridge; TLS/mTLS is an operator choice for any
  non-loopback/k8s topology.
- **Mode = presence-based `[services.state].target`**, default **local** (not yet extracted);
  add `[services.state]` to `config.example.toml`.
- **Explicit `var _` assertions on both adapters**; per-contract bufconn conformance.
- **Migration order:** domain type/interface relocation → `workflow.Store` (agent) → stand up
  `archie-state-store` → `Capture` → `Mapping` → `Binding` → `TaskStore`; NATS `storerpc`
  deleted once the agent is on gRPC; in-process store path deleted in a same-commit flip.
- **Orphans `ClearTerminalTasks`/`IncrementRetryCount`** retained but not built upon.
