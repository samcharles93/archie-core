package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

// localPRSource implements PRSource against the task's own worktree instead of
// an external forge fetch: archie's own PRs trigger reviews a change that has
// no PR number yet, so there is nothing for a forge-backed PRSource to fetch
// by.
type localPRSource struct {
	tc *TaskContext
}

var _ PRSource = (*localPRSource)(nil)

// Metadata returns the task's own title and body as the PR's.
func (s *localPRSource) Metadata(context.Context, string, string, int) (PRMetadata, error) {
	return PRMetadata{Title: s.tc.Task.Title, Body: s.tc.Task.Body}, nil
}

// Diff returns the task's own worktree diff against its base branch.
func (s *localPRSource) Diff(ctx context.Context, _, _ string, _ int) (string, error) {
	return s.tc.Trees.Diff(ctx, s.tc.Dir, s.tc.Repo.BaseBranch())
}

// Snapshot exports the worktree HEAD without .git and reports its commit SHA,
// empty when Trees cannot report it.
func (s *localPRSource) Snapshot(ctx context.Context, _, _ string, _ int, destDir string) (string, error) {
	if err := s.tc.Trees.Snapshot(ctx, s.tc.Dir, destDir); err != nil {
		return "", err
	}
	reader, ok := s.tc.Trees.(changeStatsReader)
	if !ok {
		return "", nil
	}
	stats, err := reader.ChangedFileStats(ctx, s.tc.Dir, s.tc.Repo.BaseBranch())
	if err != nil {
		return "", nil //nolint:nilerr // head SHA is best-effort provenance, not a required result
	}
	return stats.HeadSHA, nil
}

// parkDetailBytes bounds the blocking findings rendered into a park reason.
const parkDetailBytes = 4000

// unchallengedBlockingFindings returns the scored findings the merge gate
// marked blocking that the adversary did not challenge -- confirmed or never
// reviewed by the adversary both count, since only an explicit challenge argues
// the finding away.
func unchallengedBlockingFindings(scored []prreview.ScoredFinding) []prreview.ScoredFinding {
	var out []prreview.ScoredFinding
	for _, f := range scored {
		if f.Blocking && f.Adversary != prreview.AdversaryChallenged {
			out = append(out, f)
		}
	}
	return out
}

// renderBlockingFindingsDetail formats the unchallenged blocking findings
// that parked the task, in the same shape the deleted adversarial-self-
// review's renderReviewDetail used, so a park reason here reads the way an
// operator following this repository already expects.
func renderBlockingFindingsDetail(findings []prreview.ScoredFinding) string {
	var b strings.Builder
	b.WriteString("pr-review found unchallenged blocking defects:\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "- %s:%d %s (%s)\n", f.File, f.LineStart, f.Title, f.Body)
	}
	return clip(b.String(), parkDetailBytes)
}

// StagePRReviewAndOpenPR reviews the task's own change with the pr-review
// decision stages, then opens the PR and posts the findings to it. A blocking
// finding parks the task instead of opening the PR. Posting failures are
// logged only. The stages run inside this one stage because the engine stops
// at the first stage that sets an outcome.
func StagePRReviewAndOpenPR(body func(*TaskContext) string) Stage {
	return stagePRReviewAndOpenPR(prReviewDecisionStages(), body)
}

// stagePRReviewAndOpenPR is StagePRReviewAndOpenPR's implementation, taking
// the decision-stage list as a parameter so tests can substitute a small
// fake pipeline instead of scripting all nine real phases' agent calls.
func stagePRReviewAndOpenPR(decisionStages []Stage, body func(*TaskContext) string) Stage {
	return Stage{Name: "pr-review-and-open-pr", Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.PRSource == nil {
			tc.PRSource = &localPRSource{tc: tc}
		}
		for _, stage := range decisionStages {
			if err := stage.Run(ctx, tc); err != nil {
				return fmt.Errorf("pr-review (%s): %w", stage.Name, err)
			}
			if tc.Outcome.Status != "" {
				// A decision stage ended the run itself. Honor it rather than
				// proceed to open a PR the run has already decided against.
				return nil
			}
		}

		if blocking := unchallengedBlockingFindings(tc.prReview.scored); len(blocking) > 0 {
			tc.Outcome = Outcome{Status: StatusParked, Detail: renderBlockingFindingsDetail(blocking)}
			return nil
		}

		if err := OpenPR(ctx, tc, body(tc)); err != nil {
			return err
		}

		if _, err := runPROutputPhase(ctx, tc); err != nil {
			tc.Log.Error("pr-review findings not posted; the pull request is open regardless", "err", err,
				"pr", tc.Task.PRNumber)
		}
		return nil
	}}
}
