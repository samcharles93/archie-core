# Archie durable-writing templates

Use one template at a time. Replace every angle-bracket prompt. Remove unused
sections. Keep facts, decisions, and open questions visibly separate.

## Contents

- [Architecture decision record](#architecture-decision-record)
- [Migration plan and parity matrix](#migration-plan-and-parity-matrix)
- [Incident or dead-end record](#incident-or-dead-end-record)
- [Feature ownership and deprecation record](#feature-ownership-and-deprecation-record)
- [Operational runbook](#operational-runbook)
- [Documentation change review](#documentation-change-review)
- [PRD: open capability design with multi-agent execution](#prd-open-capability-design-with-multi-agent-execution)

## Architecture decision record

```markdown
# <Decision title>

**Status:** Proposed | Approved | Superseded by <link>
**Date:** YYYY-MM-DD
**Owner:** <domain or application boundary>
**Record of authority:** <this document or existing focused document>
**Change-control reference:** <issue/review identifier>

## Decision

<One unambiguous normative statement.>

## Problem and scope

<Describe the problem, the included behavior, and explicit non-goals.>

## Current evidence

| Claim              | Evidence                              |
| ------------------ | ------------------------------------- |
| <CURRENT behavior> | `<path>` — `<symbol/test/config key>` |

## Constraints and invariants

- <Invariant and where it is enforced>

## Consumers and boundaries

| Consumer                      | Contract used                  | Failure effect |
| ----------------------------- | ------------------------------ | -------------- |
| <entry point/package/process> | <interface/command/event/data> | <effect>       |

## Options considered

| Option   | Benefits  | Costs and risks | Evidence             | Result              |
| -------- | --------- | --------------- | -------------------- | ------------------- |
| <option> | <benefit> | <cost>          | <measurement/source> | Accepted / Rejected |

## Consequences

<State new responsibilities, dependency direction, state ownership, and
operational effects.>

## Supersession and deletion

- Superseded path: `<path/symbol>` or none
- Compatibility owner: <owner or none>
- Deletion gate: <observable proof>
- Architecture test: <test or OPEN>

## Rollback

<State how code, behavior, and data return safely.>

## Open questions

| Question        | Owner   | Evidence required | Decision deadline/gate |
| --------------- | ------- | ----------------- | ---------------------- |
| <OPEN question> | <owner> | <measurement>     | <gate>                 |

## Validation

- `<exact command>` — expected <observable result>
```

## Migration plan and parity matrix

```markdown
# <Capability> migration plan

**Status:** Proposed | Approved | In progress | Complete
**Date:** YYYY-MM-DD
**Owner:** <domain>
**Source authority:** <approved target decision>
**Current baseline:** <code paths and runtime composition>

## Outcome

<Describe the final single authoritative path and what becomes easier to
change.>

## Current-path inventory

| Operation   | Entry point     | Owner today  | State/data touched | Consumers   | Disposition                   |
| ----------- | --------------- | ------------ | ------------------ | ----------- | ----------------------------- |
| <operation> | `<path.Symbol>` | <owner/none> | <state>            | <consumers> | Keep / Adapt / Merge / Delete |

## Parity matrix

| Behavior   | Current evidence    | Target owner/path | Required parity | Test or observation | Status |
| ---------- | ------------------- | ----------------- | --------------- | ------------------- | ------ |
| <behavior> | `<test/symbol/log>` | <target>          | <exact result>  | `<command>`         | OPEN   |

Include success, validation failure, retry, cancellation, crash recovery,
authorization, persistence, event emission, and shutdown where applicable.

## Dependency and data movement

| From     | To       | Adapter/backfill | Consistency rule | Rollback |
| -------- | -------- | ---------------- | ---------------- | -------- |
| <source> | <target> | <mechanism>      | <invariant>      | <method> |

## Slices and gates

### Slice 1 — <small vertical outcome>

- Change: <bounded change>
- Expected evidence: <number/result predicted before implementation>
- Command: `<exact command>`
- Pass: <observable result>
- Branch if not: <diagnostic route>
- Legacy path after slice: Active / Delegating / Read-only / Removable

<Repeat for each independently reviewable slice.>

## Cutover

1. <Prepare compatibility/data>
2. <Change production composition>
3. <Observe explicit health/parity signals>
4. <Disable legacy entry>
5. <Delete only after the deletion gate>

## Deletion gates

| Legacy path     | Replacement proof | Consumer-zero proof | Data proof | Removal test |
| --------------- | ----------------- | ------------------- | ---------- | ------------ |
| `<path.Symbol>` | <proof>           | <search/inventory>  | <proof>    | `<command>`  |

## Rollback and stop conditions

- Roll back when: <condition>
- Restore: <code/config/data sequence>
- Preserve for diagnosis: <artifacts>

## Remaining open decisions

| Decision | Owner   | Evidence needed | Blocks          |
| -------- | ------- | --------------- | --------------- |
| <OPEN>   | <owner> | <evidence>      | <slice/cutover> |
```

## Incident or dead-end record

````markdown
# <Symptom or rejected approach>

**Status:** Resolved | Mitigated | Rejected | Reopened
**Observed:** YYYY-MM-DD
**Last verified:** YYYY-MM-DD
**Owner:** <capability>
**Related changes:** <commit/issue/PR identifiers>

## Symptom and impact

<State the observable failure, affected path, and cost. Do not begin with the
proposed fix.>

## Reproduction

```text
<exact non-secret command or event sequence>
```

Expected: <result>
Observed: <result>

## Investigation chronology

| Time/order | Hypothesis   | Prediction             | Evidence             | Result               |
| ---------- | ------------ | ---------------------- | -------------------- | -------------------- |
| 1          | <hypothesis> | <expected observation> | `<command/log/test>` | Confirmed / Rejected |

## Root cause

<Name the owning code path and causal mechanism. Separate cause from trigger.>

## Attempted or rejected fixes

| Fix        | Why plausible | Evidence against it     | Status              |
| ---------- | ------------- | ----------------------- | ------------------- |
| <approach> | <reason>      | <test/runtime evidence> | Rejected / Reverted |

## Resolution and invariant

- Resolution: <what changed>
- Preserved invariant: <what must not regress>
- Regression test: `<test name/path>`
- Operational signal: <log/metric/state>

## Reopen condition

<State the evidence that would justify revisiting the settled result.>
````

## Feature ownership and deprecation record

```markdown
# <Feature/capability> ownership

**Status:** Current | Migrating | Deprecated | Removed
**Date:** YYYY-MM-DD
**Behavior owner:** <domain>
**State owner:** <domain/repository>
**Composition owner:** <application boundary>

## Responsibility and non-responsibilities

- Owns: <cohesive behavior>
- Does not own: <adjacent concerns>

## Complete path

| Layer       | Path/symbol     | Responsibility |
| ----------- | --------------- | -------------- |
| Entry       | `<path.Symbol>` | <role>         |
| Domain      | `<path.Symbol>` | <role>         |
| State       | `<path.Symbol>` | <role>         |
| Adapter     | `<path.Symbol>` | <role>         |
| Composition | `<path.Symbol>` | <role>         |

## Consumers

| Consumer   | Contract               | Configuration | Test evidence |
| ---------- | ---------------------- | ------------- | ------------- |
| <consumer> | <interface/event/data> | <key or none> | `<test>`      |

## Invariants and failures

| Invariant   | Enforcement     | Failure behavior | Test     |
| ----------- | --------------- | ---------------- | -------- |
| <invariant> | `<path.Symbol>` | <result>         | `<test>` |

## Duplicate and superseded paths

| Path            | Same operation?            | Disposition                        | Deletion gate |
| --------------- | -------------------------- | ---------------------------------- | ------------- |
| `<path.Symbol>` | Yes / No, because <reason> | Keep / Delegate / Migrate / Delete | <proof>       |

## Change checklist

- [ ] Trace every entry point and consumer.
- [ ] Decide whether the feature deprecates earlier work.
- [ ] Keep one owner for mutable state.
- [ ] Add failure-path and architecture tests.
- [ ] Prove the replacement before deleting the old path.
- [ ] Update generated reference from owned definitions.
```

## Operational runbook

```markdown
# Operate <service/capability>

**Status:** Current
**Last verified:** YYYY-MM-DD
**Environment:** <local/staging/production>
**Owner:** <operator/capability>
**External sources:** <host/config/dashboard; no secrets>

## Purpose and safety boundary

<State what this runbook operates and what requires separate authorization.>

## Preconditions

- <access/tool/config precondition>
- Secret names only; never secret values

## Start or change

1. Run `<exact command>`.
2. Expect `<observable output/state>` within `<bounded interval>`.
3. If `<alternate result>`, stop and route to `<diagnostic branch>`.

## Health and outputs

| Signal   | Location/command | Healthy result | Unhealthy branch |
| -------- | ---------------- | -------------- | ---------------- |
| <signal> | `<command/path>` | <result>       | <action>         |

## Rollback

1. <Restore previous code/config/data>
2. Run `<health command>`.
3. Preserve <logs/artifacts> for incident review.

## Known failure modes

| Symptom   | First measurement | Likely seam | Next runbook        |
| --------- | ----------------- | ----------- | ------------------- |
| <symptom> | `<command>`       | <boundary>  | <sibling skill/doc> |

## Provenance

- External fact observed by <who/how> on YYYY-MM-DD.
- Re-check: `<one-line command>`
```

## Documentation change review

```markdown
# Documentation review: <scope>

**Date:** YYYY-MM-DD
**Reviewer:** <name/session>
**Changed authority:** <path or none>

## Classification

| Claim/change | State                                                                | Owner   | Evidence                     |
| ------------ | -------------------------------------------------------------------- | ------- | ---------------------------- |
| <claim>      | CURRENT / APPROVED TARGET / OPEN / HISTORICAL / GENERATED / EXTERNAL | <owner> | `<path.Symbol/test/command>` |

## Findings

| Severity                     | Location       | Problem   | Evidence   | Required correction |
| ---------------------------- | -------------- | --------- | ---------- | ------------------- |
| Blocking / Important / Minor | <heading/path> | <problem> | <evidence> | <fix>               |

## Authority and duplication checks

- [ ] One record of authority owns each claim.
- [ ] Current and target behavior are separated.
- [ ] Open questions remain visibly open.
- [ ] Historical material is not used normatively.
- [ ] Generated output was not hand-edited.
- [ ] Symbols and headings replace durable line-number citations.

## Maintainability checks

- [ ] Responsibility, owner, boundaries, consumers, and invariants are explicit.
- [ ] Superseded paths and deletion gates are named.
- [ ] Evidence, rollback, and unresolved questions are actionable.
- [ ] A zero-context engineer can locate the complete feature path.

## Validation evidence

| Command           | Expected | Observed |
| ----------------- | -------- | -------- |
| `<exact command>` | <result> | <result> |

## Residual uncertainty

<List unverified external facts, unavailable environments, or still-open
decisions. Never convert them into implied acceptance.>
```

## PRD: open capability design with multi-agent execution

For `docs/prds/*.md` — CLAUDE.md's "no settled design exists" doc. The point
of this template is that a cheaper/faster model can implement straight from
it without re-deriving architecture: every decision the epic left open gets
made here, not deferred again, and every file an implementer touches is
named before any bead is picked up. `docs/prds/image-capability-contract.md`
is the worked example this template was extracted from — read it once for
the shape before writing a new PRD from a blank page.

The three failure modes this template exists to prevent:

- **Deferred decisions posing as scope.** "Hosted provider implementation is
  a separate bead" is not a design decision, it's a missing one. If an open
  question blocks an implementer, decide it here, in the PRD — not "this bead
  will decide it later," which is how the same question gets re-opened by
  every bead that touches it.
- **Assumed gaps.** Before writing a section for "what's missing," grep for
  it. `image-capability-contract.md`'s delivery section exists mostly because
  the pipeline it needed already shipped for video — the section reports
  that, instead of specifying new plumbing nothing required.
- **Task lists a model can't execute standalone.** A subtask is not "wire the
  delivery path"; it's the file, the interface it must satisfy, and the
  existing call site that already proves the pattern (`internal/tools/minimax/tool.go`,
  not "a tool like the video one").

```markdown
# <Capability> capability design

Epic: `<bd id>` / GitHub `#<epic number>` ("<epic title>").

<One paragraph: what shipped already and is settled (link its section
below), what this document newly decides, and what stays explicitly
out of scope. If a prior narrower doc already covers part of this
capability, say so and extend it — do not start a second document for
the same epic.>

## Problem

<What's missing today, with the exact "grep and found nothing" evidence,
not an assertion.>

## Design

<The settled contract/type shape. Code blocks over prose where a type
signature says more than describing it would.>

## <Sub-feature N> (`#<issue>`)

One section per sibling bead the epic lists as separate scope. Each one:

- **Decision, not a deferral.** If the epic or a prior doc left this open,
  decide it here and say why in one paragraph, citing what's actually in
  the tree (a similar client already in the codebase, an existing config
  convention, an interface already proven by another feature). "Decide and
  record which at that bead's own design-doc step" is the sentence this
  template exists to stop someone from writing twice.
- **Call site**, named exactly: `<package/file>`, the function/type it adds
  or extends, and the existing analogous call site it follows (never "a
  handler like the others" — name the actual one).
- **Reuse check.** Grep before specifying new plumbing. State what already
  exists and what's actually new, the way a delivery pipeline already
  proven by one media type doesn't need re-inventing for a second.
- **Error/failure mapping**, as a table, when the sub-feature crosses a
  provider/backend boundary: condition → contract error, so an implementer
  never invents a new sentinel mid-way through.
- **Testing.** What's fakeable vs. what needs a real backend, and which
  gate (`task check` vs. a documented manual smoke command) each belongs to.

## Call site inventory

| concern   | file     | change                                                             |
| --------- | -------- | ------------------------------------------------------------------ |
| <concern> | `<path>` | done (`#<issue>`) / new (`#<issue>`) / none — already handles this |

Every file any sub-feature touches, old or new, in one table — so an
implementer greps this instead of re-deriving it, and a reviewer can tell
at a glance whether a PR touched something the design didn't name.

## Execution: multi-agent team breakdown

One row per sub-feature section above. This is what turns the design into
something a `/council`-style review or a cheaper/faster implementer model
can run against directly instead of needing the full design re-explained.

| sub-feature | issue      | implementer scope                             | suggested council lenses   | why                                                                                                                                                                                                                                                                                                                                                      |
| ----------- | ---------- | --------------------------------------------- | -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| <name>      | `#<issue>` | <the call-site inventory rows this bead owns> | `lens-<key>`, `lens-<key>` | <one clause: what that lens actually catches for this slice — e.g. lens-contract for a new provider error-mapping table, lens-operator for a backend-down failure path, lens-deletionist when the section's whole point is "don't rebuild what exists", lens-boundary for a new domain/infra split, lens-maintainer for a new user-facing state machine> |

Pick lenses from the five in `.claude/workflows/council.js`
(`boundary`, `contract`, `deletionist`, `operator`, `maintainer`) by what the
slice actually risks getting wrong, not all five by default — a
generate-and-poll HTTP client mostly risks failure-mode gaps
(`lens-operator`), not domain-boundary violations. Run `/council` with
`--lenses <picked>` once the implementation is gate-clean, before opening
the PR for human review, same as any other open-decision residue at the
end of a session.

An implementer working one row needs only: this doc's matching sub-feature
section, the call-site inventory, and the linked issue's acceptance
criteria. It should not need the rest of the epic's history — that's the
signal the PRD is thorough enough to hand to a faster/cheaper model.

## File and link the beads

For each sub-feature issue already filed against the epic (or newly filed
if the epic didn't pre-file its sub-issues):

- Issue body links back to this doc's matching section by heading, not by
  restating the design — `bd update <id> --description` and, if the issue
  also lives on the forge, keep both current (check whether this repo's
  sync command actually propagates a description edit before trusting it
  updated both; direct edit on both sides is the fallback).
- Issue keeps its own acceptance criteria as the authoritative "done"
  check — the PRD section is how, the issue's criteria are whether.
- Add the suggested lenses from the Execution table as a line on the issue
  itself, so picking it up needs no round-trip back to this doc to know
  which `/council` lenses to run before it's done.
```
