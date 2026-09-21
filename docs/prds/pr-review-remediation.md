# PR review comment remediation -- decision

**Status:** Approved
**Date:** 2026-09-10, amended and ratified 2026-09-19
**Beads issue:** (filed as `archie-core-…`, parent: `archie-core-7d5u` event sources and typed reactions)

Answers: how archie reacts to review comments on a PR it opened, from a human
or from another review bot, and remediates them. Built on two settled decisions — the adversarial review
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
  webhook-intake rule). It is **one shared scan over all `pr_open` tasks**,
  not a scan inside each identity's poll loop, and it calls the forge client
  the scanned task's own identity owns. This is `reconcilePRs`'s shape
  exactly (`internal/daemon/daemon.go`: `Store.OpenPRs` then `forgeFor(&t)`).
  A per-identity scan would call one identity's forge client against another
  identity's PR.

Both paths emit the **same** typed reaction, so `PublishUnique` dedups them
for free — the `TaskEnvelope.IdempotencyKey` pattern (7d5u decision 4) applied
to reviews.

### 3. Reaction contract: producer-only, resolves to an existing task

A new `workintake.ReviewCommentEnvelope{Owner, Repo, PRNumber, Kind, CommentID,
ReviewID, Author, State, Body, Path, Line}`. Idempotency key is
`owner/repo/pr-number/kind/id` — source-independent, so a webhook delivery
and a poll of the same comment collapse.

`Kind` (`review` or `comment`) is part of the key because a review ID and a
review-comment ID come from independent forge sequences. Keying on the bare id
lets a review and an inline comment on the same PR collide, silently dropping
one of the two. This is a producer-only reaction
(7d5u decision 3): it never vetoes or mutates in-flight work; the target task
is idle (`pr_open`).

The daemon resolves `PRNumber` → the task that owns it. An unmatched PR (not
an archie task) is dropped with a counter — a public webhook must not be able
to start an agent against an arbitrary repository. A PR whose task is merged,
closed or archived is dropped through the same counter: the reaction is only
ever work against a PR archie itself opened *and still owns*.

That lookup does not exist. `pr_number` is written by
`internal/store/store.go` and never read in a `WHERE` clause, so this needs a
`TaskStore` method keyed on `(owner, repo, pr_number)`, a matching
`StateStoreService` RPC (a Go interface method on a store facade without one
is forbidden, `CLAUDE.md`), and an index on those three columns. It is the
authorization boundary, so it is sequenced as its own step below rather than
carried along with the envelope.

**Identity is derived from the resolved task, never carried in the reaction.**
The task row already stores `identity` (`internal/store/store.go`), and it is
the resolved task's identity that selects the forge client, worktree manager,
repo config and `bot_user` for the remediation run and the `ReplyToReview`
reply, through the existing `forgeFor`/`treesFor`/`repoFor` helpers. That is
what decision 5's "the identity that owns the task" means, and it is the only
place identity enters this path.

Identity is deliberately **not** added to the envelope, the idempotency key or
the index:

- A PR number is unique within an `owner/repo`, and the tasks table is keyed
  `UNIQUE(owner, repo, issue_number)` with no host or identity column, so
  archie already treats `owner/repo` as globally unique across identities. A
  `(owner, repo, pr_number)` lookup cannot return a task other than the one
  that owns that PR; there is no cross-identity resolution to guard against.
- The webhook producer has no identity to carry: webhook intake refuses to
  start when `[[identities]]` is configured (`setupForgeWebhook`,
  `internal/app/archied/bootstrap.go`).
- Keying on a field only one of the two producers can populate is precisely
  the delivery-source-specific key `CLAUDE.md` forbids, and it would break the
  webhook/poll dedup the key exists for. An index column that never appears in
  the `WHERE` clause is dead weight.

### 4. Remediation: a `remediate` workflow on the existing branch

A new workflow that runs when a reaction resolves to an archie-owned PR. It is
not a new branch/PR — it reuses the task's prepared worktree, refreshes it
onto the PR branch (the `worktree.Manager.refresh` idempotency), and:

1. Runs a builder agent whose mission is the review's actionable comments
   (each with body, path, line): "Address these review comments with the
   smallest change; run the gate." One agent run per review, per decision 5. The toolset is the normal builder toolset — edit + gate + (never
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

A **review** is the unit of remediation, never an individual comment.

