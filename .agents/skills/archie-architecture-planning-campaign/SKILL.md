---
name: archie-architecture-planning-campaign
description: Run a decision-gated architecture campaign for archie-core before implementing a cross-package feature, migration, new domain, new process boundary, configuration redesign, duplicated-path cleanup, or other change whose ownership and change surface are unclear. Use when an engineer would otherwise need to trace much of the repository to discover what a feature touches, when TDD can prove behavior but not maintainability, or when planning must distinguish the live implementation from the approved target architecture.
---

# Archie architecture planning campaign

Prevent another last-minute migration. Produce an implementation-ready
architecture handoff in which every behavior, state transition, configuration
value, boundary, compatibility path, and deletion has an explicit owner and
testable disposition. Optimize for a cohesive change that a zero-context
engineer can locate, explain, extend, and remove without tracing unrelated code,
not the first green test.

## Load the required skill chain

Load these sibling skills before starting. For smaller-context sessions,
load only the first two initially, then load the rest at their relevant
phase:

1. `archie-codebase-discovery` — investigate the complete live change cone.
2. `archie-architecture-contract` — apply Archie's load-bearing invariants.
3. `archie-research-methodology` — state hypotheses and measurable predictions
   (needed by Phase 5).
4. `archie-technical-accountability` — record evidence, decisions, debt, and
   ownership without hiding uncertainty (needed by Phase 6+).
5. `archie-change-control` — classify and gate any resulting source or runtime
   change (needed by Phase 11).

Also load `archie-domain-reference` at Phase 3 (vocabulary and ownership) and
`archie-docs-and-writing` plus its template at Phase 10 (ADR/PRD handoff).

If a sibling is unavailable, stop at the current read-only gate and report the
missing control. Do not reconstruct its doctrine from memory.

## Know when not to use this campaign

- For a localized defect whose owner, state, consumers, and boundary are already proven, use `archie-change-control` and the repository TDD workflow.
- To explore an unfamiliar package, use `archie-codebase-discovery`.
- To review an already-written patch, use `archie-technical-accountability` plus the applicable review and validation skills.
- To justify a preferred rewrite, use `archie-research-methodology`; this campaign may conclude that no architecture change is warranted.

## Use the ground-truth model

Keep two ledgers. Never blend them.

| Ledger | Authority | Meaning |
|---|---|---|
| Current implementation | Live production wiring, Go code, persistence, tests, deployment definitions, and observed behavior | What Archie does now, including duplication and defects |
| Approved target | `docs/prds/01-project-management.md` and the status-qualified decisions in `docs/prds/architecture/*.md` | What Archie has approved or explicitly left open |

Treat `ARCHITECTURE.md` as a useful earlier architecture description, not proof
that current wiring still matches it. Treat a decision marked “in progress,”
“deferred,” or “under design” as a constraint plus open questions, not a
finished design.

As of 2026-07-28, the approved target is domain-oriented, while much of the live
tree remains in technical packages such as `internal/config`, `internal/daemon`,
`internal/store`, and `internal/workflow`. Re-run the provenance commands before
relying on that volatile fact.

## Define the terms once

- **Area under review**: one cohesive application responsibility, not an existing directory name.
- **Owner**: the domain or capability with authority over behavior, vocabulary, state, settings, commands, events, and consequences.
- **Change cone**: every entry point, caller, consumer, state record, adapter, test, configuration input, process boundary, and document affected by the decision.
- **Domain contract**: the smallest behavior required by the owning domain; infrastructure implements it.
- **Compatibility path**: a temporary adapter, dual read, dual write, or translation used only to preserve behavior during cutover.
- **Authoritative path**: the sole path allowed to decide or mutate a behavior after cutover.
- **Deletion criterion**: observable evidence that makes removal safe; “after migration” is not a criterion.
- **Architecture packet**: the complete evidence and decision handoff produced by this campaign.

## Establish dynamic expected numbers

Do not invent repository counts. First verify the repository root, then
capture baselines and use them as expected numbers:

