package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// ErrNoReviewPayload is returned when a remediate run starts on a task that
// carries no review unit to address. The daemon reaction consumer is the
// only producer of Task.ReviewPayload; a remediate task without one is a
// dispatch bug upstream, not a recoverable condition here.
var ErrNoReviewPayload = errors.New("remediate: task carries no review payload")

// ReviewUnitComment is one actionable review comment the builder must
// address, carried inside a ReviewUnit.
type ReviewUnitComment struct {
	CommentID int64  `json:"comment_id"`
	Path      string `json:"path,omitempty"`
	Line      int    `json:"line,omitempty"`
	Body      string `json:"body"`
}

// ReviewUnit is the remediate workflow's input contract: one review (or one
// standalone comment with no parent review) and every actionable comment
// grouped under it, per docs/prds/pr-review-remediation.md decision 5 ("a
// review is the unit of remediation, never an individual comment"). The
// daemon's reaction consumer builds and JSON-encodes this into
// Task.ReviewPayload before dispatch; this package only decodes and acts
// on it.
type ReviewUnit struct {
	ReviewID int64  `json:"review_id,omitempty"`
	Author   string `json:"author,omitempty"`
	State    string `json:"state,omitempty"`
	// Body is the review's own top-level summary (e.g. a "requested_changes"
	// review's message), separate from any inline comment bodies.
	Body     string              `json:"body,omitempty"`
	Comments []ReviewUnitComment `json:"comments,omitempty"`
}

// DecodeReviewUnit parses a task's ReviewPayload. An empty payload is
// ErrNoReviewPayload, not a zero-value unit: a remediate task with nothing
// to address should never have been dispatched.
func DecodeReviewUnit(payload string) (ReviewUnit, error) {
	if payload == "" {
		return ReviewUnit{}, ErrNoReviewPayload
	}
	var u ReviewUnit
	if err := json.Unmarshal([]byte(payload), &u); err != nil {
		return ReviewUnit{}, fmt.Errorf("decode review payload: %w", err)
	}
	return u, nil
}

// EncodeReviewUnit renders a review unit for storage in Task.ReviewPayload.
func EncodeReviewUnit(u ReviewUnit) (string, error) {
	data, err := json.Marshal(u)
	if err != nil {
		return "", fmt.Errorf("encode review payload: %w", err)
	}
	return string(data), nil
}

// replyTarget is the one comment ID a remediation run's reply threads onto,
// and whether one exists at all. A standalone review with no inline
// comments (a plain "requested changes" message) has nothing to thread a
// reply onto, so the reply falls back to a plain PR comment.
func (u ReviewUnit) replyTarget() (int64, bool) {
	for _, c := range u.Comments {
		if c.CommentID > 0 {
			return c.CommentID, true
		}
	}
	return 0, false
}

// renderReviewUnitMission formats a review unit as the builder's mission
// input: the review's own message, if any, followed by every actionable
// comment with its location.
func renderReviewUnitMission(u ReviewUnit) string {
	var b strings.Builder
	if u.Body != "" {
		fmt.Fprintf(&b, "Review summary (%s): %s\n\n", orUnknown(u.State), u.Body)
	}
	if len(u.Comments) == 0 {
		b.WriteString("No inline comments were attached; address the review summary above.")
		return b.String()
	}
	b.WriteString("Comments:\n")
	for _, c := range u.Comments {
		loc := "(general)"
		if c.Path != "" {
			loc = c.Path
			if c.Line > 0 {
				loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
			}
		}
		fmt.Fprintf(&b, "- %s: %s\n", loc, c.Body)
	}
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unspecified"
	}
	return s
}

// remediationRoundCapBytes bounds the round-cap park detail, matching the
// other clipped park reasons in this package.
const remediationRoundCapBytes = 2000

