---
name: archie-research-methodology
description: Turn an Archie architecture, maintainability, performance, persistence, configuration, protocol, concurrency, or migration hunch into reproducible evidence and an accountable decision. Use before advocating a redesign, accepting a surprising test result, claiming code is duplicate or dead, comparing execution modes or stores, measuring NellDB scans, validating an external protocol, or promoting a candidate fix; also use when a green TDD result does not prove production wiring or design quality.
---

# Research Archie with falsifiable evidence

Use research to expose a decision, not to decorate a preferred answer. Ask one
vertical question, predict an observable result before measuring, preserve
failures and negative results, and hand any resulting change to Archie's change
control. Optimize for a small meaningful answer that improves the architecture
ledger.

All volatile repository observations in this skill were verified on
**2026-07-28**. Re-run the provenance commands before treating them as current.

## Route the work

| Need | Load instead or alongside |
|---|---|
| Trace entry points, types, callers, registrations, and complete change cones | `archie-codebase-discovery` |
| Collect repeatable counts, coverage, timings, profiles, or runtime diagnostics | `archie-diagnostics-and-tooling` |
| Decide boundaries, ownership, target packages, and migration slices | `archie-architecture-planning-campaign` |
| Reconstruct an earlier failure, rejected fix, revert, or branch-only candidate | `archie-failure-archaeology` |
| Decide test sufficiency and acceptance thresholds | `archie-validation-and-qa` |
| Classify, authorize, review, or accept a change | `archie-change-control` |
| Assign ownership, supersession, deletion criteria, and maintainability debt | `archie-technical-accountability` |

Do **not** use this skill to implement the hypothesized fix, to operate
production, or to replace NellDB domain knowledge. Use `archie-nelldb` for the
database model and `archie-run-and-operate` for live operations. Stop the
research pass before mutation.

## Define the terms once

- **Question**: one uncertainty whose answer changes a design or acceptance
  decision.
- **Hypothesis**: a falsifiable explanation, written before the experiment.
- **Prediction**: a number, relation, state, byte sequence, error, or call path
  that must be observed if the hypothesis is correct.
- **Falsifier**: an observation that rejects the hypothesis. “The test fails”
  is too vague; name the failure that matters.
- **Baseline**: the unchanged tree, environment, mode, data, and measurements
  against which a treatment is compared.
- **Treatment**: the one intentional difference between baseline and
  comparison.
- **Control**: an unchanged case that detects a broken fixture or measurement.
- **Confounder**: another difference that could explain the result.
- **Replication**: a repeat from the same frozen inputs.
- **Triangulation**: evidence from a different mechanism that does not share
  the first mechanism's assumptions or helpers.
- **Decision gate**: a predeclared rule that maps observed evidence to proceed,
  reject, redesign, or investigate.

## Keep current and target truth separate

Maintain two columns throughout the record:

| Ledger | Authority | Permitted conclusion |
|---|---|---|
| Current implementation | Checked-out production composition, source, tests, deployed assembly evidence, and captured runtime behavior | What this exact tree and assembly do now |
| Approved target | `docs/prds/01-project-management.md` and status-qualified documents under `docs/prds/architecture/` | What the project requires or has deliberately left open |

Treat `ARCHITECTURE.md`, comments, commit messages, Beads history, unmerged
branches, and proposals as leads until reconciled with those ledgers. An
approved target is authoritative for direction but is never evidence that the
live implementation already satisfies it.

Use these claim labels:

- `observed-current`: directly reproduced or traced in the frozen tree;
- `approved-target`: required by a status-qualified target document;
- `candidate`: plausible and worth testing, but not accepted;
- `open`: evidence is insufficient or contradictory.

## Apply the evidence hierarchy

Match evidence to the claim. Do not add two correlated tests and call them
independent.

| Rank | Evidence | What it can prove | Required caution |
|---:|---|---|---|
| 1 | Captured behavior from the exact binary/config/data plus a trace through live production composition and source | What the named assembly selected and did | Record version, mode, inputs, secrets redacted, and environment; one deployment is not universal |
| 2 | Independent protocol peer, official SDK, published conformance vector, or raw fixture captured outside Archie's helpers | Interoperability at an external boundary | Pin peer/version and preserve bytes or messages |
| 3 | Integration, composition, legal-transition, deterministic concurrency, cancellation, recovery, and race evidence | Behavior across real internal boundaries and schedules | Prove the test uses the production handoff rather than a parallel constructor |
| 4 | Focused unit tests, benchmarks, AST/type queries, lint findings, coverage, and source inspection | Local behavior and structural candidates | Do not infer production use, ownership, absence, or protocol compatibility |
| 5 | Current docs, history, incident reports, issue records, branches, and proposals | Intent, constraints, and candidate explanations | Reconcile with live code; never present a candidate as current |