```bash
git rev-parse --show-toplevel >/dev/null || { echo 'run from repo root' >&2; exit 1; }
test -f go.mod && test -f Taskfile.yml && test -f CLAUDE.md || {
  echo 'go.mod, Taskfile.yml, or CLAUDE.md missing — wrong directory?' >&2
  exit 1
}
git rev-parse HEAD
git status --short
go list -f '{{.ImportPath}}' ./internal/... | sort | tee /tmp/archie-architecture-packages.txt
wc -l /tmp/archie-architecture-packages.txt
find docs/prds/architecture -maxdepth 1 -type f -name '*.md' -print | sort | tee /tmp/archie-architecture-docs.txt
wc -l /tmp/archie-architecture-docs.txt
```

Name the resulting counts:

- `P` = current internal package rows in scope; for the whole migration, use every row in `/tmp/archie-architecture-packages.txt`.
- `D` = architecture decision documents.
- `F(area)` = production and test Go files in the declared change cone.
- `R(symbol)` = typed references returned for a symbol by gopls.
- `C` = decoded configuration fields and behavior-affecting literals in scope.
- `L` = legacy or competing paths in scope.

Record the commit and dirty-file list beside every baseline. If concurrent work changes either, refresh the affected baseline; never mix measurements from two tree states.

## Phase 0 — Declare one decision scope

Write one sentence in this form:

> Decide who owns `<behavior>`, how `<input>` reaches `<outcome>`, and how the current paths migrate without changing `<preserved invariants>`.

List explicit non-goals and affected operators/users. Classify the work with `archie-change-control`, but authorize no implementation yet.

**Gate 0 — scope**

- Expected declared architecture questions: `1`.
- Expected unexplained dirty files in the change cone: `0`.
- Expected implementation edits during the campaign: `0`.
- Expected non-goal list: at least `1` item.

If more than one independent architecture question remains, split campaigns. If a dirty file has unknown ownership, pause until its author or intent is known.

## Phase 1 — Build the decision corpus

Read the index and every focused architecture decision before designing:

```bash
sed -n '1,240p' docs/prds/01-project-management.md
while IFS= read -r ARCHIE_DOC; do sed -n '1,520p' "$ARCHIE_DOC"; done < /tmp/archie-architecture-docs.txt
sed -n '1,360p' ARCHITECTURE.md
sed -n '1,240p' CLAUDE.md
```

Build a current-versus-target ledger with columns:

| Claim | Current evidence | Target decision | Status | Conflict or open decision |
|---|---|---|---|---|

Give special attention to:

- `package-review.md` for the required investigation and decision set;
- `migration-decisions.md` for unresolved destinations and cutover;
- `organisation.md` and `dependencies-and-contracts.md` for ownership and dependency direction;
- `configuration.md` for the required dissolution of `internal/config`;
- `safe-change-and-recovery.md` for candidate promotion and rollback;
- identity, agent, messaging, workflow, policy, plugin, and generated-doc documents whenever their vocabulary or contracts enter the change cone.

**Gate 1 — corpus**

- Expected decision-document review rows: `D`.
- Expected documents with omitted status: `0`.
- Expected target claims presented as current behavior: `0`.
- Expected contradictions without an evidence link and disposition: `0`.

If live code contradicts an approved target, record migration work; do not silently redefine the target. If approved documents conflict, stop and request a decision instead of choosing the easier implementation.

## Phase 2 — Inventory the live change cone with syntax-aware tools

Do not start from package names. Start at production entry points and behavior.

```bash
rg -n '^func main\\(' cmd
rg -n 'flag\\.(String|Bool|Int|Int64|Duration|Var)' cmd --glob '*.go'
rg --files cmd internal | sort
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-architecture-inventory-cache go run .claude/skills/archie-architecture-planning-campaign/scripts/go-inventory.go -root . -scope internal/<area> -include-tests=true
```

Replace `<area>` with a real repository-relative directory. Run the inventory once per package in the change cone. It emits Go-parser-derived declarations as tab-separated columns:

