package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/events"
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

// ReviewUnit is the remediate workflow's input: one review and its actionable
// comments, decoded from Task.ReviewPayload.
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

// StageRemediationRoundCap parks the task with a comment once
// Task.RemediationRounds reaches the cap; otherwise it counts the round.
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
		unit, _ := DecodeReviewUnit(tc.Task.ReviewPayload)
		if err := tc.EmitDurable(ctx, events.KindRemediationRound, "round-cap",
			fmt.Sprintf("remediation round %d answers review %d by %s", tc.Task.RemediationRounds, unit.ReviewID, unit.Author),
			map[string]any{"round": tc.Task.RemediationRounds, "of": maxRounds, "review_id": unit.ReviewID, "author": unit.Author}); err != nil {
			tc.Log.Warn("remediation round not recorded", "err", err)
		}
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

// StageCheckReviewPayload fails the run before any worktree or agent work
// starts if the task's review payload cannot be decoded -- a dispatch bug
// upstream, not something a builder run can recover from.
func StageCheckReviewPayload() Stage {
	return Stage{Name: "check-review-payload", Run: func(ctx context.Context, tc *TaskContext) error {
		_, err := DecodeReviewUnit(tc.Task.ReviewPayload)
		return err
	}}
}
