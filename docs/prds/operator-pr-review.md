# Operator-triggered PR review

Status: proposed (h019.8). Reuses the shipped adversarial-self-review contract
(`docs/prds/adversarial-self-review.md`) and the `workflow.Reviewer` /
`workflow.ReviewReport` types from `internal/domain/workflow`. This PRD decides
only what is new: how an operator asks for a review of an *existing* PR, and
how that PR's content reaches the reviewer.

## Problem

The adversarial reviewer (`StageReview`) runs automatically before `StageOpenPR`
and reviews the task's own worktree HEAD. There is no way to review a PR that
already exists: one archie opened, a human opened, or one whose findings the
operator wants re-checked after a push.

## Shape

One channel-neutral command — `review_pr` — accepts a repository and PR number
and returns the structured `ReviewReport`. The Web UI chat and Telegram invoke
the same command; neither duplicates review behaviour. The reviewer model is
`models.reviewer` (falling back to `models.builder`), already the rule the
workflow stage uses, so an operator configures it independently of
planner/builder.

## Decisions

1. **Forge read.** `GetPullRequest(owner, repo, number)` returns a
   forge-neutral `PullRequest{Number, Title, Body, HeadRef, BaseRef, HeadSHA,
   BaseSHA, State}`. Declared as a narrow `PullRequestReader` interface,
   implemented by GitHub and Gitea; the noop forge does not implement it, so
   the daemon type-asserts and refuses review with "forge does not support PR
   review". Adding to the fat `Forge` interface would churn every test fake for
   a capability the noop forge cannot honestly provide.

2. **Materialisation.** `worktree.Manager.CheckoutPR(owner, repo, headRef,
   baseRef)` clones the repository fully and checks out `origin/<headRef>`.
   `Diff(dir, baseRef)` and `Snapshot(dir, destDir)` then work unchanged:
   `Diff` resolves `origin/<baseRef>` via the existing `resolveBase`, and
   `Snapshot` strips `.git` exactly as the workflow stage does. The head must
   already be pushed to the same repository — archie's own PRs always are; a
   cross-repo or deleted head is refused with that reason, not half-reviewed.

3. **Reviewer reuse.** `agentworker.NewOperatorReviewer(models, providers)`
   builds the same `subagentReviewer` the stage uses, from daemon-level config
   rather than a `taskrun.Request`. It drops only the `Repo.ReviewEnabled`
   gate (the operator asked explicitly) and the task-run scaffolding.

4. **Command.** A gateway tool `review_pr` (toolset `tasks`) bound to the
   channel's identity at construction — the model never names an identity,
   matching `task_spawn`/`task_action`. The daemon supplies `ChatPRReviewer`,
   a narrow gateway interface; the daemon adapter maps `workflow.ReviewReport`
   into a gateway-owned result shape (gateway states what it needs, the daemon
   adapts — the `ChatTaskActor` pattern).

5. **Authorization.** Identity-scoped repository allow-list, the
   `task_spawn` rule: a chat identity may only review a repo in its
   `TaskProfile.Repos`. `identity == nil` denotes an authenticated dashboard
   operator who may review any configured repository.

6. **Determinism.** One review per (owner, repo, number) at a time: the daemon
   holds an in-flight set; a concurrent request for the same PR returns
   "review already in progress" rather than launching a second reviewer and
   racing on the snapshot.

## Files

- `internal/forge/forge.go`, `github.go`, `gitea.go` — `PullRequest`,
  `PullRequestReader`, `GetPullRequest`.
- `internal/worktree/worktree.go` — `CheckoutPR`.
- `internal/app/agentworker/review.go` — `NewOperatorReviewer`.
- `internal/gateway/review_tools.go` — `ChatPRReviewer`, result shape, `review_pr`.
- `internal/gateway/turn.go`, `gateway.go` — tool wiring into the turn.
- `internal/app/archied/pr_review.go` — the `ChatPRReviewer` adapter.
- `internal/app/archied/{bootstrap.go,main.go,telegram_setup.go}` — composition.

## Not in scope

- Surfacing findings on the PR body or the dashboard (h019.6).
- Reviewer model per-invocation override: configuration via `models.reviewer`
  is the selection surface; a per-call override is a later knob.