| Column | Kind values | Meaning |
|---|---|---|
| `KIND` | `IMPORT`, `TYPE`, `FUNC`, `METHOD`, `VAR`, `CONST` | Go AST declaration kind |
| `PACKAGE` | Directory path relative to `-root` | Package directory containing the declaration |
| `FILE` | Filename within the package | Source file |
| `LINE` | Positive integer | Declaration line number |
| `NAME` | Identifier string | Exported or unexported name |
| `DETAIL` | Short type/kind string | Receiver type for `METHOD`, underlying type for `TYPE`, signature summary for `FUNC` |

Empty output means no matching Go packages were found under `-scope`.
Lines with empty `NAME` after the tab are parse-level artifacts; treat them
as noise. The inventory does not infer runtime use, cross-package references,
or dynamic registration.

Use gopls after the inventory:

- call `go_workspace` to confirm the module view;
- call `go_package_api` for every package exposing a boundary;
- call `go_search` for each domain term and competing synonym;
- call `go_symbol_references` for every constructor, interface, state type, command, event, setting, and persistence method in scope.

If gopls is unavailable, use `go doc <package>`, `go list -json <package>`, and literal `rg` as a fallback. Label text-search counts approximate; do not claim typed coverage.

Count files before reading:

```bash
rg --files internal/<area> -g '*.go' | sort | tee /tmp/archie-architecture-area-files.txt
wc -l /tmp/archie-architecture-area-files.txt
```

**Gate 2 — inventory**

- Expected files read: `F(area)`, the baseline line count.
- Expected boundary packages with API summaries: all boundary packages; omitted count `0`.
- Expected typed references traced for each selected symbol: `R(symbol)`; unexplained references `0`.
- Expected discoverable facts deferred as user questions: `0`.

If gopls and text search disagree, inspect generated files, build tags, interfaces, reflection, Yaegi loading, and registration side effects. Do not infer “unused” from zero textual callers.

## Phase 3 — Establish vocabulary and ownership

Extract verbs, nouns, records, transitions, commands, events, settings, and failure outcomes from live behavior. Resolve overloaded terms rather than globally renaming `task`, `session`, `message`, `execution`, `identity`, or `plugin`.

Create this table:

| Term or behavior | Plain-language definition | Current locations | One owner | Competing meaning | Target contract |
|---|---|---|---|---|---|

Use these ownership tests:

1. Name the cohesive job.
2. Name the mutable records and transitions it governs.
3. Name the policy consequences it decides.
4. Name only the external services it requires.
5. Explain why this code changes together.

Reject ceremonial `entities`, `services`, `helpers`, and `common` layers. A single-operation wrapper needs a boundary reason, a second genuine consumer, or removal/inlining.

For NellDB-related areas, inspect `internal/nell`, every consumer, persistence tests, and the pinned module before assigning semantics:

```bash
go list -m -f '{{.Path}} {{.Version}}' github.com/samcharles93/NellDB
rg -n 'nell\\.|NellDB|OpenStore|NewSessionStore' cmd internal --glob '*.go'
```

Do not make NellDB records domain models merely because they persist current state.

**Gate 3 — vocabulary**

- Expected behaviors or records without one owner: `0`.
- Expected unqualified terms with multiple meanings: `0`.
- Expected domain owners chosen because of an existing directory name: `0`.
- Expected boundaryless single-operation indirection: `0`, unless listed for deletion or justified by a tested contract.

If one candidate owner must know another domain's internal state, redesign the handoff as an owned command, event, or narrow shared contract.

## Phase 4 — Trace consumers, state, and failure semantics

Trace each behavior from input to observable outcome:

| Input/source | Entry point | Command/event | State mutation | Persistence | Side effect | Failure/retry | Output |
|---|---|---|---|---|---|---|---|

For every mutable record, produce a transition table:

| From | Command | Preconditions/version | To | Atomic event | Invalid result | Retry/idempotency |
|---|---|---|---|---|---|---|

