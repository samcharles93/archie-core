---
name: archie-change-control
description: "Classify, investigate, plan, gate, review, and accept archie-core changes without trading maintainability for a passing test. Use before changing or approving architecture, Go source, configuration, runtime lifecycle, dependencies, protocols, persistence, security boundaries, deployments, generated documentation, or read-only designs; also use when deciding whether scoped evidence is sufficient, whether an old path must be deprecated or deleted, or whether a change is ready to close."
---

# Archie change control

Protect Archie's quality, not merely its test result. Treat a passing test as
necessary evidence for code changes, never as proof that the design is coherent,
the old path is gone, or production selects the new path.

## Route to the right sibling

Use this skill to control a change from classification through acceptance.
Load these siblings for the specialized work:

| Need | Load |
|---|---|
| Design or revise boundaries, ownership, package structure, or a migration | `archie-architecture-planning-campaign` |
| Find duplication, hidden dead paths, poor names, misplaced responsibilities, or maintainability debt | `archie-technical-accountability` |
| Design tests, reproduce a defect, select QA evidence, or run complete gates | `archie-validation-and-qa` |
| Run, deploy, observe, recover, or roll back a live assembly | `archie-run-and-operate` |

Do **not** use this skill alone to:

- conduct an architecture campaign;
- perform a deep code-quality or AST investigation;
- invent a test strategy for a complex behaviour;
- operate production;
- replace the governing decision document for a domain.

## Use these terms consistently

| Term | Meaning here |
|---|---|
| Baseline | Recorded behaviour and gate output before the proposed change. |
| Current state | Behaviour selected by the checked-out live code, not a PRD target or branch-only fix. |
| Production wiring | The complete path by which a supported assembly selects, configures, runs, and observes an implementation. |
| Authoritative contract | The source whose semantics the producer and consumer are required to obey; a projection or shared test fake is not authoritative. |
| Compatibility adapter | Temporary code that preserves an older input or caller while migration proceeds, with explicit deletion criteria. |
| Last-known-good | The previously active version proved healthy and retained for restoration. |
| Evidence packet | Commands, outputs, traces, diffs, decisions, and reviews supporting acceptance. |
| Acceptance | Permission to close the controlled work; it is not permission to push, deploy, migrate, or delete. |

## Apply the non-negotiables

| Rule | Why it is mandatory | Repository evidence or incident |
|---|---|---|
| Investigate the live call path before planning. | A constructor, registry entry, config field, or test can exist without production ever selecting it. | Missing Compose passthroughs in `22fc0ef` and `d0624b7` left configured secrets absent at runtime. |
| Test the authoritative contract, not a convenient projection. | A best-effort or mirrored interface can pass while the real producer blocks or the peer rejects the exchange. | `6a0bd27`: consuming `TextStream` instead of draining authoritative `FullStream` deadlocked every streamed reply. `3ad57c9`: recreating a buffered reader dropped MCP messages; the harness also masked process-exit behaviour. |
| Preserve compatibility deliberately. Never rely on a decoder ignoring old input. | Silent omission turns a schema edit into an outage. | `1f8588d`: removing `[forge].token_env` compatibility caused both production containers to crash-loop because TOML ignored the old key. |
| Make deployment dependencies explicit and observable. | Hostname, network, secret, mount, and environment assumptions differ between tests and the running Compose assembly. | `37cf089` and `2ac306b`: failed Docker-network auto-detection put workers on the default bridge, so NATS returned “no responders.” |
| Inspect every mechanical or generated edit semantically. Never accept a broad cleanup by count alone. | Formatting, autofix, rebasing, and three-way application can alter behaviour or erase concurrent work. | `9b44bac`: `golangci-lint --fix` introduced a bad `:=` that would have defeated a timeout if it compiled. `affc717`: worktree diffs were based on an older tree and required explicit survival checks. `308c199`: a broad cleanup changed the skillscript command so its test now emits only a newline. |
| Enforce lifecycle transitions and audit atomically. | Ignoring expected state permits lost updates; splitting state and history permits contradictory evidence. | As of 2026-07-28, `internal/store.Store.Transition` ignores `from` and performs two writes. Fix `a7f294e` exists only on `origin/fix/48-transition-ignores-from-status-guard-allowing-lost-updates`, not on `main`; the Nell adapter also does not record equivalent transition history. |
| Keep deterministic constraints outside the proposing model. | A prompt can be ignored to make a test pass. | `CLAUDE.md` requires gates, protected tests, and diff limits to be code-enforced; the model never owns git operations. |
| Require a fresh adversarial reviewer after green. | The implementer is biased toward the path it just made pass. | `CLAUDE.md` makes a memory-independent review mandatory and blocks completion on surviving findings. |

