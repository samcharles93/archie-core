# Service discovery research for the archie-core service decomposition

Status: exploratory research, not a settled decision. This backs the "ideal
state" service-oriented sketch in
[[module-reference-awx-decomposition-vs-archie]] and its two Archify
diagrams (`/work/artefacts/archie-core-current-state-archify.html`,
`/work/artefacts/archie-core-ideal-state-archify.html`). Nothing here commits
the codebase to anything; if the service decomposition itself gets approved,
this is the input to the actual `docs/architecture/migration-decisions.md`
entry and/or a `docs/prds/` design doc, not a substitute for either.

Produced by an external research pass (Pi subagents plus supervisor-run
`deep_search_exa` queries against primary sources: NATS discovery, K8s-native
discovery, Consul operational footprint, gRPC/xDS/mesh weight). The K8s lane
detached mid-flight and was recovered via supervisor reply, covered from
fetched Kubernetes docs (Service/EndpointSlice/CoreDNS/Operator + k3s Helm).
Reproduced here close to verbatim as the record of that research; light
formatting only.

Business framing this research was scoped against, per Sam (2026-09-05): the
decomposition is being evaluated as a real product distribution strategy —
possibly sold self-hosted, possibly as a managed/hosted offering (the
RHEL/Ansible Automation Platform model: same product, sold both ways), not
ruled to either shape. Evaluate infrastructure choices on technical merit for
that audience, not through the lens of Sam's own homelab deployment
preferences (see `feedback_dont_project_homelab_prefs_onto_product` in
Claude's project memory). "Cost" in this document means operational
complexity, never dollar price — pricing and SaaS-plan questions are
explicitly out of scope here.

---

## 1. Candidate mechanisms

| # | Mechanism | Who stands it up | Health-aware endpoints? | "Install → auto-join" mechanics | Single-host fit | Multi-host fit |
|---|---|---|---|---|---|---|
| 1 | **Kubernetes-native** (Service + EndpointSlice + CoreDNS), delivered via Helm chart + Operator, on k3s (single-node) or a real cluster | A whole (even if small) control plane: api-server, etcd, scheduler, controller-manager, CoreDNS, kube-proxy. k3s packages this into a ~one-command install | Yes, free — readiness probes gate which pods land in an EndpointSlice | Deploy/helm install a Service (or an Operator reconciles a CR) → CoreDNS serves the name → existing services dial it | Strong (k3s is ~½ GB RAM) | Good (real cluster, or k3s server+agents) |
| 2 | **NATS-based discovery** (micro `$SRV` announce + JetStream KV registry) | NATS broker — already a hard dependency, so nothing new to stand up | No — only connection liveness; you write heartbeat+TTL for KV | Service connects to the bus on start, publishes/announces → peers observe the join | Excellent | Workable but requires a real NATS cluster / leaf nodes (TLS/NKey/JWT weight) |
| 3 | **Consul** (registry + agent + DNS) | A second control plane: 3–5 Raft servers + a client agent on every node | Yes (TCP/HTTP/gRPC/TTL) | Agent registers with server; peers resolve via Consul DNS | OK (one server + agents) | Good (multi-DC, WAN federation) but per-node agent burden |
| 4 | **etcd** (discovery substrate) | A Raft KV cluster you operate | Not natively — you own catalog/health semantics | Lease registration + prefix watch | OK (embeddable) | Good (R3 replication) but you build the semantics |
| 5 | **gRPC xDS / service mesh** (Istio/Linkerd/Traffic Director, proxyless gRPC) | A control plane (+ per-pod sidecar if mesh) | Yes (xDS health) | Register with control plane → EDS pushed to subscribed clients | Heavy / overkill | Heavy; K8s-first; sidecar cost (~0.2 vCPU + 60 MB/pod @1k rps in Istio-sidecar-land) |
| 6 | **DNS-SRV / mDNS** | Nothing new | No native health; TTL caching breaks fast failover | Register a record | mDNS yes (but link-local); DNS-SRV needs a resolver you control | mDNS fails at the first router/VLAN; DNS-SRV needs your own resolver + a custom gRPC resolver (grpc-go doesn't consume SRV natively) |

## 2. What this narrows to

Six candidates surfaced, but the field collapses to two real finalists once
three filters specific to archie-core apply: (a) the fleet is ~7 Go
services, (b) a gRPC-sync + NATS-async mix is already the working
assumption, and (c) the defining requirement is "customer installs a
service, the fleet auto-aligns, no redeploy/reconfig."

- **Consul / etcd are dominated by K8s-native.** Consul adds a whole second
  control plane (Raft server quorum + a client agent on every node you don't
  control) — redundant if the fleet lives in K8s (buying a second control
  plane to replace one you already have), heavy if it doesn't. etcd makes
  you own catalog + health semantics, which is exactly what K8s-native and
  NATS each give for free in different ways. Both remain viable, just not
  better.
- **gRPC xDS / service mesh is the wrong tool at this scale.** Field
  consensus (multiple 2026 sources): don't adopt a mesh until 20+ services;
  archie-core is at ~7, single-language (Go), with resilience logic already
  in Go. xDS/proxyless-gRPC earns its keep only with locality-aware routing
  / canary / circuit-breaking across hosts, which nothing here signals. The
  durable takeaway isn't "use xDS"; it's that gRPC is resolver plumbing, not
  a registry — whatever registry gets picked feeds gRPC through a resolver
  (DNS resolver, or a custom one).
- **DNS-SRV / mDNS are insufficient.** mDNS is link-local (dies at
  router/VLAN/container boundaries), so it can't span routed multi-host;
  DNS-SRV has no health and grpc-go's default resolver doesn't consume SRV
  natively anyway.

So the genuine decision is K8s-native vs. reuse-NATS.

## 3. K8s-native vs. NATS: an honest weigh

**Where K8s-native wins decisively:**
- The only mechanism that delivers "install a service → the fleet
  auto-aligns" at the infrastructure layer, with health-filtered endpoints,
  where the customer's install is a straightforward "apply a Deployment +
  Service" (or a Helm chart, or an Operator reconciling a CR). This is the
  single hardest, most product-defining requirement, and K8s solves it
  natively.
- Delivers, as a side effect, almost everything a 7-service fleet needs
  anyway: health (readiness/liveness probes), orchestration
  (restart/scale), config (ConfigMaps/Secrets), rollouts, clean external
  exposure (Ingress/Gateway). Not extra weight for its own sake — buying
  pieces that would otherwise be hand-built.
- The hosted/managed path is free. If the product is ever run for customers
  (the "sold both ways" shape kept open above), K8s runs anyway.
  K8s-native discovery makes self-hosted and hosted deployments use
  identical discovery semantics — one codebase, no divergence. This is the
  RHEL/Ansible Automation Platform lesson: same product, sold both ways,
  same internal architecture.

**Where NATS wins:**
- Zero new infrastructure. NATS is already a hard dependency and already in
  the deployment (embedded, or the existing `docker-nats-stack.toml`). For
  the lowest-end single-host customer who wants "one binary dropped on a
  box," NATS discovery means no cluster, no K8s — just the bus already
  running.
- Genuinely idiomatic: a service connects, announces via the micro `$SRV`
  tree (PING/INFO/STATS) or a JetStream KV record, and disappears the
  moment its connection closes. Re-join = "open the connection and
  publish." No per-service config, no sidecar, no hand registration.

**Where NATS genuinely falls short for this product:**
1. It's a NATS service locator, not a gRPC endpoint registry. The `$SRV`
   framework and the KV bucket hand you names/responders, not `host:port`
   gRPC endpoints. Each service's `host:port` has to be carried in
   metadata/KV and a custom gRPC resolver hand-written; gRPC's built-in
   resolver/health model won't "just work." Real integration work owned in
   the sync path — now the product's primary contract.
2. No native health. NATS has no last-will or probe. Heartbeat+TTL (KV)
   must be implemented to distinguish "installed but unhealthy" from "not
   installed," and stale entries are your problem. K8s readiness probes do
   this for free.
3. Discovery is coupled to broker availability. If NATS is down, every
   service looks down (a hard SPOF unless NATS is itself clustered).
   Multi-host is where the cost shows: a real NATS cluster or leaf nodes,
   with TLS/NKey/JWT credential management — operator weight, not "just
   works."

The balance: NATS is a legitimate fallback and a genuine part of the
answer; it is not a substitute for the registry that resolves gRPC
endpoints with health. If the product's sync path is gRPC, the registry
serving it should be K8s-native (DNS → resolver, headless-Service SRV for
ports, EndpointSlice health) rather than a hand-built NATS registry.

## 4. Recommendation

**Primary:** Kubernetes-native service discovery (Service +
headless-Service/DNS + EndpointSlice, served by CoreDNS), delivered as a
Helm chart + Operator, on k3s for single-host customers and a real cluster
for multi-host / hosted.

**Fallback:** NATS-based discovery (micro `$SRV` announce + JetStream KV
registry) for no-K8s single-host installs, using a custom gRPC resolver
that reads `host:port` from the KV bucket.

**Rationale, short version:**
- K8s-native is the only candidate that solves the hardest requirement —
  "install a service and the rest of the system joins in to align" — at
  the infrastructure layer with health-filtered endpoints and a
  customer-comprehensible install. Not extra weight for its own sake; it
  replaces a stack of pieces a 7-service product needs anyway, and makes
  the multi-host + hosted paths free from one codebase.
- k3s packages the control plane into a ~one-command install a customer can
  stand up, and the Operator/Helm/CRD pattern is exactly the "install
  Curator, Gateway picks it up" product story.
- NATS is not discarded — it stays in the fleet for async fan-out, and
  becomes the second discovery implementation so the smallest no-K8s
  customer keeps "one binary on a box." The fallback is the honest
  acknowledgment that a full K8s cluster is genuinely heavy for the very
  smallest single-node customer.

Why not NATS-only: it's a service locator needing hand-built gRPC endpoint
resolution + health, and it couples discovery to broker availability. Why
not Consul/etcd: a redundant second control plane (if K8s) or "you own the
semantics" (etcd), with per-node agents. Why not a mesh: overkill at ~7 Go
services; consensus is 20+.

### The seam that keeps both shapes on one codebase

The Gateway (and any service that routes to peers) should talk to discovery
through one internal interface —

```go
type ServiceRegistry interface {
    Resolve(service string) []Endpoint
    Watch(service string) <-chan Event // join/leave
}
```

— with two implementations: a K8s one (DNS / EndpointSlice / custom
resolver) and a NATS one (KV / `$SRV`). The rest of the code is
registry-agnostic. This is what makes "K8s-native primary, NATS fallback" a
coherent architecture rather than a fork: business logic doesn't change,
only the registry injection point does.

## 5. End-to-end: "install Curator, Gateway discovers it"

**Path A — K8s-native (primary):**
1. Customer installs the product (`helm install archie`, or pre-provisioned
   on k3s). Base services (Gateway, UI, Messaging, Scheduler, Execution,
   State Store) each deploy as a Deployment + Service; Gateway is always up
   and already holds a resolver for every known service name.
2. Customer installs Curator: `helm install archie-curator`, or the
   Operator watches a `Curator` Custom Resource and reconciles it — creating
   a Deployment (pods) + a `curator` Service.
3. The Service's label selector matches the Curator pods; the endpoint
   controller writes their IPs into an EndpointSlice. Readiness probes gate
   which pods are "ready" (a Curator that only registers once healthy won't
   receive traffic).
4. CoreDNS immediately serves `curator.<ns>.svc.cluster.local` (A record; a
   headless Service additionally gives an SRV record with the port).
5. The Gateway resolves `curator.<ns>.svc.cluster.local` through its gRPC
   DNS resolver (or a K8s-aware resolver). On the next re-resolution/update
   it picks up the new endpoint. No Gateway redeploy, no Gateway config
   change — and because it's DNS, the Gateway code doesn't even need to know
   it's running under K8s.
6. Removal: `helm uninstall archie-curator` (or scale to 0 / delete the CR)
   → EndpointSlice empties → DNS record disappears → Gateway's resolver
   drops it. Clean.

**Path B — NATS fallback (single-host, no K8s):**
1. One NATS (embedded, or external via the existing compose stack) is the
   shared bus; its URL is part of the base install.
2. Every service, on start, connects to that URL and either (a) registers
   via the micro framework (`AddService("curator", "1.0.0", …)`), appearing
   on the `$SRV` tree, or (b) writes its `host:port` + version into a
   JetStream KV bucket (`archie/registry/curator/<id>`) and heartbeats it
   with a TTL.
3. The Gateway holds a live roster (from `$SRV.INFO` or a KV watch) and
   feeds it into a custom gRPC resolver mapping service-name → `host:port`.
4. Customer starts one Curator binary (`archie-curator`, or `docker run`
   the image) with the same NATS URL in its config. It connects, announces,
   and heartbeats. The Gateway's roster observes the new entry (within one
   announce/heartbeat window) and starts routing to it. No Gateway restart,
   no config change beyond the shared NATS URL already present in the base
   install.
5. Removal: stop Curator → its connection closes → it leaves `$SRV`
   immediately and its KV record expires via TTL. The Gateway routes that
   capability to "not installed."

## 6. Failure-mode honesty

- **K8s-native:** pod death → readiness fails → dropped from EndpointSlice
  within seconds, so the gateway stops routing to it (health-aware). DNS
  TTL caching is mostly harmless because a Service's ClusterIP is stable
  and all churn lives behind it in the EndpointSlice. If the control plane
  goes down, already-running pods keep serving and DNS still resolves from
  cache; only new scheduling/propagation halts. Known gap: cross-cluster
  and off-cluster consumers (client-side resolvers, a worker fleet on VMs)
  need ExternalDNS or a bridge. mTLS/auth, if needed, is a separate concern
  (NetworkPolicy / cert-manager), not the mesh.
- **NATS fallback:** service dies → dropped from `$SRV` immediately, KV
  record lingers until heartbeat-TTL (bounded staleness). Broker
  unreachable → every service appears down (hard SPOF unless NATS is
  clustered). Plain subjects aren't a consensus store (split-brain across
  partitions possible); JetStream KV gives Raft per stream but KV placement
  across hosts needs its own decisions. Health semantics must be built by
  hand.