Inspect production construction in `cmd/archied/main.go` and `cmd/archie-agent/main.go`; constructors alone do not prove production use. Trace RPC, NATS, container, in-process, and subprocess paths separately. Trace tests after production; a mirrored fake can validate the same wrong assumption.

**Gate 4 — runtime trace**

- Expected production consumers reconciled per symbol: `R(symbol)`.
- Expected mutable records without a complete transition table: `0`.
- Expected state mutations without an identified transaction boundary: `0`.
- Expected side effects without failure, timeout, retry, and idempotency evidence: `0`.
- Expected competing authoritative write paths: `0`, or exactly `L` rows with cutover and deletion criteria.

If two paths create the same outcome, do not add a third abstraction over both. Decide authority and migration direction first.

## Phase 5 — Compare solutions and predict change locality

Use `archie-research-methodology`. Compare at least three explicit options:

| Rank | Approach | Normal use | Fence |
|---|---|---|---|
| 1 candidate default | Domain-first modular monolith with owner-defined contracts and composition in `internal/app` | Behavior and ownership are understood | Do not retain old technical ownership merely to reduce moves |
| 2 migration tool | Strangler cutover through the narrowest compatibility adapter | Data or live callers require staged migration | Require owner, expiry trigger, metrics, rollback, and deletion test |
| 3 conditional | Retain a process boundary or extract a service | Measured security isolation, failure containment, scaling, or independent-operation need exists | Future possibility is not evidence |
| rejected | Rename a generic package, pass global config, add a service locator, or keep parallel implementations indefinitely | Never | Violates approved architecture |

Score each legitimate option on cohesion, dependency direction, state authority, contract size, configuration ownership, failure containment, migration risk, reversibility, deletion cost, and testability.

Before choosing, rehearse at least three representative future changes:

1. add a small behavior to the area;
2. replace an infrastructure implementation;
3. change one owned setting or policy.

Predict the packages, contracts, and tests each would touch. Prefer obvious ownership and exclusion of unrelated packages over the smallest immediate diff.

**Gate 5 — option decision**

- Expected compared options: at least `3`, including the current structure.
- Expected selected option: `1`.
- Expected approved-invariant violations in the winner: `0`.
- Expected representative change rehearsals: at least `3`.
- Expected unexplained unrelated packages per rehearsal: `0`.
- Expected predictions without a measurement or later verification command: `0`.

If no option clears every invariant, leave the decision open. Do not downgrade a MUST to get a winner.

## Phase 6 — Complete the package destination matrix

For the architecture migration, create one row for every package in `P`. For a bounded change, cover the measured change cone and prove why other packages are outside it.

| Current package/behavior | Owner | Target path | Disposition | Dependencies to remove | State/contracts to preserve | Prerequisites | Deletion criterion | Decision status |
|---|---|---|---|---|---|---|---|---|

Use only these dispositions: move, merge with named replacement, infrastructure adapter, application composition, approved shared contract, plugin/extra/deployment/example/generated artifact, or delete after proof.

**Gate 6 — destinations**

- Expected destination rows: `P` for the whole migration, otherwise every package in the recorded change cone.
- Expected blank owner, disposition, or status cells: `0`.
- Expected confirmed rows that still require an implementer to invent domain semantics: `0`.
- Expected compatibility rows without deletion criteria: `0`.

An open row blocks that row, not an independent confirmed slice.

## Phase 7 — Dissolve configuration by ownership

