# Adversarial self-review

> **Removed, not shipped (2026-09-28, `d73e0748`). This page records a system
> that no longer exists in the tree.**
>
> `d73e0748` ("remove the superseded adversarial-self-review system") deleted the
> stage and its contract: `workflow.Reviewer`, `ReviewRequest`, `ReviewReport`
> and `StageReview` are gone from `internal/domain/workflow`, so everything under
> "Contract" below describes types that cannot be found.
>
> The authority is `docs/prds/pr-review-agent.md`, which says so itself: it
> "supersedes the reviewer executor and blocking rule in
> `docs/architecture/adversarial-review.md`". archied now runs its PR review
> pipeline before it opens a pull request; there is no separate adversarial
> self-review pass and no `repo.review_enabled` setting.
>
> The page is kept rather than deleted because PRDs link to it
> (`docs/prds/adversarial-self-review.md`, `docs/prds/inline-review.md`,
> `docs/prds/pr-review-agent.md`). Read it as history.
>
> One section is stale in a second, older way: "Operator-triggered review" below
> describes the synchronous `review_pr` (fetch the head, snapshot it, run the
> reviewer, return findings), which `archie-core-afbk.14` replaced with a queued
> pr-review task.

Archie ran a fresh adversarial review of the diff before it opened a pull
request, so a PR arrives with a findings record rather than an unreviewed
claim. The reviewer provably cannot see the implementer's context: it reviews a
`.git`-free snapshot of the committed tree, not the worktree, the commit
history, or the implementer's transcript.

Promoted from `docs/prds/adversarial-self-review.md` (the design decision;
`archie-core-h019`). Describes the stage as shipped.

## Contract (`internal/domain/workflow`)

The workflow domain owns the contract and the blocking decision; it never names
the executor.

- `Reviewer.Review(ctx, ReviewRequest) ReviewReport` — the interface the
  workflow stage consumes.
- `ReviewRequest` is the reviewer's entire input: `SnapshotDir` (a `.git`-free
  export of the reviewed commit), `Diff` (base...head), `IssueText` (the
  originating issue title/body), and `MaxSteps`.
- `ReviewFinding` / `ReviewReport` are the structured result. The load-bearing
  rule is `Blocking() == (Verdict == confirmed && Level == error)`: only a
  confirmed error-level finding blocks; a plausible finding never blocks
  however severe it claims to be.

`ReviewReport` distinguishes `not_run`, `skipped`, and `completed` (zero
findings) structurally, so "ran and found nothing" is not the same as "never
ran" — collapsing them would make an outage indistinguishable from a clean
review.

## Execution (`internal/app/agentworker`)

The executor is an ai-sdk `agent.Subagent`: a nested synchronous generation in
the same worker process, with its own model, system prompt, toolset and step
budget.

- **Conversation isolation is structural.** `Subagent.Run` sends only a prompt
  string, never the implementer's message history.
- **Workspace isolation is the `.git`-free snapshot.** The reviewer's toolset is
  read-only (`read`, `grep`, `find`) and rooted at `SnapshotDir` — never the
  worker's own toolset, which carries the `worktreerpc` publication grant.
- **Findings return through a typed capture tool** (`record_finding`), validated
  on the way in, so the reviewer reports through the contract rather than free
  text the caller regexes.
- **Fail-closed.** A reviewer that errors, truncates, or reaches no conclusion
  yields `ReviewStatusNotRun`, which the stage treats as a park — never a silent
  "PR opened unreviewed".

## Lifecycle effect

`StageReview` runs in the implement workflow after the deterministic gates and
before `StageOpenPR`, gated by `repo.review_enabled` (opt-in, default off). A
surviving blocking finding — or a review that failed to run — parks the task
(`StatusParked`, not `waiting_human`) with the findings rendered into the park
reason; `StageOpenPR` never runs. Warn/plausible findings and the zero-finding
case leave the outcome unset, so the PR opens with a findings section rendered
onto its body. `StagePostReviewComments` then runs after `StageOpenPR` and posts
the **line-anchored** findings as inline review comments, carrying a one-click
suggestion on a confirmed finding the reviewer could state a fix for. That stage
is best-effort — a comment that fails to post is logged, never a park — because
the body section already lists every finding, and it is the record a whole-file
finding (no line to anchor to) only ever appears in.

## Model and budget

The reviewer model is `models.reviewer`, falling back to `models.builder`.
Effort is the per-stage `MaxSteps` budget, mapped onto `Subagent.MaxSteps`
(default 10).

## Operator-triggered review

The same reviewer runs against an _existing_ PR via the channel-neutral
`review_pr` chat command: fetch the PR's head, snapshot it, run the isolated
reviewer, and return the structured findings. Authorisation is identity-scoped
to the operator's configured repositories; duplicate or concurrent reviews of
the same PR are deduplicated.