Both paths are robust against "a service dies"; the difference is who does
the health checking — K8s readiness probes do it for free, NATS makes you
build it.

## 7. Addendum — recovered K8s lane, optional-service semantics

A separately recovered K8s-focused research lane landed independently on
the same verdict (K8s-native as the canonical, declarative, self-healing
fleet-discovery answer; fits multi-host and the AAP-style "sold both ways"
model best) and confirmed the same install → Operator → CoreDNS → resolver
flow above. It sharpened one point specific to archie-core's shape:

**The optional, intermittently-running Curator needs explicit "absent"
semantics under K8s.** A scaled-to-zero or not-yet-created Curator has no
ready endpoint and no DNS record — the Gateway's gRPC `dns://` resolver
gets `NXDOMAIN`/empty resolution, and this must be treated as "Curator is
disabled," not "Curator is broken." Concretely:
- Lazy, on-demand re-resolution — don't pin a dead subchannel as a hard
  failure.
- A distinct `NotInstalled` vs. `Down`/`Unhealthy` state for routing — the
  same distinction the NATS path needs a KV "installed" marker to express.
  Under K8s, health comes free (readiness gates the EndpointSlice), but the
  *presence* of an optional service still has to be modeled as a resolution
  outcome, not a failure.

This is a new implementation requirement on both paths (`ServiceRegistry`'s
`Resolve`/`Watch` contract needs a way to express "absent" distinctly from
"unhealthy"), not a change to the primary/fallback recommendation.

