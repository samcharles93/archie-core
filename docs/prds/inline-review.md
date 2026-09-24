# Inline review -- decision

**Status:** Finalised
**Beads issue:** `archie-core-q9au`
**Builds on:** `archie-core-h019` (adversarial self-review), `docs/architecture/adversarial-review.md`

## Problem

Findings reach the operator only as a flat list in the PR body
(`renderPRReviewSection`, h019.6): ``- `file.go` (confirmed, error): <defect>
— <scenario>``. That is a report _about_ the code, not a review _of_ it. A real
reviewer anchors each finding to the line it is about, so the author sees it in
context — and, when the fix is mechanical, attaches the replacement so it can be
applied in one click.

Both halves are missing:

- **No line-anchored comment.** `forge.Forge.Comment` posts to the PR
  _conversation_ (`Issues.CreateComment`); nothing creates a review comment at
  `path` + `line`.
- **No suggested fix.** `ReviewFinding` carries `File`/`Line`/`Defect`/
  `FailureScenario`/`Verdict`/`Level`/`Category` — no replacement code, so even
  an anchored comment could only describe the fix, not offer it.

## Decisions

### 1. A narrow, type-asserted write surface

The set of comments travels in one call, so a forge that can only carry them on
a submitted review needs one request rather than one per finding:

```go
type ReviewCommentWriter interface {
    CreateReviewComments(ctx context.Context, owner, repo string, number int,
        comments []InlineReviewComment) error
}
```

Type-asserted like `PullRequestReader`/`PullRequestReviewReader`, so a forge
that cannot do it is skipped rather than faked. The stage reaches it through
`workflow.Forger`, whose production implementation is `forgerpc.Client` — the
worker holds no forge credentials, so the daemon's forgerpc server type-asserts
`forge.ReviewCommentWriter` and answers an incapable forge with an error the
best-effort stage logs.

The comment's line is anchored to the pull request's **current head revision**,
resolved inside each implementation rather than passed in by the caller: it is
the revision the comments are attached to, and the caller that produced the line
numbers holds no forge credentials to read it. The caller does pass the revision
those line numbers were **measured on** (`TaskContext.ReviewedHeadSHA`,
recorded when `StageOpenPR` opens the pull request), and the implementation
posts only while the head it read still matches it — see §4.

Per-forge reality, verified against the SDKs:

- **GitHub** — direct: `PullRequests.CreateComment` with
  `{Body, Path, Line, Side: "RIGHT", CommitID}`.
- **Gitea** — no direct inline-comment call. Inline comments ride on a submitted
  review (`CreatePullReview` with `Comments` and a `COMMENT` state). The Gitea
  implementation therefore submits a single `COMMENT`-state review carrying the
  findings, or the capability is simply absent (the type assertion skips it).
  Decided: implement Gitea as a `COMMENT`-state review; do not block the feature
  on it.

### 2. `Suggestion`, and only on confirmed findings

`ReviewFinding` gains an optional `Suggestion string` — the replacement text for
the anchored line, rendered as a fenced `suggestion` block (GitHub's one-click
"Commit suggestion").

**Only `confirmed` findings may carry one.** A wrong suggestion is a
one-click path to breaking code a reviewer then has to notice; `confirmed` is by
definition the verdict the reviewer traced and can state the failure for. A
`plausible` finding is a worry, and a worry gets a sentence, not a patch. This is
the h019.5 calibration rule reused, not a new one.

`Suggestion` replaces the anchored line exactly, and is only rendered when
`Verdict == confirmed`. Single-line only in this cut;
multi-line (GitHub's `StartLine`/`StartSide`) is a follow-up, because a
multi-line suggestion that is off by one line silently corrupts the file. The
rule is enforced in `ReviewFinding.Validate` — an embedded newline is rejected
there, so the `record_finding` tool refuses it with feedback the model can act
on, rather than the renderer silently posting a fence that applies only its
first line.

### 3. Which findings are posted inline

Only findings that are **line-anchored** (`Line > 0`). A whole-file finding
(`Line == 0`, e.g. "README.md is stale") has no line to attach to and stays in
the PR-body list. The body section keeps listing everything, so it remains the
complete record and the fallback.

Findings that block never reach this path: a surviving blocking finding parks
the task before `StageOpenPR`, so there is no PR to comment on. Inline comments
are the non-blocking, line-anchored subset — which is exactly the set that would
otherwise be a body-only list the author has to map back to lines by hand.

### 4. A best-effort stage after `StageOpenPR`

New `StagePostReviewComments`, after `StageOpenPR`:

1. Read the report stashed by `StageReview` (`tc.ReviewReport`, h019.6).
2. Post each line-anchored finding as an inline comment, with a suggestion block
   when `Verdict == confirmed && Suggestion != ""`. The head SHA the comments are
   anchored to is resolved by the forge implementation, not here; there is no
   `PullRequestReader` on this side of the agent boundary. The stage passes
   `tc.ReviewedHeadSHA` alongside them.

Nothing in the run pushes between `StageReview` and this stage, so at that
moment the pull request's head _is_ the revision the review read. That was an
assumption, and a collaborator can falsify it by pushing to the branch between
the review and the posting. A line number is not validated against a revision:
both forges anchor it to whatever now occupies that line, so a stale anchor
mislabels rather than failing. The revision is therefore carried with the
comments and the set is refused when the head has moved off it
(`reviewHeadDrift`) — a refusal lands in the failure path below, leaving the PR
body as the record. With no measured revision (a `Trees` that cannot report one)
the comments post unverified rather than being dropped.

**Failures log and never fail the task.** The PR is already open; a comment that
failed to post must not park a task whose actual work succeeded, and the body
section already carries every finding. This mirrors `OpenPR`'s existing
best-effort `LinkBranch` call.

### 5. No separate opt-in flag

Inline comments are posted whenever a review ran and produced line-anchored
findings — the same `repo.review_enabled` gate already covers it. A second flag
is a knob to add if it proves noisy in practice; adding it speculatively is the
smallest-change principle's opposite.

## Out of scope

- Commenting on the blocking/parked case (no PR exists).
- Resolving/dismissing threads, or reacting to replies (that is the
  `archie-core-8li9` remediation direction).
- Multi-line suggestions.
- Gitea's "suggested change" rendering: Gitea has no one-click apply, so the
  `Suggestion` there is informational text in the comment body.

## Packages this touches

- `internal/domain/workflow` — `ReviewFinding.Suggestion`; `StagePostReviewComments`;
  the `Forger` subset gains the writer (or `TaskContext` gains the writer seam).
- `internal/forge` — `ReviewCommentWriter`, GitHub + Gitea implementations.
- `internal/app/agentworker` — the reviewer prompt asks for a `Suggestion` on
  confirmed, mechanically-fixable findings; `record_finding` accepts it.