// resumableTrees is the optional capability a Trees implementation offers
// for continuing work on an already-open PR branch instead of starting a
// fresh worktree. It stays separate because Trees is already at the interface
// size cap and most workflow stages do not need branch resumption.
//
// *worktree.Manager (real git access) and the container-mode hybrid
// implementation both already satisfy this structurally; a Trees
// implementation that doesn't (a narrower test fake, say) simply cannot run
// the remediate workflow, which StageResumeWorktree reports plainly.
type resumableTrees interface {
	Dir(owner, repo string, issue int) string
	Resume(ctx context.Context, dir, branch string) error
}

// StageResumeWorktree re-syncs the task's already-prepared worktree onto
// its PR branch's remote tip, so the remediate workflow continues on the
// branch the implement run pushed instead of resetting to base the way
// StagePrepareWorktree's refresh would (docs/prds/pr-review-remediation.md
// decision 4). A remediate task always has a branch: it only ever runs
// against a PR archie itself opened.
func StageResumeWorktree() Stage {
	return Stage{Name: "resume", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.Task.Branch == "" {
			return fmt.Errorf("remediate: task has no branch to resume")
		}
		rt, ok := tc.Trees.(resumableTrees)
		if !ok {
			return fmt.Errorf("remediate: this worktree implementation cannot resume a PR branch")
		}
		dir := rt.Dir(tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber)
		if err := rt.Resume(ctx, dir, tc.Task.Branch); err != nil {
			return fmt.Errorf("resume worktree onto %s: %w", tc.Task.Branch, err)
		}
		tc.Dir, tc.Branch = dir, tc.Task.Branch
		return nil
	}}
}

// StageRemediationRoundCap enforces the hard stop on an unbounded
// remediate/re-review exchange (decision 5, "bounding the exchange").
// Task.RemediationRounds is the remediation's own budget, separate from
// RetryCount (the operator's): one shared counter made N operator retries
// eat the review-remediation budget and vice versa, and let a repo-level
// round cap re-park a task an operator had just legally retried. On the
// cap it parks the task and posts one comment explaining why, without
// running the builder; under the cap it counts this round and proceeds.
func StageRemediationRoundCap() Stage {
	return Stage{Name: "round-cap", Run: func(ctx context.Context, tc *TaskContext) error {
		maxRounds := tc.Repo.EffectiveMaxRetries(tc.Cfg.MaxRetries)
		if maxRounds > 0 && tc.Task.RemediationRounds >= maxRounds {
			detail := fmt.Sprintf("remediation round cap reached (%d/%d); stopping and parking for an operator", tc.Task.RemediationRounds, maxRounds)
			postRemediationComment(ctx, tc, "Archie has stopped remediating this review: "+detail+".")
			tc.Outcome = Outcome{Status: StatusParked, Detail: clip(detail, remediationRoundCapBytes)}
			return nil
		}
		tc.Task.RemediationRounds++
		return nil
	}}
}

// postRemediationComment posts a plain PR comment, best-effort: a park or a
// reply failure on the messaging side must never mask the round-cap
// decision or the remediation outcome that already happened.
func postRemediationComment(ctx context.Context, tc *TaskContext, body string) {
	if tc.Task.PRNumber <= 0 {
		return
	}
	if _, err := tc.Forge.Comment(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber, body); err != nil {
		tc.Log.Warn("remediation comment not posted", "pr", tc.Task.PRNumber, "err", err)
	}
}

// remediateMission builds the builder agent's mission from the task's
// decoded review unit, stashing it on tc.reviewUnit so later stages
// (commit-push, reply) don't re-decode the payload. StageCheckReviewPayload
// already guarantees the payload decodes before this stage runs.
func remediateMission(tc *TaskContext) string {
	unit, _ := DecodeReviewUnit(tc.Task.ReviewPayload)
	tc.reviewUnit = unit
	return fmt.Sprintf(
		"Address the review comments below on pull request #%d for %s with the smallest change that "+
			"satisfies them, then run the gate.\n\n%s\n\n"+
			"Do not run git  --  the orchestrator commits and pushes for you. When done, call finish with "+
			"status \"passed\" and a summary written for the human who will read your reply: what changed and "+
			"why. If nothing here actually requires a code change, call finish with status \"passed\" and say "+
			"so in the summary.",
		tc.Task.PRNumber, tc.Repo.FullName(), renderReviewUnitMission(unit),
	)
}