## Classify the change

Classify every affected surface. For a composite change, apply the union of all
required controls and use the highest-risk class.

| Class | Examples | Minimum control |
|---|---|---|
| Read-only investigation | Review, diagnosis, inventory, impact map | Do not mutate. Cite live code, tests, history, and unresolved uncertainty. |
| Design or documentation | PRD, ADR-like decision, architecture guide, skill | Separate current facts from approved target and open candidates. Validate links/format. Do not imply implementation. |
| Source | Go behaviour, refactor, API, workflow | Current-state map, red-green, scoped iteration, architecture and maintainability review, final relevant gates. |
| Configuration | Decoder, default, env name, overlay, secret reference, setting owner | Old/new input fixtures, precedence/default proof, production composition trace, invalid-input behaviour, explicit rollback. |
| Runtime lifecycle | Start, stop, reload, concurrency, stream, retry, health | Cancellation/backpressure/failure tests, race test, bounded retry, shutdown and rollback proof. |
| Dependency | Root or `tools` module dependency/version | Adoption rationale, API/behaviour delta, both module manifests as applicable, transitive/security review, reverse plan. |
| Protocol | MCP, NATS, RPC, subprocess, JSON/wire schema | Version and compatibility matrix, independent-peer or recorded-wire evidence, malformed/partial/multi-message tests. |
| Persistence | SQLite, NellDB, schema, lifecycle state, migrations | Old-data fixture, legal-transition and concurrency tests, atomic state/audit proof, backup/rollback strategy. |
| Security or trust boundary | Secret flow, plugins, tools, containers, identity, authorization | Threat/authority analysis, deny tests, deterministic enforcement, fresh security review, no “subprocess equals sandbox” claims. |
| Deployment | Dockerfile, Compose, image/tag, env passthrough, volume/network | Exact assembly validation, secret-name-to-container trace, health observation, previous image/config recovery plan. |
| Generated documentation | `tools/docsgen`, registries, generated JSON, VitePress | Generator tests, byte-for-byte drift check, site build, generated-vs-handwritten ownership check. |

Escalate the risk class when the change crosses a process boundary, changes
stored or wire data, changes operator-visible defaults, alters authorization,
removes compatibility, or can affect a live deployment.

## Execute the control loop

### 1. Establish authority and scope

For implementation work, start from the existing Beads issue and claim it.
Create durable work before writing code if no issue exists. Do not create a
Markdown task list.

```bash
bd prime
: "${BEADS_ISSUE_ID:?set BEADS_ISSUE_ID to the target issue}"
bd show "$BEADS_ISSUE_ID"
bd update "$BEADS_ISSUE_ID" --claim
```

For a review or investigation, remain read-only unless the user separately
authorizes implementation.

Record:

- the requested outcome and explicit non-goals;
- every selected change class;
- files and domains believed to be in scope;
- user authority for source, config, deployment, dependency, and destructive
  operations;
- whether another session owns overlapping files.

### 2. Protect concurrent work

Inspect before editing:

```sh
git status --short
git diff --stat
git worktree list --porcelain
```

Treat every pre-existing change as somebody else's. Stop before overlapping it
unless ownership and integration order are known. Never discard, reset, or
silently reformat unrelated changes.

