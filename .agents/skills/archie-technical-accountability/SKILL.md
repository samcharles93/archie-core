---
name: archie-technical-accountability
description: Prove that an Archie change is maintainable, owned, reachable, reversible, and simpler than the code it replaces. Use when designing, implementing, reviewing, or accepting a feature, refactor, configuration path, registry, adapter, interface, or cleanup; when tests are green but code quality is uncertain; or when duplicate paths, dead code, one-use abstractions, supersession, production wiring, or concern ownership may be hidden.
---

# Archie Technical Accountability

Accept behavior only after proving both correctness and maintainability. Treat a
green test as evidence about the tested seam, never as proof that production
uses the seam or that the design has one coherent owner.

Use imperative runbook voice in the resulting review. Mark every conclusion as
`proven`, `candidate`, or `open`. Do not convert absence of evidence into proof.

## Route the work

Load the sibling skill named for each responsibility:

| Need | Load instead or next |
| --- | --- |
| Select domains, boundaries, ownership, or a migration shape | `archie-architecture-planning-campaign` first |
| Traverse an unfamiliar area, its AST, consumers, runtime paths, or history | `archie-codebase-discovery` first |
| Classify the change and determine required gates and reviewers | `archie-change-control` |
| Prove tests, lint, formatting, race behavior, or acceptance thresholds | `archie-validation-and-qa` |
| Triage an active failure or unexplained runtime symptom | `archie-debugging-playbook` first |
| Reconstruct a historical failure, revert, rejected fix, or settled dead end | `archie-failure-archaeology` first |

Do **not** use this skill as:

- a substitute for architecture planning before ownership is decided;
- a substitute for deep codebase discovery or AST-aware call-path analysis;
- a debugging playbook for an active incident;
- permission to implement before the repository's change-control and TDD rules;
- a reason to keep an obsolete path "for safety"; define a real rollback and
  deletion plan instead.

## Define the evidence vocabulary

- **Owner**: the one domain or capability accountable for a behavior, its
  vocabulary, state, rules, settings, and required service contracts.
- **Source of truth**: the only authoritative implementation or mutable record
  for one meaning. Adapters may translate it; they must not reimplement it.
- **Consumer**: production code, test code, generated code, or an external
  process that calls, imports, decodes, registers, or depends on the subject.
- **Production wiring**: the executable path from a real `cmd/*` entry point,
  configuration source, or inbound adapter through composition to an observable
  effect.
- **Parallel path**: another implementation that can satisfy the same intent or
  mutate the same state, even when names and types differ.
- **One-shot abstraction**: an interface, wrapper, registry, service, or helper
  introduced for one call or implementation without owning policy, lifecycle,
  translation, isolation, or a stable boundary.
- **Deletion criterion**: an observable condition that permits removing a
  legacy or compatibility path.
- **Archaeology**: history evidence explaining why a seam exists, what it
  replaced, and which failed approach must not be repeated.

## Produce one accountability record

Attach one record to the change artifact selected by
`archie-change-control`. Do not create an ad hoc TODO document. Complete the
record before implementation, then update it from the final diff.

### Header

| Field | Required content |
| --- | --- |
| Requested outcome | One behavior stated without naming an implementation |
| Change class | Classification from `archie-change-control` |
| Owner | One current owner and, during migration, one approved target owner |
| Source of truth | Current authoritative code/state and the post-change authority |
| Scope | Packages, entry points, settings, persisted records, and contracts touched |
| Accountable actors | Implementer and independent adversarial reviewer |
| Status | `proven`, `candidate`, or `open` |

### Consumer and path inventory

Record every row; use `none proven` rather than leaving a cell blank.

| Consumer/path | Entry | Dispatch or translation | Authority called | Effect | Evidence |
| --- | --- | --- | --- | --- | --- |
| Production | command, channel, worker, poller, or startup | exact functions | exact owner | state/output | file:line and test |
| Alternate process | subprocess, NATS worker, container, or RPC | exact contract | exact owner | state/output | file:line and test |
| Dynamic | registry, plugin, Yaegi, callback, reflection, or config | registration/load seam | exact owner | state/output | file:line and test |
| Tests | focused, integration, contract, race | seam exercised | fake or real authority | assertion | test name |
| Generated/docs | generator or schema consumer | source contract | owner | artifact | generator path |

Block acceptance if a production row stops at a constructor, parser, registry,
schema, test fake, or helper subcommand without reaching the running
composition path.

## Run the accountability workflow

### 1. Freeze the behavioral claim

Write one sentence:

> When `<real entry>` receives `<input>`, `<owner>` performs `<behavior>` and
> produces `<observable result or failure>`.

Reject claims such as "add manager," "add interface," or "support YAML." Those
name an implementation, not an outcome.

