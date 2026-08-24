# Adversarial self-review stage -- decision

**Status:** Active
**Date:** 2026-08-22, section 1 replaced 2026-08-25
**Beads issue:** `archie-core-h019.1`, blocks `h019.2/.3/.4/.5`

> **Section 1 was superseded on 2026-08-24 and re-decided on 2026-08-25.**
> The original execution mechanism assumed daemon-owned in-process execution
> and a daemon-side second `Pool.Acquire`; `archie-core-q15y` removed both
> call sites, leaving one open question: how a fresh reviewer receives an
> isolated snapshot from inside the task-scoped worker, without a host
> executor or a nested container lifecycle. Section 1 below now answers it
> with `agent.Subagent`. Sections 2-5 were never invalidated and still hold;
> section 3's contract shipped as `archie-core-h019.2`.

Answers the five questions in `h019.1`'s description against the actual
implement workflow, not designed from scratch: the gate pattern, the
container pool, and the workflow engine's `Outcome` mechanism already answer
most of this.

## 1. Where it runs: `agent.Subagent`, inside the task worker

The implement workflow's stage list
(`internal/domain/workflow/implement.go:110-185`) already has the right slot:
after `StageDiffCap()` (line 178), before `StageOpenPR()` (line 179) -- gates
have passed, the diff is final, and nothing has told the forge about this
change yet.

**The reviewer is an `agent.Subagent` run, not a second container.** ai-sdk
`v0.1.22` (already the pinned version) added `agent/subagent.go`: a nested,
synchronous `core.GenerateText` call in the same process, with its own model,
system prompt, toolset and step budget. That is what makes this feature
buildable at all after `q15y` -- the reviewer runs inside the task-scoped
`archie-agent` worker that is already executing the workflow, so there is no
host executor, no second `Pool.Acquire`, and no nested container lifecycle.

**Two isolations are needed, and Subagent only provides one of them.**

*Conversation isolation is structural, and free.* `Subagent.Run`
(`agent/subagent.go:76-96`) calls `GenerateText` with `Prompt` set and
`Messages` never populated. The implementer's history has no code path into
the nested run; only the prompt string crosses, and only text comes back.
This is the literal form of "environmental enforcement over prompt rules":
the reviewer is not told to ignore the implementer's reasoning, because
nothing hands it any. `h019.3`'s required test asserts this as a construction
fact.

*Workspace isolation is still ours to build.* A sub-agent shares the parent's
process, working directory and credentials. So the `.git`-free snapshot from
the original decision survives unchanged and is load-bearing: export the
reviewed commit (`git archive <commit> | tar -x`) into a fresh scratch
directory beside, not inside, the task worktree, and write `diff.patch` (the
diff already computed for `StageYaegiGate`'s `Trees.Diff()`) and `issue.md`
(the originating issue's title and body, nothing else) alongside it. Stripping
`.git` is what makes commit messages, branch name and reflog -- the actual
channel a multi-commit implementer's reasoning leaks through -- structurally
unreachable rather than merely undisclosed.

`Subagent.Tools` therefore takes a **distinct read-only toolset rooted at the
snapshot**. Never the worker's own toolset: it carries the `worktreerpc`
publication grant, so a reviewer holding it could push the branch it exists to
block.

**Call `Run()`, not `Tool()`.** `Tool()` would register delegation as a tool
the *implementer's* model may choose to invoke -- self-certification by the
implementer, which is the exact failure this feature exists to prevent. The
stage calls `Run()` unconditionally. That also sidesteps both of `Tool()`'s
documented footguns: the parent's two-step minimum and the recursion risk of
passing a toolset back to itself.

**Findings return through a closure, not the return value.** `Run` yields only
final text, so section 3's structured contract cannot ride the return. Because
the sub-agent is in-process, the capture tool's Go implementation appends
directly to a `[]workflow.ReviewFinding` the stage owns. This preserves the
"cannot end its turn without calling a typed tool" property, and it defeats
`Subagent`'s documented "no partial results on failure" limitation: findings
already captured survive an error return, so **the stage reads the captured
slice even when `Run` returns an error**. A reviewer that logged three defects
and then hit its step limit still yields those three.

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

**A review that fails to run is fail-closed** (decided 2026-08-25). The
blocking rule is `Verdict == confirmed && Level == error`, so only a confirmed
finding blocks -- but that governs findings, not the reviewer's own failure. A
sub-agent that errors, is truncated, or produces no conclusion yields
`ReviewStatusNotRun`, for which `ReviewReport.Passed()` is false and
`Blocking()` is also false; the contract deliberately refuses to guess. This
stage resolves that as **park, do not open the PR**. The reasoning is that
`Repo.ReviewEnabled` is opt-in: an operator who turned the gate on wants the
gate, and a provider outage silently degrading into "PR opened unreviewed" is
the failure they were buying protection against. The cost is accepted -- a
transient model error parks a task, and `Parked`'s retry action is the
remedy. `Detail` must say the review did not run, never that it passed.

Note this is why `h019.2` models "ran and found nothing" separately from "did
not run": collapsing them to an empty slice would make an outage
indistinguishable from a clean review, and fail-closed unimplementable.

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
the plan stage already can. Map that budget onto `Subagent.MaxSteps` and
`Subagent.MaxTokens` deliberately: **`MaxTokens` is a per-call ceiling, not a
cumulative budget**, so an N-step reviewer can emit up to N x `MaxTokens`.
Setting it as though it bounded the whole run is a costing error, not a
safety one. `Subagent.MaxSteps` defaults to 10 when unset.

**Observability:** the nested run is synchronous and non-streaming -- nothing
is emitted between the parent's call and its result, so a long review reads as
a stalled stage. Emit task events around the call so the dashboard shows the
reviewer working rather than a gap.

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
  `ReviewReport`, `ReviewBlocking` -- shipped -- and a `ReviewStage` inserted
  between `StageDiffCap` and `StageOpenPR` in `implement.go`. The domain owns
  the contract and the pure blocking decision, and declares the reviewer
  interface it needs; it never names `agent.Subagent`.
- `internal/worktree` or a sibling package (`h019.3`): the clean-snapshot
  export (`git archive`, no `.git`) building the reviewer's isolated
  workspace. Still required -- `Subagent` isolates conversation, not
  filesystem.
- `internal/app/agentworker` (`h019.3`): constructs the `agent.Subagent` from
  the worker's own resolved provider (`agentexec.NewRuntime`), with a
  read-only toolset rooted at the snapshot and the findings capture tool. This
  is app-layer wiring: the domain gets an implementation of its reviewer
  interface, not an SDK type.
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
