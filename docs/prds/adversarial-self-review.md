# Adversarial self-review stage -- decision

**Status:** Superseded; requires redesign before implementation
**Date:** 2026-08-22
**Beads issue:** `archie-core-h019.1`, blocks `h019.2/.3/.4/.5`

> **Superseded 2026-08-24 by the full-task worker boundary.** This document is
> retained as the record of the earlier decision, not as an implementation
> contract. It assumes daemon-owned in-process execution and a daemon-side
> second `Pool.Acquire`; both call sites were removed by `archie-core-q15y`.
> The still-useful findings and lifecycle decisions below do not authorize an
> implementation. The feature must first decide how a fresh reviewer receives
> an isolated snapshot from inside the existing task-scoped worker without
> creating a host executor or a nested container lifecycle.

Answers the five questions in `h019.1`'s description against the actual
implement workflow, not designed from scratch: the gate pattern, the
container pool, and the workflow engine's `Outcome` mechanism already answer
most of this.

## 1. Where it runs: a second workspace, not a second worktree

The implement workflow's stage list
(`internal/domain/workflow/implement.go:110-185`) already has the right slot:
after `StageDiffCap()` (line 178), before `StageOpenPR()` (line 179) -- gates
have passed, the diff is final, and nothing has told the forge about this
change yet.

**The isolating property is the workspace path, not container-vs-in-process.**
Both execution modes already scope an agent's tools to a directory --
`agentexec.Runner.Run(ctx, workspace string, ...)`
(`internal/agentexec/inprocess.go:20-26`) roots tool access there in-process,
and container mode mounts only what `Pool.Acquire`
(`internal/container/pool.go:161-177`) is given. So "the reviewer cannot see
the implementation" becomes an environmental fact by construction, in both
deployment shapes, as long as the reviewer's workspace is never
`tc.WorktreeDir`.

**What the workspace is:** before running the reviewer, the app-layer code
that owns this stage exports a `.git`-free snapshot of the reviewed commit
(`git archive <commit> | tar -x`) into a fresh scratch directory next to, not
inside, the task's real worktree, then writes two files alongside it:
`diff.patch` (the unified diff already computed for `StageYaegiGate`'s
`Trees.Diff()`) and `issue.md` (the originating issue's title and body, read
from `store.Task`, nothing else). The reviewer's `agentexec.Request.Workspace`
points at this directory. Stripping `.git` is deliberate: it is what makes
commit messages, branch name, and reflog -- the actual channel a multi-commit
implementer's back-and-forth reasoning would leak through -- structurally
unreachable rather than merely undisclosed. Container mode acquires a second,
distinct container for this (`Pool.Acquire` again, different mounts); in-process
mode builds a second `agentexec.Request` with this workspace. Neither needs a
new execution mechanism.

## 2. What it's given, and what it must never be given

**Given:** the clean file-content snapshot (so the reviewer can read whole
files -- follow an import, check interface satisfaction, see a call site --
not just diff hunks, which the manual-read checklist in CLAUDE.md requires),
`diff.patch`, and `issue.md`.

**Never mounted or passed, by construction:** the implementer's
`agentexec.Result` (summary, captures, stop reason), any prior turn's tool-call
transcript, commit messages/git history/branch name (excluded by stripping
`.git`, not by instruction), daemon internal state, and any other task's data.
This is the environmental-enforcement principle applied literally: the
reviewer is never told "ignore the implementer's reasoning," because there is
no code path that hands it any.

## 3. Findings contract: `workflow.ReviewFinding`, workflow domain owns it

Not a reuse of `internal/gate.Finding` (`gate.go:22-31`), despite the similar
shape. That type belongs to the Yaegi gate contract (`.archie/gate.go`,
repo-owner-authored checks); coupling the review stage's blocking contract to
gate's evolution would violate "a domain declares the interface it needs, it
never names who implements it." `h019.2` implements, in
`internal/domain/workflow`:

```go
type ReviewFinding struct {
    Level    string // "error" (blocks) | "warn" (advisory)
    Category string // dead-code | unchecked-error | hardcoded-value |
                     // interface-satisfaction | nil-risk | goroutine-leak |
                     // race | other -- CLAUDE.md's own checklist categories
    File     string
    Line     int
    Message  string
}

func ReviewBlocking(findings []ReviewFinding) bool // mirrors gate.Blocking
```