For a current-code claim, include at least one live composition/source trace and
one independently produced check. For an external protocol claim, include Rank
2. For a concurrency claim, include deterministic ordering plus `-race`.

## Run the decision-gated method

### Gate 0 — Declare one vertical question

Write one sentence:

> Determine whether `<current mechanism>` causes `<observable outcome>` under
> `<named mode/input/state>`, so we can decide `<specific architecture or
> maintenance action>`.

Require:

- question count: exactly `1`;
- named decision owner: exactly `1`;
- implementation edits during research: `0`;
- meaningful falsifiers: at least `1`;
- explicit non-goals: at least `1`.

Split the work if the question contains “and” between independently testable
behaviors. A short success must still traverse one complete path from real
input through ownership and state to observable outcome.

### Gate 1 — Freeze the observation point

Run from the repository root before changing anything:

```bash
git rev-parse HEAD
git status --short
go version
go env GOMOD GOWORK GOOS GOARCH CGO_ENABLED GOTMPDIR GOCACHE
go list -m -f '{{.Path}} {{.Version}} {{.GoVersion}}'
find . \( -path './.git' -o -path './.references' -o -path './.claude/worktrees' -o -name node_modules \) -prune -o -name go.mod -print
```

Record:

- commit and every dirty path;
- Go/tool versions and module boundary;
- operating system, architecture, sandbox/host, and resource restrictions;
- execution mode and concrete implementation;
- configuration source and relevant effective values, with secrets redacted;
- data/fixture identity and size;
- exact command, exit status, output, and run time.

The root and `tools/` directories are separate Go modules as of 2026-07-28.
Never blend their results. Do not run `task check` to establish a read-only
baseline: it runs `gofumpt -w`, `go fix`, and builds into `bin/`.

The focused command below currently fails with `Run() = "\n"`; preserve that
signature as a real pre-existing code failure rather than an environmental
confounder:

```bash
env GOTMPDIR=/tmp GOCACHE=/tmp/archie-research-gocache \
  GIT_CONFIG_GLOBAL=/dev/null go test ./internal/skillscript \
  -run '^TestRunWrapsExternalCommand$' -count=1 -v
```

If the tree, dependency set, configuration, mode, fixture, or environment
changes, invalidate the affected baseline and restart this gate.

### Gate 2 — Predict before measuring

State both the explanation and the null:

```text
Hypothesis H1:
Null H0:
Mechanism:
Primary metric/observable:
Baseline prediction:
Treatment prediction:
Falsifier:
Invariant predictions:
```

Use numbers whenever the result is countable. If no dated baseline exists,
write `predict before measurement`, choose the number or relation now, and only
then run the command. Never backfill an “expected” value from the observed
output.

Predict all of:

1. the primary result;
2. one negative/control result;
3. preserved behavior in every affected mode, adapter, or data form;
4. the expected error or failure consequence;
5. what result makes the experiment inconclusive.

### Gate 3 — Design independent controls

Build a matrix before execution:

| Variable | Baseline | Treatment | Held constant | Measurement |
|---|---|---|---|---|
| one intentional difference | exact value | exact value | commit, mode, data, tools, timeouts | exact command/output |

List confounders explicitly. Check at least:

- sandbox listener, process, keyring, Docker socket, and cache restrictions;
- stale editor/compiler diagnostics versus a real `go build` or `go test`;
- warm versus cold cache and network access;
- test-only constructors versus production composition;
- reused parsing, framing, constants, fixtures, or server helpers;
- background goroutines, timing, retries, and global mutable state;
- local module replacements and unpublished dependency checkouts;
- dirty concurrent work and generated artifacts.

Require one control that would fail if the fixture or instrumentation were
broken. For protocols, require an independently implemented peer. For
architecture claims, require a source/type trace and a runtime or
composition-level observation.

### Gate 4 — Execute without moving the target

Run the baseline first, then the treatment, then the baseline again when
ordering or cache effects are plausible. Preserve raw output under a
task-specific temporary path or in the controlled work artifact.

Do not:

- edit implementation, tests, config, fixtures, or success criteria mid-run;
- discard outliers without a predeclared rule;
- turn a failing hypothesis into a differently worded success;
- replace a denied listener, missing peer, or cold dependency with a weaker
  test and claim equivalence.

If execution exposes a separate defect, freeze it as a new question. Do not
fold its repair into the experiment.

### Gate 5 — Apply the predeclared decision

Classify the result exactly:

| Result | Action |
|---|---|
| Every prediction and invariant holds, controls work, and required evidence ranks are present | Mark `supported`, not “proven forever”; proceed to replication and change control |
| A falsifier occurs with valid controls | Mark `falsified`; preserve the negative result and reject solutions depending on H1 |
| Fixture, environment, baseline, independence, or statistical power is inadequate | Mark `inconclusive`; state the next evidence needed |
| Current and target ledgers conflict | Record the migration gap; do not redefine either ledger |
| Another mechanism explains the result equally well | Design a discriminating experiment; do not choose by preference |

