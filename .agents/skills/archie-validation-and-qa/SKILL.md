---
name: archie-validation-and-qa
description: "Design, run, interpret, and review defensible validation evidence for archie-core. Use when adding or assessing Go tests, proving a red-then-green change, choosing focused versus repository-wide gates, validating production wiring or protocols, investigating race/cancellation/state-transition behavior, distinguishing sandbox failures from code regressions, or deciding whether an implementation has enough evidence to be accepted."
---

# Archie validation and QA

Prove the behavior, integration, and maintainability claim. A passing unit test
is necessary evidence; it is not proof that production selects the code, a
protocol interoperates, concurrency is safe, or the superseded path is gone.

## Route work before testing

| Need | Load instead or alongside this skill |
|---|---|
| Classify risk, authorize mutation, choose mandatory gates, or accept/close work | `archie-change-control` |
| Find duplicate, dead, misplaced, or unmaintainable code and test whether the change is locally comprehensible | `archie-technical-accountability` |
| Triage an unexplained runtime or test symptom | `archie-debugging-playbook` |
| Decide ownership, boundaries, migration, configuration responsibility, or deletion of competing paths | `archie-architecture-planning-campaign` |

Do **not** use this skill:

- to turn a design dispute into a test implementation;
- to waive a gate because a focused test passes;
- to diagnose an unfamiliar failure without the debugging playbook;
- to certify maintainability without technical-accountability review;
- to run `task check` during a read-only review or in a shared dirty tree: it
  rewrites Go files.

## Define the evidence terms

| Term | Meaning here |
|---|---|
| Right-reason red | The intended behavioral assertion fails against compiling code because the capability is absent or wrong. |
| Focused test | One named test or one owning package used for fast iteration. |
| Root suite | `go test ./... -count=1` in the repository root module. |
| Tools suite | `go -C tools test ./... -count=1` in the separate `tools` Go module. |
| Composition test | A test that crosses the real construction or dispatch handoff used by a binary. |
| Independent peer | A protocol implementation, official SDK, executable, or conformance fixture that does not reuse the code under test. |
| Environmental failure | The test cannot acquire a required host resource, such as a listener, writable keyring, executable, or cache. |
| Code regression | The test reached the behavior under test and observed the wrong result. |
| Evidence packet | The exact commands, outputs, tree identity, test design, integration proof, and independent review used for acceptance. |

## Recheck the volatile baseline

Treat this as a dated warning, not a permanent exception.

As of **2026-07-28**:

- A recorded unrestricted run of `go test ./... -count=1` has one code failure:
  `internal/skillscript.TestRunWrapsExternalCommand` gets `"\n"` instead of
  `"wrapped\n"`.
- The root suite excludes `tools` because `tools/go.mod` starts a nested module.
- No separate integration-test task or build tag exists. Embedded NATS servers,
  loopback HTTP/SMTP servers, and Git fixture commits run inside the ordinary
  root suite.
- `TestDesktopCommanderClientCompatibility` is the one explicit external
  integration opt-in found; it skips unless
  `ARCHIE_TEST_DESKTOP_COMMANDER=1`.
- `task check` runs `gofumpt -w .`, `go fix ./...` twice, `go vet ./...`,
  the two binary builds, and the root suite. It does **not** run
  `golangci-lint`, race tests, the `tools` module, docs generation, or the docs
  build.
- The Gitea deploy workflow builds and pushes images, and the GitHub docs
  workflow builds VitePress. Neither workflow runs the Go quality gate.

This baseline means the repository-wide suite is not green. Prove local
non-regression if work must continue, but never report the full gate as passing
until the failure is fixed and the unrestricted suite is rerun. Route any
acceptance decision through `archie-change-control`.

## Choose the required evidence surface

Apply every row that matches the change.

