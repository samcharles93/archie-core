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

  **Status 2026-09-03: lint mode shipped (t2db.12).** A standalone
  gopls-shaped binary (`cmd/archie-playbooks`, lint subcommand) validates
  playbook directories against the exact loaders the daemon uses
  (`LoadPlaybookDirs`/`LoadKindWorkflowsYAML`/`LoadLabelWorkflowsYAML`),
  exiting non-zero on any collision / malformed file / invalid binding.
  Findings are file-granular, not line-granular: the loader decodes with
  `yaml.Unmarshal` into a plain map, which discards line numbers. A
  compiler-style file:line diagnostic needs a `yaml.Node` decoding
  upgrade, tracked separately -- the linter agrees with runtime
  validation by construction, which is the load-bearing property.
  Discoverable via `task lint:playbooks` or direct `go run
  ./cmd/archie-playbooks lint -dir ...`. The LSP/serve mode is a later
  entrypoint of the SAME binary, per the shared-package decision above.
- **LSP**: same schema source again, feeding author-time hover/completion/
  inline errors while someone edits a playbook YAML in an editor. Kept
  explicitly in scope per Sam (not deferred), because it's the same schema
  artifact as the linter's, just a different consumer.

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

One shared implementation, two thin consumer shapes (in-process and
out-of-process). Folding CLI/LSP protocol handling into `archied` itself
would grow the daemon into unrelated concerns, against the plugin-engine
rule's spirit of narrow capability families -- so the answer is not
"bundle or don't," it's "don't duplicate the logic, and pick the thinnest
consumer for each caller's actual constraint."

## Execution-time gaps (identified 2026-09-03; both resolved 2026-09-05)

Everything above covers *loading* a playbook. Nothing above covers what
happens while one *runs*. Two gaps, both load-bearing enough to resolve
before implementation, not defer:

### 1. Mid-playbook action failure

**Status 2026-09-05: resolved by archie-core-t2db.18.** The default below
is a committed decision, not a candidate list; changing it later would
require its own decision. Gap 2 below is resolved by `archie-core-t2db.17`.

A playbook is an ordered action list with data flowing between steps. If
action 2 of 4 fails at runtime (an image-gen API times out, a forge call
403s), the doc previously said nothing about what happens next. This
codebase already answers a structurally identical question for its
existing workflow engine, precisely enough to reuse rather than
re-derive: `workflow.Run` (`internal/domain/workflow/workflow.go:294`)
draws a three-way distinction on a stage failure, and a playbook run
draws the same one on an action failure.

**Decision: reuse `Run`'s three-way distinction directly.** The stated
default is **stop-on-first-failure**: a real action error halts the
playbook run immediately and is reported, with no continue-to-next-action
and no automatic retry or compensating follow-up. The three points below
are the complete decision; reasoning and evidence follow each.

1. **A real action error** (the action's own logic failed -- an API
   error, a validation failure inside the action) stops the run
   immediately. No later actions execute. The failure is reported the
   same way a load collision is reported -- dropped, logged plainly
   (which action and why), and returned to the caller, never swallowed
   as a background log line only (this document's Dedup section, lines
   155-159, the t2db.9-12 drop-and-report rule). `Run` is the mechanical
   precedent for the stop itself: a stage error sets the park reason and
   returns without running further stages (`workflow.go:324-326`). This
   is stop-on-first-failure, matching the document's existing bias
   against clever automatic recovery, and it is now a stated decision
   rather than a nobody-chose default.
2. **Interruption** (the daemon is shutting down mid-dispatch, `ctx.Err()
   != nil`) is explicitly **not** a failure. `Run` already treats this
   case specially -- "Daemon shutdown is not a workflow failure. Leave
   the task running ...; parking here would publish a false failure and
   require manual intervention" (`workflow.go:317-323`) -- and the same
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
   silently" (`workflow.go:336`). A playbook with actions that all ran
   but never reached a coherent terminal state is a definition bug in the
   playbook, reported as such.

**Rollback:** explicitly **not attempted**, for exactly the reason the
gap's own description named -- a posted forge comment is not revocable.
This is not a limitation to work around later; it is the correct
semantic. An operator who needs a corrective action after a failure
writes a *new* playbook/action for that, they do not get automatic
undo.

