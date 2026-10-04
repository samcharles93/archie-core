package workflow

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Review step types: reviewing a pull request, and answering a review of
// archie's own.
const (
	ReviewPRStepName    = "review.pr"
	ReviewRoundStepName = "review.start-round"
	ReviewReplyStepName = "review.reply"
)

// ReviewStepTypes contributes the review step types.
func ReviewStepTypes() []StepType {
	return []StepType{
		{Name: ReviewPRStepName, Factory: newReviewPRStage},
		{Name: ReviewRoundStepName, Factory: newReviewRoundStage},
		{Name: ReviewReplyStepName, Factory: newReviewReplyStage, Settings: reviewReplySettings{}},
	}
}

// newReviewPRStage runs the whole review pipeline on the task's pull request:
// intake, anatomy, lenses, review, precision gate, verification, coverage,
// synthesis, operator approval, merge gate and output. Its phases share
// private state, so they run as one step.
func newReviewPRStage(yaml.Node) (Stage, error) {
	phases := prReviewStandaloneStages()
	return Stage{Name: ReviewPRStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		for _, phase := range phases {
			if err := phase.Run(ctx, tc); err != nil {
				return fmt.Errorf("%s (%s): %w", ReviewPRStepName, phase.Name, err)
			}
			if tc.Outcome.Status != "" {
				return nil
			}
		}
		return nil
	}}, nil
}

// newReviewRoundStage opens one remediation round: it refuses a review
// payload that does not decode, parks with a PR comment once the repository's
// round cap is reached, and otherwise counts the round.
func newReviewRoundStage(yaml.Node) (Stage, error) {
	check, roundCap := StageCheckReviewPayload(), StageRemediationRoundCap()
	return Stage{Name: ReviewRoundStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		if err := check.Run(ctx, tc); err != nil {
			return err
		}
		if err := roundCap.Run(ctx, tc); err != nil {
			return err
		}
		tc.reviewUnit, _ = DecodeReviewUnit(tc.Task.ReviewPayload)
		return nil
	}}, nil
}

type reviewReplySettings struct {
	Body string `yaml:"body" doc:"The reply to the review."`
}

// newReviewReplyStage answers the review this round addressed, in its own
// thread when it has one, clears the consumed payload so a resumed run cannot
// address it twice, and returns the task to pr_open.
func newReviewReplyStage(settings yaml.Node) (Stage, error) {
	var s reviewReplySettings
	if err := decodeSettings(ReviewReplyStepName, settings, &s); err != nil {
		return Stage{}, err
	}
	if strings.TrimSpace(s.Body) == "" {
		return Stage{}, fmt.Errorf("%s: settings.body is required", ReviewReplyStepName)
	}
	return Stage{Name: ReviewReplyStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		reply := fmt.Sprintf("%s\n\n---\n*archie remediation, round %d*", s.Body, tc.Task.RemediationRounds)
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
	}}, nil
}