| Change surface | Minimum additional evidence |
|---|---|
| One genuinely isolated package | Right-reason red and green; whole-package test; scoped lint; formatting/fix check; prove no cross-package runtime contract changed. |
| Cross-package behavior or shared contract | Root suite; full lint; vet/build; race suite; composition test at each changed handoff. |
| `cmd/archied` or `cmd/archie-agent` wiring | Test from real configuration/construction through selected implementation and observable result; test missing/invalid wiring. |
| Protocol, RPC, MCP, NATS, subprocess, or wire schema | Malformed, partial, multiple, out-of-order, timeout, cancellation, and peer-exit cases; at least one independent-peer or conformance test. |
| Mutable shared state | Deterministic concurrency test plus `-race`; ownership/aliasing test; cancellation and shutdown proof. |
| Store or lifecycle transition | Legal/illegal transition table; stale-`from` contenders; atomic state-and-audit proof; adapter parity where both SQLite and NellDB implement the contract. |
| Configuration | Default, explicit, absent, invalid, compatibility, precedence, secret-reference, and production-passthrough tests. |
| `tools/**` or generated contracts | Tools suite and lint; generator determinism/drift check; root consumer tests; docs build when rendered data changes. |
| Docker/Compose/deployment | Configuration-to-container passthrough, network/volume/health behavior, failure visibility, and last-known-good recovery evidence. |
| External integration | Hermetic tests first; then an explicitly authorized live compatibility run with version, environment, and observed output recorded. |

Do not call a change isolated merely because only one file changed. Trace its
callers and consumers with the architecture campaign or technical-accountability
skill first.

## Execute red then green

### 1. Freeze one behavioral claim

Write:

> Given `<input and state>`, when `<real entry point>` is called, `<owner>`
> produces `<observable result or error>` while preserving `<invariants>`.

Name the failure surface, production consumer, and superseded behavior. If
ownership or the affected paths are unclear, stop and run the architecture
campaign.

### 2. Establish the pre-change baseline

Run the nearest existing tests before adding the repro. Record the current
commit or supplied tree identity, existing dirty paths, command, exit status,
and failures. Do not assign a concurrent or pre-existing failure to the change
without a before/after comparison.

### 3. Write the test first

Make the test compile against the intended public or package contract. Exercise
the behavior, not a private implementation sequence. Then run only the named
test and prove:

- the assertion fails;
- the failure message names the wrong behavior;
- the fixture reached the intended branch;
- the cause is not compilation, a missing executable, a listener denial, GPG,
  a timeout chosen too tightly, or stale generated data.

A compile error is not red. A test that panics in setup is not red. Deliberately
corrupting unrelated code to manufacture red is not TDD.

For a behavior-preserving refactor, retain green characterization tests. Add a
structural test only for a stable, load-bearing architecture constraint; do not
invent a brittle implementation assertion merely to manufacture red. Have
`archie-change-control` classify the work before proceeding.

### 4. Implement the smallest coherent change

Make the named test green, then run the owning package. “Smallest” does not mean
leaving a second active implementation or a one-call wrapper with no boundary
purpose. Use `archie-technical-accountability` to inspect deletion,
supersession, dependency direction, and comprehensibility.

### 5. Complete the test matrix

Use table-driven cases. Put identities, repositories, configuration values,
durations, states, and expected results in case fields or fixture builders.
Never couple tests or implementation to live identities such as production bot
names. Use named constants only when the value is itself a contract.

Test every reachable error path. At minimum consider:

- empty, malformed, duplicate, and boundary inputs;
- missing configuration, environment references, credentials, and resources;
- downstream returned errors, partial results, and peer exit;
- timeout before work, cancellation during blocked work, and shutdown;
- partial side effects, retry, idempotency, and recovery;
- unauthorized identity or cross-scope access;
- old configuration, stored data, or wire forms when compatibility is claimed.

Assert the result **and** the consequence: returned error identity/wrapping,
persisted state, emitted event, audit entry, side-effect count, and absence of
silent fallback.

## Test the seams models commonly miss

### Prove production wiring

Trace and test this chain:

```text
real input -> decode/load -> binary composition -> selected owner
           -> state or side effect -> observable success/failure
```

A decoder test proves decoding. A constructor test proves construction. A
registry test proves registration. None proves that `cmd/archied` or
`cmd/archie-agent` selects the implementation.

Add one test at the owning behavior and one composition/contract test across
each changed handoff. Include a negative case showing that absent or failed
wiring cannot report success. Structural source-string tests may protect an
especially fragile seam, but they cannot be the only production-wiring proof.

### Test protocols independently

Do not validate a client only against a fake that calls the same framing,
parser, constants, or helpers. That proves self-consistency.

Use, in ranked order:

1. the supported real peer pinned to an explicit version;
2. an official SDK or independent reference implementation;
3. published conformance vectors or captured wire fixtures;
4. a deliberately independent minimal peer that encodes from the written
   contract without importing the implementation.