The domain owns the type and the pure blocking decision; turning the
reviewer agent's raw output into `[]ReviewFinding` is app/infrastructure code
(`h019.3`/`h019.4`), not domain code -- same direction as gate's Yaegi
evaluator producing `[]gate.Finding` for the domain to consume.

**Findings are reported through a structured capture tool**, the same
`agentexec.CaptureTool` pattern `feasibility.go`'s `decideCaptureTools`
already uses, not free text the caller regexes out of a final message. This
is the findings contract's own application of environmental enforcement:
the reviewer cannot end its turn without calling a typed tool, rather than
being asked to format its answer as JSON and trusted to comply.

## 4. Lifecycle effect of a surviving finding: `StatusParked`, not `WaitingHuman`

The epic's acceptance criteria say a surviving finding "parks the task as
`waiting_human`," which names two different states in the actual machine
(`internal/taskstate/taskstate.go:44-59`). This decides between them:
**`store.StatusParked`**, following `StageDiffCap`'s existing precedent
(`steps.go:91-94`) exactly -- a stage-computed policy stop with a `Detail`
explaining why, before PR open. `Parked`'s available actions, retry and
abandon, are the right shape here: fix the flagged code and retry, or
abandon/override if a finding is a false positive. `WaitingHuman`'s
approve/reject actions have no obvious referent -- there is no PR yet to
approve or reject.

A `Level == "error"` finding that survives sets
`tc.Outcome = Outcome{Status: store.StatusParked, Detail: <rendered findings>}`
and the stage returns `nil`; `StageOpenPR` never runs. `Level == "warn"`
findings, and the zero-findings case, leave `tc.Outcome` unset and the stage
returns `nil`, so `StageOpenPR` runs next. Attaching warn-level findings to
the task record for PR-body/dashboard display is `h019.6`'s job, not decided
here.

## 5. Model, effort, and whether a repo even runs this yet

**Model:** a new `"reviewer"` key in the existing `Config.Models` map (already
a generic `map[string]string`, no schema change) with the same single-level
fallback precedent `agent.go:68-73` already uses for planner->builder:
`Models["reviewer"]`, falling back to `Models["builder"]` if unset. Operators
who want a genuinely independent second opinion configure a distinct model;
nothing forces one, because CLAUDE.md's project scope is "smallest change
that works," not a mandated model-diversity policy.

**Effort/budget:** reuse the per-stage budget override `AgentStage` already
supports (`agent.go:35`), not a new config surface -- a repo wanting a
cheaper or more thorough reviewer sets `MaxSteps` the same way
the plan stage already can.

**Opt-in, not automatic:** `h019.5` (calibration -- confirm findings
adversarially before they surface) is a separate, lower-priority bead than
`h019.4` (wiring). Shipping the stage unconditionally the moment `h019.4`
lands would gate every existing repo's PR flow on an uncalibrated reviewer
before calibration exists, which is exactly the "reviewer that cries wolf"
risk the epic names as the thing that kills this feature. `h019.4` adds
`Repo.ReviewEnabled bool` (default `false`) to `internal/config`; an operator
opts a repo in once they trust it, the same shape as any other per-repo gate
setting.

## Packages this touches

- `internal/domain/workflow` (`h019.2`, `h019.4`): `ReviewFinding`,
  `ReviewBlocking`, and a `ReviewStage` inserted between `StageDiffCap` and
  `StageOpenPR` in `implement.go`. Contract only; no execution mechanism.
- `internal/worktree` or a sibling package (`h019.3`): the clean-snapshot
  export (`git archive`, no `.git`) building the reviewer's isolated
  workspace.
- `internal/agentexec` / `internal/container` (`h019.3`): no new mechanism --
  a second `Request`/`Pool.Acquire` scoped to the snapshot directory reuses
  the existing `Runner` interface.
- `internal/config` (`h019.4`): `Models["reviewer"]` (no type change) and
  `Repo.ReviewEnabled bool`.
- `internal/store`/`internal/taskstate`: unchanged; reuses `StatusParked`.
- Surfacing findings on the PR body and dashboard is `h019.6`, out of scope
  here.

## What this does not decide

The exact review checklist prompt content, how many findings can survive
before `MaxSteps` is exhausted, and the adversarial-confirmation mechanism
that keeps a finding from surfacing on a single reviewer's say-so are
`h019.5`. The PR-body/dashboard rendering of findings is `h019.6`. This
document decides isolation, inputs, the findings contract's owner and shape,
the lifecycle effect, and model/effort/opt-in configuration only.