Partition parallel work by non-overlapping file ownership. Before applying an
agent result based on another tree, compare its base and verify that every
intervening semantic fix survives. The `affc717` history demonstrates why a
cleanly applied diff is not proof of preservation.

Do not run broad autofix against a shared dirty tree. `task check` and
`task fmt` are broad mutators; isolate them and inspect their complete diff.

### 3. Map the current state before proposing a change

Read `CLAUDE.md`, `ARCHITECTURE.md`, `docs/prds/01-project-management.md`, and
the focused decision document linked from the architecture index.

For the selected behaviour, trace all of:

- entry points under `cmd/` and application composition;
- constructors, registries, interfaces, adapters, and concrete selection;
- every caller and consumer, including RPC/process peers;
- settings, defaults, overlays, secrets, and deployment passthrough;
- state ownership, persistence, lifecycle, cancellation, and recovery;
- tests and what they do **not** exercise;
- duplicated implementations, compatibility adapters, generated copies, and
  apparently dead paths;
- git history for rejected, stalled, reverted, or branch-only work;
- documents that disagree with live code.

Use read-only evidence:

```bash
# Guard: verify repository root first
git rev-parse --show-toplevel >/dev/null || { echo 'run from repo root' >&2; exit 1; }
: "${ARCHIE_SYMBOL:?set ARCHIE_SYMBOL to the symbol or config key}"
rg -n "$ARCHIE_SYMBOL" cmd internal config*.toml docker-compose.yml docs
: "${ARCHIE_PACKAGE:?set ARCHIE_PACKAGE to the package path relative to repo root}"
go list -deps "./internal/$ARCHIE_PACKAGE/..."
: "${ARCHIE_PATHS:?set ARCHIE_PATHS to the space-separated affected paths}"
git log --all --oneline -- $ARCHIE_PATHS
git blame -- $ARCHIE_PATHS
```

Do not ask the user for a fact the repository can reveal. Ask only when a real
product, compatibility, ownership, or risk decision remains.

### 4. Write the change brief

Do not begin implementation until the brief answers:

| Question | Required answer |
|---|---|
| What owns this behaviour? | One cohesive domain/capability and the governing decision document. |
| Where does production select it? | Exact entry point-to-implementation path. “Registered” is insufficient. |
| What changes and what stays stable? | Behavioural contract, invariants, non-goals, and affected consumers. |
| What does this supersede? | Every older or parallel path, with keep/merge/deprecate/delete decision. |
| What gets deleted? | Obsolete code, compatibility adapters, duplicated config, tests, and docs, or a justified reason to retain each. |
| What proves failure first? | A behavioural red test that fails for the intended reason, not a compile error. |
| What proves maintainability? | Ownership, dependency direction, naming, size, duplication, and dead-path checks. |
| What is the compatibility plan? | Old/new config, protocol, data, caller, and rollout combinations. |
| How is it reversed? | Last-known-good state and evidence that reversal preserves data and service. |
| What remains uncertain? | Explicit open/candidate claims; never convert a hunch into a requirement. |

If adding a feature makes an existing path redundant, deletion is part of the
feature unless compatibility has a named owner, deadline or deletion
criterion, and test. Do not leave two active paths merely because deleting the
old one is harder.

Route boundary or ownership uncertainty through
`archie-architecture-planning-campaign` before coding. Route unlocatable impact,
duplication, or dead-path uncertainty through `archie-technical-accountability`.

### 5. Capture a baseline

Run the narrowest non-mutating checks that exercise the affected area before
the red test. Preserve exact command, exit status, and output.

Example for one Go package:

```sh
go test ./internal/gateway/... -count=1
go vet ./internal/gateway/...
golangci-lint run ./internal/gateway/...
gofumpt -l internal/gateway
```

Distinguish:

- **clean baseline**: proceed;
- **unrelated known failure**: preserve its exact signature and escalate;
- **failure in the affected surface**: do not build on it; repair or split the
  work with explicit authority;