**Producer-only interaction, stated explicitly -- why this decision does
not violate the rule.** `event-sources-and-reactions.md`'s producer-only
rule is "no veto, no mutation of in-flight work"
(`event-sources-and-reactions.md:61-63`), and it explicitly routes any
reaction that "can veto or mutate an *in-flight* task (block a PR from
opening, alter a running stage)" away from this epic to the
adversarial-self-review one (`event-sources-and-reactions.md:78-81`).
This decision touches neither half of that boundary:

- **The rule constrains what an event source may do to *someone else's*
  in-flight work.** Stop-on-first-failure is the playbook run deciding
  its *own* terminal state from its own action's error. It vetoes
  nothing: no task, stage, or PR that was already in flight is blocked
  or altered, and the run only declines to start actions that had not
  started yet. A run deciding its own continuation is not veto or
  mutation of another producer's work; it is simply how the run ends.
- **Everything after the stop is still producer-only.** The failed run is
  reported to its caller per the collision precedent, the same way `Run`
  records the failure on its own unit -- park reason set and the task
  transitioned to `StatusParked` (`workflow.go:324-326`, `park` at
  `workflow.go:351-357`) -- never on someone else's in-flight work. A
  future compensating action (e.g. "if the notify action failed, also
  log an incident") would be **producing new work** -- new events, new
  dispatches -- which is exactly the already-permitted shape
  (`event-sources-and-reactions.md:73-76`). No playbook syntax for "on
  failure, also run X" is designed here; this resolution only confirms
  that if that syntax is added later, it stays inside the producer-only
  boundary rather than requiring a new exception.

The boundary therefore holds on both sides: the failure default never
hands an event source veto or mutation power over a playbook run, and
anything a run does after stopping is itself still just producing work.

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

**Status 2026-09-05: resolved** (per bead `archie-core-t2db.17`, a
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
outgoing message (`internal/infrastructure/eventbus/nats/publisher.go:18-23`;
key constant `message.go:11-13`); JetStream suppresses *republished*
messages carrying a repeated key inside `Config.DedupWindow`
(`client.go:67`, `Duplicates: cfg.DedupWindow`), default 2 minutes
(`config.go:15`, `DefaultDedupWindow = 2 * time.Minute`). A consumer
redelivery is the same *stored* message re-delivered on Nak or
acknowledge-timeout (`internal/eventbus/eventbus.go:64-65`: "a handler
returning an error causes redelivery") -- not a republish; its
`Nats-Msg-Id` is never re-firewalled, and even if it were, two minutes is
not an at-most-once guarantee for a side effect that must never repeat.
**Conclusion: side-effecting action kinds need a consumer-side, durable
dedup record; the bus cannot supply it.** That is precisely the situation
`binding_dispatches` was built for -- so the decision below reuses its
mechanism rather than designing a new one.

#### Decision

**Granularity: per-action-per-event -- one dedup record per
(action, event) pair, not per playbook run.** This supersedes the
earlier draft's whole-run default. Whole-run keying has two crash
windows: the run row is written when the run starts, so a crash mid-run
leaves the run "started" and a redelivery skips *every remaining
action* (work silently lost); a crash before the row write re-runs
*every already-fired action* (duplicate side effects -- the exact harm
this gap exists to prevent). Per-action keys collapse both into one
window and one behavior: an action's record is written immediately
before that action's side effect is invoked, so an action fires iff its
own record is absent, no fired action ever re-fires, and a redelivered
event resumes at the first unrecorded action. For today's shipped
single-action playbooks the two granularities are equivalent in effect;
the difference starts with multi-action runs -- where whole-run keying
would already be wrong.

**Storage/lookup: a new durable ledger table `playbook_dispatches` in
`internal/store`, copying `binding_dispatches`'s conventions exactly**
(durable table, `INSERT OR IGNORE`, sentinel error, rows freed with
their parent, survives daemon restarts -- `internal/store/bindings.go:31-37`,
`355-382`):

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
(`ErrAlreadyDispatched`, `bindings.go:59-63`), so a duplicate is a no-op
write rather than a constraint error, and the caller matches it with
`errors.Is` the way the binding dispatch loop already does
(`internal/daemon/daemon.go:591-597` -- "another cycle already won this
race," not an error to surface). The record is consumed by the playbook
coordinator at dispatch time through a domain-side interface implemented
by `internal/store` -- the same shape as `BindingDispatcher`
(`internal/store/interface.go:123-139`) -- and the table lands in the
store's schema application (`internal/store/store.go:137-139`). No change
to `internal/eventbus`; no per-message dedup state on the client.

**Lifetime: no time-based expiry.** Rows live as long as their playbook
exists; when a playbook is removed from the configured directories, the
coordinator deletes its rows -- mirroring binding-dispatches-rows-deleted-
with-binding (`bindings.go:204-214`, same transaction as the delete). The
at-most-once guarantee must not silently decay, which is exactly the
property that disqualifies `DedupWindow` above; table growth is bounded
by distinct (playbook, event, action) pairs over the playbook's lifetime
and is accepted, the same tradeoff `binding_dispatches` already makes.

**Redelivery behavior: skip the recorded action and continue the run.**
A detected duplicate is not re-invoked, is not reported to the caller as
an error, and is logged at debug level rather than as a warning -- this
is the expected, correct behavior of at-least-once delivery, not an
anomaly (same treatment as `ErrAlreadyDispatched` at
`daemon.go:592-597`).
The run continues with the next action (or ends, for a single-action
playbook whose one action is recorded), which is what makes a redelivery
a resume rather than a dead restart.

**Key derivation: a fixed composition of four structural identities,
computed by the coordinator -- not a CEL expression.** The key is the
`(playbook_id, playbook_version, event_id, action_id)` tuple above, where:

- `playbook_id` is `Playbook.ID` -- the playbook file's path relative to
  its configured directory root, slash-normalized, unique within the load
  composition by construction (`internal/domain/eda/playbook/playbook.go:39-45`,
  `149-155`). Note this prerequisite already landed in code: the earlier
  draft's "not yet decided in code" paragraphs are stale --
  `edd689e` (`feat(eda): thread Playbook ID/Version and DispatchInput task
  identity (gap-2 prereqs)`) shipped `Playbook.ID`/`Version` and
  `DispatchInput.TaskID`.
- `playbook_version` is `Playbook.Version` -- a SHA-256 of the loaded
  file, recomputed on every load (`playbook.go:46-49`, `154-155`),
  mirroring `Binding.Version`'s provenance-pinning purpose: the dispatch
  is pinned to the exact definition that was active when it fired. A
  changed playbook definition is a different key and may fire again for
  the same event -- deliberate, not a bug.
- `event_id` is `DispatchInput.TaskID` (`playbook.go:214-222`), which
  carries the `TaskEnvelope.IdempotencyKey()` value
  (`"archie:" + owner/repo/number`,
  `internal/domain/workintake/envelope.go:105-107`). It is chosen over
  `store.Task.ID` because the trigger vocabulary is issue-level
  workintake label/kind, dispatched at discovery time (pollNATS, forge
  webhook receiver), where no task row exists yet (`playbook.go:214-222`
  comment; `playbook_test.go:338-345`: "the value available at the
  discovery/dispatch point... NOT a store.Task.ID int64"). Keying on the
  issue identity rather than the delivery source is the load-bearing
  property the existing contract already established for `TaskEnvelope`
  (`event-sources-and-reactions.md` question 4: a webhook-sourced
  envelope for the same issue produces the *same* idempotency key as a
  poll-discovered one; `internal/forge/webhook/receiver.go:14-20` --
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
  position in the playbook: deterministic and stable for today's
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
a *condition* surface (`when`), never a key surface. { Call: if a future
action kind genuinely needs finer granularity than (action, event) --
e.g. "per comment, not per issue" -- that is the documented exception
path, and it arrives with its own trigger type and its own event
identity, per the per-trigger-type rule. }

**Record-before-invoke -- one deliberate divergence from
`binding_dispatches`, justified by the harm profile.** The binding
dispatch loop enqueues the task first and records the ledger row after
(`daemon.go:585-599`), accepting that racing cycles may both enqueue
("the duplicate task remains queued... delete-on-races is its own can of
worms"); the selection query excludes already-dispatched captures
(`bindings.go:395-399`), but the race window remains. That ordering is
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
contradiction, argued explicitly.** Three points, keyed to that
document's sections:

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
2. Its decision 3 (producer-only, no veto/mutation of in-flight work)
   is untouched: the ledger is the running reaction's own bookkeeping
   about its own actions -- it suppresses re-firing an action the
   coordinator already ran for the same event; it blocks, mutates, or
   reorders nothing else, and no other producer's work is touched.
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
already inherits for free (`daemon.go:858-869`, `PublishTask` publishes
with `task.IdempotencyKey()`), with its cross-poll/webhook property
untouched. The ledger gate applies to the side-effecting positions
(channel, forge, module), which is why the closing statement below
still holds: the first slice (Module + loader + workflow-kind dispatch)
was buildable without this resolution, and channel/forge/multi-action
playbooks were not. A coordinator MAY implement one position-uniform
gate as a simplification; what this resolution requires is only that
the side-effecting positions are gated.

Both execution-time gaps are now resolved (gap 1 by
`archie-core-t2db.18` above, gap 2 by `archie-core-t2db.17` above). For
the record, the first slice (Module + loader + workflow-kind dispatch)
never needed gap 2: workflow-kind actions had idempotency for free via
`TaskEnvelope`, and the ledger is only required once side-effecting
positions dispatch. Channel/Forge actions and multi-action playbooks
are unblocked on the execution-time questions.

## Open questions (for sign-off before implementation)

1. **Data-flow/condition syntax.** Does this project want a small existing
   Go expression evaluator (there may be one already in the codebase worth
   checking, e.g. anything backing gate conditions), or a bespoke minimal
   grammar? Not decided here.

   **Status 2026-09-03: resolved.** CEL (cel.dev/cel-go) is the single
   mechanism for both halves. The resolution below replaces this entry
   and is not an open question. Judgment calls are flagged inline.

   ## Data-flow and condition syntax -- decision (resolves open question 1)

   **Status:** Draft, awaiting sign-off (not yet in `docs/architecture/`)
   **Date:** 2026-09-03
   **Decision:** CEL via `cel.dev/cel-go` (v0.32.0), pinned; one
   expression mechanism covers both an action's `when` condition and its
   `args` values.

   ### The requirement, stated directly

   A playbook action is gated and parametrised by runtime data: (a) a
   condition deciding whether the action runs; (b) a later action's `args`
   values referencing an earlier action's `Result` and the triggering
   event's fields. Both are **CEL expressions**, evaluated against one
   evaluation context. There is no separate interpolation syntax: CEL
   reads nested maps and fields natively, so `args` values that need data
   are written as CEL, and only plain literal args remain plain YAML
   scalars.

   Both halves are read-only: expressions may read context values and
   compute a result from them, never mutate, never call host functions,
   never perform I/O. CEL enforces this structurally (no side effects,
   linear evaluation when macros are bounded, cost-limited).

   ```yaml
   actions:
     - position: module
       kind: log
       args:
         message: '"build finished"'                 # literal
       when: 'event.label == "bugfix"'               # gate on event field
     - position: module
       kind: notify
       args:
         message: '"priority " + string(event.priority)'
       when: 'actions.notify.result.delivered == true'
   ```

   ### Trust boundary of the expression

   A playbook YAML is **operator-installed, in-process, daemon-privileged**
   -- the same tier as `ModuleDir`/`PluginDir`/`SecretEngineDir`
   (module-position.md's trust-boundary section): loaded from a configured
   directory at startup, never from a webhook body or task worktree. The
   schema-by-example flow uses live events to *design against*; the saved
   playbook is operator-authored file content in an operator-configured
   location.

   Two consequences:

   1. The expression string is **trusted**. Operator-authored config, not
      attacker input; no sandboxing against a hostile expression string is
      required. If a future playbook source is less trusted (auto-generated
      bindings from unauthenticated captures), that is the playbook-file
      trust tier moving -- the loader's acceptance check is the right
      place, not the evaluator.
   2. The **data the expression reads is NOT trusted** (decoded webhook
      body, prior results through the same pipeline). Robustness against
      hostile data is required: no panic on wrong-typed/missing fields,
      no unbounded cost on deep/cyclic maps, bounded evaluation time. CEL
      provides this structurally (cost limits, linear non-Turing-complete
      evaluation, error values instead of panics -- verified in the
      vetting record below).

   ### The gate precedent is deliberately NOT copied

   `internal/gate/gateeval` evaluates arbitrary interpreted Go
   (`.archie/gate.go`) as the repo's conditional-logic precedent. That is
   the wrong precedent for playbook conditions because this document's
   premise is: **YAML is pure orchestration data; Yaegi is where
   imperative logic lives** (Problem section; module-position.md's
   "no generic hook" section). Yaegi snippets in YAML would move
   imperative logic into the data layer -- the inversion this design
   exists to prevent -- and make playbooks un-lintable.

   The line: conditions are **declarative predicates over runtime data,
   not programs**. CEL is non-Turing-complete and side-effect-free, so it
   cannot become a program; a condition that needs real logic gets a
   Module kind (Yaegi), referenced by the action, not a stronger
   expression body.

   ### Plugin-engine-rule interaction: confirmed non-issue (stated once)

   The plugin engine rule forbids a generic `Module` interface with an
   `any` payload; it exists to keep static implementation contracts
   typed. Evaluating CEL against runtime `map[string]any` is **not the
   same problem**: the rule concerns *implementation contracts* (static
   operations a capability family exposes), not *runtime data values*
   (legitimately dynamic -- an event payload's shape is unknown until it
   arrives). The Module contract stays fully typed (generated
   `Args`/`Result` schemas); CEL is a read-only data reader under the
   playbook engine family, not a new capability contract. Stated once.

   ### Minimum viable evaluation context (what expressions see)

   One flat context, namespaced from the playbook run, built at dispatch
   time:

   | Name | Type | Source |
   |---|---|---|
   | `event` | `map(string, dyn)` | The triggering event's decoded payload (webhook body / forge issue / schedule tick). Field access via `event.<field>`, map access via `event["field"]`, presence via `has(event.<field>)`. |
   | `actions` | `map(string, dyn)` | Previous actions' results, keyed by the action's `id` as declared in the playbook (`actions.<id>.result.<field>`), so a later action reads an earlier one's `Result` map regardless of position. The current action and later actions are not present. |

   Expressions may also read literal-only state (numbers, strings,
   booleans) directly. CEL's `has()` macro covers the missing-key case
   (`has(event.labels)` -> bool), which replaces any bespoke `has`/
   `contains` operator.

   `actions.<id>.result` is the `map[string]any` `Module.Invoke` already
   returns (t2db.13), so no result-shape change is needed for data flow;
   channel/forge kinds' results are declared to the same shape.

   Judgement call J1: `action` id is a required field on every action
   that later actions reference; ids are validated at load (unique,
   stable-identifier-shaped), and a `when`/`args` expression referencing
   an unknown `actions.<id>` is a **load failure**, not a runtime miss --
   the same reject-at-load rule as collisions (see below).

   ### Load-time validation (consistent with the collision rule)

   The collision-handling rule is: schema defines the accepted message;
   anything outside it is rejected at load, logged plainly, reported to
   the caller -- never deferred to runtime. Expressions are validated the
   same way, at playbook load:

   - **Syntax**: every `when` and every CEL-typed `args` value is parsed
     and checked by CEL's checker when the playbook is loaded. A syntax
     or type error is a reported load failure (the playbook is dropped and
     the error goes to the caller), not a runtime surprise.
   - **Context conformance**: with the context declared to CEL
     (`event` as `map(string, dyn)`, `actions` as `map(string, dyn)`),
     unknown top-level identifiers are rejected at compile time.
     Field-level typing inside `dyn` values is deferred to runtime by
     CEL's design (verified: `data.items.lenght` with `data` dyn compiles
     clean and errors at eval) -- so the loader declares as much type as
     the schema provides (the action kinds' generated `Result` structs
     become CEL type declarations at load, giving field-level rejection
     for result reads), and `event` stays `dyn` because its shape is
     unknown by design (schema-by-example).
   - **Names**: unknown `actions.<id>` references and unknown roots are
     compile-time/lint-visible errors (J1).

   This means the lint tool (t2db.12) and the daemon share one checker
   -- same single-source argument as the playbook loaders.

   ### Candidate comparison (surveyed 2026-09-03, pkg.go.dev + GitHub;
   govaluate and gval checked against actual maintenance state, not
   training memory)

   | Candidate | Maintenance | Safety surface | Cost (measured) | Verdict |
   |---|---|---|---|---|
   | **CEL (cel.dev/cel-go)** | Actively maintained (google/cel, monthly releases) | Non-Turing-complete, linear eval, `CostLimit`/`CostTracking`/`ParserRecursionLimit`, no side effects, error values not panics | **+13.0MB** (2.34MB -> 15.25MB) | **Chosen** |
   | `expr-lang/expr` v1.17.8 | Actively maintained | Side-effect-free, `DisableAllBuiltins`, depth limits (CVE fixed w/ tests) | +3.9MB (2.34MB -> 6.28MB) | Not chosen: two safety CVEs this cycle; smaller ecosystem; no protobuf/type-model fit with future schema-gen |
   | `gval` v1.2.3 | Maintained, slower | No builtin-restriction surface; extras (ternary/`??`) not wanted | +3.0MB (2 transitive deps) | Not chosen |
   | `govaluate` | **ARCHIVED** (author archived 2024) | n/a | n/a | Not chosen: dead |
   | `starlark-go` | Maintained (Bazel) | Turing-complete with step limits | large (full language) | Not chosen: whole-language surface |

   ### Vetting record (vetting-dependencies skill, 2026-09-03)

   Source reviewed at HEAD, not docs:

   - **Repo hygiene**: clean. No binaries/media in tree (the only
     non-source artifacts are expected generated protobuf files); 16-line
     `.gitignore`.
   - **Hard-boundary correctness**: the place correctness is hard for an
     expression evaluator is bounding evaluation and surviving hostile
     input. Dedicated regression tests exist and pass at HEAD:
     `TestCostLimit`, `TestCostTracking*`, `TestParserRecursionLimit`,
     and a large conformance suite. Verified by execution: evaluation
     against a 2000-deep nested map under `CostLimit` completes in ~10
     microseconds and returns an error value for a missing key -- no
     panic, no unbounded walk.
   - **Dependency footprint**: antlr (parser), protobuf + genproto (CEL's
     type model), `cel.dev/expr` (CEL spec types), yaml.v3. All
     mainstream, canonical, vendored widely by Google-adjacent projects.
     No runtime-impure third-party code; the REPL-only deps (readline,
     tview, tcell) are not in the library import graph.
   - **Security record**: no advisories on record for `cel.dev/cel-go`
     (GitHub Advisory Database, queried 2026-09-03).
   - **Module path**: migrated to `cel.dev/cel-go` (v0.32.0 is current);
     the old `github.com/google/cel-go` path is the pre-migration identity.
     Pin `cel.dev/cel-go`.
   - **Binary size cost**: +13.0MB is the largest of the candidates
     (measured: baseline 2.34MB -> 15.25MB with CEL). Accepted because:
     the daemon already embeds a large Yaegi runtime; CEL's type model is
     the load-bearing win (compile-time rejection of result-path typos,
     matching the reject-at-load rule); and the alternative bespoke
     grammar would re-implement CEL's checker to get the same guarantee.
     Flagged for sign-off: if 13MB is unacceptable for a specific
     deployment target, the fallback is a bespoke grammar with a
     re-implemented subset of this checker -- a strictly worse trade.

   ### Why bespoke was not sufficient

   A bespoke grammar for the shipped cases (path read + comparison + bool
   composition) is small, but the load-time-validation requirement makes
   the comparison: the reject-at-load rule requires *field-level* static
   checking of result paths against generated `Result` schemas, which is
   CEL's type-checker's job. Re-implementing that on a hand-rolled
   grammar means re-implementing a type system -- more code than the
   grammar itself, with worse diagnostics, no conformance suite, and no
   community surface for future expression needs (even declarative ones).
   The bespoke option only wins on binary size, and the size cost of CEL
   is measured and bounded.

   ### Judgment calls (flagged for sign-off)

   - **J1 -- `id` required on referenced actions, unknown id = load
     failure.** Matches the reject-at-load rule. { Call: an alternative
     is positional `actions[0]` access, which breaks on reorder; named is
     the operator-friendlier shape. }
   - **J2 -- every `args` value is evaluated as a CEL expression.** No
     split between "literal args" and "expression args": a plain string
     value is a CEL string literal, a number is a CEL number literal, and
     any value that reads context data is written in CEL. One mechanism,
     no interpolation marker, no second syntax to learn. { Call: CEL
     string literals must be quoted inside YAML (e.g. `message:
     '"build finished"'`), which is slightly noisy for mostly-literal
     args; accepted for uniformity -- the linter catches quoting mistakes
     at load. }
   - **J3 -- condition failure semantics.** A condition that errors at
     runtime (missing/wrong-typed field) evaluates to **false**: the
     action is skipped, logged, run continues. This is the simplest
     candidate consistent with gap 1's bias against clever recovery;
     gap 1 (resolved by archie-core-t2db.18) commits
     stop-on-first-failure, so per-action control is not coming and J3
     stands. { Call: CEL separates
     false from error; we treat evaluation error as false + log. }
   - **J4 -- `event` stays `dyn` (schema-by-example).** Load-time we
     cannot know event field types; declaring them typed would reject
     valid plays. Result reads get field-level checking via generated
     schema declarations. { Call: when schema-by-example ships, tighten
     event field declarations from captured samples. }
   - **J5 -- cost limit default.** A `CostLimit` at load time (e.g.
     100,000) is applied to every expression; the linter and daemon use
     the same value so they agree. { Call: exact number is a constant to
     tune at first use. }

   ### Interaction with the execution-time gaps

   This resolution does not resolve the execution-time gaps (both are
   resolved separately: gap 1 by `archie-core-t2db.18`, gap 2 by the
   gap-2 decision below). Conditions make gap 1
   concrete (condition-failure vs. action-failure semantics, J3) and
   CEL's read-only result access makes gap 2's keying derivation
   readable (an idempotency key expression can read event fields) -- both
   flagged as the gaps' first real exercise. Gap 2's exercise is
   subsequently solved by the gap-2 decision above (2026-09-05): the
   answer is a fixed structural derivation, and idempotency keys are
   deliberately NOT CEL expressions (see the "No CEL in the key"
   passage).

   ### Implementation placement (for the follow-up slice)

   - New `internal/domain/eda/expr` package: builds the CEL environment
     (context declarations, generated `Result` schema types as CEL type
     declarations, cost limit), compiles/validates playbook expressions
     at load, evaluates them at dispatch. Domain layer; no infrastructure
     imports; owned by the playbook engine family. One new dependency:
     `cel.dev/cel-go` (pinned), `go.mod` otherwise unchanged.
   - Consumption: the playbook event coordinator (open question 4's
     loader, not yet built) evaluates `when` before dispatch and
     evaluates `args` values before `Module.Invoke`; the lint tool
     (t2db.12) shares the same compile/validate path for author-time
     diagnostics.
   - Schema integration: action kinds' generated `Result`/`Args` structs
     (t2db.13's `logextract` pattern) are declared to CEL at load so
     `actions.<id>.result.<field>` type-checks; `args` expressions
     type-check against the target kind's `Args` schema before the
     playbook is accepted.

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

   **Status 2026-09-03: resolved by t2db.11 (corrected).** The directory
   shape landed as `playbook_dirs` (a LIST of directories of
   `*.yaml`/`*.yml` binding files), loaded at startup as an additional
   input to the two single-file fields, which remain supported unchanged.
   Sam's correction 2026-09-03: a single `playbook_dir` would contradict
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

   **Status 2026-09-03: resolved across t2db.13-15.** The Module position
   (registry + log kind, t2db.13), the CEL expression environment
   (t2db.14), and the playbook document + event coordinator with
   single-action workflow-kind dispatch (t2db.15) are shipped. The
   coordinator is NOT yet wired into production task intake
   (daemon.go/pollNATS/publishTask) -- that is a follow-up ticket;
   production wiring would make a real forge-triggered task flow through
   this coordinator instead of only workflow.Route(). The trigger shape
   reuses the existing workintake kind/label vocabulary; multi-action and
   non-workflow-position playbooks remain rejected at load (hard
   boundary, blocked on gap 2, idempotency).
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
