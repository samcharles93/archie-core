# EDA playbook engine -- design

**Status:** Draft
**Date:** 2026-09-03
**Beads epic:** `archie-core-t2db`

## Problem

"Which workflow runs for which trigger" is hardcoded Go: a literal
label vocabulary (`workintake.labelKinds`) and a literal label->workflow map
(`workflow/routing.go`), extended only by adding a Go case or shipping a
skill whose `metadata.archie.workflow` overrides one *named* built-in
workflow. Extending the system to a new trigger, a new action, or a new
event source means a feature branch, a code change in a hardcoded
path, and new Go tests -- for every addition. That is the barrier this
document exists to remove.

The goal: triggers, the actions they run, and the
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

- `docs/prds/event-sources-and-reactions.md` ruled
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
- `docs/architecture/plugins-and-extensions.md#plugin-engine-rule-strict`
  governs every extension point
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
- Webhook event intake is a separate design. This
  document defines the router each webhook-sourced event should be able to
  reach; it does not redesign webhook receipt itself.

## Shape: four logic positions, each a typed engine family

Every entrypoint kind (mechanism, action, loop, schedule, route, trigger) is itself a schema emitter: defining a new kind produces the
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
  by name, unchanged from the existing execution path.
- **Module** -- the new general-purpose action position. A `Module` is a
  Yaegi-evaluated implementation of a generated interface (args in, typed
  result out) for one declared action kind (e.g. `image-gen`, `notify`,
  `open-pr-comment`). This is the position that makes actions "not
  restricted to code-generation." Full design (schema-per-kind, the
  registry shape, the trust boundary, the recommended first slice):
  `docs/prds/module-position.md`.
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

Schemas are not a future nicety bolted onto an ad hoc
YAML shape. They are the struct+interface contracts for each logic
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

The generator itself is not designed here. The constraint is
schema-first, `go:generate`-driven, per logic position.

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

  **Resolved** (`archie-core-t2db.12`). A standalone
  gopls-shaped binary (`cmd/archie-playbooks`, lint subcommand) validates
  playbook directories against the exact loaders the daemon uses
  (`LoadPlaybookDirs`/`LoadKindWorkflowsYAML`/`LoadLabelWorkflowsYAML`),
  exiting non-zero on any collision / malformed file / invalid binding.
  A finding about one binding key leads with the key's `file:line`, read
  from the `yaml.Node` the loader decodes; the linter agrees with runtime
  validation by construction, which is the load-bearing property. `-eda-dir`
  lints an `eda_playbook_dir` the same way, through `playbook.Load` with its
  CEL, action-id and args checks.
  Discoverable via `task lint:playbooks` or direct `go run
  ./cmd/archie-playbooks lint -dir ...`. The LSP/serve mode is a later
  entrypoint of the SAME binary, per the shared-package decision above.
- **LSP**: same schema source again, feeding author-time hover/completion/
  inline errors while someone edits a playbook YAML in an editor. Kept
  explicitly in scope per Sam (not deferred), because it's the same schema
  artifact as the linter's, just a different consumer.

  `archie-playbooks serve` is the language server, over stdio. When a file is
  opened or saved it lints the file's saved directory with the loader the
  daemon runs for it (a file with a top-level `trigger` key is an EDA
  playbook, anything else a routing binding file) and publishes the findings
  for that file as error diagnostics, each on the line of the key, action,
  `when` or args key it names; a file-level finding sits on the first line.
  Unsaved edits are not validated. Hover and completion are later additions.

The startup log-and-refuse path exists for what the linter/LSP structurally
cannot see -- sources composed together only at daemon startup, never
checked against each other by either tool. If the linter and LSP do the job
they're built for, a real collision reaching that startup path should be
rare in practice, not a routine occurrence the design leans on.

**Shared package, thin consumers -- not a bundled-vs-standalone choice.**
The validation logic (`LoadKindWorkflowsYAML`, `LoadLabelWorkflowsYAML`, the
directory-merge from t2db.11) lives in `internal/domain/workflow`, a plain
Go package with no knowledge of which binary calls it. This makes "should
the linter/LSP be bundled into archied or a separate tool" a non-question:
- `archied` already imports the package directly and validates in-process
  at startup -- no subprocess, no external tool call.
- An agent that needs to validate a playbook in-process can import the same
  package directly, for the same reason.
