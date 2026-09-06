# State Store — contract boundary & transport (ratification)

**Status:** Ratified — the pre-implementation design document that Phase 2 subtasks
`.4.2` (generalize `storerpc` as the State Store gRPC contract) and `.4.3` (stand up
`archie-state-store`) implement against.
**Date:** 2026-09-06
**Beads milestone:** archie-core-8cda.4.1
**Parent:** `docs/prds/service-decomposition.md` (open-question Q4 **RESOLVED**)

This document fixes the **contract boundary** and **transport** for the State Store
service. It does not redesign Archie's domain model, does not invent a second store,
and does **not** split `archie.db`. It records method ownership, DTO/wire
representation, error semantics, limits, mode/config shape, local-adapter
compatibility, and cutover/deletion rules so that `.4.2` and `.4.3` have exactly one
authoritative surface to build against.

---

## 1. Decision (the invariant everything follows)

Archie is composed such that a single **`archie-state-store`** service owns the one
existing SQLite file (`archie.db`) and exposes it over **gRPC behind the narrow typed
contracts already defined in `internal/store/interface.go`**. There is exactly one
store, one file, one service. No database-per-service, no distributed transactions, no
second store abstraction.

The store contracts remain **producer-owned** (they live in `internal/store`, not in a
consumer package). Two adapters satisfy the *same* interfaces:

- **Local adapter** — `*store.Store` (the in-process SQLite implementation).
- **Remote adapter** — `*staterpc.Client` (wraps the generated gRPC client).

Each adapter carries an explicit compile-time assertion against the store interfaces it
serves. Composition picks local vs. remote by the presence of `[services.state].target`
(empty = local; set = dial the gRPC service). Default is **local** (the State Store is
not yet extracted), which is the PRD §3 posture; it differs from the gateway's
remote-default only because the gateway is already a separate deployment and the State
Store is not.

> **Contract-ownership fork, decided.** The gateway precedent is consumer-owned
> (`gateway.SessionStore`, `gateway.ChatContract` live beside the consumer, both
> adapters live outside). The store precedent is producer-owned (`store.WorkflowStore`
> lives in `internal/store`, both adapters live outside). The State Store keeps the
> **producer-owned** shape: the contracts are the domain store's native interface, and
> they must not be re-declared in a consumer package. This is what `.4.2`/`.4.3` assume.

---

## 2. Contract boundary

`internal/store/interface.go` is the authoritative source. `TaskStore` is the only
composite; every other interface is flat. Narrow slices are split along consumer-capability
lines so no consumer acquires a surface it does not need (`interfacebloat` cap = 8).

| Interface | Embeds | Methods | Local impl | Remote impl |
|---|---|---|---|---|
| `TaskStore` | `TaskLifecycle`, `TaskEvents`, `TaskQueries`, `TaskArchiver`, `TaskRetryer` | 24 (union) | `*Store` | `*staterpc.Client` |
| `TaskLifecycle` | — | 8 | `*Store` | `*staterpc.Client` |
| `TaskEvents` | — | 7 | `*Store` | `*staterpc.Client` |
| `TaskQueries` | — | 7 | `*Store` | `*staterpc.Client` |
| `TaskArchiver` | — | 1 | `*Store` | `*staterpc.Client` |
| `TaskRetryer` | — | 1 | `*Store` | `*staterpc.Client` |
| `WorkflowStore` | — | 3 | `*Store` | `*staterpc.Client` |
| `CaptureStore` | — | 2 | `*Store` | `*staterpc.Client` |
| `MappingStore` | — | 5 | `*Store` | `*staterpc.Client` |
| `BindingStore` | — | 6 | `*Store` | `*staterpc.Client` |
| `BindingDispatcher` | — | 3 | `*Store` | `*staterpc.Client` |
| `BindingTaskCreator` | — | 1 | `*Store` | `*staterpc.Client` |

