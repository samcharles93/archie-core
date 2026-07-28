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

## Architecture decision record

````markdown
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

| Claim | Evidence |
|---|---|
| <CURRENT behavior> | `<path>` — `<symbol/test/config key>` |

## Constraints and invariants

- <Invariant and where it is enforced>

## Consumers and boundaries

| Consumer | Contract used | Failure effect |
|---|---|---|
| <entry point/package/process> | <interface/command/event/data> | <effect> |

## Options considered

| Option | Benefits | Costs and risks | Evidence | Result |
|---|---|---|---|---|
| <option> | <benefit> | <cost> | <measurement/source> | Accepted / Rejected |

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

| Question | Owner | Evidence required | Decision deadline/gate |
|---|---|---|---|
| <OPEN question> | <owner> | <measurement> | <gate> |

## Validation

- `<exact command>` — expected <observable result>
````

## Migration plan and parity matrix

````markdown
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

| Operation | Entry point | Owner today | State/data touched | Consumers | Disposition |
|---|---|---|---|---|---|
| <operation> | `<path.Symbol>` | <owner/none> | <state> | <consumers> | Keep / Adapt / Merge / Delete |

## Parity matrix

| Behavior | Current evidence | Target owner/path | Required parity | Test or observation | Status |
|---|---|---|---|---|---|
| <behavior> | `<test/symbol/log>` | <target> | <exact result> | `<command>` | OPEN |

Include success, validation failure, retry, cancellation, crash recovery,
authorization, persistence, event emission, and shutdown where applicable.

## Dependency and data movement

| From | To | Adapter/backfill | Consistency rule | Rollback |
|---|---|---|---|---|
| <source> | <target> | <mechanism> | <invariant> | <method> |

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

| Legacy path | Replacement proof | Consumer-zero proof | Data proof | Removal test |
|---|---|---|---|---|
| `<path.Symbol>` | <proof> | <search/inventory> | <proof> | `<command>` |

## Rollback and stop conditions

- Roll back when: <condition>
- Restore: <code/config/data sequence>
- Preserve for diagnosis: <artifacts>

## Remaining open decisions

| Decision | Owner | Evidence needed | Blocks |
|---|---|---|---|
| <OPEN> | <owner> | <evidence> | <slice/cutover> |
````

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

| Time/order | Hypothesis | Prediction | Evidence | Result |
|---|---|---|---|---|
| 1 | <hypothesis> | <expected observation> | `<command/log/test>` | Confirmed / Rejected |

## Root cause

<Name the owning code path and causal mechanism. Separate cause from trigger.>

## Attempted or rejected fixes

| Fix | Why plausible | Evidence against it | Status |
|---|---|---|---|
| <approach> | <reason> | <test/runtime evidence> | Rejected / Reverted |

## Resolution and invariant

- Resolution: <what changed>
- Preserved invariant: <what must not regress>
- Regression test: `<test name/path>`
- Operational signal: <log/metric/state>

## Reopen condition

<State the evidence that would justify revisiting the settled result.>
````

## Feature ownership and deprecation record

````markdown
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

| Layer | Path/symbol | Responsibility |
|---|---|---|
| Entry | `<path.Symbol>` | <role> |
| Domain | `<path.Symbol>` | <role> |
| State | `<path.Symbol>` | <role> |
| Adapter | `<path.Symbol>` | <role> |
| Composition | `<path.Symbol>` | <role> |

## Consumers

| Consumer | Contract | Configuration | Test evidence |
|---|---|---|---|
| <consumer> | <interface/event/data> | <key or none> | `<test>` |

## Invariants and failures

| Invariant | Enforcement | Failure behavior | Test |
|---|---|---|---|
| <invariant> | `<path.Symbol>` | <result> | `<test>` |

## Duplicate and superseded paths

| Path | Same operation? | Disposition | Deletion gate |
|---|---|---|---|
| `<path.Symbol>` | Yes / No, because <reason> | Keep / Delegate / Migrate / Delete | <proof> |

## Change checklist

- [ ] Trace every entry point and consumer.
- [ ] Decide whether the feature deprecates earlier work.
- [ ] Keep one owner for mutable state.
- [ ] Add failure-path and architecture tests.
- [ ] Prove the replacement before deleting the old path.
- [ ] Update generated reference from owned definitions.
````

## Operational runbook

````markdown
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

| Signal | Location/command | Healthy result | Unhealthy branch |
|---|---|---|---|
| <signal> | `<command/path>` | <result> | <action> |

## Rollback

1. <Restore previous code/config/data>
2. Run `<health command>`.
3. Preserve <logs/artifacts> for incident review.

## Known failure modes

| Symptom | First measurement | Likely seam | Next runbook |
|---|---|---|---|
| <symptom> | `<command>` | <boundary> | <sibling skill/doc> |

## Provenance

- External fact observed by <who/how> on YYYY-MM-DD.
- Re-check: `<one-line command>`
````

## Documentation change review

````markdown
# Documentation review: <scope>

**Date:** YYYY-MM-DD
**Reviewer:** <name/session>
**Changed authority:** <path or none>

## Classification

| Claim/change | State | Owner | Evidence |
|---|---|---|---|
| <claim> | CURRENT / APPROVED TARGET / OPEN / HISTORICAL / GENERATED / EXTERNAL | <owner> | `<path.Symbol/test/command>` |

## Findings

| Severity | Location | Problem | Evidence | Required correction |
|---|---|---|---|---|
| Blocking / Important / Minor | <heading/path> | <problem> | <evidence> | <fix> |

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

| Command | Expected | Observed |
|---|---|---|
| `<exact command>` | <result> | <result> |

## Residual uncertainty

<List unverified external facts, unavailable environments, or still-open
decisions. Never convert them into implied acceptance.>
````
