# Review findings as inline comments -- decision

**Status:** Proposed
**Beads issue:** `archie-core-q9au`
**Builds on:** `archie-core-h019` (adversarial self-review), `docs/architecture/adversarial-review.md`

## Problem

Findings reach the operator only as a flat list in the PR body
(`renderPRReviewSection`, h019.6): `` - `file.go:12` (confirmed, error): <defect>
— <scenario> ``. That is a report *about* the code, not a review *of* it. A real
reviewer anchors each finding to the line it is about, so the author sees it in
context — and, when the fix is mechanical, attaches the replacement so it can be
applied in one click.

Both halves are missing today:

- **No line-anchored comment.** `forge.Forge.Comment` posts to the PR
  *conversation* (`Issues.CreateComment`); nothing creates a review comment at
  `path` + `line`.
- **No suggested fix.** `ReviewFinding` carries `File`/`Line`/`Defect`/
  `FailureScenario`/`Verdict`/`Level`/`Category` — no replacement code, so even
  an anchored comment could only describe the fix, not offer it.

## Decisions

### 1. A narrow, type-asserted write surface

```go
type ReviewCommentWriter interface {
    CreateReviewComment(ctx context.Context, owner, repo string, number int,
        commitID, path string, line int, body string) error
}
```

Type-asserted like `PullRequestReader`/`PullRequestReviewReader`, so a forge
that cannot do it is skipped rather than faked.

Per-forge reality (verified against the SDKs, 2026-09-11):

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

`Suggestion` replaces the anchored line exactly. Single-line only in this cut;
multi-line (GitHub's `StartLine`/`StartSide`) is a follow-up, because a
multi-line suggestion that is off by one line silently corrupts the file.

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
2. Resolve the head SHA via `PullRequestReader.GetPullRequest` (the PR now
   exists).
3. Post each line-anchored finding as an inline comment, with a suggestion block
   when `Verdict == confirmed && Suggestion != ""`.

**Failures log and never fail the task.** The PR is already open; a comment that
failed to post must not park a task whose actual work succeeded, and the body
section already carries every finding. This mirrors `OpenPR`'s existing
best-effort `LinkBranch` call.

### 5. No separate opt-in flag (for now)

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