Post-hoc success criteria count: `0`. Unexplained deviations count: `0`.

### Gate 6 — Replicate and triangulate

Repeat deterministic checks from a clean process. For variable measurements,
declare the run count before starting and report every sample. Compare an
independent mechanism:

- AST/type references versus runtime composition;
- SQLite behavior versus NellDB behavior;
- in-process behavior versus subprocess/NATS behavior;
- raw protocol frames versus an official peer;
- unit behavior versus race/integration behavior;
- source expectation versus persisted reload/recovery.

If two checks share the same implementation helper, parser, mock, global, or
fixture generator, treat them as one evidence source.

### Gate 7 — Preserve the reproducibility packet

Attach this compact record to the controlled issue, architecture handoff, or
decision document selected by sibling skills. Do not create an orphan report.

```markdown
## Research record R-<id>
Question:
Decision affected:
Current claim / target claim:
Tree: <commit>; dirty paths:
Environment/mode/config/data:
H1 / H0 / mechanism:
Predictions: <metric | baseline | treatment | falsifier | invariants>
Controls and confounders:
Commands and raw-output locations:
Observed results:
Verdict: supported | falsified | inconclusive
Independent evidence:
Negative results and fenced paths:
Open uncertainty:
Owner and next gate:
```

Preserve negative results with the same care as positive results. Route a
recurring rejected approach to `archie-failure-archaeology`.

### Gate 8 — Hand off; do not self-approve

Send ownership/boundary conclusions to
`archie-architecture-planning-campaign`. Send deprecation and deletion to
`archie-technical-accountability`. Send tests and acceptance thresholds to
`archie-validation-and-qa`, then route every behavior-changing proposal through
`archie-change-control`.

Research acceptance does not authorize implementation, config mutation,
dependency changes, deployment, data migration, or deletion. A fresh
adversarial reviewer must be able to reproduce the packet without the
researcher's unstated knowledge.

## Choose an Archie experiment

Write every `N`, `R`, `M`, latency, allocation, and ratio prediction before
running its command.

| Question | Controlled method | Required prediction and falsifier |
|---|---|---|
| Are two paths duplicates? | Run `golangci-lint run --enable-only=dupl ./internal/<area>/...`; then trace both by domain effect, entrypoint, state mutation, mode, and consumer with `archie-codebase-discovery` | Predict textual candidates `N`, live authorities, and preserved differences. Falsify “one authority” with a second production-selected path that decides or mutates the same meaning. |
| Is code dead or a one-shot abstraction? | Run `staticcheck -checks=U1000 ./internal/<area>/...`, `gopls references -d <file:line:column>`, and the discovery skill's AST construction scan; inspect registrations, Yaegi, tags, build variants, tests, and both binaries | Predict typed references `R`, production constructions, dynamic registrations, and compatibility consumers. Zero text hits alone is never the falsifier. |
| Does configuration affect production? | Trace `key → decode → default → validate → translate → cmd/archied read → concrete component → observable`; inspect Compose passthrough separately | Predict exactly one effective owner and every production read. As of 2026-07-28, `cmd/archied` has two `config.LoadOverlay` call sites and no `LoadDir` call; `LoadDir` tests therefore do not prove production selection. |
| Do execution modes have parity? | Enumerate the three accepted `agent.mode` values from `internal/config`; test the same input/error/cancel matrix through `inprocess`, `subprocess`, and `nats`; treat containers as a NATS deployment assembly, not a fourth mode | Predict `M` matrix rows and the exact outcome for every cell. One direct runner unit test cannot falsify a mode-wiring defect. |
| Are persistence transitions correct? | Run shared legal/illegal transition cases against SQLite and `internal/nell`; include two stale-`from` contenders, state reload, and audit evidence under `-race` | Predict `2/2` adapters reject stale `from`, one winner, unchanged loser, and one matching audit record. Current `Store.Transition` and `Adapter.Transition` ignore `from`; classify that observation as current/open until corrected and accepted. |
| Is a protocol compatible? | Preserve canonical bytes/messages; run malformed, partial, multi-message, timeout, EOF, and cancellation cases; then run an official or independent peer | Predict handshake/result bytes and error identity. A fake peer using Archie's `readMessage`/`writeMessage` cannot satisfy independence. |
| Is cancellation/concurrency safe? | Use barriers/channels to force ordering; test cancel-before-start, cancel-while-blocked, close/send, retry, and shutdown; run `go test -race ./internal/<area>/... -count=1` | Predict terminal result, side-effect count, retry count, resource closure, and absence of races. Wall-clock sleep alone is not a schedule proof. |
| Do NellDB scans meet a requirement? | Define fixture sizes `N` and `10N`; benchmark the exact adapter query with fixed data, cache state, and run count; capture `-benchmem` | Predict latency/allocation slope and acceptance bound before running. `internal/nell/adapter.go` has 14 `AllDocs` call sites as of 2026-07-28; that is structural evidence of scans, not a performance result. |
| Did maintainability improve? | Compare owner count, production paths, package edges, public surface, config consumers, complexity findings, and files a future feature must understand; inspect every changed non-test file | Predict which counts fall, stay equal, or may rise with rationale. Linter silence and a lower aggregate score do not prove cohesion, readability, or artfulness. |
| Is a migration parity-safe? | Build an old/new matrix across config forms, stored data, modes, peers, errors, restart/recovery, rollback, and deletion criteria; execute every supported row | Predict `M` rows and exact observable parity. A PRD or candidate branch cannot satisfy a live row. Permanent duplicate authoritative paths after cutover must be `0`. |