Keep fast in-process fakes for malformed and failure cases. Add boundaries for
split/coalesced frames, multiple buffered messages, unknown fields, peer EOF,
stderr noise, slow response, cancellation, and restart. For MCP, the live
Desktop Commander opt-in is compatibility evidence; a helper server that uses
Archie's own `readMessage`/`writeMessage` is not.

### Test aliasing and ownership

For maps, slices, schemas, options, registries, and configuration projections:

1. mutate the input after construction and prove owned state does not change;
2. mutate the returned value and prove internal state does not change;
3. mutate nested maps/slices, not only the top level;
4. run concurrent readers/writers under `-race` when sharing is permitted.

### Test concurrency and cancellation deterministically

Use barriers, channels, fakes, and explicit clocks to force ordering. Do not use
`time.Sleep` as the sole synchronization proof.

Cover:

- cancellation before work starts;
- cancellation while blocked on I/O, a stream, child process, or queue;
- child and grandchild termination for subprocesses;
- no retry after caller cancellation;
- start/stop and close/send races;
- bounded drain and no goroutine leak;
- same-repository serialization and permitted cross-repository overlap;
- defensive copies around shared mutable state.

Run the race detector after deterministic tests pass. `-race` can reveal a race;
it cannot prove the intended ordering or state machine.

### Test transitions as compare-and-set operations

For every state-changing method, table legal and illegal transitions. Assert
that:

- the supplied `from` state or version is enforced;
- two contenders starting from the same state cannot both succeed;
- the new state and audit/event record are one atomic outcome;
- retries are idempotent or explicitly rejected;
- failure leaves neither partial state nor a false success record;
- every live adapter preserves the same semantics.

Do not accept a test that only shows `queued -> running` succeeds. It misses
lost updates, invalid transitions, and state/audit divergence.

## Run exact gates

Create writable, task-specific caches once:

```bash
mkdir -p /tmp/archie-qa-tmp /tmp/archie-qa-gocache /tmp/archie-qa-lintcache
```

### Iterate on one test and package

Replace the placeholders with real names:

```bash
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go test ./internal/<package>/... -run '^TestName$' -count=1 -v
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go test ./internal/<package>/... -count=1
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache GOLANGCI_LINT_CACHE=/tmp/archie-qa-lintcache golangci-lint run ./internal/<package>/...
```

Use the same command for red and green. Record both outputs.

### Run the root evidence

Run `task check` only in an authorized, isolated tree. It mutates source with
`gofumpt -w .` and `go fix ./...`, and writes binaries under `bin/`. Inspect
the complete before/after diff; never let its broad rewrite hide concurrent
changes.

```bash
git status --short
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache task check
git status --short
gofumpt -l .
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go fix -diff ./...
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache GOLANGCI_LINT_CACHE=/tmp/archie-qa-lintcache golangci-lint run ./...
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go test -race ./... -count=1
```

Expected acceptance output:

- `gofumpt -l .`: no paths;
- `go fix -diff ./...`: no diff and exit zero;
- lint: zero findings;
- tests, vet, and builds: exit zero;
- race suite: exit zero and no race report.

Do not say `task check` covers lint or race. It does not.

### Run the nested tools and documentation evidence

Run this when `tools/**`, published contracts, generated data, or their root
consumers change:

```bash
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go -C tools test ./... -count=1
(cd tools && env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache GOLANGCI_LINT_CACHE=/tmp/archie-qa-lintcache golangci-lint run ./...)
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go -C tools run ./docsgen
git diff --exit-code -- docs-2/data/generated/contracts.json
(cd docs-2 && pnpm install --frozen-lockfile)
(cd docs-2 && pnpm build)
```

The current docsgen command generates data only. Do not use target-only
`docsgen all` or `docsgen check` commands from the architecture PRD until the
live binary implements them.

## Separate environment failures from regressions

| Symptom | Classify first as | Required next action |
|---|---|---|
| `listen tcp ... operation not permitted`, `httptest: failed to listen`, or NATS “Unable to start” | Restricted sandbox lacks loopback/listener access | Rerun the same command in an environment that permits local listeners. Do not weaken or skip the test. |
| Git fixture commit fails with GPG/keybox read-only errors | Inherited host `commit.gpgSign`, inaccessible keyring | Rerun with the temporary Git config override below; do not modify global Git configuration. |
| Go or lint cache reports read-only/permission errors | Environment/cache | Use the writable cache paths above. If the module cache itself is read-only, use a prewarmed writable `GOMODCACHE`; a dependency-download failure is still environmental evidence, not a product result. |
| Desktop Commander test is skipped | Expected default | Run the explicit opt-in only with authorization for `npx` network/download and external process execution. |
| `TestRunWrapsExternalCommand` returns `"\n"` | Known 2026-07-28 code baseline | Treat as a real failure until fixed; do not group it with sandbox denials. |
| Assertion reports wrong state/output after setup completed | Probable code regression | Minimize with the named test, compare pre-change baseline, and use `archie-debugging-playbook`. |

