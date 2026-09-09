# PR review comment remediation -- decision

**Status:** Proposed
**Date:** 2026-09-10
**Beads issue:** (filed as `archie-core-…`, parent: `archie-core-7d5u` event sources and typed reactions)

Answers: how archie reacts to a human's review comments on a PR it opened,
and remediates them. Built on two settled decisions — the adversarial review
epic's isolation contract (`docs/prds/adversarial-self-review.md`) and the
event-sources decision (`docs/prds/event-sources-and-reactions.md`) — plus the
webhook intake security contract (`docs/prds/webhook-intake-security.md`).
Nothing here re-decides those; it names the one concrete reaction each one's
machinery was built to carry.

## Problem

Archie opens a PR (`pr_open`) and then never learns what a human said about
it. `forge.Forge` has no method to list PR reviews or review comments; the
daemon poll loop finds only new issues; the forge webhook receiver
(`internal/forge/webhook/receiver.go`) decodes only `issues` events and
explicitly ignores `pull_request` events. A "requested changes" review, or an
inline comment on a line, is invisible work.

## Decisions

### 1. Read surface: a narrow `PullRequestReviewReader`, type-asserted

A new forge interface, implemented by GitHub and Gitea, not folded into the
fat `Forge` interface (the h019.8 `PullRequestReader` precedent — the noop
forge cannot honestly provide it):

```go
type ReviewComment struct { ID int64; Author, Body, Path string; Line int; InReplyTo int64; CreatedAt time.Time }
type Review struct { ID int64; Author, State string /* requested_changes | approved | commented | dismissed */; SubmittedAt time.Time }

type PullRequestReviewReader interface {
    ListReviews(ctx, owner, repo string, number, sinceID int64) ([]Review, error)
    ListReviewComments(ctx, owner, repo string, number, sinceID int64) ([]ReviewComment, error)
    // ReplyToReview posts a reply to a review or comment thread.
    ReplyToReview(ctx, owner, repo string, number, inReplyTo int64, body string) error
}
```

GitHub: `pulls/{n}/reviews` + `pulls/{n}/comments`; reply via
`pulls/{n}/comments/{id}/replies`. Gitea: `pulls/{n}/reviews` +
`pulls/{n}/comments`; reply via `pulls/{n}/comments/{id}`. `sinceID` is the
cursor for both poll and webhook dedup.

### 2. Event source: webhook first, poll as the universal backstop

- **Webhook (GitHub, low latency):** extend `receiver.go` to also decode
  `pull_request_review` and `pull_request_review_comment` events, behind the
  existing HMAC verification, producing the same reaction as the poller. The
  receiver stays a forge-intake surface — not the generic chat webhook, not a
  new generic receiver (7d5u decision 4).
- **Poll (both forges, no webhook dependency):** a periodic scan of `pr_open`
  tasks calls `ListReviews`/`ListReviewComments` with a persisted `sinceID`
  cursor per task. This is the Gitea path and the deployment-without-webhook
  path; it must stay first-class, not a legacy fallback (the repo's own
  webhook-intake rule).

Both paths emit the **same** typed reaction, so `PublishUnique` dedups them
for free — the `TaskEnvelope.IdempotencyKey` pattern (7d5u decision 4) applied
to reviews.

### 3. Reaction contract: producer-only, resolves to an existing task

A new `workintake.ReviewCommentEnvelope{Owner, Repo, PRNumber, CommentID,
ReviewID, Author, State, Body, Path, Line}`. Idempotency key is
`owner/repo/pr-number/comment-id` — source-independent, so a webhook delivery
and a poll of the same comment collapse. This is a producer-only reaction
(7d5u decision 3): it never vetoes or mutates in-flight work; the target task
is idle (`pr_open`).

The daemon resolves `PRNumber` → the task that owns it (store lookup by
owner/repo/pr_number). An unmatched PR (not an archie task) is dropped with a
counter — a public webhook must not be able to start an agent against an
arbitrary repository.

### 4. Remediation: a `remediate` workflow on the existing branch

A new workflow that runs when a reaction resolves to an archie-owned PR. It is
not a new branch/PR — it reuses the task's prepared worktree, refreshes it
onto the PR branch (the `worktree.Manager.refresh` idempotency), and:

1. Runs a builder agent whose mission is the review comment (body, path,
   line): "Address this review comment with the smallest change; run the
   gate." The toolset is the normal builder toolset — edit + gate + (never
   git; the orchestrator commits/pushes) — not the reviewer's read-only set.
2. Runs the repo gate (`[[repos.gate]]`) and `diff_cap_lines` — the same
   enforcement as any other work (webhook-intake-security point 4; no
   relaxation for review-triggered edits).
3. Commits and pushes to the **same** PR branch.
4. Replies to the review/comment via `ReplyToReview`, summarising what
   changed.

The comment is injected through the task record (e.g. `Task.Notes` or a
dedicated review field), not through a prompt-only path, so the reaction is
recoverable and auditable.

### 5. Routing and lifecycle

- `requested_changes` review → queue one remediation run addressing its
  actionable comments.
- `commented` review with actionable inline comments → queue a remediation run
  per comment (deduped by comment ID).
- `approved` review → no remediation. Record the review state on the task;
  do **not** auto-merge — merging stays an operator action (the dashboard/chat
  `/approve` precedent). Auto-merge is a separate decision if ever wanted.
- Comments from the bot itself are excluded (the `RepliesAfter` `exclude`
  precedent), so archie's own reply does not retrigger it.

### 6. Security posture

The webhook reaction is not a public no-code binding, so it does not pass
through the `draft → pending_approval → armed` gate (webhook-intake-security
point 2) — but every other constraint holds: HMAC (already in the receiver),
per-source rate limiting (add when the receiver gains a second event family),
and blast radius bounded by the existing gate/sandbox/worktree enforcement,
**plus** the new archie-owned-task check (decision 3), which is the actual
authorization boundary: a review event only ever causes work against a PR
archie itself opened.

### 7. Sequencing

1. `PullRequestReviewReader` + GitHub/Gitea implementations + `ReplyToReview`.
2. `ReviewCommentEnvelope` + idempotency + `PRNumber → task` resolution + the
   archie-owned-task guard.
3. `remediate` workflow (worktree refresh + builder + gate + push + reply).
4. Webhook receiver extension (`pull_request_review` / `_comment` events).
5. Poll backstop with the persisted `sinceID` cursor.
6. Tests + `task check`.

## Out of scope

- Auto-merge on approval (separate decision).
- Reacting to comments on PRs archie did not open.
- The calibration epic's confirm-before-surfacing (h019.5) — orthogonal.