**Key property of the remote client:** `*staterpc.Client` is a *single* adapter that wraps
one `StateStoreClient` and is asserted against **whichever narrow Go interfaces its
caller needs** (exactly as `gatewayrpc.Client` asserts both `gateway.ChatContract` and
`gateway.SessionStore`). The Go contracts stay narrow (≤8, the `interfacebloat` cap);
the wire is one service. `WorkflowStore` introduces **no new RPCs** — it is a narrow
consumer face over the same `Update`/`Transition`/`InsertEvent` RPCs.

---

## 3. Method ownership

`*Store` remains the single implementation of all eleven interfaces. Ownership below is
about which **process** is the primary consumer of each method today, and therefore
which methods are genuinely cross-process on the wire today vs. daemon-internal.

Process legend: **DAEMON** = `archied` (incl. in-process webui + gateway runtime +
`storerpc.Server`); **AGENT** = `archie-agent` worker (no DB connection); **GATEWAY**
= store access through daemon-side narrow adapter interfaces.

| Contract | Primary consumers (process) | Remote on the wire? |
|---|---|---|
| `TaskLifecycle` (8) | DAEMON (dispatch, claim, action) + **AGENT** (`Transition`, `Update` via storerpc) | `Transition`/`Update` = **YES** (agent); rest DAEMON |
| `TaskArchiver` (1) | DAEMON (`taskactions` archive) | not yet |
| `TaskRetryer` (1) | DAEMON (`taskactions` retry) | not yet |
| `TaskQueries` (7) | DAEMON + GATEWAY (status/list adapters) | not yet (`OpenPRs`/`Tasks`/`StatusCounts` do cross to gateway adapters in-process) |
| `TaskEvents` (7) | DAEMON + **AGENT** (`InsertEvent` via storerpc) | `InsertEvent` = **YES** (agent); rest DAEMON |
| `WorkflowStore` (3) | **AGENT** only | **YES** (the sole genuinely remote consumer today) |
| `CaptureStore` (2) | DAEMON (webhook intake + mapping editor) | not yet |
| `MappingStore` (5) | DAEMON (dashboard editor + dispatch loop) | not yet |
| `BindingStore` (6) | DAEMON (dashboard editor + dispatch loop) | not yet |
| `BindingDispatcher` (3) | DAEMON (dispatch loop) | not yet |
| `BindingTaskCreator` (1) | DAEMON (dispatch loop) | not yet |

**Consequence for migration order:** the **agent's `WorkflowStore` is already
cross-process today** (via the NATS `storerpc` surrogate), so it is the first contract
to move to gRPC. The daemon-internal surfaces (`Capture`/`Mapping`/`Binding`) stay
in-process until the daemon itself points at `archie-state-store`. `TaskStore` (the
large composite) moves last.

**Two orphan methods** — `TaskQueries.ClearTerminalTasks` and
`TaskQueries.IncrementRetryCount` have **no production consumer** (only `*Store` tests).
They remain part of the Go `TaskQueries`/`TaskStore` surface, and therefore part of the
wire contract, but are flagged for deletion: they must **not** gain dedicated consumers
or wire methods in this work. Do not add the rest of the codebase to them.

---

## 4. DTO / wire representation

### Proto layout

- **Service:** `proto/state/v1/state.proto`, `package state.v1`, **one** `StateStore`
  service (mirrors the gateway's single `ChatService`; proto bypasses the Go
  `interfacebloat` cap, as `ChatContract` does via `//nolint:interfacebloat`).
- **Codegen:** the existing buf v2 pipeline. `proto/buf.yaml` (module, lint STANDARD,
  breaking FILE against `main`), `proto/buf.gen.yaml` **managed mode** with
  `go_package_prefix → github.com/samcharles93/archie-core/internal/contracts`, plugins
  `protoc-gen-go` + `protoc-gen-go-grpc`, `out: ../internal/contracts`,
  `opt: paths=source_relative`. **No `go_package` in source** — managed mode owns it.
  Generated Go lands in `internal/contracts/state/v1/` (package `statev1`), committed
  (`proto:check` fails on uncommitted/unmodified generated files).