- `requested_changes` review → one remediation run addressing every actionable
  comment in that review.
- `commented` review with actionable inline comments → one remediation run
  addressing every actionable comment in that review.
- `approved` review → no remediation. Record the review state on the task;
  do **not** auto-merge — merging stays an operator action (the dashboard/chat
  `/approve` precedent). Auto-merge is a separate decision if ever wanted.

One run per review, strictly. A review carrying eight inline comments arrives
as one `pull_request_review` event plus eight `pull_request_review_comment`
events; remediating each separately means eight builder runs, eight gate runs
and eight pushes to answer one review. The comment events are collected
against their parent `ReviewID` and remediated together. A standalone comment
with no parent review is its own unit of one.

Remediation runs for a single task are **serialised**, whatever
`[[repos]].allow_concurrent` says. Two runs against one PR branch would race
on the shared worktree and on the push; `allow_concurrent` is about unrelated
tasks in a repo, not about one task reacting to itself.

#### Whose comments count

Only archie's **own** comments are excluded, matched on the forge identity
that owns the task (`bot_user`, or the identity's `bot_user` under
`[[identities]]`), so its `ReplyToReview` replies do not retrigger it.

Every other author is actionable, including other bots. The external review
bot on this repository is the main source of findings on an archie-opened PR,
and discarding comments because their author is a bot would discard the review
this feature exists to act on. There is no "is a bot" test anywhere in this
path.

(The original draft justified a broader exclusion by citing a `RepliesAfter`
`exclude` precedent. No such mechanism exists in the tree; the citation is
withdrawn.)

#### Bounding the exchange

Because bot comments are actionable, the exchange can run away without a human
ever touching it: archie pushes a remediation, the review bot re-reviews the
new commit, comments, archie remediates again. Nothing in the reaction path
terminates that on its own.

A task therefore carries a remediation round count, bounded by the repo's
existing `max_retries`. On the cap, archie stops remediating, posts one
comment saying it has stopped and why, and parks the task for an operator.
This is a hard stop, not a backoff: the failure it guards against is
unbounded spend, so slowing it down does not address it.

### 6. Security posture

The webhook reaction is not a public no-code binding, so it does not pass
through the `draft → pending_approval → armed` gate (webhook-intake-security
point 2) — but every other constraint holds: HMAC (already in the receiver),
per-source rate limiting (add when the receiver gains a second event family),
and blast radius bounded by the existing gate/sandbox/worktree enforcement,
**plus** the new archie-owned-task check (decision 3), which is the actual
authorization boundary: a review event only ever causes work against a PR
archie itself opened and still owns.

Accepting other bots' comments (decision 5) does not widen that boundary: an
author is never a reason to act, only the owned-task check is. It does widen
the *spend* surface, which the round cap bounds.

### 7. Sequencing

1. **The reaction stream.** `ARCHIE_REACTIONS`, under the fan-out
   `jetstream.LimitsPolicy` the parent decision requires
   (`event-sources-and-reactions.md` §2). `Config.Retention` is
   parameterised for exactly this (`archie-core-7d5u.2`), but no reaction
   stream exists and `ARCHIE_TASKS` carries `archie.task.>` only, so every
   publish to `archie.reaction.*` fails against no matching
   stream. This step is first because nothing after it can be exercised
   without it.
2. `PullRequestReviewReader` + GitHub/Gitea implementations + `ReplyToReview`.
   *(Landed.)*
3. `ReviewCommentEnvelope` + the kinded idempotency key.
4. **The owned-task guard**: `(owner, repo, pr_number)` lookup, its
   `StateStoreService` RPC, its index, and the drop-with-counter path for an
   unmatched, merged, closed or archived PR. Nothing may consume a reaction
   before this exists.
5. `remediate` workflow: worktree refresh, one run per review, builder, gate,
   push, reply, round cap.
6. Webhook receiver extension (`pull_request_review` / `_comment` events).
7. Poll backstop with the persisted `sinceID` cursor.
8. Tests + `task check`.

Steps 6 and 7 are the producers and come last on purpose. Building a producer
before its consumer and its authorization guard yields a path that publishes
into nothing, and becomes an unguarded intake the moment a stream appears.

## Out of scope

- Auto-merge on approval (separate decision).
- Reacting to comments on PRs archie did not open.
- The calibration epic's confirm-before-surfacing (h019.5) — orthogonal.
