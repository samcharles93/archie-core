# EDA playbook engine -- design

**Status:** Draft, awaiting sign-off (not yet in `docs/architecture/`)
**Date:** 2026-09-03

## Problem

Today, "which workflow runs for which trigger" is hardcoded Go: a literal
label vocabulary (`workintake.labelKinds`) and a literal label->workflow map
(`workflow/routing.go`), extended only by adding a Go case or shipping a
skill whose `metadata.archie.workflow` overrides one *named* built-in
workflow. Extending the system to a new trigger, a new action, or a new
event source today means a feature branch, a code change in a hardcoded
path, and new Go tests -- for every addition. That is the barrier this
document exists to remove.

The goal (Sam, 2026-09-02): triggers, the actions they run, and the
conditions that connect them should be **drop-in and load**, not **drop-in
and touch application code**. Yaegi-evaluated Go is the unit of imperative
"do a thing" logic; YAML playbooks are pure orchestration data -- no logic,
just "on this event, with this data, call these actions, in this order,
under these conditions." The daemon's event coordinator doesn't know what a
`bugfix` or `image-gen` action does, it resolves the name against a
registry and pipes typed data through.

This is explicitly **not** scoped to code-generation workflows. A playbook
action can be any registered capability -- run a codegen workflow, send a
message on a channel, open something on a forge, generate an image -- so
long as it's implemented behind one of the typed engine families below.

## Relationship to existing decisions

- `docs/prds/event-sources-and-reactions.md` (2026-08-22, decided) ruled
  that reactions in the two epics it covers are **producer-only** (no veto,
  no mutation of in-flight work) and explicitly rejected a single generic
  `Source` interface as "the untyped-hook shape the plugin engine rule
  exists to prevent." **This document extends that scope**: playbooks can
  dispatch to multiple typed action kinds, not just "produce a
  `TaskEnvelope`." It does not relitigate producer-only-ness (still no
  veto/mutation of in-flight work here) and it does not introduce one
  generic interface -- see "Typed positions, not one generic hook" below,
  which is how it stays compliant with the plugin engine rule instead of
  reopening that document's #4.
- `ARCHITECTURE.md#plugin-engine-rule-strict` governs every extension point
  introduced here. Every position below is a typed domain contract with an
  owning registry, not a generic callback map. This is a hard constraint,
  confirmed against the existing rule text before writing this doc.
- Yaegi is already a proven in-process extension mechanism
  (`internal/plugin`, `internal/domain/workflow/wfeval/yaegi.go`,
  `internal/gate/gateeval/yaegi.go`, `internal/skill/plugin.go`) -- this
  design generalizes its existing role, it does not introduce a new
  execution mechanism.
- `internal/domain/workflow/skillbuild.BuildRegistry` already composes a
  `workflow.Registry` from a skills catalog, plugin-defined overriding
  built-in. The playbook loader described here supersedes label routing
  specifically; it does not replace `skillbuild`'s workflow-composition role,
  it becomes an additional input the daemon composes at startup.
- Webhook event intake is separately in flight (Sam, in progress). This
  document defines the router each webhook-sourced event should be able to
  reach; it does not redesign webhook receipt itself.

## Shape: four logic positions, each a typed engine family

