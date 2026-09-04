# Module reference: AWX decomposition / Ansible task model vs. Archie

Status: exploratory reference, not a design decision. Nothing here is
authoritative -- `docs/architecture/` stays the source of truth. This exists
so a later PRD (if one gets written) doesn't have to re-derive "what do we
already have vs. what AWX/Ansible are describing." See
[[2026-09-05-awx-service-decomposition-and-ansible-task-model]] for the
source material this is built from.

Two follow-on artifacts extend this line of exploration:
- Two Archify diagrams contrasting current vs. an ideal service-oriented
  shape: `/work/artefacts/archie-core-current-state-archify.html` and
  `/work/artefacts/archie-core-ideal-state-archify.html`. The current-state
  diagram is grounded in verified coupling (webui's task-action path routes
  through its own HTTP handler in-process; direct `gateway.SessionStore` /
  `Router` / `TurnHistory` access; a shared `config.Holder`) -- not the
  "already split" claim this document's earlier draft mistakenly made.
- [[service-discovery-research-2026-09-05]] -- externally researched
  comparison of service-discovery mechanisms for the ideal-state's "install
  a service, the fleet auto-aligns" requirement. Recommends Kubernetes-native
  discovery as primary (Helm chart + Operator on k3s / a real cluster) with
  NATS-based discovery (already a hard dependency) as the built-in fallback
  for no-K8s single-host installs, both behind one `ServiceRegistry`
  interface.

## Reading the two source ideas separately

The AWX post and the Ansible `Task` paste are two different asks and should
stay two different tracks:

1. **AWX's decomposition tree** is an argument for splitting a monolith
   along service boundaries (Auth, Comms, Runtime, Content, ...) with a
   pluggable communication mesh between them. Archie's six-domain split
   (`docs/architecture/index.md`) already **is** that argument, applied
   earlier and at a different grain (domain boundaries in one Go module +
   NATS transport, not separate deployable services). The gap isn't "do we
   need boundaries," it's "are the boundaries clean and are they enforced."
2. **Ansible's `Task`/module system** is a request for a *content* format:
   let users write YAML tasks against a stable action vocabulary and have
   Archie interpret them, the way Ansible interprets `- name: ... action:
   module: args`. Archie has the skeleton of this already (EDA playbook
   engine, Module action position) but it's deliberately narrow today.

## AWX decomposition tree -> Archie's existing domains

