# Service decomposition open questions: research for #1, #4, #5

Status: research pass that resolves three of the five blocking open
questions in [[service-decomposition]] (the Proposed-status PRD). It
extends — it does not replace — that PRD and
[[service-discovery-research-2026-09-05]]. Questions #2 (contract
definition mechanism) and #3 (in-process vs out-of-process during
migration) are deliberately left to the resolver working those tracks;
where a phase below depends on #2's mechanism it is flagged as a
dependency, not answered here.

Produced by an agent research pass against primary sources: a
supervisor-run set of `deep_search_exa` queries plus direct fetches of
the canonical primary texts (Martin Fowler / Zhamak Dehghani's
monolith-breaking article, microservices.io's database-per-service
pattern, Microsoft's .NET microservices data-sovereignty guidance, the
Kubebuilder book, Helm's chart documentation). Every recommendation is
grounded in the verified archie-core coupling findings from the PRD and
in the code (checked at `internal/app/archied/main.go`,
`internal/webui/server.go`, `internal/webui/api_chat.go`,
`internal/store/interface.go`, `internal/store/store.go`,
`internal/storerpc/storerpc.go`, `internal/gateway/session_store_sqlite.go`,
`internal/app/archied/bootstrap.go`). Reproduced close to verbatim as the
record; light formatting only.

Repositioning note: the PRD's own text floats a hypothesis ("Gateway
Service's `ChatContract` should exist before UI Service is extracted").
This document **confirms that hypothesis with code archaeology and
strengthens it**, and it is the single most important finding: archie-core's
Gateway is both the *stickiest* capability and the one with an already-
clean seam and already-isolated data. The generic "extract a leaf /
read-only thing first" advice does not apply here, and I say so with the
reason why.

---

## Summary of the three recommendations

| # | Question | Recommendation (one line) |
|---|---|---|
| 1 | Extraction order | **Gateway Service first**, then State Store, then UI, then Messaging, then Execution/Runner/Scheduler, then Curator (optional) — preceded by an in-process "contract seams" phase (modular monolith) that is #2's real prerequisite. |
| 4 | State Store's contract shape | **One State Store service owns a single SQLite file behind narrow typed contracts** (the existing `internal/store` split already does the ownership; the gap is enforcement). No physical database-per-service at this scale, and no distributed-transaction problem, because there is still one store. Gateway keeps its own already-separate session SQLite. |
| 5 | Helm chart / Operator scoping | **Three charts** (a `library` chart of the shared contract templates, one `archie` chart for the 7 core services, one `archie-curator` chart + a Curator Operator) — and the smallest first milestone is a **single Curator CR that the Operator reconciles into a Deployment + Service, with Gateway resolving `curator` via the `ServiceRegistry`**. |

---

## Question #1 — Extraction order

### The question and the hypothesis

The PRD asks which service is pulled out of the monolith first and what
staged sequence follows. Its own text proposes: Gateway Service's
`ChatContract` should exist before UI Service is extracted, "since UI is
the most entangled consumer today."

### Verified coupling (the load-bearing facts)

The PRD's three cited couplings are real; I re-checked the call sites:

1. **Telegram task-action routes through webui's HTTP handler.** The
   message path is `chatTaskActorAdapter.ApplyChatTaskAction`
   (`internal/app/archied/main.go:655`) which the daemon wires and which
   dispatches into `internal/webui`'s `handleTaskAction`
   (`internal/webui/api_tasks.go:136`, mounted at
   `server.go:294`). Messaging Service (the Telegram adapter) has no
   direct route to the gateway today — it must detour through a UI HTTP
   handler to apply a pause/resume/abandon action.
2. **UI reaches directly into gateway internals.**
   `internal/webui/api_chat.go`'s `ChatService` is *literally* `Router
   *gateway.Router`, `Sessions gateway.SessionStore`, `Turns
   *gateway.Turns` plus `Models gateway.ModelManager` and `Personas
   *gateway.PersonaRegistry`, and it calls `chat.Sessions.(gateway.TurnHistory)`
   at line 198 and `chat.Router.Route`/`ResolveSessionKey` at 256/261.
   This is not "a UI calling a Gateway API" — it is the UI holding the
   Gateway's internal objects and calling their methods in-process.
3. **A shared `config.Holder`.** `bootstrap.go:1236` comments explicitly:
   "web and the daemon must share ONE Holder"; the daemon's live Holder is
   handed to webui at `bootstrap.go:336` and again where the dashboard
   mutation path runs.

Two further facts the PRD does not foreground but that decide the order:

- **The Gateway is already decoupled from the store.** `main.go:139`
  and `gateway/tasks.go:44` state it deliberately: gateway does **not**
  import `internal/store`. It consumes narrow contracts
  (`StatusReader`, `TaskCreator`, `TaskLister`, `TaskController`,
  `AgentReader`) that `internal/app/archied` adapts. This is a ready seam.
- **The Gateway already owns an isolated session store.**
  `internal/gateway/session_store_sqlite.go` is a *separate* SQLite DB
  (`OpenSQLiteSessionStore`, own `sqliteSessionSchema`) from
  `internal/store`. Chat session/turn/message data is **already** separate
  from task/work-intake data.

### What the research says about sequencing

The canonical guidance is Fowler / Dehghani's *How to break a Monolith
into Microservices*. Its explicit sequencing principles are: **warm up
with a simple and fairly decoupled capability**; **minimise dependency
back to the monolith**; **split sticky capabilities early**; **decouple
vertically and release the data early**; **decouple what is important to
the business and changes frequently**; **go macro first, then micro**;
**migrate in atomic evolutionary steps**. The popular-strangler-fig
commentary (YuSMP, Md Sanwar Hossain, CloudRPS) adds a pragmatic
variant: improve the monolith into a *modular monolith* first, create a
routing facade, extract the **low-coupling / leaf / read-heavy** context
first, defer auth and the core "sticky" engines to later, and treat the
first extraction as an **operations dry-run** (build pipeline, contract
testing, observability, rollback) rather than maximising value.

There is a real tension here. The bulk of the practitioner advice says
"extract the least-coupled leaf first." But that advice is calibrated for
large, team-owned, revenue-critical systems where a first extraction
regression is a production incident and where the team has not yet found
its seams. Two of its own authors note the counterweight: Fowler's
**"split sticky capabilities early"** explicitly warns against deferring
the tangled capability, on the theory that the longer you wait the more
sticky and expensive it becomes; and StackAuthority's blueprint sorts by
**data coupling and change velocity**, not pure ease — "order matters more
than most teams realise."

### Why the generic "leaf first" is wrong here

archie-core is not the enterprise monolith those guides assume. It is
already a **modular monolith with clean domain boundaries**
(`organisation.md` is enforced; `internal/domain/*` owns one cohesive
job; the six-domain split is settled). It has **no** organisational
blast-radius to protect (solo maintainer's daemon, not a payment path),
and its **actual** cost is concentrated in the process-packaging tangle,
not in undiscovered seams. It also has what the "seam-finding" guides
tell you to look for but rarely find: a capability that is **sticky and
already has a clean contract seam and already-isolated data**.

So the deciding question is not "which is the safest to cut first?" It is
"which capability, if cut, releases the most entangled consumer and
stands up the product's primary contract?" The answer is the Gateway.

This **confirms** the PRD hypothesis, and adds a reason the PRD did not
state: the Gateway is extractable *now*, on its own, with zero store
migration, because it is already store-decoupled and already owns its
session DB. UI is **not** extractable first — it is trapped behind the
Gateway's internals and can only become a thin client once
`ChatContract` exists. Extracting Gateway first is therefore also the
cheapest way to unblock UI (the most entangled consumer), which is the
PRD's ultimate goal.

### Decision: the ordered phase list

This is a **phase list** (each phase is a stable, working, independently
releasable step; it is not a task breakdown). Each phase is one "atomic
evolutionary step" per Fowler.

**Phase 0 — Contract seams (still a modular monolith, no process split).**
Introduce, in-process, the seams that make later extraction a swap of
transport rather than a rewrite: a `ChatContract` interface owned by the
Gateway (aligning `Router`/`SessionStore`/`Turns`/`ModelManager`/
`PersonaRegistry` behind it, so `webui` depends on the contract type, not
on the Gateway's structs); the `ServiceRegistry` interface from the
discovery research; and the State Store's already-narrow interfaces
(`TaskStore`/`CaptureStore`/`MappingStore`/`BindingStore`) ratified as the
State Store contract. **Dependency:** this is where #2's contract
definition mechanism (`proto/` tree, codegen) lands; the seam is a
prerequisite for the process split but #2 owns its mechanism. No network
hop yet — this is the "modular monolith first" safe step.

**Phase 1 — Extract the Gateway Service (first real extraction).**
Stand up `archie-gateway` as its own binary serving `ChatContract` over
gRPC, using its existing isolated session SQLite and its existing
store-adapters. Migrate consumers (UI, and the Messaging/Telegram
task-action path) to call `ChatContract` over gRPC rather than holding
`gateway.Router` in a struct or HTTP-detouring through webui. **Why
here:** stickiest capability (everything routes through it) → Fowler's
"split sticky capabilities early"; already store-decoupled → "minimise
dependency back to the monolith" is already satisfied; highest-value
contract (the PRD calls gRPC sync the product's primary contract); and it
directly dissolves coupling #1 (Telegram→webui detour) and #2
(webui→gateway internals) at their root. This is the operations
dry-run — prove the GCP/gRPC/discovery playbook on the thing the whole
product runs through.

**Phase 2 — Extract the State Store Service.**
Expose `internal/store` as its own service behind its narrow contracts
over gRPC (this is the generalisation we already have the seed for —
`internal/storerpc` today proxies `store.WorkflowStore` to the agent over
NATS; see Q4). Migrate the direct store imports in `internal/webui`,
`internal/domain/workflow`, and the intake/binding paths onto the
contract. **Why here:** "release the data early" (Fowler); UI and Workflow
both read store state **directly** today, so neither can become a peer
until that path is a contract call; and it is the lowest-risk data move
because per Q4 it stays one SQLite file behind an API. Leaves a working
system: Gateway on gRPC, everything else in the monolith but reading
state via contract.

**Phase 3 — Extract the UI Service.**
Now a genuinely thin client: serves the SPA from `ui/dist` and calls
Gateway's `ChatContract` and State Store's read contracts. The
`config.Holder` sharing and store/gateway internal access are gone. This
closes the last verified coupling (#3 — shared Holder) and the last of the
webui-entanglement list.

**Phase 4 — Extract the Messaging Service.**
Telegram/email/webhook adapters become a peer service that calls
Gateway's `ChatContract` directly (no UI HTTP detour). Now every async
channel is a first-class peer; the webui is no longer in any message path.

**Phase 5 — Extract Execution Environment + Runner + Scheduler.**
`internal/domain/workflow` dispatch, `internal/container/pool.go`,
`internal/agentexec`, `internal/worktree` become the Execution Environment /
Runner services (consuming State Store's `WorkflowStore` contract and the
existing container contract); `internal/domain/scheduling` +
`internal/infrastructure/cronstore` become the Scheduler. These are the
heaviest and most store-coupled, so they go last; by here the facade
(Gateway) and the data seam (State Store) are already proven.

**Phase 6 — Curator Service (the optional one, last).**
Install as a Gateway plugin (its current shape is already a contract /
registrar / registry family) and prove the latest-order discovery path +
the optional-service `NotInstalled` semantic. This is deliberately last:
it is the proof-of-optionality and the Q5 exercise, not a load-bearing
first step. (The K8s Operator to *deploy* it is Q5's concern and can be
built on the Q1 timeline once the base chart exists.)

### What this doesn't settle

- **#2 and #3.** The phase list assumes #2 (proto/codegen) and #3
  (multiplexed in-process) are resolved consistently: Phase 0 is the
  modular-monolith with in-process seams, and Phase 1 is the first true
  process split. If #3 decides against in-process multiplexing, Phase 0
  collapses into Phase 1 for the Gateway.
- **Whether Gateway should also host a "chat gateway" that owns the
  Session Store contract** vs. delegating chat state to a future concern.
  Decided *not* to move session data into State Store here — it is already
  isolated and Gateway-owned; moving it would be churn with no benefit.
- **Exact cadence / whether Curator can be extracted earlier.** It *can*,
  but only as the operator/optionality exercise; keeping it last avoids
  spending the "warm-up learning budget" on an optional component when the
  base services are not yet on the contract.

---

## Question #4 — State Store's contract shape

### The question

Does every extracted service get its **own storage** (full microservice
data isolation), or do **several services share one State Store service's
contract** (less isolation, less migration risk, avoids a
distributed-transactions problem)?

### Codebase evidence

- **`internal/store` is one SQLite DB** (`internal/store/store.go`,
  `Open()`, `sqliteDSN()`, WAL + busy_timeout pragmas) with 7 tables:
  `tasks`, `transitions` (workflow execution), `events` (observability),
  `captured_events`, `bindings`, `binding_dispatches` (work intake /
  binding), `field_mappings` (mapping). It is a single file used by many
  domains.
- **The store is already interface-decomposed, not monolith-shaped.**
  `internal/store/interface.go` splits a broad `TaskStore` into narrow
  contracts: `TaskStore`, `WorkflowStore`, `CaptureStore`, `MappingStore`,
  `BindingStore`, `BindingDispatcher`, `BindingTaskCreator`. Each is
  deliberately narrow ("the interfacebloat limit").
- **A store-as-contract-to-foreign-process pattern already exists and
  works.** `internal/storerpc/storerpc.go` exposes `store.WorkflowStore`
  (Update / Transition / InsertEvent) to the `archie-agent` container over
  NATS request/reply, and its doc comment states the design fact: "archied
  remains the sole owner of the `store.TaskStore`." This is the exact shape
  the State Store service needs; it just needs to be generalised from
  `WorkflowStore` to the full contract set and moved to the sync (gRPC)
  transport.
- **The Gateway already owns a separate session SQLite.** As noted in Q1,
  `internal/gateway/session_store_sqlite.go` is its own DB with its own
  schema. So session/chat state is **already** isolated from task/work
  state — the natural two-owner split is already present in code.

### Does "every service its own storage" buy anything here?

The research (microservices.io, Microsoft Learn data-sovereignty, AWS
Prescriptive Guidance) is emphatic that the database-per-service pattern is
**about ownership and enforcement, not about physically provisioning one
database cluster per service.** microservices.io names three ways to keep a
service's data private — *private-tables-per-service*,
*schema-per-service*, and *database-server-per-service* — and says the
first two have "the lowest overhead," and that a shared MySQL instance with
per-service logical databases and per-service credentials is a legitimate
instance of the pattern. Microsoft's guidance and the ownership-over-
isolation posts (Progressive Robot, DEV's Aditya Pradhan, aakashx) make
the same point with a shared framing: **shared write ownership is the real
danger, not shared physical infrastructure.** aakashx's maturity ladder
(Level 0 shared-DB+shared-tables → Level 3 service-owned DBs → Level 5
independent lifecycle) is precisely the "ownership first, physical split
only when justified" path.

The decisive variable everywhere is **team topology**, not technology.
DevStarSJ's 2026 comparison gives the rule of thumb: 1–5 engineers →
shared DB (monolith is fine); 5–20 → shared DB with strict ownership
boundaries; 20–50 → hybrid; 50+ → database per service. The argument,
across all sources, is that database-per-service's *benefits* are team
autonomy, independent deploy cadence, and failure blast-radius isolation —
three things that are **largely absent for a solo maintainer**. You still
pay the full "distributed data tax" (no cross-service joins → sagas or
CDC/outbox + read models; eventual consistency; more data stores to
operate and back up; a new class of ordering/duplicate events). For a
single person shipping one product, that tax buys autonomy you do not
have a team boundary to exploit.

### Decision: one State Store service, single SQLite, narrow contracts, no physical per-service database

**Recommendation:** archie-core keeps **one State Store service** that
owns the existing single SQLite file and exposes its **narrow typed
contracts** (`TaskStore`, `CaptureStore`, `MappingStore`, `BindingStore`,
`WorkflowStore`, …) over gRPC. Consumers (UI, Workflow, Work Intake,
Execution/Runner) call the State Store's contract; they never reach into
each other's tables — and because there is **still one store**, there is
**no distributed-transactions problem** (the very thing the question's
"share one contract" alternative is trying to avoid). The Gateway keeps
its own already-separate session SQLite (it is Gateway-private chat state
and already isolated; moving it is churn).

This is "less isolation, less migration risk" chosen deliberately — it is
the *honest* choice for a solo maintainer per the team-topology rule of
thumb, and it matches the store's existing interface decomposition (the
ownership is already correct — the *enforcement* is the gap: webui and
workflow import `internal/store` directly across a would-be service
boundary).

**Do NOT provision a physical database per service.** The only parts of
the PRD's "every service its own storage" framing that buy anything at
this scale are the ones that are already true in code: the Gateway owns
its session DB, and the interface split gives each domain a narrow
contract. Splitting the remaining SQLite file into per-service files
would introduce the distributed-data tax — cross-store joins for the
dashboard's combined task/event/capture views, sagas for intake→workflow
transitions, m×n data-store operations — with no corresponding autonomy
gain for a single maintainer. It is also the single most common source of
a *distributed monolith*, which the sources call the failure mode to
avoid.

### Concrete migration path from the current single SQLite store

1. **Ratify the contracts (cheap, already done).** Treat the narrow
   interfaces in `internal/store/interface.go` as the State Store
   contract surface. No code change; this is a naming/authority decision.
2. **Generalise the store surrogate — this is the step that becomes the
   State Store service.** `internal/storerpc` currently proxies
   `WorkflowStore` to the agent over NATS. Broaden it to serve the full
   narrow contract set, and (per the PRD, gRPC is the sync transport) move
   it from NATS request/reply to gRPC. The `archie-agent` / `storerpc.Client`
   that satisfies `store.WorkflowStore` is the template for the client
   every consumer gets.
3. **Swap consumers one contract at a time from direct import to the
   contract call.** Order by blast radius: start with the low-touch reads
   (`CaptureStore`, `MappingStore`, `BindingStore`) that a single UI
   screen uses, then `WorkflowStore` (already proxied), then the
   `TaskStore` lifecycle surface the daemon uses. Each swap leaves a
   working binary because the in-process `*store.Store` still backs the
   contract — this is a pure interface-boundary change, not a data move.
4. **Stand up the State Store process.** Once every consumer speaks the
   contract, run `internal/store` as `archie-state-store` (a gRPC server
   over the same SQLite file), and delete the in-process imports. The
   single `archie.db` file is unchanged; only the access path changes.
5. **Only split the file if a concrete need appears.** If task-history
   volume or a specific isolation/security requirement (e.g. a hosted
   multi-tenant boundary) later justifies it, SQLite makes a per-domain
   physical split cheap (one file per contract group, e.g.
   `archie-tasks.db`, `archie-intake.db`). But do **not** pre-split: the
   sources' consensus is to migrate toward physical separation "only when
   there is evidence (slow queries, deployment coupling, scaling
   bottlenecks)."

### What this doesn't settle

- **Cross-store reads for reporting.** The dashboard's combined
  task/event/capture/mapping views are built in one SQLite today. If
  those are split later, they need an API-composition / read-model path
  (the sources name CQRS / materialised view). Not a blocker now; it is
  the cost of a future physical split, which is exactly why the
  recommendation is to not pre-split.
- **Whether the Gateway's session store stays Gateway-owned or moves to
  State Store.** Decided: stays Gateway-owned (already isolated, high
 -frequency chat state; moving it has no benefit).
- **The exact gRPC contract schema / versioning.** That is #2's
  mechanism. The contract *surface* is decided here; the wire schema is
  not.
- **Migration of the `storerpc` NATS path.** The `archie-agent` container
  still calls `storerpc` today. Under the PRD the Execution/Runner
  processes are peers too, so the agent's store access should eventually
  use the same contract. When that moves is an Execution-track question,
  not a State-Store-shape one.

---

## Question #5 — Helm chart / Operator scoping

### The question

`service-discovery-research-2026-09-05.md` already commits to
Kubernetes-native discovery via a Helm chart + Operator as the primary
path. Scope what that actually requires as a first real deliverable: what
a first Helm chart for the ~7-service fleet needs to contain, what the
Curator Operator's reconciliation loop needs to do, and the smallest real
first milestone that proves "install Curator, Gateway discovers it" end
to end.

### What the base Kubernetes discovery needs (from the discovery research)

The discovery research's Path A (K8s-native) flow is the contract our
deliverable has to satisfy: base services deploy as Deployment + Service;
a Service's label selector matches the Curator pods; the endpoint
controller writes pod IPs into an EndpointSlice; readiness probes gate
which pods are ready; CoreDNS serves `curator.<ns>.svc.cluster.local`; the
Gateway's gRPC DNS resolver picks up the new endpoint on re-resolution
with no redeploy; removal empties the EndpointSlice and the record
disappears. Two facts shape the chart design:

1. **Optional service semantics.** A scaled-to-zero / not-yet-created
   Curator must resolve to `NotInstalled`, not `Down`. Under K8s that
   comes from DNS `NXDOMAIN`/empty resolution being treated as "absent,"
   which requires the Gateway to model "absent" as a distinct resolution
   outcome on the `ServiceRegistry` interface (already flagged in the
   research's addendum).
2. **Chart count.** Seven services is too many for one monolithic
   template and too few to justify seven charts. The idiomatic Helm
   answer is a **library chart** for the shared contract templates plus
   small per-deployable charts.

### Decision: three charts (library + core services + Curator-with-Operator)

- **`archie-library`** — a Helm **library chart** (`type: library`,
   only `_*.tpl` files). Holds the shared resource templates: the
   Deployment template, the Service template, readiness/liveness probe
   helper, container-port naming, the label/selector block
   (`app.kubernetes.io/name`, `instance`, `managed-by`, `component`), and
   the `ServiceRegistry`-relevant named ports (`grpc`, `http`, `ssr`,
   `nats`). This keeps the per-service charts declarative and consistent.
- **`archie`** — the chart for the **7 core services**: UI, Gateway,
   Messaging, Scheduler, Execution Environment, Runner, State Store. One
   `values.yaml` with a `services:` map; each service resolves to a small
   template from the library chart. It also lands the base **ConfigMaps
   / Secrets** that replace `config.toml` (a phased concern flagged in the
   discovery research — the chart should *reference* config as a separate
   concern, not block on relocating it). It must make each service's
   `Service` a **named-port** Service so gRPC resolution works, and each
   a **ClusterIP** Service (no headless needed for the base sync path; a
   headless/SRV Service is an optional addition the Gateway resolver can
   consume but is not required for the DNS A-record flow).
- **`archie-curator`** — the chart for the **optional Curator**, plus the
   **Curator Operator**. Operator chart installs the CRD, the
   controller Deployment (with the ServiceAccount / ClusterRole /
   ClusterRoleBinding the reconciler needs), and the RBAC markers; the
   Curator chart installs the sample `Curator` CR. Because Curator is
   optional, this chart is **not** deployed by default with `archie` — the
   whole point is that a customer installs it later and the fleet
   auto-aligns.

### What the Curator Operator's reconcile loop actually needs to do

This is a textbook Kubebuilder / Operator-SDK controller. The reconcile
loop (per the Kubebuilder book and the multi-resource reconciliation
pattern) is **level-triggered, not status-triggered**: it must always
converge the *whole* desired set, never react to a specific event. Per
`Curator` CR, the loop:

1. **Fetch the CR.** `r.Get(ctx, req.NamespacedName, &curator)`; treat
   `IsNotFound` as a normal "already deleted" outcome (owner references
   cascade child cleanup), not an error.
2. **Build the desired children.** Pure builder functions
   (`deploymentForCurator`, `serviceForCurator`) produce the Deployment
   (image, replicas, container port, readiness/liveness probes,
   `app.kubernetes.io/{name,instance,managed-by,component}` labels, env
   from a ConfigMap/Secret generated in the same pass) and the Service
   (label-selector matching the Deployment pods, the gRPC named port).
3. **Set owner references** with `controllerutil.SetControllerReference`
   so deleting the `Curator` CR cascades to its Deployment + Service.
4. **Apply idempotently** with `controllerutil.CreateOrUpdate` (or
   Server-Side Apply) — create if absent, patch toward desired if
   drifted; a manual `kubectl edit` on the Deployment is corrected on the
   next reconcile.
5. **Watch the children** via `Owns(&appsv1.Deployment{})` and
   `Owns(&corev1.Service{})` in `SetupWithManager`, so a pod crash or an
   out-of-band edit enqueues a reconcile of the owning Curator CR. Without
   `Owns()`, drift correction does not fire.
6. **Report status** on the status subresource (the
   `+kubebuilder:object:root`/`subresource:status` marker): set a `Ready`
   condition (via `meta.SetStatusCondition` with `ObservedGeneration`,
   and never hand-set `LastTransitionTime`), reflecting the worst child's
   readiness, and update only when `Status` changed. This is what the
   Gateway (or an operator/`kubectl wait --for=condition=Ready`) observes.
7. **RBAC** via Kubebuilder markers: full verbs on `curators` and
   `curators/status`, plus `get;list;watch;create;update;patch;delete` on
   `deployments` and `services`.

The reconciler stays a flat, dependency-ordered sequence (Deployment
before Service is not strictly required here, but the pattern is
dependency-first and idempotent-across-orders).

### The smallest real first milestone ("install Curator → Gateway discovers it")

Keep the first milestone as small as possible while proving the whole
flow through the `ServiceRegistry` — this is the "warm up on an optional
thing with a clean seam" exercise (and it is exactly what the discovery
research calls the product story). Concretely:

**Milestone M1 — "Curator installs and Gateway resolves it, no redeploy."**

- **CRD + Operator:** a `Curator` CRD and a controller that reconciles
  one Curator CR into one Deployment + one Service with owner references
  and a `Ready` status condition. (This is the whole operator; it is two
  files at its core — the API type and the reconciler.)
- **Base chart:** `helm install archie` deploys Gateway + one
  representative base service (start with Gateway + State Store + UI)
  behind the `ServiceRegistry`, with Gateway holding a resolver that
  already knows `curator`.
- **Curator chart:** `helm install archie-curator` (or `kubectl apply -f`
  a `Curator` CR) → the Operator creates the Deployment + `curator`
  Service → the endpoint controller writes the pod IP into the
  EndpointSlice → CoreDNS serves `curator` → the Gateway's resolver picks
  it up on re-resolution.
- **Assertion (the real "prove it"):** the Gateway logs / health shows
  `curator` flipping from `NotInstalled` to `Installed(Requesting)`
  (ready), and a Gateway-config or Gateway-restart is **not** required. Then
  `helm uninstall archie-curator` (or delete the CR) → EndpointSlice
  empties → DNS record goes away → Gateway drops `curator` back to
  `NotInstalled`. No Gateway redeploy, no code change.

This milestone deliberately does **not** include: config relocation from
`config.toml` to ConfigMaps/Secrets (a separate phased concern), the
NATS-fallback `ServiceRegistry` implementation (can be stubbed with a
static map for the assertion), auth/mTLS/NetworkPolicy, or the full 7
services. It proves the one hard requirement — "install a service, the
fleet auto-aligns, no redeploy" — and the optional-service `NotInstalled`
semantic, with the smallest surface.

**What beads-epic + first issues would look like** (filing this directly
from here, as the task asks):

- Epic: `archie-core-k8s` (or the codename already in use) — "K8s-native
  service discovery: base Helm chart + Curator Operator proving the
  install→discover flow."
- Issue 1: Scaffold the Kubebuilder project and the `Curator` API
  (`group: archie.samcharles93.io`, `v1alpha1`), CRD, RBAC markers.
- Issue 2: Implement the `Curator` reconciler (create/update/delete
  Deployment + Service, owner refs, `Ready` status condition).
- Issue 3: Create `archie-library` chart with the shared Deployment /
  Service templates and named-ports.
- Issue 4: Create `archie` chart deploying Gateway + State Store + UI
  with per-service `ServiceRegistry`-friendly Services.
- Issue 5: Wire the Gateway's `ServiceRegistry` K8s implementation
  (DNS resolver) to model `NotInstalled` distinctly from `Down/Unhealthy`
  (the research's addendum requirement).
- Issue 6: End-to-end M1 assertion (install Curator CR → Gateway resolves
  it → uninstall → Gateway drops it), likely as an envtest / kind test.

### Sources used for Q5

- Kubebuilder Book — [Getting Started](https://www.kubebuilder.io/getting-started.html),
  [Resources Managed by the Operator](https://book-v3.book.kubebuilder.io/reference/watching-resources/operator-managed),
  [Owned Resources](https://kubebuilder.io/reference/watching-resources/secondary-owned-resources)
  (for the reconcile loop, `Owns()`, owner references, status subresource,
  RBAC markers).
- DEV Community — [Building Your First Kubernetes Operator with
  Kubebuilder](https://dev.to/tim_derzhavets/building-your-first-kubernetes-operator-with-kubebuilder-from-zero-to-production-4ipm)
  and [GoLinuxCloud — Kubernetes Operator Multi-Resource Reconciliation
  Patterns](https://www.golinuxcloud.com/multi-resource-reconciliation/)
  (level-triggered reconcile, idempotent `CreateOrUpdate`, dependency
  order, per-child status aggregation).
- Helm — [Charts](https://helm.sh/docs/topics/charts/) (the chart file
  structure; `Chart.yaml`; template dirs; the library-chart use of
  `_*.tpl` and `type: library`).

### What this doesn't settle

- **Config relocation.** The charts reference ConfigMaps/Secrets but the
  actual move of `config.toml` semantics into them is its own
  phased decision (flagged in the discovery research) and is out of scope
  for the first milestone. The base chart should degrade gracefully with
  an in-cluster config so M1 works before the relocation is done.
- **The NATS-fallback `ServiceRegistry` implementation.** M1 uses a K8s
  DNS resolver; the NATS KV implementation is a later, separate issue.
- **Auth / mTLS / NetworkPolicy / cert-manager.** Called out as a
  separate concern in the discovery research; not in the first milestone.
- **Operator SDK vs raw Kubebuilder/controller-runtime.** The loop is
  identical; the choice of kubebuilder scaffolding vs. Operator SDK is a
  tooling preference that does not change the M1 scope. Kubebuilder is the
  documented norm and is assumed here.
- **Whether the Operator should manage the *base* services too.** The
  first milestone only has an Operator for Curator (the optional one).
  A "fleet Operator" that reconciles all services is a larger, later
  idea; the base services can be plain chart templates for now.

---

## Sources (primary, fetched during this pass)

- Zhamak Dehghani, *How to break a Monolith into Microservices* (Martin
  Fowler site) — https://martinfowler.com/articles/break-monolith-into-microservices.html
- microservices.io, *Pattern: Database per service* —
  https://microservices.io/patterns/data/database-per-service.html
- Microsoft Learn, *.NET microservices: Data sovereignty per microservice* —
  https://learn.microsoft.com/en-us/dotnet/architecture/microservices/architect-microservice-container-applications/data-sovereignty-per-microservice
- Kubebuilder Book, *Getting Started* —
  https://www.kubebuilder.io/getting-started.html
- Kubebuilder Book, *Resources Managed by the Operator* —
  https://book-v3.book.kubebuilder.io/reference/watching-resources/operator-managed
- Kubebuilder Book, *Owned Resources* —
  https://kubebuilder.io/reference/watching-resources/secondary-owned-resources
- Helm, *Charts* — https://helm.sh/docs/topics/charts/
- (Supporting synthesis from the deep-search lanes: YuSMP strangler-fig
  guide, Md Sanwar Hossain strangler-fig, CloudRPS strangler-fig,
  Mobisoft first-microservice-to-extract, StackAuthority
  monolith-to-services blueprint, DevStarSJ database-per-service-vs-shared
  2026, Progressive Robot / DEV Aditya Pradhan database-per-service
  ownership-not-isolation, aakashx database ownership in microservices,
  AWS Prescriptive Guidance data persistence, DEV Tim Derzhavets building
  a Kubebuilder operator, GoLinuxCloud operator SDK tutorial and
  multi-resource reconciliation, Codelooru how to write a Kubernetes
  operator. These consulted sources corroborate the primary texts; the
  primary texts are cited for the load-bearing claims.)

Code evidence cited throughout is the archie-core tree at the time of
writing (see the file list at the top of this document).