- The standalone `cmd/` binary (t2db.12, gopls-shaped: one binary, a lint
  mode now, an LSP/serve mode later) exists only because CI and editors are
  out-of-process consumers by nature -- a CI step needs an exit code, an
  editor speaks LSP over a process boundary, neither can `import` a Go
  package. It is a thin wrapper per `organisation.md`'s `cmd/` rule, not a
  second implementation.

Folding CLI/LSP protocol handling into `archied` itself would grow the
daemon into unrelated concerns, against the plugin-engine rule's spirit of
narrow capability families.

## Execution-time gaps (both resolved)

Everything above covers *loading* a playbook. Nothing above covers what
happens while one *runs*. Two gaps, both load-bearing enough to resolve
before implementation, not defer:

### 1. Mid-playbook action failure

**Resolved** (`archie-core-t2db.18`). The default below
is a committed decision, not a candidate list; changing it later would
require its own decision. Gap 2 below is resolved by `archie-core-t2db.17`.

A playbook is an ordered action list with data flowing between steps. If
action 2 of 4 fails at runtime (an image-gen API times out, a forge call
403s), the run needs a defined outcome. This
codebase already answers a structurally identical question for its
existing workflow engine, precisely enough to reuse rather than
re-derive: `workflow.Run` (`internal/domain/workflow/workflow.go`)
draws a three-way distinction on a stage failure, and a playbook run
draws the same one on an action failure.

**Decision: reuse `Run`'s three-way distinction directly.** The stated
default is **stop-on-first-failure**: a real action error halts the
playbook run immediately and is reported, with no continue-to-next-action
and no automatic retry or compensating follow-up. The three points below
are the complete decision.

1. **A real action error** (the action's own logic failed -- an API
   error, a validation failure inside the action) stops the run
   immediately. No later actions execute. The failure is reported the
   same way a load collision is reported -- dropped, logged plainly
   (which action and why), and returned to the caller, never swallowed
   as a background log line only (the Dedup section's drop-and-report
   rule). `Run` is the mechanical
   precedent for the stop itself: a stage error sets the park reason and
   returns without running further stages (`workflow.go`). This
   is stop-on-first-failure, matching the document's existing bias
   against clever automatic recovery, and it is now a stated decision
   rather than a nobody-chose default.
2. **Interruption** (the daemon is shutting down mid-dispatch, `ctx.Err()
   != nil`) is explicitly **not** a failure. `Run` already treats this
   case specially -- "Daemon shutdown is not a workflow failure. Leave
   the task running ...; parking here would publish a false failure and
   require manual intervention" (`workflow.go`) -- and the same
   reasoning applies to a playbook run: an in-flight dispatch interrupted
   by restart must not be recorded as a broken playbook. What "leave it
   alone for restart" means for a playbook run (there is no `store.Task`
   row a playbook run inherently owns the way a workflow stage does) is
   an open implementation detail for whoever builds the coordinator, not
   decided here -- but the *semantic* (interruption ≠ failure) is decided.
3. **A run that produces no outcome at all** (every action ran without
   error, but nothing was assigned to happen) is itself an error
   condition, matching `Run`'s own "a workflow must end with an explicit
   outcome; not doing so is a definition bug, which still must not vanish
   silently" (`workflow.go`). A playbook with actions that all ran
   but never reached a coherent terminal state is a definition bug in the
   playbook, reported as such.

**Rollback:** explicitly **not attempted**, for exactly the reason the
gap's own description named -- a posted forge comment is not revocable.
This is not a limitation to work around later; it is the correct
semantic. An operator who needs a corrective action after a failure
writes a *new* playbook/action for that, they do not get automatic
undo.

