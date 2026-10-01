package workflow

import (
	"context"
	"fmt"
	"os"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

// ReviewPullRequest runs the same decision phases as PRReview through its
// merge gate, without phase 9 or any forge writes. The caller supplies a fresh
// context with operator approval disabled.
type PRReviewDecision struct {
	Comments      []prreview.ScoredFinding
	SkippedPhases []string
}

// ReviewPullRequest refuses operator approval outright, so the gate is left
// out of the stage list rather than reached and skipped.
func ReviewPullRequest(ctx context.Context, tc *TaskContext) (PRReviewDecision, error) {
	if tc.Cfg.Review.ApproveBeforePost {
		return PRReviewDecision{}, fmt.Errorf("headless review cannot wait for operator approval")
	}
	ctx, cancel := context.WithTimeout(ctx, prReviewTotalBudget.WallClock)
	defer cancel()
	defer func() {
		if tc.prReview != nil && tc.prReview.snapshotDir != "" {
			_ = os.RemoveAll(tc.prReview.snapshotDir)
		}
	}()
	for _, stage := range prReviewDecisionStages() {
		if err := ctx.Err(); err != nil {
			return PRReviewDecision{}, err
		}
		if err := stage.Run(ctx, tc); err != nil {
			return PRReviewDecision{}, fmt.Errorf("pr-review %s: %w", stage.Name, err)
		}
		if tc.Outcome.Status != "" {
			return PRReviewDecision{}, fmt.Errorf("pr-review stopped: %s", tc.Outcome.Detail)
		}
	}
	if err := ctx.Err(); err != nil {
		return PRReviewDecision{}, err
	}
	if failed := tc.prReview.reviewerFailures.Load(); failed != 0 {
		return PRReviewDecision{}, fmt.Errorf("pr-review: %d reviewer(s) did not finish", failed)
	}
	return PRReviewDecision{
		Comments:      tc.prReview.scored,
		SkippedPhases: append([]string(nil), tc.prReview.skippedPhases...),
	}, nil
}
