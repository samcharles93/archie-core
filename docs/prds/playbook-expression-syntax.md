# Playbook data-flow and condition syntax

**Status:** Approved
**Date:** 2026-09-03
**Parent:** `docs/prds/eda-playbook-engine.md`, epic `archie-core-t2db`
**Beads issue:** `archie-core-t2db.14` (expression environment).
Unbuilt decisions: `archie-core-t2db.25` (J1 action ids),
`archie-core-t2db.26` (J2 args as CEL),
`archie-core-t2db.27` (Result schemas declared to CEL).

**Decision:** CEL via `cel.dev/cel-go` (v0.32.0), pinned. One expression
mechanism covers both an action's `when` condition and its `args` values.

## The requirement, stated directly

A playbook action is gated and parametrised by runtime data: (a) a condition
deciding whether the action runs; (b) a later action's `args` values referencing
an earlier action's `Result` and the triggering event's fields. Both are **CEL
expressions**, evaluated against one evaluation context. There is no separate
interpolation syntax: CEL reads nested maps and fields natively, so `args`
values that need data are written as CEL, and only plain literal args remain
plain YAML scalars.

Both halves are read-only: expressions may read context values and compute a
result from them, never mutate, never call host functions, never perform I/O.
CEL enforces this structurally (no side effects, linear evaluation when macros
are bounded, cost-limited).

```yaml
actions:
  - id: build
    position: module
    kind: log
    args:
      message: '"build finished"' # literal
    when: 'event.label == "bugfix"' # gate on event field
  - position: module
    kind: log
    args:
      message: '"done: " + string(actions.build.result.written)'
    when: "actions.build.result.written == true"
```

## Trust boundary of the expression

A playbook YAML is **operator-installed, in-process, daemon-privileged** -- the
same tier as `ModuleDir`/`PluginDir`/`SecretEngineDir` (module-position.md's
trust-boundary section): loaded from a configured directory at startup, never
from a webhook body or task worktree. The schema-by-example flow uses live
events to _design against_; the saved playbook is operator-authored file content
in an operator-configured location.

Two consequences:

1. The expression string is **trusted**. Operator-authored config, not attacker
   input; no sandboxing against a hostile expression string is required. If a
   future playbook source is less trusted (auto-generated bindings from
   unauthenticated captures), that is the playbook-file trust tier moving -- the
   loader's acceptance check is the right place, not the evaluator.
2. The **data the expression reads is NOT trusted** (decoded webhook body, prior
   results through the same pipeline). Robustness against hostile data is
   required: no panic on wrong-typed/missing fields, no unbounded cost on
   deep/cyclic maps, bounded evaluation time. CEL provides this structurally
   (cost limits, linear non-Turing-complete evaluation, error values instead of
   panics -- verified in the vetting record below).

## The gate precedent is deliberately NOT copied

`internal/gate/gateeval` evaluates arbitrary interpreted Go (`.archie/gate.go`)
as the repo's conditional-logic precedent. That is the wrong precedent for
playbook conditions because this document's premise is: **YAML is pure
orchestration data; Yaegi is where imperative logic lives** (Problem section;
module-position.md's "no generic hook" section). Yaegi snippets in YAML would
move imperative logic into the data layer -- the inversion this design exists to
prevent -- and make playbooks un-lintable.

The line: conditions are **declarative predicates over runtime data, not
programs**. CEL is non-Turing-complete and side-effect-free, so it cannot become
a program; a condition that needs real logic gets a Module kind (Yaegi),
referenced by the action, not a stronger expression body.

## Plugin-engine-rule interaction

The plugin engine rule forbids a generic `Module` interface with an `any`
payload; it exists to keep static implementation contracts typed. Evaluating CEL
against runtime `map[string]any` is **not the same problem**: the rule concerns
_implementation contracts_ (static operations a capability family exposes), not
_runtime data values_ (legitimately dynamic -- an event payload's shape is
unknown until it arrives). The Module contract stays fully typed (generated
`Args`/`Result` schemas); CEL is a read-only data reader under the playbook
engine family, not a new capability contract. Stated once.

## Minimum viable evaluation context (what expressions see)

One flat context, namespaced from the playbook run, built at dispatch time:

| Name      | Type                     | Source                                                                                                                                                                                                                                                                                                |
| --------- | ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `event`   | `map(string, dyn)`       | The triggering event's decoded payload (webhook body / forge issue / schedule tick). Field access via `event.<field>`, map access via `event["field"]`, presence via `has(event.<field>)`.                                                                                                            |
| `actions` | per-playbook object type | Previous actions' results, one field per declared action `id` (`actions.<id>`), each field typed `{ result: <KindResult> }` so a later action reads an earlier one's `Result` struct (`actions.<id>.result.<field>`) with field-level checking. The current action and later actions are not present. |

Expressions may also read literal-only state (numbers, strings, booleans)
directly. CEL's `has()` macro covers the missing-key case (`has(event.labels)`
-> bool), which replaces any bespoke `has`/ `contains` operator.