- **environment failure**: prove it is environmental before classifying it as
  non-code.

Do not call a baseline green when a command was skipped.

### 6. Apply red-green without surrendering design

1. Add a behavioural test for the intended contract.
2. Run it and capture a meaningful failure.
3. Implement the smallest coherent design, not the smallest patch.
4. Run the focused test until green.
5. Add every relevant error, boundary, compatibility, cancellation, and
   concurrency case.
6. Re-read every changed non-test file for ownership, clarity, duplication,
   unused branches, and superseded paths.

Load `archie-validation-and-qa` for the test design. Load
`archie-technical-accountability` before accepting “works but ugly” code.

Never weaken or rewrite the red test merely to admit the implementation.
Environmental test protection is preferable to a prompt-only prohibition.

### 7. Prove production wiring

Demonstrate all applicable links:

```text
external input
  -> decoder/adapter
  -> application composition
  -> selected implementation
  -> state or external effect
  -> observable outcome
```

Test at the highest practical seam. A package unit test does not prove that
`cmd/archied` supplies the new setting, registers the implementation, forwards
the environment variable, chooses the right mode, or exposes the result.

For configuration, test the deployed legacy form and the new form. Verify both
the source environment and `docker-compose.yml` passthrough. For protocols,
verify bytes/messages with an independently implemented peer or fixture rather
than a client and server sharing the same wrong assumption.

### 8. Make rollback executable

Identify last-known-good **before** changing runtime state. Define the trigger,
owner, commands or deterministic mechanism, data consequences, and health check
for restoration.

| Change | Required rollback evidence |
|---|---|
| Source or dependency | Exact prior version plus proof that reverting does not leave incompatible data, config, or generated output. |
| Configuration | Prior accepted input and effective values, secret-safe diff, reload/restart consequence, and health check. |
| Protocol | Old/new peer matrix; retain the old path until mixed-version operation or coordinated cutover is proved. |
| Persistence | Backup/restore or reversible migration rehearsal against an old-data fixture; never call a destructive forward-only migration rollback-safe. |
| Deployment | Previous image digest and config, exact supported assembly, restoration command, and post-restore observation. |
| Generated documentation | Regenerate from authoritative definitions; never recover by hand-editing generated JSON. |

The candidate-promotion and automatic last-known-good protocol in
`docs/prds/architecture/safe-change-and-recovery.md` is an approved requirement,
but its exact Go contracts and atomicity boundary are explicitly still under
design as of 2026-07-28. Do not claim Archie currently provides it. For live
work, route the concrete manual or implemented recovery procedure through
`archie-run-and-operate`.

### 9. Run the required evidence set

Use scoped checks while iterating. Use a scoped final gate only for a genuinely
isolated single-package change allowed by `CLAUDE.md`. Require full and
class-specific checks for cross-package, configuration, runtime, dependency,
protocol, persistence, security, deployment, or generated-documentation work.

`task check` is repository doctrine but is not the complete evidence set:

```sh
task check
golangci-lint run ./...
go test -race ./... -count=1
go -C tools test ./... -count=1
go -C tools vet ./...
```

For generated contracts and the site, run the current commands:

```sh
go -C tools test ./docsgen -count=1
go -C tools run ./docsgen --repo-root .. --out /tmp/archie-contracts.check.json
cmp /tmp/archie-contracts.check.json docs-2/data/generated/contracts.json
pnpm --dir docs-2 build
```

Inspect the diff after any formatter, fixer, generator, or `task check`. A zero
exit status does not authorize unrelated edits.

### 10. Run fresh adversarial review

Give a reviewer with no implementation memory the requirement, governing
architecture, current diff, and raw validation output. Require findings first;
do not let that pass silently fix its own findings.

Require review of:

