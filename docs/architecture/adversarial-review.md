# Adversarial self-review

Archie runs a fresh adversarial review of the diff before it opens a pull
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
onto its body.

## Model and budget

The reviewer model is `models.reviewer`, falling back to `models.builder`.
Effort is the per-stage `MaxSteps` budget, mapped onto `Subagent.MaxSteps`
(default 10).

## Operator-triggered review

The same reviewer runs against an *existing* PR via the channel-neutral
`review_pr` chat command: fetch the PR's head, snapshot it, run the isolated
reviewer, and return the structured findings. Authorization is identity-scoped
to the operator's configured repositories; duplicate or concurrent reviews of
the same PR are deduplicated.