Inventory both decoded fields and behavior-affecting literals:

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-architecture-inventory-cache go run .claude/skills/archie-architecture-planning-campaign/scripts/go-inventory.go -root . -scope internal/config -include-tests=false
rg -n '\\bconfig\\.' cmd internal --glob '*.go'
rg -n 'time\\.(Second|Minute|Hour)|time\\.Duration|http\\.DefaultClient|0\\.0\\.0\\.0|localhost|/usr/share|/data/' cmd internal --glob '*.go'
```

For each value, record external input, default/provenance, typed owner, validation, mutability, consumers, secrets, and migration. Keep the decoded document private to configuration infrastructure; never pass complete app configuration into a domain.

Classify every literal as invariant, owned setting/default, policy value, external constraint, or defect. One current value does not make an invariant.

**Gate 7 — configuration**

- Expected inventoried configuration items: `C`.
- Expected items without exactly one runtime owner and classification: `0`.
- Expected domains importing configuration infrastructure or full app config:
  `0` in the target.
- Expected live-setting mutation paths omitted from the migration ledger: `0`.
- Expected plugin settings added to an unrelated global model: `0`.

If an item cannot reload safely, mark it restart-required and design candidate
validation, health observation, last-known-good preservation, and rollback.

## Phase 8 — Justify process and trust boundaries

Inventory current boundaries:

```bash
rg -n 'Subprocess|NATS|nats\\.|RPC|rpc|container|Docker|Yaegi|plugin' cmd/archied cmd/archie-agent internal/{agentexec,container,nats,natsrpc,taskrun,forgerpc,storerpc,worktreerpc,plugin,yaegiutil} --glob '*.go'
```

For each retained boundary, record purpose, trusted principal, secrets and
resources exposed, protocol owner/version, failure containment, health,
startup/shutdown order, retries, and operator burden.

Keep source ownership independent of deployment. Treat a subprocess as
transport, not a security sandbox. Keep model-owned work away from git
credentials and daemon authority. Give plugins narrow typed registrars, never
daemon access or generic hooks.

**Gate 8 — boundaries**

- Expected retained boundaries with one concrete justification: all retained
  boundaries; unjustified count `0`.
- Expected external or cross-process contracts without owner/version: `0`.
- Expected boundaries described as isolation without an enforced isolation
  mechanism: `0`.
- Expected domains split only for possible future deployment: `0`.

If removal of a boundary changes confidentiality, integrity, availability, or
operations, return to Phase 5 with those constraints.

## Phase 9 — Plan deprecation, cutover, rollback, and deletion

Mine compatibility and prior replacement attempts:

```bash
rg -n 'legacy|compat|deprecated|deprecat|TODO|FIXME|HACK|remove|delete' cmd internal docs --glob '*.go' --glob '*.md'
git log --oneline --decorate --all -- cmd internal docs/prds/architecture
git log -S'internal/config' --oneline --all
```

For each legacy path, record predecessor, successor, parity evidence, data
backfill, dual-read/write need, production switch, rollback trigger, observation
window, deletion criterion, and regression test.

Require the production composition test to prove the successor is selected.
Require a negative test or architecture check to prevent reintroduction after
deletion. Time-box transitional duplication by an observable trigger, not a
calendar promise alone.

**Gate 9 — cutover**

- Expected legacy paths inventoried: `L`.
- Expected successor authorities per legacy behavior: exactly `1`.
- Expected compatibility paths without owner, rollback, and deletion proof:
  `0`.
- Expected permanent duplicate authoritative paths after cutover: `0`.
- Expected old states/records without migration or explicit safe abandonment:
  `0`.

If parity cannot be measured, do not switch production wiring. Design the
observable first.

## Phase 10 — Produce the ADR/PRD handoff

Write or propose the focused architecture document only where change control
authorizes it. Include:

1. scope and non-goals;
2. current-state trace with commit and dynamic baselines;
3. vocabulary and ownership;
4. current-versus-target ledger;
5. considered options, predicted numbers, and rejection reasons;
6. chosen owner, contracts, state machine, settings, policy, and boundaries;
7. destination matrix;
8. migration, compatibility, cutover, rollback, and deletion;
9. implementation slices and dependencies;
10. verification commands, expected evidence, unknowns, and named decisions
    still requiring the user.

Ask the user only questions whose answers materially change behavior or
structure and cannot be discovered in the repository.

**Gate 10 — handoff**

- Expected discoverable facts presented as questions: `0`.
- Expected undecided semantics delegated to implementers in a confirmed slice:
  `0`.
- Expected claims without source evidence or explicit “candidate/open” status:
  `0`.
- Expected unresolved normative contradictions: `0`.

If a decision remains open, isolate it from executable slices and name its
owner. Never disguise it as an implementation detail.

## Phase 11 — Slice for short, artful successes

Route every slice through `archie-change-control`. Keep TDD and fresh
adversarial review, but add architecture acceptance before code acceptance.

Each slice must:

- deliver one observable behavior or migration proof;
- have one domain owner and a bounded change cone;
- establish or consume the final contract rather than create another temporary
  model;
- preserve a working application at its boundary;
- remove a predecessor, or advance its recorded deletion criterion;
- add deterministic conformance protection where mechanically possible;
- state what becomes easier to change after the slice.

Order slices as: characterization and observability, final contract, adapter or
data migration, production cutover, legacy deletion, architecture regression
guard. Do not create all target folders as empty scaffolding.

**Gate 11 — executable plan**

- Expected observable outcome per slice: `1`.
- Expected owner per slice: `1`.
- Expected unrelated domains modified per slice: `0`.
- Expected predecessors neither removed nor advanced toward deletion: `0`.
- Expected implementation slices bypassing change control: `0`.

If a slice requires broad service-locator access or the full configuration
document, return to ownership and contract design.

## Phase 12 — Re-measure after every slice

Run the repository gate selected by `archie-change-control`, then compare the
slice against this packet. At minimum, re-run package inventory, typed
references, production construction tracing, configuration inventory, and
legacy-path search.

Use a fresh adversarial reviewer to inspect every changed non-test file for
cohesion, hidden dead paths, duplicated authority, unchecked errors,
hard-coded behavior, interface leakage, nil/race/goroutine risk, and production
wiring. A green TDD suite proves required behavior; it does not prove the code
is maintainable.

**Gate 12 — conformance**

- Expected unexplained package/reference/configuration deltas from baseline:
  `0`.
- Expected new authoritative paths beyond the selected design: `0`.
- Expected surviving reviewer findings: `0`.
- Expected destination/cutover rows left stale by the slice: `0`.
- Expected representative change rehearsals made harder without an accepted
  rationale: `0`.

If the slice passes tests but fails conformance, reject or redesign it. “Make it
work and move on” is not completion.

## Fence off settled wrong paths

- Do not code before mapping ownership, consumers, state, and cutover.
- Do not equate moving or renaming packages with architecture.
- Do not centralize unrelated runtime settings in a renamed global config.
- Do not add abstractions solely to wrap one operation or hide duplication.
- Do not preserve old and new models indefinitely “for compatibility.”
- Do not expose domain meaning through broker, database, SDK, or DTO types.
- Do not use process splits as source boundaries or subprocesses as sandboxes.
- Do not add capability methods to the generic plugin contract.
- Do not ask the user to rediscover code facts the tools can establish.
- Do not accept linter silence as dead-code evidence; inspect registration,
  reflection, build tags, generated code, and production composition.
- Do not let a mirrored test fake become the sole proof of an external or
  cross-process contract.
- Do not permit implementation difficulty to waive an approved invariant.

## Provenance and maintenance

Grounded on 2026-07-28 in the approved architecture index and every file under
`docs/prds/architecture/`, plus live `cmd`, `internal`, tests, `Taskfile.yml`,
and the user's quality requirements. Re-verify volatile facts before each use.

```bash
sed -n '1,240p' docs/prds/01-project-management.md
for ARCHIE_DOC in docs/prds/architecture/*.md; do sed -n '1,8p' "$ARCHIE_DOC"; done
go list -f '{{.ImportPath}}' ./internal/... | sort
rg -n '^func main\\(' cmd
rg -n '\\bconfig\\.' cmd internal --glob '*.go'
rg -n 'legacy|compat|deprecated|TODO|FIXME|HACK' cmd internal docs --glob '*.go' --glob '*.md'
git log --oneline --decorate --all -- cmd internal docs/prds/architecture
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-architecture-inventory-cache go run .claude/skills/archie-architecture-planning-campaign/scripts/go-inventory.go -root . -scope internal/config -include-tests=false
task --list
```