- **Adapter package:** `internal/infrastructure/staterpc/` with `server.go` (registers
  the generated server over a `store.TaskStore`), `client.go` (wraps the generated
  client into `store.*` facades), `values.go` (proto ⇄ domain mappers), and
  `conformance_test.go` (bufconn local-vs-grpc dual-adapter suite), mirroring
  `internal/infrastructure/gatewayrpc/`.
- **Binary:** `cmd/archie-state-store/main.go`, mirroring `cmd/archie-gateway/main.go`
  and the serve pattern in `internal/app/archied/gateway.go` (`serveGateway`).

### The `StateStore` RPC set (union of the contracts, grouped)

Methods are named after the store methods so the mapping is unambiguous. `WorkflowStore`
is a *view*, not new RPCs — `Update`/`Transition`/`InsertEvent` are shared with
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

That is **40 unique RPCs** across one service (the `TaskEvents`.Close method is dropped).
`Close()` is **excluded** from the wire (it is server lifecycle, not a client call) — see §8.

### Domain types and `values.go` mapping

`values.go` maps proto ⇄ these types (all already cross boundaries today via JSON):

| Domain type | Package | Notes for `values.go` |
|---|---|---|
| `store.Task` | `internal/store` | time fields stored as layout strings; convert to/from `google.protobuf.Timestamp` |
| `events.Event` | `internal/events` | `Timestamp` → `google.protobuf.Timestamp` |
| `store.CapturedEvent` | `internal/store` | headers/body/remote_addr; body capped 256 KiB |
| `mapping.Mapping` | `internal/domain/mapping` | JSON-path field bindings |
| `binding.Binding` | `internal/domain/binding` | secret is **already decrypted** before returning; keep decrypted form out of any durable/logged representation |
| `store.WorkflowStat` | `internal/store` | aggregate row |
| `store.StageStat` | `internal/store` | aggregate row |
| `store.DayTokens` | `internal/store` | aggregate row |

### Wire-contract blocker fixed here: `RecordDispatch`

`store.BindingDispatcher.RecordDispatch(ctx context.Context, tx *sql.Tx, bindingID,
bindingVersion int64, captureID int64, taskID int64) error` takes a **`*sql.Tx`** that
cannot cross a gRPC boundary.

**Evidence that the `*sql.Tx` is removable:** the dispatch loop calls
`RecordDispatch(ctx, nil, ...)` (`internal/daemon/daemon.go:591`). The implementation
falls back to the store's own `*sql.DB` when `tx == nil` ("best-effort, not atomic"; the
comment and the `TestRecordDispatchViaExplicitTx` test are the only consumers of the
non-nil `tx` path). Production never opens the explicit transaction.

**Decision:** the contract drops `*sql.Tx` from `RecordDispatch` (the method becomes
`RecordDispatch(ctx, bindingID, bindingVersion, captureID, taskID) error`). The store
service owns any transaction boundary internally when atomicity is wanted; today the
at-most-once guarantee is the `INSERT OR IGNORE` dedup row, which is already
transactionless in production. `TestRecordDispatchViaExplicitTx` is superseded by the
store service's own internal-transaction test.

---

## 5. Error semantics

### Current state (both boundaries lose error identity)

- **NATS `natsrpc.Envelope`** (`internal/natsrpc/natsrpc.go`) serialises any error to a
  single `err.Error()` string and rehydrates a fresh `errors.New(string)`. The original
  type, `%w` chain, and sentinel identity are lost → `errors.Is(err, store.ErrStaleTransition)`
  is **false** across the boundary.
- **gRPC `gatewayrpc`** currently returns raw `err` from handlers; gRPC-Go converts to
  `codes.Unknown` with only the message. The **only** explicit code in the repo is
  `codes.Unavailable` for a missing session store (`server.go:31`). No status-code
  mapping for store errors exists.

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
| not-found-as-`(nil,nil)` (`TaskByID`, `GetBinding`, `GetMapping`, `ClaimNext`, `ClaimByIssue`, …) | `NotFound` with a `found=false` field (NOT an error) | `(nil, nil)` |
| infra / wrapped error | `Internal` (full message in details) | wrapped `fmt.Errorf(...%w)` |
| capability absent | `Unavailable` | context/typed error |

