# Service decomposition -- decision

**Status:** Proposed -- not yet approved for implementation
**Date:** 2026-09-05
**Beads epic:** archie-core-8cda
**Prior art:** `docs/inspiration/2026-09-05-awx-service-decomposition-and-ansible-task-model.md`,
`docs/inspiration/module-reference-awx-decomposition-vs-archie.md`,
`docs/inspiration/service-discovery-research-2026-09-05.md`,
`/work/artefacts/archie-core-current-state-archify.html`,
`/work/artefacts/archie-core-ideal-state-archify.html`

## Decision

archie-core moves from one Go binary (`archied`) hosting every domain
in-process to a set of independently deployable services, each exposing
exactly one typed contract. Consumers depend on the contract; they never
reach into another service's internal types, session state, or config.
gRPC is the synchronous transport; NATS remains for asynchronous fan-out
(status, telemetry) rather than being replaced.

This is a **distribution decision**, not a rewrite of Archie's domain
model. The six-domain split already approved in
`docs/architecture/index.md` and `docs/architecture/organisation.md`
(Identity, Agent, Messaging, Workflow, Work Intake, Plugin) is unchanged;
this PRD is about how the *processes* running that domain logic get
packaged, contracted, and discovered -- not about redrawing domain
boundaries.

### Target services

| Service | Contract it owns | Existing code it replaces/wraps |
|---|---|---|
| UI Service | thin client only; talks to Gateway's contract | `internal/webui` (SPA hosting stays; API surface becomes a Gateway client, not a peer with direct access) |
| Gateway Service | routing / turn / session contract | `internal/gateway` |
| Messaging Service | channel-plugin contract (Telegram, email, Discord, Teams, ...) | `internal/channels` |
| Curator Service | curator contract; **optional**, installs as a Gateway plugin | `internal/domain/curator` |
| Scheduler Service | cron/ticker contract | `internal/domain/scheduling` |
| Execution Environment | execution-dispatch contract | `internal/domain/workflow`, task dispatch paths |
| Runner | isolated worktree/container execution | `internal/container`, `internal/agentexec`, `internal/worktree` |
| State Store | state-access contract | `internal/store` |

### Discovery

Per `docs/inspiration/service-discovery-research-2026-09-05.md`:

- **Primary:** Kubernetes-native discovery (Service + headless-Service/DNS
  + EndpointSlice, served by CoreDNS), delivered as a Helm chart + Operator,
  on k3s for single-host installs and a real cluster for multi-host or a
  hosted offering.
- **Fallback:** NATS-based discovery (micro `$SRV` announce + JetStream KV
  registry) for no-K8s single-host installs, with a custom gRPC resolver
  reading `host:port` from the KV bucket.
- Both implementations sit behind one internal interface:

  ```go
  type ServiceRegistry interface {
      Resolve(service string) []Endpoint
      Watch(service string) <-chan Event // join/leave
  }
  ```

- An optional service (Curator today; others later) that is not installed
  must resolve to a distinct `NotInstalled` outcome, never a `Down`/
  `Unhealthy` one. Under K8s this falls out of DNS returning no record;
  under the NATS fallback it requires an explicit "installed" marker in the
  KV registry, not just absence-implies-down.

### Distribution model this supports

Both self-hosted (customer runs the Helm chart or the NATS-fallback
binaries themselves) and a managed/hosted offering (we run the same Helm
chart) are in scope -- this is the RHEL / Ansible Automation Platform
model: one architecture, sold both ways. This PRD does not decide the
business model; it decides that the architecture must not foreclose
either shape.

## Why this shape

`docs/inspiration/module-reference-awx-decomposition-vs-archie.md`
established that Archie's existing domain split already does what AWX's
service-decomposition post argues for -- boundaries exist. What doesn't
exist is deployability: `internal/webui` is not a passive frontend today.
Verified couplings that this decomposition must eliminate:

- Telegram's task-action execution (pause/resume) currently routes through
  `internal/webui`'s own HTTP handler in-process
  (`chatTaskActorAdapter.ApplyChatTaskAction`,
  `internal/app/archied/main.go:224-242`, dispatching into
  `internal/webui/server.go:231`'s `handleTaskAction`). Under this PRD,
  Messaging Service calls Gateway Service's contract directly -- no HTTP
  detour through a UI process.
- `internal/webui/api_chat.go` calls `gateway.SessionStore`, `Router`, and
  `TurnHistory` directly (lines 97, 142, 198, 256, 261) instead of through
  a narrow interface. Under this PRD, UI Service only ever sees Gateway
  Service's `ChatContract`.
- `bootstrap.go:1239` shares the daemon's live `config.Holder` with webui
  rather than passing config. Under this PRD, each service owns its own
  configuration surface; nothing shares a live in-process struct across a
  service boundary because there is no longer a shared process.

## Non-goals

- Redesigning the domain model (Identity/Agent/Messaging/Workflow/Work
  Intake/Plugin boundaries are settled elsewhere and unchanged).
- Replacing NATS. It remains the async transport; this PRD only adds gRPC
  for synchronous request/response and (optionally) K8s-native discovery.
- Deciding the business model (self-hosted vs. managed vs. both). Kept
  open deliberately; the architecture must serve either.
- Config relocation (`config.toml` -> ConfigMaps/Secrets) is real work
  implied by a K8s-native deployment but is its own decision, not decided
  here. Flagged in the discovery research as a phased concern.
- Any concrete adoption of Pi (or another external agent harness) as an
  execution runner. Raised in conversation alongside this decomposition,
  but it is a separate decision (agent execution as a strict data
  boundary, `ARCHITECTURE.md`) and does not gate or get gated by this PRD.

## Open questions (blocking before implementation starts)

1. **Extraction order.** Which service gets pulled out of the monolith
   first? The webui entanglement findings above suggest Gateway Service's
   `ChatContract` should exist before UI Service is extracted, since UI is
   the most entangled consumer today. Not yet sequenced.

   **RESOLVED (2026-09-05).** Gateway Service first, then State Store,
   then UI, then Messaging, then Execution/Runner/Scheduler, then Curator
   (optional) — preceded by an in-process "contract seams" (modular
   monolith) phase. Confirms the hypothesis and strengthens it: the
   Gateway is already store-decoupled (it uses narrow contract adapters,
   not `internal/store`) and already owns an isolated session SQLite, so it
   is both the stickiest capability *and* the one with a ready seam and
   isolated data — extractable alone, first. Full phase list and reasoning:
   `docs/inspiration/service-decomposition-open-questions-research-2026-09-05.md#1`.
2. **Contract definition mechanism.** Protobuf/gRPC service definitions
   need a home (a new `proto/` or `contracts/` tree, versioning scheme,
   codegen wiring into `Taskfile.yml`). Not yet designed.

   **RESOLVED (2026-09-05).** buf v2 driving `protoc-gen-go` +
   `protoc-gen-go-grpc` (grpc-go, not connect-go — connect-go's net/http
   client breaks the discovery research's gRPC resolver plumbing for K8s
   DNS and NATS-KV). One committed `proto/<service>/v1/` tree, generated
   Go committed to `internal/contracts/<service>/v1/`, directory
   versioning (breaking change -> new `vN`, enforced by `buf breaking` in
   the gate), and `task proto:generate`/`proto:lint`/`proto:check` wired
   into `task check` and mirrored into `.github/workflows/deploy.yml`
   (CI does not run `task check`). Full toolchain comparison, layout
   rationale, and scratch-verified `buf`/`buf breaking`/`connect-go`
   results:
   `docs/inspiration/service-decomposition-open-questions-research-2026-09-05.md#2`.
3. **In-process vs. out-of-process during migration.** Whether services
   can run multiplexed inside one binary behind the same `ServiceRegistry`
   interface during a transition period (so extraction is incremental and
   testable) or whether each service must be a separate binary from day
   one. Strongly recommend the former for a solo maintainer, but not
   decided here.

   **RESOLVED (2026-09-05).** Multiplexed in-process is the migration
   default -- this is `docs/architecture/organisation.md`'s standing
   "Process boundaries" rule, not a new lean. Mechanism: one wire-safe Go
   contract interface per service; a local adapter and a generated gRPC
   client adapter, both satisfying the interface via a compile-time
   assertion in the existing `var _ store.WorkflowStore = (*Client)(nil)`
   idiom; composition (not `ServiceRegistry`, which stays network-only)
   picks `mode = "inproc" | "remote"`, default `inproc`; a per-contract
   conformance suite runs against both adapters over `bufconn`; the flip
   to a real process is a same-commit deletion of the in-process path, no
   dual-live window. Consequence: Gateway's Phase 0 (`ChatContract` seam)
   ships in-process first, with the network protocol staying out of the
   production path until Phase 1 -- no amendment to Q1's phase list
   needed. Full mechanism and evidence:
   `docs/inspiration/service-decomposition-open-questions-research-2026-09-05.md#3`.
4. **State Store's contract shape.** Whether every service gets its own
   storage (true microservice isolation) or several services share the
   State Store service's contract (less isolation, less migration risk).
   Not yet decided.

   **RESOLVED (2026-09-05).** One State Store service owns the existing
   single SQLite file behind narrow typed contracts (`TaskStore`,
   `CaptureStore`, `MappingStore`, `BindingStore`, `WorkflowStore`) over
   gRPC. No physical database-per-service: the solo-maintainer / team-
   topology rule of thumb plus the low value of per-service autonomy for
   one person rule it out, and one store means no distributed-transactions
   problem. The Gateway keeps its own already-separate session SQLite.
   Concrete migration path (generalise `storerpc`, swap consumers one
   contract at a time, stand up the store service, split the file only on
   evidence):
   `docs/inspiration/service-decomposition-open-questions-research-2026-09-05.md#4`.
5. **Helm chart / Operator ownership.** Net-new work with no existing
   analog in this repository (`deployments/` currently holds TOML profiles
   and a `docker-compose.yml`, not Kubernetes manifests). Scoping not yet
   started.

   **RESOLVED (2026-09-05).** Three charts: an `archie-library` chart
   (shared Deployment/Service templates, named ports), an `archie` chart
   for the 7 core services, and an `archie-curator` chart + Curator
   Operator (CRD, controller, RBAC). The Curator reconciler is a
   level-triggered loop that fetches the CR, builds Deployment + Service,
   sets owner refs, applies idempotently (`CreateOrUpdate`), watches via
   `Owns()`, and reports a `Ready` status condition. Smallest real
   milestone (M1): install a Curator CR → Operator creates Deployment +
   Service → CoreDNS serves `curator` → Gateway's resolver flips it from
   `NotInstalled` to Installed with no redeploy; uninstall flips it back.
   Full scoping and first bead-issue list:
   `docs/inspiration/service-decomposition-open-questions-research-2026-09-05.md#5`.

## Completion criteria

Not applicable yet -- this PRD records the target shape and the discovery
decision. It does not authorize implementation. Before work begins: file
a beads epic, resolve the open questions above (at minimum #1-#3), and
get explicit go-ahead given the scale of this change relative to
`AGENTS.md`'s "smallest workable change" default for this solo project.