// remediateBuildStage runs the builder agent against the task's review
// unit. StageCheckReviewPayload runs first, so a decode failure parks the
// run before the builder ever sees an empty mission.
func remediateBuildStage() Stage {
	return AgentStage{
		Name: "remediate-build",
		Role: "builder",
		Gate: func(tc *TaskContext) agentexec.Gate {
			return GateFromRepo(tc.Repo, tc.Cfg.Budgets)
		},
		Mission: remediateMission,
		OnResult: func(tc *TaskContext, res agentexec.Result) error {
			tc.BuildSummary = res.Summary
			if res.Status == agentexec.StatusPassed && len(res.Changes) == 0 {
				tc.BuildNoChanges = true
			}
			return nil
		},
	}.Stage()
}

// StageCheckReviewPayload fails the run before any worktree or agent work
// starts if the task's review payload cannot be decoded -- a dispatch bug
// upstream, not something a builder run can recover from.
func StageCheckReviewPayload() Stage {
	return Stage{Name: "check-review-payload", Run: func(ctx context.Context, tc *TaskContext) error {
		_, err := DecodeReviewUnit(tc.Task.ReviewPayload)
		return err
	}}
}

// StageRemediationCommitPush commits and pushes the remediation to the
// existing PR branch. Unlike StageCommitPush, no changes is a normal
// outcome, not an empty-tree error: a review can be satisfied by
// explanation alone, and there is no issue to close either way -- the PR
// this task owns stays open regardless.
func StageRemediationCommitPush() Stage {
	return Stage{Name: "remediate-commit-push", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.BuildNoChanges {
			return nil
		}
		changed, err := tc.Trees.CommitAll(ctx, tc.Dir, remediationCommitMessage(tc))
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if err := tc.Trees.Push(ctx, tc.Dir, tc.Branch); err != nil {
			return err
		}
		tc.captureChanges(ctx, capturedAfterCommitPush)
		return nil
	}}
}

func remediationCommitMessage(tc *TaskContext) string {
	return fmt.Sprintf("fix: address review feedback (archie)%s", commitIssueReference("Refs", tc.Task))
}

// StageRemediationReply replies to the review this run addressed, then
// returns the task to pr_open and clears the consumed review payload so a
// later crash-and-resume cannot reprocess it. It is the workflow's only
// terminal stage: it always sets Outcome, so it always ends the run.
func StageRemediationReply() Stage {
	return Stage{Name: "remediate-reply", Run: func(ctx context.Context, tc *TaskContext) error {
		summary := tc.BuildSummary
		if tc.BuildNoChanges {
			summary = "Reviewed -- no code change was required."
		}
		reply := fmt.Sprintf("%s\n\n---\n*archie remediation, round %d*", summary, tc.Task.RemediationRounds)

		if id, ok := tc.reviewUnit.replyTarget(); ok {
			if err := tc.Forge.ReplyToReview(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber, id, reply); err != nil {
				tc.Log.Warn("remediation reply not posted", "pr", tc.Task.PRNumber, "comment", id, "err", err)
			}
		} else {
			postRemediationComment(ctx, tc, reply)
		}

		tc.Task.ReviewPayload = ""
		tc.Outcome = Outcome{Status: StatusPROpen, Detail: fmt.Sprintf("remediated review round %d", tc.Task.RemediationRounds)}
		return nil
	}}
}

// Remediate runs one remediation round against an archie-owned, still-open
// pull request in response to a forge review reaction
// (docs/prds/pr-review-remediation.md decision 4). It reuses the task's
// existing worktree and branch rather than opening a new PR.
func Remediate() Workflow {
	return Workflow{
		Name: "remediate",
		Stages: []Stage{
			StageCheckReviewPayload(),
			StageRemediationRoundCap(),
			StageResumeWorktree(),
			remediateBuildStage(),
			StageRemediationCommitPush(),
			StageRemediationReply(),
		},
	}
}