Use this non-persistent signing override for tests that spawn Git commits:

```bash
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=commit.gpgSign GIT_CONFIG_VALUE_0=false go test ./internal/workflow/... -count=1
```

Run the external MCP compatibility check only when explicitly authorized:

```bash
env ARCHIE_TEST_DESKTOP_COMMANDER=1 GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go test ./internal/tools/provider/mcp -run '^TestDesktopCommanderClientCompatibility$' -count=1 -v
```

Record the external package version, network availability, platform, and
whether the failure occurred during download/startup or after MCP exchange.

## Require adversarial review after green

Use a fresh reviewer with no implementation narrative. Give it the request,
diff, governing contract, and raw gate outputs. Do not give it the author's
defense of the design.

Require the reviewer to:

- rerun or inspect all applicable gates;
- read every changed non-test file and its production callers/consumers;
- find dead or duplicate paths and identify what the feature supersedes;
- inspect unchecked errors, nil risks, hardcoded identity/config, and partial
  side effects;
- inspect interface satisfaction, ownership, aliasing, cancellation,
  goroutine/process leaks, races, and state transitions;
- verify real composition, protocol independence, deployment passthrough, and
  negative cases;
- report findings without silently fixing them.

Fix blocking and important findings in a separate implementation pass. Rerun
all affected gates, then have the reviewer verify that **zero surviving
findings** remain. The implementer never self-certifies this pass.

## Enforce acceptance thresholds

Build this evidence packet:

| Evidence | Threshold |
|---|---|
| Scope and tree identity | Exact change claim, affected packages/processes, commit or supplied tree identity, and pre-existing dirty paths recorded. |
| Red | Named test fails for the intended assertion; compile/setup/environment failures: `0`. |
| Green | Named test and owning package pass; unexpected failures: `0`. |
| Error behavior | Every reachable error path in scope has an assertion on error and consequence; unexplained paths: `0`. |
| Wiring | At least one composition/contract test per changed handoff plus a negative wiring case. |
| Protocol | At least one independent-peer/conformance result for changed protocol semantics. |
| Concurrency/state | Deterministic ordering/transition tests pass; race reports: `0`; partial state/audit outcomes: `0`. |
| Formatting/fix/lint | Formatter paths: `0`; fix diff: `0`; lint findings: `0`. |
| Applicable suites | Scoped or root gate, race, tools, docs, and external checks selected by the matrix above; unexplained failures/skips: `0`. |
| Maintainability | Superseded paths have keep/delete/cutover dispositions; unexplained duplicate or dead production paths: `0`. |
| Review | Fresh adversarial reviewer has zero surviving blocking or important findings. |
| Uncertainty | Every unproved claim is labeled `open` or `candidate`; hidden assumptions: `0`. |

Do not average evidence. One surviving race, wiring gap, unexplained skip,
state-transition flaw, or blocking review finding blocks acceptance even when
hundreds of unit tests pass. Route the completed packet through
`archie-change-control`; testing does not itself authorize closure, deployment,
or migration.

## Provenance and maintenance

Grounded on 2026-07-28 in `CLAUDE.md`, `Taskfile.yml`, root and `tools` module
manifests, both workflow directories, the ordinary test suite, the Desktop
Commander opt-in, and the live docsgen command. Recheck volatile facts before
using them:

```bash
sed -n '1,120p' Taskfile.yml
find . -name go.mod -print | sort
rg -n '^//go:build|ARCHIE_TEST_[A-Z0-9_]+' --glob '*_test.go' .
rg -n 'task check|go test|golangci-lint|go vet|pnpm build' .github/workflows .gitea/workflows Taskfile.yml
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go test ./... -count=1
env GOTMPDIR=/tmp/archie-qa-tmp GOCACHE=/tmp/archie-qa-gocache go -C tools test ./... -count=1
go -C tools run ./docsgen -h
```
