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
     revisit if gap 1 wants per-action control. { Call: CEL separates
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

   ### Interaction with the unresolved execution-time gaps

   This resolution does not resolve gaps 1-2. Conditions make gap 1
   concrete (condition-failure vs. action-failure semantics, J3) and
   CEL's read-only result access makes gap 2's keying derivation
   readable (an idempotency key expression can read event fields) -- both
   flagged as the gaps' first real exercise, not solved here.

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
   boundary, blocked on the execution-time gaps).
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