`actions.<id>.result` is the kind's hand-written `Result` struct:
`ModuleRegistry.DecodeResult` converts `Module.Invoke`'s flat `map[string]any`
(`archie-core-t2db.13`) back into that struct before a later expression reads
it, so the checker and the run read the same typed value.

Judgement call J1: `action` id is a required field on every action that later
actions reference; ids are validated at load (unique, stable-identifier-shaped),
and a `when`/`args` expression referencing an unknown `actions.<id>` is a **load
failure**, not a runtime miss -- the same reject-at-load rule as collisions (see
below).

## Load-time validation (consistent with the collision rule)

The collision-handling rule is: schema defines the accepted message; anything
outside it is rejected at load, logged plainly, reported to the caller -- never
deferred to runtime. Expressions are validated the same way, at playbook load:

- **Syntax**: every `when` and every CEL-typed `args` value is parsed and
  checked by CEL's checker when the playbook is loaded. A syntax or type error
  is a reported load failure (the playbook is dropped and the error goes to the
  caller), not a runtime surprise.
- **Context conformance**: with the context declared to CEL (`event` as
  `map(string, dyn)`, `actions` as a per-playbook object type with one typed
  field per declared id), unknown top-level identifiers are rejected at compile
  time. Field-level typing inside `dyn` values is deferred to runtime by CEL's
  design (verified: `data.items.lenght` with `data` dyn compiles clean and
  errors at eval) -- so the loader declares as much type as the schema provides
  (the action kinds' generated `Result` structs become CEL type declarations at
  load, giving field-level rejection for result reads), and `event` stays `dyn`
  because its shape is unknown by design (schema-by-example).
- **Names**: unknown `actions.<id>` references and unknown roots are
  compile-time/lint-visible errors (J1).

This means the lint tool and the daemon share one checker -- same single-source
argument as the playbook loaders.

## Candidate comparison

| Candidate                | Maintenance                                        | Safety surface                                                                                                                | Cost (measured)                 | Verdict                                                                                                      |
| ------------------------ | -------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- | ------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| **CEL (cel.dev/cel-go)** | Actively maintained (google/cel, monthly releases) | Non-Turing-complete, linear eval, `CostLimit`/`CostTracking`/`ParserRecursionLimit`, no side effects, error values not panics | **+13.0MB** (2.34MB -> 15.25MB) | **Chosen**                                                                                                   |
| `expr-lang/expr` v1.17.8 | Actively maintained                                | Side-effect-free, `DisableAllBuiltins`, depth limits (CVE fixed w/ tests)                                                     | +3.9MB (2.34MB -> 6.28MB)       | Not chosen: two safety CVEs this cycle; smaller ecosystem; no protobuf/type-model fit with future schema-gen |
| `gval` v1.2.3            | Maintained, slower                                 | No builtin-restriction surface; extras (ternary/`??`) not wanted                                                              | +3.0MB (2 transitive deps)      | Not chosen                                                                                                   |
| `govaluate`              | **ARCHIVED** (author archived 2024)                | n/a                                                                                                                           | n/a                             | Not chosen: dead                                                                                             |
| `starlark-go`            | Maintained (Bazel)                                 | Turing-complete with step limits                                                                                              | large (full language)           | Not chosen: whole-language surface                                                                           |

## Vetting record

Source reviewed at HEAD:

- **Repo hygiene**: clean. No binaries/media in tree (the only non-source
  artifacts are expected generated protobuf files); 16-line `.gitignore`.
- **Hard-boundary correctness**: the place correctness is hard for an expression
  evaluator is bounding evaluation and surviving hostile input. Dedicated
  regression tests exist and pass at HEAD: `TestCostLimit`, `TestCostTracking*`,
  `TestParserRecursionLimit`, and a large conformance suite. Verified by
  execution: evaluation against a 2000-deep nested map under `CostLimit`
  completes in ~10 microseconds and returns an error value for a missing key --
  no panic, no unbounded walk.
- **Dependency footprint**: antlr (parser), protobuf + genproto (CEL's type
  model), `cel.dev/expr` (CEL spec types), yaml.v3. All mainstream, canonical,
  vendored widely by Google-adjacent projects. No runtime-impure third-party
  code; the REPL-only deps (readline, tview, tcell) are not in the library
  import graph.
- **Security record**: no advisories on record for `cel.dev/cel-go` (GitHub
  Advisory Database).