**This stays inside the producer-only rule.** Stop-on-first-failure is the
run deciding its own terminal state from its own action's error. Nothing
already in flight is blocked or altered; the run only declines to start
actions that had not started yet, and it records the failure on its own
unit the way `Run` sets a park reason and transitions the task to
`StatusParked`. A future compensating action ("if the notify action
failed, also log an incident") would be producing new work, which the rule
already permits. No syntax for it is designed here.

**J3 relationship, stated explicitly:** J3 (a CEL `when` condition
erroring at eval time → treated as false → skip that action → run
continues) is a **different case from an action failure** and is not
revisited by this resolution. A condition error means "we couldn't
determine whether to run this action," which this document already
resolved as "then don't, and move on." An action failure means "we tried
to run this action and it broke," which stops the run per point 1 above.
The two must not be conflated: a coordinator implementation should be
able to point at one `when`-evaluation code path and one
action-invocation code path and show they use different outcomes.

### 2. Idempotency at execution time

**Resolved** (`archie-core-t2db.17`, a
design-investigation ticket: resolve, do not implement). The decision
below is the answer; the keying scheme itself is separate follow-up work
and is intentionally not built here. It **extends**
`event-sources-and-reactions.md`'s existing contract -- it does not
contradict it (relationship argued at the end).

**Problem (unchanged):** `event-sources-and-reactions.md` decided
reactions ride `internal/eventbus`'s at-least-once delivery specifically
so `PublishUnique`/idempotency keys absorb redelivery without duplicate
side effects. This document inherits that for free only for
`workflow`-kind actions, because `TaskEnvelope` already carries an
idempotency key. It said nothing for `channel`, `forge`, or `module`
actions with real side effects -- a posted comment, a sent message, a
billed image generation.

One correction to the problem statement, checked against the code rather
than assumed, because it changes what the answer must be:
**`PublishUnique`'s dedup is not a consumer-redelivery guard at all.**
`PublishUnique` only sets the JetStream `Nats-Msg-Id` header on the
outgoing message (`internal/infrastructure/eventbus/nats/publisher.go`;
key constant `message.go`); JetStream suppresses *republished*
messages carrying a repeated key inside `Config.DedupWindow`
(`client.go`, `Duplicates: cfg.DedupWindow`), default 2 minutes
(`config.go`, `DefaultDedupWindow = 2 * time.Minute`). A consumer
redelivery is the same *stored* message re-delivered on Nak or
acknowledge-timeout (`internal/eventbus/eventbus.go`: "a handler
returning an error causes redelivery") -- not a republish; its
`Nats-Msg-Id` is never re-firewalled, and even if it were, two minutes is
not an at-most-once guarantee for a side effect that must never repeat.
**Conclusion: side-effecting action kinds need a consumer-side, durable
dedup record; the bus cannot supply it.** That is precisely the situation
`binding_dispatches` was built for -- so the decision below reuses its
mechanism rather than designing a new one.

#### Decision

**Granularity: per-action-per-event -- one dedup record per
(action, event) pair, not per playbook run.** Whole-run keying, the
obvious alternative, has two crash windows: the run row is written when the run starts, so a crash mid-run
leaves the run "started" and a redelivery skips *every remaining
action* (work silently lost); a crash before the row write re-runs
*every already-fired action* (duplicate side effects -- the exact harm
this gap exists to prevent). Per-action keys collapse both into one
window and one behavior: an action's record is written immediately
before that action's side effect is invoked, so an action fires iff its
own record is absent, no fired action ever re-fires, and a redelivered
event resumes at the first unrecorded action. For the shipped
single-action playbooks the two granularities are equivalent in effect;
the difference starts with multi-action runs -- where whole-run keying
would already be wrong.

**Storage/lookup: a new durable ledger table `playbook_dispatches` in
`internal/store`, copying `binding_dispatches`'s conventions exactly**
(durable table, `INSERT OR IGNORE`, sentinel error, rows freed with
their parent, survives daemon restarts -- `internal/store/bindings.go`):

```sql
CREATE TABLE playbook_dispatches (
    playbook_id      TEXT NOT NULL,
    playbook_version TEXT NOT NULL,
    event_id         TEXT NOT NULL,
    action_id        TEXT NOT NULL,
    dispatched_at    TEXT NOT NULL,
    PRIMARY KEY (playbook_id, playbook_version, event_id, action_id)
);
```

Written with the same `INSERT OR IGNORE` + sentinel-error convention
(`ErrAlreadyDispatched`, `bindings.go`), so a duplicate is a no-op
write rather than a constraint error, and the caller matches it with
`errors.Is` the way the binding dispatch loop already does
(`internal/daemon/daemon.go` -- "another cycle already won this
race," not an error to surface). The record is consumed by the playbook
coordinator at dispatch time through a domain-side interface implemented
by `internal/store` -- the same shape as `BindingDispatcher`
(`internal/store/interface.go`) -- and the table lands in the
store's schema application (`internal/store/store.go`). No change
to `internal/eventbus`; no per-message dedup state on the client.

**Lifetime: no time-based expiry, and no automatic reclamation.** Rows are
retained until an explicit `DeletePlaybookDispatches` for their playbook.
Removing a playbook file does not delete its rows: playbooks are read only
at boot, so the only place to notice a removal is a boot-time reconcile,
and a boot that finds the directory missing or unmounted loads as "no
playbooks" and would reclaim the whole ledger, letting redelivered events
fire their side effects again. A binding's rows go with an explicit
operator delete (`bindings.go`, same transaction); a file disappearing is
not an equally deliberate act. The at-most-once guarantee must not
silently decay, which is exactly the property that disqualifies
`DedupWindow` above; table growth is bounded by distinct (playbook,
event, action) pairs and is accepted, the same tradeoff
`binding_dispatches` already makes.

**Redelivery behavior: skip the recorded action and continue the run.**
A detected duplicate is not re-invoked, is not reported to the caller as
an error, and is logged at debug level rather than as a warning -- this
is the expected, correct behavior of at-least-once delivery, not an
anomaly (same treatment as `ErrAlreadyDispatched` at
`daemon.go`).
The run continues with the next action (or ends, for a single-action
playbook whose one action is recorded), which is what makes a redelivery
a resume rather than a dead restart.

**Key derivation: a fixed composition of four structural identities,
computed by the coordinator -- not a CEL expression.** The key is the
`(playbook_id, playbook_version, event_id, action_id)` tuple above, where:

- `playbook_id` is `Playbook.ID` -- the playbook file's path relative to
  its configured directory root, slash-normalized, unique within the load
  composition by construction (`internal/domain/eda/playbook`).
- `playbook_version` is `Playbook.Version` -- a SHA-256 of the loaded
  file, recomputed on every load (`internal/domain/eda/playbook`),
  mirroring `Binding.Version`'s provenance-pinning purpose: the dispatch
  is pinned to the exact definition that was active when it fired. A
  changed playbook definition is a different key and may fire again for
  the same event -- deliberate, not a bug.
- `event_id` is `DispatchInput.TaskID` (`playbook.go`), which
  carries the `TaskEnvelope.IdempotencyKey()` value
  (`"archie:" + owner/repo/number`,
  `internal/domain/workintake/envelope.go`). It is chosen over
  `store.Task.ID` because the trigger vocabulary is issue-level
  workintake label/kind, dispatched at discovery time (pollNATS, forge
  webhook receiver), where no task row exists yet (`playbook.go`
  comment; `playbook_test.go`: "the value available at the
  discovery/dispatch point... NOT a store.Task.ID int64"). Keying on the
  issue identity rather than the delivery source is the load-bearing
  property the existing contract already established for `TaskEnvelope`
  (`event-sources-and-reactions.md` question 4: a webhook-sourced
  envelope for the same issue produces the *same* idempotency key as a
  poll-discovered one; `internal/forge/webhook/receiver.go` --
  "Idempotency is not this package's job"); this resolution inherits that
  property rather than re-deciding it. Consequence, stated explicitly:
  same-issue re-discoveries (a re-label that re-triggers the same kind)
  are the *same event* by design -- the workintake trigger vocabulary is
  issue-granular, exactly as `TaskEnvelope`'s own key is. A future
  capture/binding-originated trigger type keys on `captured_events.id`
  instead; derivation is per-trigger-type, not one formula for all
  triggers (rule carried over unchanged from the draft).
- `action_id` is the action's declared `id` -- J1's named ids, "a
  required field on every action that later actions reference", unique
  and stable-identifier-shaped, validated at load (the CEL resolution's
  J1 and its context table above in this document). For an action declaring no `id`
  (permitted by J1 while unreferenced), the key uses the action's 1-based
  position in the playbook: deterministic and stable for the
  single-action playbooks, and a later reorder is a file edit whose
  content hash changes `playbook_version` with it, so a positional
  fallback cannot silently collide with a prior definition.

**No CEL in the key -- stated explicitly because the CEL resolution
flagged this as gap 2's "first real exercise"** ("CEL's read-only result
access makes gap 2's keying derivation readable" -- the CEL resolution's
closing note). Readability
is not the binding constraint; **determinism under redelivery is**, and
CEL key expressions lose three ways. First, they are not load-checkable:
`event` stays `dyn` by the resolved J4 (schema-by-example), so
a key expression over event fields cannot be type-checked at load,
violating the document's reject-at-load philosophy. Second, they can
fail at dispatch (a missing field is a CEL error), and a key that cannot
be computed is an unfireable state -- neither "skip" nor "fire" is safe
without losing or duplicating work. Third, the contract needs one
derivation, and two authors writing different key expressions would
fragment the ledger. The key is therefore structural by construction:
every component above is an immutable identity of the run's inputs, so
two deliveries of the same event derive the same key with no evaluation.
The exercise's actual outcome is the derivation rule above; CEL remains
a *condition* surface (`when`), never a key surface. An action kind that
genuinely needs finer granularity than (action, event), such as "per
comment, not per issue", arrives with its own trigger type and its own
event identity, per the per-trigger-type rule.

**Record-before-invoke -- one deliberate divergence from
`binding_dispatches`, justified by the harm profile.** The binding
dispatch loop enqueues the task first and records the ledger row after
(`daemon.go`), accepting that racing cycles may both enqueue
("the duplicate task remains queued... delete-on-races is its own can of
worms"); the selection query excludes already-dispatched captures
(`bindings.go`), but the race window remains. That ordering is
correct there because the duplicate artifact is an internal task row --
recoverable, low-harm. The playbook ledger's ordering is reversed on
purpose: **the row is written and committed before the action's side
effect is invoked** -- the `INSERT OR IGNORE` is the atomic gate, and
the lost-write loser skips without invoking. The cost is stated, not
hidden: a crash between the row commit and the invoke loses that one
action's side effect, repairable by hand -- the reverse direction of the
same window that would otherwise duplicate a posted comment or
re-bill an image generation. For side effects whose duplication is
externally visible and not revocable, at-most-once is the required
direction; the binding system's at-least-once-tolerant ordering is not
the precedent for this ledger.

**Gap-1 interplay, stated to keep the two mechanisms separate:** the
ledger answers one question per action -- has this action already fired
for this event? It does not answer whether the run continues, stops, or
is re-attempted; that is gap 1's coordinator concern (a run-outcome
record; gap 1 explicitly leaves "what leave it alone for restart means
for a playbook run" to whoever builds the coordinator). One consequence
of record-before: an action that FAILED (gap 1's real-action-error case)
has a ledger row, so a later redelivery of the same event skips it --
consistent with gap 1's "no automatic retry" resolve (a corrective
action is a *new* playbook/action, per gap 1's rollback paragraph). A
failed action is never re-attempted by the bus; if a run must be
recovered, an operator edits the playbook -- new version, new key --
rather than fighting ledger state.

**Relationship to `event-sources-and-reactions.md` -- extension, not
contradiction.** Keyed to that document's sections:

1. Its decision 2 commits reactions to at-least-once delivery via
   `PublishUnique`'s "without duplicate enqueue" guarantee -- which, with
   the code facts above, is accurate for the *publish* path only:
   `PublishUnique` absorbs duplicate publishes of the same event (poll
   vs. webhook), which is what that decision was actually about. This
   document's own problem statement, taken literally, overstates it
   ("keys absorb redelivery without duplicate side effects") -- that
   overstatement is exactly what this gap exists to correct. The
   existing contract never claimed consumer redeliveries of
   side-effecting reactions were absorbed, so nothing it says contradicts
   a consumer-side ledger; this resolution pins down the boundary the
   sentence left implicit (publish-path dedup vs. consumer-path dedup)
   rather than changing either.
2. Its decision 3 is untouched: the ledger is the run's own bookkeeping
   about its own actions, suppressing a re-fire and nothing else.
3. Its decision 4 (no generic `Source` interface; `TaskEnvelope` is the
   typed contract, keyed on issue identity, not delivery source): the
   event half of the playbook key IS that same identity
   (`TaskEnvelope.IdempotencyKey()`), and the per-trigger-type rule
   means any future trigger type brings its own identity rather than a
   new generic one -- the playbook ledger introduces no second identity
   vocabulary and no untyped payload. The only new type in this
   resolution is the ledger's own value object, consumed through a
   narrow interface like `BindingDispatcher` -- the same shape the
   document's "typed families" rule (decision 1, plugin engine rule)
   already blesses.

**Scope: this is the `channel`/`forge`/`module` decision, not a
`workflow`-position change.** Workflow-kind actions keep their existing
keying claim unchanged -- the `TaskEnvelope` path that this document
already inherits for free (`daemon.go`, `PublishTask` publishes
with `task.IdempotencyKey()`), with its cross-poll/webhook property
untouched. The ledger gate applies to the side-effecting positions
(channel, forge, module), which is why the closing statement below
still holds: the first slice (Module + loader + workflow-kind dispatch)
was buildable without this resolution, and channel/forge/multi-action
playbooks were not. A coordinator MAY implement one position-uniform
gate as a simplification; what this resolution requires is only that
the side-effecting positions are gated.

## Resolved questions

1. **Data-flow/condition syntax.** Does this project want a small existing
   Go expression evaluator (there may be one already in the codebase worth
   checking, e.g. anything backing gate conditions), or a bespoke minimal
   grammar?

   **Resolved** (`archie-core-t2db.14`). CEL (`cel.dev/cel-go`, pinned) is
   the one mechanism for both an action's `when` condition and its `args`
   values. The trust boundary, evaluation context, load-time validation and
   the five judgment calls are in
   `docs/prds/playbook-expression-syntax.md`.

2. **Playbook directory config field.** Where this lives in
   `internal/config` / `configuration.Document`, and whether it's one
   directory or a list (mirroring `SkillsDir`'s shape) -- follow existing
   config precedent, not invented fresh.

   **Resolved** (`archie-core-t2db.11`). The directory
   shape landed as `playbook_dirs` (a LIST of directories of
   `*.yaml`/`*.yml` binding files), loaded at startup as an additional
   input to the two single-file fields, which remain supported unchanged.
   A single `playbook_dir` would contradict
   supporting multiple independently-maintained playbook sources, which
   the Dedup section already assumes ('independently-sourced playbook
   directories collide'), so the field is a list. Cross-source collision
   (same key in two directories, or in a single-file field and a
   directory) is reported like an in-directory collision -- nothing is
   arbitrated by source precedence. This is what finally exercises the
   design doc's drop-and-report collision rule, since a single file
   cannot collide with itself. Repo-scoping (tying directories to
   `Config.Repos` owner/name) is explicitly deferred to a separate,
   later decision; org/tenant keying is out of scope (no auth/identity
   model).

   Decisions recorded from the label-vocabulary slice:
   - Arbitrary labels bind to registered workflow names via
     `LabelWorkflows` + `LoadLabelWorkflowsYAML`/`SetLabelWorkflows`
     (`internal/domain/workflow/routing.go`).
   - The closed `Kind`/NATS-subject set is untouched; the label map only
     extends binding authority for labels the kind layer does not own.
   - Collision rule: a label already owned by the
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
   repository-supplied code that must run in a container.

   **Resolved** in `docs/prds/module-position.md`, "Trust boundary": a
   Module is operator-installed, in-process and daemon-privileged, the same
   tier as `PluginDir` and `SecretEngineDir`, never repository-supplied
   task code.
4. **First implementation slice.** Recommend: Module position + the
   playbook loader + trigger-to-workflow dispatch only (subsuming the existing
   label routing) first, proving the schema-gen -> Yaegi -> playbook path
   end to end on the smallest useful case, before adding Channel/Forge
   action kinds or the linter/LSP.

   **Resolved** (`archie-core-t2db.13`, `.14`, `.15`). The Module position
   (registry + log kind, t2db.13), the CEL expression environment
   (t2db.14), and the playbook document + event coordinator with
   single-action workflow-kind dispatch (t2db.15) are shipped.

   **Resolved** (`archie-core-t2db.23`): the coordinator selects a real
   task's workflow. The wiring point is the daemon's definition pin
   (`Daemon.resolveWorkflowID`, consumed by `pinWorkflowFromCollection` in
   `internal/daemon`), not `pollNATS`/`publishTask`. Routing belongs to the
   pin because a workflow definition is a
   database row pinned before dispatch, which makes the pin the one place a
   task's workflow is chosen. Precedence there is explicit `Task.Workflow`
   (the waiting_human -> approved requeue) → matching playbook →
   `workflow.ResolveWorkflowID`'s label binding → kind binding → triage →
   implement. A playbook naming an undefined workflow is a reported failure
   that parks the task rather than a silent fall-through to the binding's
   choice.

   `Store.Dispatch` returns the selected workflow *name* and the matching
   playbook's id and version, not a compiled `workflow.Workflow`: the
   production caller decides against the active definition collection, and
   the id and version are the first two components of the gap-2 idempotency
   key. The playbook package does not import `internal/domain/workflow`.

   A `when` condition at this point reads what the intake knows about the
   originating issue: `kind`, `labels`, `owner`, `repo`, `number`, `title`,
   `body`, `source`, built by `playbookInput` in `internal/daemon`.

   The trigger shape reuses the existing workintake kind/label vocabulary.
   The single-action hard boundary is superseded by
   `docs/prds/multi-action-playbooks.md` (two-shape rule); Module, Channel,
   and Forge positions still have no production dispatch path.
## Standing constraint

This may be commercialized, and is to be discernible from other open-source
event-driven-automation tooling. No design decision follows from that yet;
it bears on licensing or feature-gating questions about playbook authoring
and the LSP when they arise.

## Packages this touches (first slice)

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