- production selection and every affected consumer;
- correctness and error propagation;
- architecture ownership and dependency direction;
- obsolete, duplicate, disconnected, or single-use paths;
- config/default/identity hardcoding;
- interface satisfaction and wire compatibility;
- nil, cancellation, goroutine, race, transaction, and rollback hazards;
- whether tests prove the contract rather than mirror the implementation.

Resolve every blocking and important finding, rerun affected checks, then use a
fresh final pass if semantics changed. Surviving findings block acceptance.

### 11. Accept or stop

Accept only when the evidence packet contains:

- issue/scope and all change classes;
- current-state impact map and governing decisions;
- baseline, red, green, and final command outputs;
- production-wiring proof;
- compatibility, deprecation, deletion, and rollback decisions;
- architecture and maintainability review;
- fresh adversarial review with zero surviving blocking or important findings;
- explicit uncertainties and follow-up ownership;
- no unexplained diff.

Do not close a Beads issue until its acceptance criteria and the session close
protocol are satisfied. Do not push, deploy, migrate, or delete data without
separate authority.

## Know the current gate gaps

**Volatile snapshot: 2026-07-28. Re-verify before every acceptance decision.**

- `task check` runs `task fmt`, which executes `gofumpt -w .` and
  `go fix ./...`; it then executes `go fix ./...` a second time before vet,
  build, and root tests. It mutates source.
- `task check` does not run `task lint`, race tests, the nested `tools` module,
  docsgen drift checks, or the VitePress build.
- Root `go test ./...` does not cross the nested `tools/go.mod` boundary.
- `.gitea/workflows/deploy.yml` builds and pushes images without a Go quality
  job. `.github/workflows/docs.yml` installs and builds VitePress but does not
  run the Go gate or docsgen.
- `go test ./internal/skillscript -count=1` currently fails
  `TestRunWrapsExternalCommand`: `Run() = "\n"`.
- The approved generated-documentation document describes future
  `task docs:*` and `docsgen check` commands, but neither exists in the current
  `Taskfile.yml`/CLI. Use the current commands above.
- No `task deadcode` target exists. Never report it as executed.

Repository doctrine still requires a clean relevant gate. A known baseline
failure is not permission to report completion. Either repair it under
separately controlled scope or report the work blocked with scoped no-regression
evidence.

## Escalate instead of improvising

Stop and obtain a decision when:

- a requested feature conflicts with an approved architecture decision;
- ownership or the production selection path remains ambiguous after research;
- a new path overlaps an existing feature and no deprecation/deletion decision
  exists;
- compatibility requires supporting two behaviours with no sunset criterion;
- a migration can lose data or cannot demonstrate restoration;
- a runtime/config change cannot preserve last-known-good state;
- a protocol change lacks an independent interoperability oracle;
- a security rule exists only in a prompt;
- a baseline failure touches the same surface;
- concurrent work overlaps the files or semantics;
- the required full gate cannot run or has an unexplained failure;
- an adversarial finding survives.

Do not escalate discoverable facts or ordinary implementation choices. Bring
the evidence, the smallest decision required, and the consequences of each
option.

## Provenance and maintenance

Re-verify commands, gates, current defects, and incidents before updating this
skill:

```sh
sed -n '1,220p' Taskfile.yml
sed -n '1,260p' CLAUDE.md
find .gitea/workflows .github/workflows -maxdepth 1 -type f -print
sed -n '1,140p' .gitea/workflows/deploy.yml
sed -n '1,180p' .github/workflows/docs.yml
sed -n '1,130p' docs/prds/architecture/safe-change-and-recovery.md
sed -n '1,120p' docs/prds/architecture/package-review.md
sed -n '300,350p' docs/prds/architecture/agent-system.md
sed -n '255,280p' internal/store/store.go
sed -n '270,305p' internal/nell/adapter.go
go test ./internal/skillscript -count=1
git log --all --format='%h %ad %s' --date=short --grep='FullStream\|MCP transport framing\|token_env\|container Docker network\|autofix\|Transition() ignores' -i
git branch -a --contains a7f294e
rg -n 'docs:|deadcode' Taskfile.yml
```