The `found=false`-not-error convention matters: the repo deliberately returns
`(nil, nil)` for not-found in reads and a sentinel for not-found in writes. The wire must
preserve **both** conventions (a `GoogleRPCStatus` with `NotFound` is an *error*, which
would wrongly surface to callers that expect `(nil, nil)`). Implement `found` as an
explicit boolean field on read responses, and map `(nil,nil)` ⇄ `found=false` + `OK`.

---

## 6. Limits

**Interface bloat (`interfacebloat` cap = 8)** — `.golangci.yml:89` `interfacebloat: max: 8`,
which overrides the upstream default of 10. The Go consumer facades must stay ≤8 methods.
`TaskLifecycle` is exactly 8 (at the cap); `TaskStore` is the 24-method composite that the
cap is why it is decomposed; proto services bypass the cap (like `ChatContract`).

**Payload caps:**
- Capture body: `defaultCaptureMaxBodyBytes = 256 KiB` (may reduce further; wire message
  must not exceed gRPC's default 4 MiB receive limit for a single capture).
- Task `detail`/`park_reason`/event `Detail`: 4000-char hard cap (`clip(s, 4000)`).

**Retention (stays server-side in the store; the wire does not carry pruning decisions):**
- `events` table: **no prune, unbounded growth** — a known risk to revisit, not changed here.
- `captured_events`: prune-on-write, default 7 days / 5000 events.

**Pagination/arity:** store read methods take a raw `int limit` (no store-side cap); the
caller-side caps (task list 100, log 2000, captures 100, capture scan window 5000, binding
dispatch batch 100, session list 20) remain caller-side. The wire passes `limit int` and
returns `[]*` slices, mirroring the existing Go signatures — **no cursor/paging protocol is
introduced** in this work (the current `EventsSince(sinceID, limit)` offset cursor is the
only one, and it stays as-is).

---

## 7. Mode / config shape

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
  extracted). **Set `Target` → dial gRPC** and use `*staterpc.Client`. This differs from
  the gateway, where `Target` is always defaulted to `127.0.0.1:8585` because the gateway
  is already a separate process.
- Because `[services.state]` is **not** in `config.example.toml` today (the gateway's own
  section is also missing — documentation debt), **add `[services.state]`** with a
  commented `target` to `config.example.toml` in the same change that introduces the
  config field, so the seam is visible to operators.
- Composition root: `internal/app/archied/bootstrap.go`. Base path = the local
  `*store.Store` (already opened by `openProductionTaskStore` / the store service owns
  `openStores`); if `cfg.Services.State.Target != ""`, dial gRPC and assign
  `b.stateStore = staterpc.NewClient(conn)`.

> **Mode fork, decided.** The gateway uses a single `target` address (no enum). The State
> Store boundary uses the same seam for symmetry, but with the opposite default
> (local, not remote) because the State Store is not yet extracted. No `mode` field is
> added.

---

## 8. Local-adapter compatibility + conformance