- **Module path**: migrated to `cel.dev/cel-go` (v0.32.0 is current); the old
  `cel.dev/cel-go/cel` path is the pre-migration identity. Pin `cel.dev/cel-go`.
- **Binary size cost**: +13.0MB is the largest of the candidates (measured:
  baseline 2.34MB -> 15.25MB with CEL). Accepted because: the daemon already
  embeds a large Yaegi runtime; CEL's type model is the load-bearing win
  (compile-time rejection of result-path typos, matching the reject-at-load
  rule); and the alternative bespoke grammar would re-implement CEL's checker to
  get the same guarantee. Flagged for sign-off: if 13MB is unacceptable for a
  specific deployment target, the fallback is a bespoke grammar with a
  re-implemented subset of this checker -- a strictly worse trade.

## Why bespoke was not sufficient

A bespoke grammar for the shipped cases (path read + comparison + bool
composition) is small, but the load-time-validation requirement makes the
comparison: the reject-at-load rule requires _field-level_ static checking of
result paths against generated `Result` schemas, which is CEL's type-checker's
job. Re-implementing that on a hand-rolled grammar means re-implementing a type
system -- more code than the grammar itself, with worse diagnostics, no
conformance suite, and no community surface for future expression needs (even
declarative ones). The bespoke option only wins on binary size, and the size
cost of CEL is measured and bounded.

## Judgement calls (flagged for sign-off)

- **J1 -- `id` required on referenced actions, unknown id = load failure.**
  Matches the reject-at-load rule. Alternative considered: an alternative is
  positional `actions[0]` access, which breaks on reorder; named is the
  operator-friendlier shape.
- **J2 -- every `args` value is evaluated as a CEL expression.** No split
  between "literal args" and "expression args": a plain string value is a CEL
  string literal, a number is a CEL number literal, and any value that reads
  context data is written in CEL. One mechanism, no interpolation marker, no
  second syntax to learn. Alternative considered: cEL string literals must be
  quoted inside YAML (e.g. `message: '"build finished"'`), which is slightly
  noisy for mostly-literal args; accepted for uniformity -- the linter catches
  quoting mistakes at load.
- **J3 -- condition failure semantics.** A condition that errors at runtime
  (missing/wrong-typed field) evaluates to **false**: the action is skipped,
  logged, run continues. This is the simplest candidate consistent with gap 1's
  bias against clever recovery; gap 1 (`archie-core-t2db.18`) commits
  stop-on-first-failure, so per-action control is not coming and J3 stands.
  Alternative considered: cEL separates false from error; we treat evaluation
  error as false + log.
- **J4 -- `event` stays `dyn` (schema-by-example).** Load-time we cannot know
  event field types; declaring them typed would reject valid plays. Result reads
  get field-level checking via generated schema declarations. Alternative
  considered: when schema-by-example ships, tighten event field declarations
  from captured samples.
- **J5 -- cost limit default.** A `CostLimit` at load time (e.g. 100,000) is
  applied to every expression; the linter and daemon use the same value so they
  agree. Alternative considered: exact number is a constant to tune at first
  use.

## Interaction with the execution-time gaps

This resolution does not resolve the execution-time gaps (both are resolved
separately: gap 1 by `archie-core-t2db.18`, gap 2 by the gap-2 decision).
Conditions make gap 1 concrete (condition-failure vs. action-failure semantics,
J3) and CEL's read-only result access makes gap 2's keying derivation readable
(an idempotency key expression can read event fields) -- both flagged as the
gaps' first real exercise. Gap 2's exercise is subsequently solved by that
decision: the answer is a fixed structural derivation, and idempotency keys are
deliberately NOT CEL expressions (see the "No CEL in the key" passage).

## Implementation placement (for the follow-up slice)

- New `internal/domain/eda/expr` package: builds the CEL environment (context
  declarations, generated `Result` schema types as CEL type declarations, cost
  limit), compiles/validates playbook expressions at load, evaluates them at
  dispatch. Domain layer; no infrastructure imports; owned by the playbook
  engine family. One new dependency: `cel.dev/cel-go` (pinned), `go.mod`
  otherwise unchanged.
- Consumption: the playbook event coordinator (open question 4's loader)
  evaluates `when` before dispatch and evaluates `args` values before
  `Module.Invoke`; the lint tool (`archie-core-t2db.12`) shares the same
  compile/validate path for author-time diagnostics.
- Schema integration: action kinds' generated `Result`/`Args` structs
  (`archie-core-t2db.13`'s `logextract` pattern) are declared to CEL at load so
  `actions.<id>.result.<field>` type-checks; `args` expressions type-check
  against the target kind's `Args` schema before the playbook is accepted.