List the behavior that must remain unchanged. Include persistence, errors,
timeouts, concurrency, process boundaries, authorization, and compatibility
when applicable.

### 2. Establish the owner and source of truth

Read the approved architecture requirements and the live code. Keep the two
states distinct. As of 2026-07-28:

- `docs/prds/architecture/organisation.md` defines the approved target
  organization.
- `docs/prds/architecture/dependencies-and-contracts.md` defines strict target
  dependency direction and contract ownership.
- `docs/prds/architecture/configuration.md` requires dissolution of the current
  shared `internal/config` model; this is an approved target, not completed
  live behavior.
- `docs/prds/architecture/package-review.md` requires current-state research
  before package decisions.

Answer:

1. Which cohesive job owns this behavior?
2. Which mutable record or function is authoritative now?
3. Which authority will remain after the change?
4. Is another package allowed to mutate the same meaning?
5. Does the proposed location reduce or increase required knowledge?

Block if the answer is "shared," "config," "utils," "manager," or "registry"
without naming the behavior owner. Mechanics may be shared; domain meaning may
not.

### 3. Inventory all consumers and call paths

Start with text and package evidence:

```bash
rg -n '\bMessageEvent\b' --glob '*.go' .
rg -n '\bMessageEvent\b' --glob '*.go' --glob '!**/*_test.go' .
go doc github.com/samcharles93/archie-core/internal/gateway.MessageEvent
go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./...
go list -deps ./cmd/archied ./cmd/archie-agent
go -C tools list ./...
```

Replace `MessageEvent` and its package path with the subject under review.
Separate production, test, generated, and documentation hits. An imported
package proves only package reachability, not function reachability. The
`tools` directory is a nested Go module, so root `go list ./...` does not include
it; inspect it separately whenever the change touches generation tooling.

Trace each real path in both directions:

1. Start at `cmd/archied`, `cmd/archie-agent`, or an inbound adapter.
2. Follow construction, registration, translation, dispatch, and callbacks.
3. Continue through interfaces to every implementation.
4. End at persisted state, outbound I/O, or a user-visible result.
5. Walk backward from the effect to find alternate writers.

Use `archie-codebase-discovery` for AST traversal. `rg` cannot resolve
interface dispatch, method expressions, callbacks, reflection, generated
registries, Yaegi-loaded code, or config-selected implementations. Never declare
code dead from text search alone.

### 4. Detect duplicate, parallel, and superseded paths

Search by domain language and effects, not only symbol names:

```bash
rg -n -i 'message|conversation|session|reply|acknowledg' internal cmd --glob '*.go'
rg -n -i 'insert|update|put|delete|transition|publish|enqueue' internal cmd --glob '*.go'
rg -n 'TODO|FIXME|HACK|XXX' internal cmd docs --glob '*.go' --glob '*.md'
```

Narrow those searches to the changed behavior. Compare:

- inputs and outputs;
- state read and written;
- retry, timeout, error, and fallback semantics;
- authorization and identity;
- startup, shutdown, and health behavior;
- process-specific implementations;
- test doubles that may mirror the implementation rather than the contract.

Complete this supersession table:

| Existing path | Proposed relation | Production caller | Parity needed | Deletion criterion |
| --- | --- | --- | --- | --- |
| exact path | retain, adapt, merge, supersede, or delete | exact entry | named semantics | observable condition |

Block if new work overlaps an existing path without one explicit relation.
Block any indefinite "temporary" compatibility path. A dual path requires a
named cutover, authoritative side, observation method, and deletion criterion.

### 5. Prove production wiring

For every feature, prove this complete chain:

```text
real input -> loader/adapter -> application composition -> owner
           -> side effect -> observable success/failure
```

Require all of:

- a source citation at each handoff;
- one focused behavior test;
- one composition, integration, or contract test that crosses the handoff;
- a negative test proving the old/unconfigured/failed path does not masquerade
  as success;
- an operator- or caller-visible failure when silent fallback would hide loss.

A decode test proves decoding. A constructor test proves construction. A
registry test proves registration. None proves production wiring.

For protocols, validate against an independent implementation, published
fixture, or official SDK. A client tested only against a server using the same
framing/parser proves self-consistency, not interoperability.

### 6. Hold configuration to concern ownership

Classify every behavioral value as the approved configuration requirements
direct:

| Class | Meaning |
| --- | --- |
| Invariant | Changing it changes a domain concept or wire meaning |
| Runtime setting | An installation, identity, domain, plugin, or deployment may choose it |
| Policy value | It controls permission, limits, routing, retry, protection, or gates |
| Derived value | It is computed and not independently mutable |
| Runtime state | A domain command changes it during operation |

For each new or changed setting, require:

- one domain/plugin owner;
- type, units, constraints, safe bounds, default, and derivation;
- startup-only, reloadable, or restart-required semantics;
- scope: installation, identity, repository, workflow, channel, or plugin;
- secret/sensitive/reportable classification;
- external DTO decoding separated from the owner's runtime settings;
- composition translation and validation;
- an exact production consumer;
- compatibility and last-known-good behavior;
- documentation and tests for invalid, omitted, and explicit values;
- removal of any superseded field or a bounded migration plan.

Do not deepen `internal/config` merely because it is convenient. If migration
timing forces a compatibility edit there, label it transitional and route the
ownership decision through `archie-architecture-planning-campaign`.

### 7. Challenge every abstraction and dead-path candidate

Use this table during manual review:

| Signal | Required challenge | Accept only when |
| --- | --- | --- |
| Interface has one implementation and one consumer | Why is dynamic substitution required? | It owns a consumer need, stable boundary, or useful test seam |
| Wrapper delegates one operation | What policy, translation, lifecycle, or isolation does it own? | The added responsibility is named and tested |
| Manager/registry exists without lifecycle wiring | Who starts, uses, observes, and stops it? | Production composition and failure paths are proven |
| Exported type appears only in tests/generation | Is it a contract or an abandoned future path? | A real external consumer is cited or it is removed/labeled candidate |
| Config field is decoded but never consumed | What running behavior changes? | Exact constructor/operation use is cited and tested |
| Branch/fallback has no constructible input | What reaches it? | A real path and negative test exist |
| Replacement leaves old reads/writes | Which side is authoritative? | Cutover and deletion criteria are explicit |
| Helper exists for one call | Would direct code be clearer? | The helper names a stable concept or removes real duplication |

Do not keep an abstraction for hypothetical reuse. Do not delete a dynamic path
until AST, registry, plugin, configuration, and runtime evidence are checked.

### 8. Justify interfaces and locations

Require a one-paragraph location decision:

1. Name the behavior and owner.
2. Name the dependencies it may import.
3. Name the smallest contract it needs.
4. State why the contract belongs with the domain/capability that defines its
   meaning.
5. State why infrastructure representations do not leak through it.
6. State what future change should remain local to this package.

Reject ceremonial `entities`, `repositories`, or `services` layers; the
approved organization document prohibits them. Reject an interface that merely
duplicates a concrete API without narrowing authority.

### 9. Review readability and artfulness

Treat artful code as code with an obvious owner, a small surface, explicit
consequences, and no cleverness tax. Review every changed non-test file and
answer:

- Can a zero-context mid-level engineer explain the behavior without traversing
  unrelated domains?
- Do names use the domain vocabulary rather than `helper`, `manager`, `data`,
  `thing`, or transport vocabulary?
- Does each function perform one coherent step at the right abstraction level?
- Are error, rollback, cancellation, and shutdown paths adjacent and visible?
- Do comments explain why a constraint exists rather than narrate syntax?
- Can a reader distinguish authority from adapter, DTO, projection, and cache?
- Did the change remove more accidental concepts than it added?

Block if the reviewer cannot summarize owner, input, decision, effect, and
failure in five plain sentences. Simplify before adding comments that explain a
convoluted path.

### 10. Prove reversibility and deletion

Do not use retained dead code as rollback. Record:

| Concern | Required proof |
| --- | --- |
| Source rollback | Known-good release/change and behavioral compatibility |
| Data rollback | Migration direction, backup, forward/backward readability, and loss risk |
| Runtime rollback | Health signal, observation period, bounded retry, and last-known-good behavior |
| Compatibility | Authoritative path, expiry/cutover, and removal owner |
| Deletion | No production/test/generated consumers, parity evidence, config/docs removal, regression guard |

The approved recovery requirements are in
`docs/prds/architecture/safe-change-and-recovery.md`; exact runtime-supervision
mechanics remain under design as of 2026-07-28. Label unimplemented mechanics
`open`; do not describe them as available.

### 11. Perform post-change archaeology

Re-run discovery against the final diff:

```bash
git diff --name-status
git diff --stat
git log --all --date=short --oneline -- internal/gateway
git log -S'MessageEvent' --all --oneline -- internal/gateway
git blame internal/gateway/messageevent.go
rg -n '\bMessageEvent\b' --glob '*.go' .
```

Replace the example path and symbol. Record:

- which historical decision the change preserves or overturns;
- what old path became obsolete;
- what was actually deleted;
- what remains transitional and why;
- which regression test or architecture check prevents reintroduction;
- which uncertainty still requires production evidence.

Then route validation through `archie-validation-and-qa` and acceptance through
`archie-change-control`. The implementer must not self-certify the adversarial
maintainability review.

## Apply blocking acceptance rules

Return `BLOCK` when any row lacks evidence:

| Blocking proof | Minimum evidence |
| --- | --- |
| Owner | One behavior owner; current and target separated |
| Source of truth | One authoritative implementation/record after cutover |
| Consumers | Production, alternate-process, dynamic, test, and generated paths classified |
| Wiring | Real entry-to-effect chain plus integration/contract evidence |
| Supersession | Every overlap marked retain/adapt/merge/supersede/delete |
| Deletion | Objective criteria for every legacy/compatibility path |
| Configuration | Owner, translation, validation, consumer, reload, and compatibility semantics |
| Boundary | Interface and package location justified |
| Failure semantics | Errors, cancellation, rollback, health, and observability reviewed |
| Readability | Independent reviewer can state the path plainly |
| Reversibility | Source, state, runtime, and compatibility rollback addressed |
| Archaeology | Relevant history and rejected paths recorded |
| Validation | Required evidence from `archie-validation-and-qa` |

Return `ACCEPT` only when all rows are proven and the fresh adversarial reviewer
reports no surviving blocking or important maintainability finding. Return
`CANDIDATE` for an explicitly experimental seam that is not production-wired;
never call it shipped or complete.

## Learn from repository incidents

Treat these as evidence patterns, not legends:

| Incident | Repository evidence | Accountability lesson |
| --- | --- | --- |
| Richer message path exists beside the live path | `internal/gateway/messageevent.go`; `docs/prds/architecture/messaging-and-work-intake.md` states that adapters use `gateway.Message` while richer `MessageEvent` is unused; production-only `rg` finds no concrete live adapter use as of 2026-07-28 | Tests and a rich contract do not establish adoption; inventory callers and delete or cut over the competing contract |
| Feature YAML loader is not the daemon loader | `internal/config/feature_config.go` defines `LoadDir`; its hits are tests, while `cmd/archied/main.go` calls `config.LoadOverlay` as of 2026-07-28 | Parsed and heavily tested configuration can still be unwired |
| Indexing arrived as an isolated feature/settings island | `ddf624d` added indexing; `f934ef5` records undiscoverable settings and silent degradation; as of 2026-07-28 `cmd/archied` uses indexing only in the helper subcommand and does not construct `indexing.Manager` | Prove configuration surface, running composition, and observable failure separately |
| Mirrored MCP server validated the same wrong protocol | Before `dda4cde`, the test helper in `internal/tools/mcp/transport_test.go` called the same `readMessage`/`writeMessage` functions as the client; `dda4cde` replaced LSP-style `Content-Length` with MCP newline-delimited JSON for official-SDK interoperability | A mirrored fake can preserve a shared misconception; require independent contract evidence |
| A green tool registry needed eight adversarial fixes | `9f6c36c` records deadlock, shared-reference mutation, shallow copies, incomplete AST literal parsing, abort-on-first-error, and an over-broad register-call matcher | Test reentrancy, ownership/aliasing, partial failure, and AST false positives—not only happy-path registration |
| Mechanical cleanup broke a focused behavior | `308c199` changed `exec.Command("echo", "wrapped")` to `exec.CommandContext(ctx, "sh", "-c", "echo", "wrapped")`; `wrapped` became shell `$0`, and `TestRunWrapsExternalCommand` returns only `"\n"` as of 2026-07-28. The same commit added 237 tracked `docs-2/node_modules` symlinks | A broad cleanup needs semantic diff review, artifact review, and focused tests; "mechanical" is not a lower evidence class |
| Autofix nearly changed timeout semantics | `9b44bac` records `golangci-lint --fix` changing an assignment to a shadowing declaration in `internal/memory/manager.go`; it was manually restored | Inspect every automated rewrite; compilation is necessary but does not prove preserved semantics |

Re-verify volatile status before reusing any "as of" claim.

## Provenance and maintenance

- Re-verify architecture status: `sed -n '1,180p' docs/prds/architecture/{package-review,organisation,configuration,dependencies-and-contracts,safe-change-and-recovery}.md`
- Re-verify live entry points and config loaders: `rg -n 'func (main|run)\b|config\.(Load|LoadOverlay|LoadDir)\(' cmd internal --glob '*.go'`
- Re-verify message-path consumers: `rg -n '\b(Message|MessageEvent)\b' cmd internal --glob '*.go'`
- Re-verify indexing wiring: `rg -n 'indexing\.|IndexingConfig|cfg\.Indexing' cmd internal --glob '*.go'`
- Re-verify incident commits: `git show -s --format='%H%n%B' 9f6c36c dda4cde f934ef5 9b44bac 308c199`
- Re-verify cleanup regression: `go test ./internal/skillscript -run '^TestRunWrapsExternalCommand$' -count=1`
- Re-verify repository gates: `sed -n '1,140p' Taskfile.yml && sed -n '90,145p' CLAUDE.md`
- Re-verify this skill structurally with the skill validator available in the active agent environment.
