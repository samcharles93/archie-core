# Review calibration -- decision

**Status:** Proposed
**Beads issue:** `archie-core-h019.5` (parent `archie-core-h019`)

Decides the half of calibration `docs/prds/adversarial-self-review.md` left
open: "the adversarial-confirmation mechanism that keeps a finding from
surfacing on a single reviewer's say-so."

## The problem, precisely

The shipped contract (`internal/domain/workflow/review_findings.go`, h019.2)
already makes only a **confirmed** error-level finding block
(`Blocking() == Verdict == confirmed && Level == error`), and a zero-finding
review is already distinguishable from one that never ran (`ReviewStatus`).
What is missing is the other half: a reviewer can currently report *nothing*
and say only "I found nothing" in free text. The operator cannot tell "I
checked the five things that matter and they are clean" from "I did not look."
That is the failure the epic names -- a findings surface that consumes attention
without earning trust.

## Decisions

### 1. "Checked and cleared" becomes structured, not prose

`ReviewReport` gains `Checked []ReviewCheck`:

```go
type ReviewCheck struct {
    Property string // what was verified clean, e.g. "nil-safety of Foo callers"
    Evidence string // how it was verified, e.g. "read all 4 callers; each nil-checks"
}
```

`Evidence` is required, for the same reason `ReviewFinding.FailureScenario` is:
a check without evidence is an assertion, and the whole point is to separate a
verified property from an unexamined one. A zero-finding review is credible
only when it lists what it checked.

### 2. Report checks through a typed tool, like findings

The reviewer calls a new capture tool `record_checked` (mirroring
`record_finding`) once per property it verified clean. The same environmental
argument as findings applies: the reviewer cannot end its turn having silently
skipped the checklist. `ReviewReport.Summary` stays as the human overview.

### 3. Style nits are out of contract

A finding must describe wrong behaviour or a crash. Style, formatting, naming,
and preference nits are explicitly out of contract -- they are the padding that
trains the operator to skim. Stated in the reviewer prompt and in the
`ReviewFinding` type's own documentation. Not machine-enforced: deciding "is
this a style nit" from free text is unreliable, and a wrong rejection is worse
than a tolerated one. The category enum already has no style bucket; the rule
closes the gap that would otherwise let one hide under `other`.

### 4. Surfacing

The PR-body findings section lists the cleared properties alongside findings,
so "ran and found nothing" arrives with its evidence. (Park reasons keep
rendering only blocking findings -- what a human needs to act on, not the full
checklist.)

## What this does not decide

- Adding a second, independent reviewer pass. The verdict distinction plus the
  recorded checks is the confirmation mechanism; a second model pass is a
  separate, heavier decision and nothing in `h019.5` requires it.
- How many findings can survive before `MaxSteps` is exhausted (a budget
  question, already satisfied by the per-stage budget).

## Packages this touches

- `internal/domain/workflow` -- `ReviewCheck`, `ReviewReport.Checked`, validation.
- `internal/app/agentworker` -- `record_checked` tool, prompt rules.
- `internal/domain/workflow/review.go` -- render cleared properties on the PR body.