Replace `<area>` and `<file:line:column>` with concrete values recorded in the
research packet. Use `archie-diagnostics-and-tooling` when a metric needs a
repeatable helper rather than an ad hoc shell pipeline.

## Learn from settled failures

| Lesson | Archie evidence | Research rule |
|---|---|---|
| Mirrored peers can validate the same wrong protocol | MCP `Content-Length` work in `f2768eb`/`3ad57c9` passed a server using the same helpers; `dda4cde` moved live framing to newline-delimited JSON and `TestDesktopCommanderClientCompatibility` supplies an opt-in external peer | Require independent bytes or peer behavior before claiming compatibility. |
| Green loader tests do not prove shipped wiring | `internal/config/feature_config_test.go` exercises `LoadDir`, while `cmd/archied/main.go` selects `LoadOverlay` as of the dated snapshot | Trace from a real entrypoint through the effect. |
| A linter name is not a semantic verdict | `86cfc19` records eleven `nilerr` findings whose errors were deliberately carried through text, envelopes, shutdown, or best-effort behavior; live sites explain local suppressions | Predict the contract consequence per site before changing it. |
| Diagnostics can measure the environment instead of the code | Listener-denied sandboxes, inherited GPG settings, read-only temp paths, and stale editor diagnostics have produced different failures from host runs | Freeze environment and confirm with real build/test output; never waive the blocked evidence. |
| A local dependency can make a local result unreproducible | `07fb291` removed a machine-local NellDB `replace` that CI/container builds could not resolve | Use the published module and checksum path; label local-checkout evidence candidate. |
| “Cleanup” can change behavior while looking mechanical | `308c199` changed the skillscript wrapper to `sh -c` with argument placement that now yields only a newline in the focused test | Treat every cleanup diff as authored semantics and keep the pre-change baseline. |

## Fence off wrong paths

- Do not choose success criteria after seeing output.
- Do not let a client and fake server import the same framing, parser, or
  constants and call the result compatibility.
- Do not change implementation, tests, config, or fixtures during baseline
  capture.
- Do not treat a candidate branch, commit subject, comment, PRD, or old
  architecture document as live proof.
- Do not infer dead code from text search or `U1000` alone.
- Do not equate coverage, lint silence, or green TDD with production wiring,
  one coherent owner, or maintainability.
- Do not suppress a negative result because the attempted slice was small.
- Do not reduce artfulness to one complexity score; measure locality and then
  review names, ownership, cohesion, failure clarity, and deletion.

## Provenance and maintenance

Re-verify current versus target authority: `sed -n '1,180p' docs/prds/01-project-management.md && sed -n '1,180p' docs/prds/architecture/package-review.md`

Re-verify module and gate boundaries: `find . \( -path './.git' -o -path './.references' -o -path './.claude/worktrees' -o -name node_modules \) -prune -o -name go.mod -print && sed -n '1,180p' Taskfile.yml`

Re-verify configuration selection: `rg -n 'config\.(Load|LoadOverlay|LoadDir)\(' cmd internal --glob '*.go'`

Re-verify execution modes: `rg -n 'case "inprocess"|case "subprocess"|case "nats"|agent\.mode' cmd/archied internal/config --glob '*.go'`

Re-verify transition semantics: `sed -n '255,285p' internal/store/store.go && sed -n '268,290p' internal/nell/adapter.go`

Re-verify NellDB scan candidates: `rg -n 'AllDocs\(' internal/nell/adapter.go`

Re-verify protocol independence: `rg -n 'readMessage|writeMessage|TestDesktopCommanderClientCompatibility|newline-delimited|Content-Length' internal/tools --glob '*.go'`

Re-verify the cleanup regression: `env GOTMPDIR=/tmp GOCACHE=/tmp/archie-research-gocache GIT_CONFIG_GLOBAL=/dev/null go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1 -v`