Every entrypoint kind (mechanism, action, loop, schedule, route, trigger --
Sam's list) is itself a schema emitter: defining a new kind produces the
generated contract downstream tooling consumes. The kinds slot into a fixed
hierarchy of logic positions:

```
Workflow  ->  Module  ->  Channel (telegram/whatsapp/email/...)
                      ->  Forge   (GitHub/Gitea/GitLab/...)
```

Each position is its own typed engine family per the plugin engine rule:
identity + capability-specific operations, an owning `Registry`/`Manager`,
explicit lifecycle where the implementation owns resources, narrow host
access. Concretely:

- **Workflow** -- already exists (`internal/domain/workflow`). A playbook
  action of kind `workflow` invokes a registered `workflow.Registry` entry
  by name, unchanged from today's execution path.
- **Module** -- the new general-purpose action position. A `Module` is a
  Yaegi-evaluated implementation of a generated interface (args in, typed
  result out) for one declared action kind (e.g. `image-gen`, `notify`,
  `open-pr-comment`). This is the position that makes actions "not
  restricted to code-generation."
- **Channel** -- already exists as `channels.Channel` (telegram, email,
  webhook). A playbook action of kind `channel` sends through a named,
  already-registered channel. No new mechanism; this document just makes it
  addressable from a playbook.
- **Forge** -- forge clients (GitHub, Gitea, GitLab) become an addressable
  action position the same way Channel is, so a playbook can say "comment
  on this forge issue" without that being a special case wired only into
  the poller.

**Typed positions, not one generic hook.** There is no single `Action`
interface with an `any` payload. Each position keeps its own typed
contract; the playbook YAML names a position and an implementation, and the
event coordinator resolves that pair against the matching typed registry.
This is what keeps the design compliant with the plugin engine rule and
with `event-sources-and-reactions.md`'s rejection of a generic `Source`/
hook interface -- the generality lives in the YAML data model (which
positions exist, which named implementations are registered), not in a
runtime `any`-typed dispatch path.

## `go:generate`-driven schemas, from day one

Sam's correction: schemas are not a future nicety bolted onto an ad hoc
YAML shape -- they are the struct+interface contracts for each logic
position, and they drive code generation from the start.

- Each logic position (Workflow/Module/Channel/Forge) has a schema source
  (Go-annotated types, following the existing pattern elsewhere in the
  repo) from which `go:generate` emits the interface and struct scaffolding
  an implementation must satisfy.
- A Yaegi module implements the generated interface for its position. The
  generated contract is what makes an implementation valid, not hand-review.
- The schema is also what the linter and LSP (below) validate a playbook
  YAML against -- one source of truth for codegen, startup loading, and
  authoring-time tooling, so the three can't drift from each other.
- Schema versioning is explicit per position, independent of a playbook's
  own workflow version (see collision handling below).

This document does not attempt to design the full generator today. It
records the constraint (schema-first, `go:generate`-driven, per logic
position) so the first implementation slice builds toward it instead of
away from it.

## Playbook YAML: pure orchestration data

A playbook declares no embedded logic -- only:

- **trigger**: the event predicate (e.g. forge issue assigned + labelled
  `bugfix`, a schedule tick, a webhook payload shape).
- **actions**: an ordered list, each naming a logic position and a
  registered implementation, with typed argument data and optional
  conditions.
- **data flow between actions**: results/facts from one action available to
  later actions in the same playbook run -- exact interpolation syntax is
  an open point, not decided here (see Open questions).

Playbooks live in a configured directory (repo/directory-scoped, per Sam's
"define repos, directories, playbooks" framing) and are loaded at archied
startup, alongside the existing skills-catalog composition.

## Dedup: detect and log, no arbitration mechanism

No runtime auto-resolution rule. An automatic "pick a winner" algorithm
(e.g. by version) is itself a mechanism that can fail and need debugging
when a collision happens -- the wrong kind of complexity to introduce for
a case that should just be visible and fixed by a human. Instead:

- **At daemon startup:** if independently-sourced playbook directories
  declare a collision on the same trigger, that is a definition failure.
  The colliding definitions are dropped (not loaded) and the failure is
  logged plainly (which sources, which trigger) and reported back to the
  caller that requested the load -- not swallowed as a background log line
  only. The schema is the definition; a schema conflict is a reported
  failure, not something the daemon silently arbitrates.
- **Linter**: a CI/dev-time tool, reading the same generated schemas, that
  flags duplicate or conflicting trigger definitions *within* a single
  maintained tree before merge.
- **LSP**: same schema source again, feeding author-time hover/completion/
  inline errors while someone edits a playbook YAML in an editor. Kept
  explicitly in scope per Sam (not deferred), because it's the same schema
  artifact as the linter's, just a different consumer.

The startup log-and-refuse path exists for what the linter/LSP structurally
cannot see -- sources composed together only at daemon startup, never
checked against each other by either tool. If the linter and LSP do the job
they're built for, a real collision reaching that startup path should be
rare in practice, not a routine occurrence the design leans on.

## Execution-time gaps (identified 2026-09-03, unresolved)

Everything above covers *loading* a playbook. Nothing above covers what
happens while one *runs*. Two gaps, both load-bearing enough to resolve
before implementation, not defer:

### 1. Mid-playbook action failure

A playbook is an ordered action list with data flowing between steps. If
action 2 of 4 fails at runtime (an image-gen API times out, a forge call
403s), the doc currently says nothing about:

- whether the playbook run stops, continues to independent later actions,
  or rolls back;
- what "rollback" could even mean for an action with a real external side
  effect already committed (a forge comment already posted is not
  revocable the way an uncommitted DB write is);
- whether this interacts with `event-sources-and-reactions.md`'s
  producer-only constraint -- a playbook that reacts to a failure by
  triggering compensating actions is arguably still "producing new work,"
  not vetoing/mutating in-flight work, but this needs to be stated, not
  assumed.

No default is proposed here. Candidates to evaluate before sign-off:
stop-on-first-failure with the full run outcome (which actions ran, which
didn't) logged and reported the same way a load collision is (see
above) is the simplest option and matches this document's existing bias
against clever automatic recovery -- but it should be a stated decision,
not a default nobody chose.

### 2. Idempotency at execution time

`event-sources-and-reactions.md` decided reactions ride
`internal/eventbus`'s at-least-once delivery specifically so
`PublishUnique`/idempotency keys absorb redelivery without duplicate side
effects. This document inherits that for free only for `workflow`-kind
actions, because `TaskEnvelope` already carries an idempotency key. It says
nothing for `channel`, `forge`, or `module` actions with real side effects
(posting a comment, sending a message, generating and billing for an
image) -- if the triggering event is redelivered, nothing here prevents
those actions from firing twice.

This needs a keying scheme before Module/Channel/Forge actions ship:
either the playbook run itself is keyed (one idempotency key per
trigger-event + playbook, so the whole run dedups) or each action
carries its own key derived from the run key. The former is simpler and
should be the default unless a specific action needs finer-grained
dedup than "did this playbook already run for this event."

Both gaps block open question 4's first-slice recommendation only
partially: the first slice (Module + loader + workflow-kind dispatch)
already has idempotency for free via `TaskEnvelope`, so it can proceed
without resolving gap 2. Gap 1 (failure mid-run) is real even for a
single-action first slice's *future* multi-action runs, but is not
required to build the first slice today. Both gaps must be resolved
before Channel/Forge actions or multi-action playbooks ship.

## Open questions (for sign-off before implementation)

1. **Data-flow/condition syntax.** Does this project want a small existing
   Go expression evaluator (there may be one already in the codebase worth
   checking, e.g. anything backing gate conditions), or a bespoke minimal
   grammar? Not decided here.
2. **Playbook directory config field.** Where this lives in
   `internal/config` / `configuration.Document`, and whether it's one
   directory or a list (mirroring `SkillsDir`'s shape) -- follow existing
   config precedent, not invented fresh.

   **Status 2026-09-03: partially resolved.** The label-vocabulary slice
   landed with a single-file shape (`workflow_labels_file`, mirroring
   `workflow_routing_file` and `SkillsDir`'s single-path precedent) rather
   than a directory, because it is the smallest useful case. A directory
   (multiple playbooks, per-repo scoping) remains open and larger; the
   single-file shape does not preclude it -- a future `workflow_dir` would
   compose `workflow_labels_file`'s role into a loader.

   Decisions recorded from the label-vocabulary slice (commit pending):
   - Arbitrary labels bind to registered workflow names via
     `LabelWorkflows` + `LoadLabelWorkflowsYAML`/`SetLabelWorkflows`
     (`internal/domain/workflow/routing.go`).
   - The closed `Kind`/NATS-subject set is untouched; the label map only
     extends binding authority for labels the kind layer does not own.
   - Collision rule (per Sam, 2026-09-03): a label already owned by the
     kind set (bug/feature/bootstrap), an empty label, an empty workflow
     name, or a duplicate binding is a reported load failure -- dropped
     and logged, the error returned to the caller. Nothing is silently
     arbitrated by schema/version. This is the "schema defines the
     accepted message; anything else is the caller's fault" rule applied
     to the label layer.
   - Precedence in `Route()`: explicit `t.Workflow` → arbitrary-label
     binding → kind binding → triage → implement → default.
3. **Trust boundary for Module Yaegi code.** The plugin engine rule's
   invariant 6 draws a line between operator-trusted in-process code and
   repository-supplied code that must run in a container. A user-authored
   Module is closer to "operator-installed plugin" than "repository code
   from a task," but this should be stated explicitly before Modules ship,
   not assumed.
4. **First implementation slice.** Recommend: Module position + the
   playbook loader + trigger-to-workflow dispatch only (subsuming today's
   label routing) first, proving the schema-gen -> Yaegi -> playbook path
   end to end on the smallest useful case, before adding Channel/Forge
   action kinds or the linter/LSP.
5. **Monetization boundary.** Sam has flagged this may be commercialized,
   and explicitly wants it discerned from other OSS event-driven-automation
   tooling. No design decision needed yet, but worth a note if/when
   licensing or feature-gating questions arise for playbook authoring or
   the LSP.

## Packages this touches (first slice, per open question 4)

- New: a schema-generation package/convention (`go:generate` source +
  emitted contracts) for the Module position.
- New: playbook loader + event coordinator (name/location TBD -- likely
  `internal/domain/eda` or similar, following the domain-layer rule: no
  infrastructure imports, no untyped hooks).
- `internal/domain/workflow`: dispatch target for `workflow`-kind actions,
  unchanged in its own contracts.
- `internal/domain/workintake`: `labelKinds`/routing becomes one playbook
  among others once the loader subsumes it; existing Go fallback stays for
  backward compatibility until playbooks fully replace it (mirrors the
  `dynamic-workflow-triage.md` precedent of a two-line fallback change, not
  a rip-and-replace).