Two secondary observations from that lane, noted but not verdict-changing:
- **Two discovery systems, not one.** Under K8s, sync gRPC is served by
  cluster DNS/EndpointSlice, while async NATS status/telemetry is served by
  NATS's own clustering. These should not be conflated — this is exactly
  why the `ServiceRegistry` seam matters: it isolates the sync path and
  keeps NATS purely for async fan-out, as already planned.
- **Config relocation.** Moving install/run/upgrade from `config.toml` into
  ConfigMaps/Secrets is a real change to how archie-core is deployed,
  worth a deliberate PRD/settled-design before committing — a phased
  concern, not a blocker for the discovery decision.

## Bottom line

Treat Kubernetes-native discovery (Helm chart + Operator on k3s / a real
cluster) as primary, and reuse NATS as the built-in fallback so single-host
no-K8s customers still get "install and auto-join." This is the only
ordering that satisfies the hardest requirement at the infrastructure
layer, keeps the hosted path free from one codebase, and doesn't force the
smallest customer into a full cluster. The two implementations live behind
one `ServiceRegistry` interface, so neither is a fork. The one added
implementation requirement is the optional-service "absent ≠ down"
resolution model on both paths.

Full recovered K8s-lane transcript, for the record: session path
`.../cf1825c4-0c08-4aaa-9ea1-aac40893825f/run-0/session.jsonl` (external to
this repository; not reproduced here).