- `*store.Store` (local) and `*staterpc.Client` (remote) satisfy the **same** store
  interfaces. Use **explicit** `var _ store.X = (*Client)(nil)` assertions on **both**
  adapters (the gateway's local adapters use only structural/return-type assertions; the
  store precedent's explicit `var _` style is stronger and should be adopted on both sides).
  ```go
  var _ store.WorkflowStore = (*staterpc.Client)(nil)  // first contract to move
  // then progressively CaptureStore, MappingStore, BindingStore,
  // BindingDispatcher, BindingTaskCreator, and finally TaskStore.
  var _ store.WorkflowStore = (*Store)(nil)            // local, already present in store
  ```
- **Conformance suite:** a `staterpc/conformance_test.go` bufconn suite runs the **same**
  contract through both `"local"` and `"grpc"` adapters (mirroring
  `gatewayrpc/conformance_test.go`), asserting error-sentinel fidelity and the
  not-found-as-`(nil,nil)` convention both ways.
- **`Close()`:** stays on the Go store interfaces (so `TaskStore`/`TaskEvents` remain
  fully satisfiable) but is **not** a `StateStore` RPC. The remote `staterpc.Client`
  implements `Close()` as a **no-op**; the store service owns its own DB lifecycle. When
  the daemon hands the store to `archie-state-store`, `b.cleanup()` no longer calls
  `store.Close()` on a remote client (it is the daemon's own lifecycle only when it owns
  the store in-process).

---

## 9. Cutover / deletion rules

1. **Ratify the contract (this document).** No code.
2. **`.4.2` — generalize `storerpc` to the gRPC contract.** Move the agent's
   `WorkflowStore` (`Update`/`Transition`/`InsertEvent`) from NATS JSON `storerpc` to
   `staterpc.Client` over gRPC. The daemon serves the gRPC `StateStore` server
   **in-process (multiplexed)** first so the agent has a gRPC target before the standalone
   binary exists (PRD §3 "multiplexed in-process is the migration default").
3. **`clear NATS storerpc` only after the agent is fully on gRPC** — delete
   `internal/storerpc` (Server, Client, subjects `archie.store.update|transition|insert_event`)
   and the `natsrpc.RegisterAll` registration in one commit, and remove the now-unused
   `store.WorkflowStore` NATS path in `internal/infrastructure/agenttransport/nats`.
4. **`[services.state].target` stays empty (inproc)** through `.4.2`; `archie-state-store`
   `cmd/` binary is **`.4.3`** and hosts the same `StateStore` gRPC service.
5. **Swap daemon-internal contracts one at a time** (only once the daemon points at
   `archie-state-store`): `Capture → Mapping → Binding → TaskStore`. Each swap is
   independent — add the remote-client composition path, run the per-contract conformance
   suite, flip `[services.state].target`, delete the local use. A consumer mixes in-process and
   remote contracts for a contract-code (no dual-live *window*; each contract is either
   local or remote at a time).
6. **Final flip / deletion of the in-process serving path:** when the daemon no longer
   owns the store (i.e. `TaskStore` has moved), delete the in-process `openProductionTaskStore` /
   `wireWebStoreSurfaces` path in the daemon in a **same-commit** deletion — no dual-store
   ownership. The single SQLite file remains owned by `archie-state-store`.
7. **Never** split `archie.db` or add a second store. The gateway keeps its own
   already-separate session SQLite; that is unchanged and out of state-store scope.

---

## 10. Decisions recorded (for the review trail)

- **Producer-owned contract** (contracts stay in `internal/store`), not consumer-owned.
- **One `StateStore` gRPC service** (40 RPCs, grouped by contract) + narrow Go consumer
  facades (≤8) on `staterpc.Client` — mirrors the single-`ChatService` precedent.
- **`RecordDispatch` drops `*sql.Tx`** (wire-blocker; production passes `nil` today).
- **`Close()` not on the wire**; remote client `Close()` is a no-op.
- **Structured gRPC error codes** with client rehydration to store sentinels (preserves
  `errors.Is`), and a `found=false` + `OK` convention for read not-found.
- **Mode = presence-based `[services.state].target`**, default **local** (not yet
  extracted); add the `[services.state]` section to `config.example.toml`.
- **Explicit `var _` assertions on both adapters**; per-contract bufconn conformance.
- **Migration order:** `WorkflowStore` (agent) → stand up `archie-state-store` →
  `Capture` → `Mapping` → `Binding` → `TaskStore`; NATS `storerpc` deleted once the agent
  is on gRPC; in-process store path deleted in a same-commit flip.
- **Orphans `ClearTerminalTasks`/`IncrementRetryCount`** retained but not built upon.