| AWX node | Archie equivalent today | Location |
|---|---|---|
| Auth (RBAC / SSO / Policy-ABAC) | `internal/domain/binding` (task-to-identity/config binding) + `internal/policy` (cross-cutting, zero internal imports) + forge/channel auth (Telegram pairing, webhook HMAC) | `internal/domain/binding/`, `internal/policy/`, `internal/pairing/`, `internal/webhookguard/` |
| Communication / Inter-Service | NATS (`ARCHIE_TASKS` JetStream work queue, core-NATS RPC via `_INBOX.*`) | `internal/infrastructure/eventbus/nats/`, `internal/natsrpc/`, `internal/eventbus/` (contract, zero internal imports) |
| Execution (mesh) | Container pool running isolated agent worktrees over NATS, not a mesh of remote execution nodes (Ansible's Receptor equivalent doesn't exist -- Archie's "remote execution" is a Docker container, not a fleet) | `internal/container/pool.go`, `internal/agentexec/`, `internal/worktree/` |
| Runtime / Scheduler | `internal/domain/scheduling` (ticker contract) + cron-driven work intake | `internal/domain/scheduling/`, `internal/infrastructure/cronstore/` |
| Content / Projects | Nearest analogue is the **EDA playbook + module system** (below) plus `internal/skill` (skill catalog) -- Archie has no "project" = git-checkout-of-automation-content concept the way AWX does | `internal/domain/eda/playbook/`, `internal/domain/eda/module/`, `internal/skill/` |
| Telemetry | `internal/logging` (task sink, feed, registry) + `internal/events` | `internal/logging/`, `internal/events/` |
| Inventory | No direct equivalent. Closest is forge-side repo/identity config (`[[identities]]`, `internal/forge`) -- Archie targets one identity acting across a small set of forge repos, not a dynamic inventory of managed hosts | `internal/forge/`, `internal/config/` |
| Credentials | `internal/secret` (engine host, env/age/sops/vault engines) | `internal/secret/`, `internal/secret/enginehost/` |
| Subscription/Billing | No equivalent -- Archie is self-hosted, single-tenant; this AWX node doesn't map to anything and shouldn't | -- |
| UI split from backend | Already true: `internal/webui` (Go API server) serves a separately built SPA from `ui/dist` (Vite/React-ish, `ui/src/<feature>/`) over HTTP/SSE. There is no shared-process coupling to undo. | `internal/webui/`, `ui/src/` |

**Read on "reframe to capture market":** the honest gap next to AWX/AAP isn't
domain boundaries -- Archie already has those, arguably cleaner (env-enforced
gates, one dependency direction, contracts with zero internal imports). The
gap is (a) no content/inventory layer letting non-programmers hand Archie
declarative automation the way an Ansible playbook does, and (b) everything
still ships as one Go binary + one container image, not independently
deployable/upgradable services. (a) is the more differentiated, more
buildable opportunity given a one-person team; (b) is expensive AWX-style
service splitting with limited payoff at Archie's current scale and should
not be pursued for its own sake.

## Ansible `Task` model -> Archie's EDA playbook/module system

Ansible's `task.py` is the runtime representation of one YAML task after
`ModuleArgsParser` resolves the legacy free-form shape and `TemplateEngine`
renders Jinja2. Archie's nearest shipped analogue is the EDA playbook engine,
which is **intentionally much narrower right now**:

| Ansible concept | Archie's current shape | Gap / what porting the idea would mean |
|---|---|---|
| `Task` (one step, `action` + `args`) | `playbook.Action` (`internal/domain/eda/playbook/playbook.go`) -- but **exactly one action per playbook, position must be `"workflow"`**, hard-rejected at load otherwise | Multi-action, multi-position playbooks are the literal ask ("use existing ansible playbooks... within archie") but are explicitly blocked today on unresolved execution-time gaps: mid-run failure semantics, idempotency for non-workflow actions (see the package doc comment and `docs/architecture/plugins-and-extensions.md`'s Event sources section). This is a known, named boundary, not an oversight. |
| Module (`ansible.builtin.*`, arbitrary Python) | `module.Kind` registry, Yaegi-interpreted `.go` files against a generated per-kind symbol table (`internal/domain/eda/module/`) -- **one shipped kind: `log`** | Ansible modules are dynamically loaded from a huge ecosystem; Archie's Module position is closed-registry-of-named-kinds, each with its own hand-written contract package (`internal/domain/eda/module/<kind>`). Adding "run a shell command," "call a forge API," "send a channel message" module kinds is possible under the existing pattern but each is new work, not automatic from having the mechanism. |
| `when:` conditional | `Action.When`, compiled CEL (`internal/domain/eda/expr`), not Jinja2 | Already decided: CEL over Jinja2 (open question 1 in the playbook engine PRD, resolved). Don't reopen. |
| `register` / fact accumulation across tasks | Not present -- `DispatchInput.Event` is a flat map exposed to CEL, there's no cross-action result chaining because there's only one action | Needed before multi-action playbooks are meaningful; tracked implicitly by the same "unresolved execution-time gaps" blocker above. |
| `loop` / `until`/`retries`/`delay` | Not present | Same -- follows from allowing more than one action per playbook. |
| Field-attribute inheritance (task -> block -> play) | No block/parent chain -- a playbook is flat: one `Trigger` + one `Action` | Ansible's play/block/role hierarchy has no Archie counterpart yet; would only matter once multi-action/roles are in scope. |
| Roles (reusable task bundles) | Nearest is `internal/skill` (skill catalog, frontmatter, tool defs) -- conceptually closer to Claude Skills than Ansible roles | Not a role system; skills are agent-facing capability bundles, not declarative task sequences. |
| Channel / Forge action positions | Named and designed but explicitly **not implemented** (`docs/architecture/plugins-and-extensions.md`: "Channel and Forge action positions are designed but not yet implemented," open investigation `archie-core-t2db.19`) | This is the direct next step toward "playbooks that do things," independent of the Ansible-compatibility idea. |

**What porting real Ansible playbooks would actually require**, in rough
dependency order, if this becomes a real proposal later:

1. Lift the playbook engine's hard boundary: multi-action playbooks, which
   needs the mid-run failure/idempotency semantics the doc comment flags as
   the blocker today.
2. A result-passing mechanism between actions (Ansible's `register`) --
   currently nothing carries one action's output into the next action's
   `when`/`args`.
3. Loop/retry primitives per action.
4. A much larger Module `Kind` registry, or a deliberate decision to accept
   a narrower "Archie automation language inspired by Ansible's shape" rather
   than "runs unmodified Ansible YAML" -- the latter also drags in Jinja2,
   the module_utils Python runtime, and inventory/host-targeting concepts
   Archie has no use for (Archie targets git repos and forge issues, not
   fleets of managed hosts). Recommend treating "Ansible-*inspired* syntax,
   not Ansible-*compatible* execution" as the working assumption if/when a
   PRD gets written -- it avoids importing Ansible's inventory/host model,
   which doesn't fit Archie's single-repo-per-task execution shape at all.

## On the multi-agent / Pi-harness-as-runner idea

Out of scope for this reference doc (it's an operating-model question, not a
module-mapping one) but noted since it was raised alongside the above: this
is a `migration-decisions.md`-track question (agent execution as a strict
data boundary is a named invariant in `ARCHITECTURE.md`), not something to
back into via the EDA/module work. Flag separately if it's worth pursuing.

## Bottom line

- No code changes implied by this document. It's a map, not a proposal.
- The two ideas in Sam's message decompose cleanly: AWX's service-split
  argument is mostly already satisfied by Archie's domain architecture; the
  Ansible-playbook-content idea is a real, currently-blocked extension of
  the EDA Module/Channel/Forge action positions, gated on the same
  execution-semantics work already named in `plugins-and-extensions.md`.
- If this becomes a real initiative, it belongs in `docs/prds/` as a
  decisive 1-pager per `AGENTS.md`'s scope-discipline rule -- not as an
  elaboration of this file.
