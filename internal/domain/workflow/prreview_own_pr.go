package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

// localPRSource implements PRSource against the task's own worktree instead
// of an external forge fetch: archie's own PRs trigger (docs/prds/pr-review-
// agent.md, Triggers: "the implement workflow runs the pipeline before
// opening its PR") reviews a change that has no PR number yet, so there is
// nothing for a forge-backed PRSource to fetch by. Trees already holds
// everything the pipeline's phase 1/2 need: the diff against base, and a
// snapshot of the (as yet unopened) PR's head.
type localPRSource struct {
	tc *TaskContext
}

var _ PRSource = (*localPRSource)(nil)

// Metadata returns the task's own title and body as the PR's title and body
// -- the same text OpenPR uses to open the real PR moments later. Commit
// messages are left empty, matching the external PRSource implementation's
// precedent (internal/infrastructure/prsource): neither Trees nor Task
// carries them structured, and they are only one of three hallucination-check
// inputs, not load-bearing alone.
func (s *localPRSource) Metadata(context.Context, string, string, int) (PRMetadata, error) {
	return PRMetadata{Title: s.tc.Task.Title, Body: s.tc.Task.Body}, nil
}

// Diff returns the task's own worktree diff against its base branch.
func (s *localPRSource) Diff(ctx context.Context, _, _ string, _ int) (string, error) {
	return s.tc.Trees.Diff(ctx, s.tc.Dir, s.tc.Repo.BaseBranch())
}

// Snapshot exports the task's own worktree HEAD, no .git present, and
// reports the commit SHA it was measured on -- read through the same
// changeStatsReader capability captureChanges already uses, since Trees'
// core contract has no head-SHA method. A Trees implementation without that
// capability (a test fake, most commonly) snapshots successfully but
// reports an empty SHA; postPRReview already treats an empty headSHA as "not
// measured" rather than fails the run over it, matching captureChanges'
// same convention.
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

// parkDetailBytes bounds how much of a park Detail the rendered blocking
// findings occupy, matching the deleted adversarial-self-review's
// reviewDetailBytes (diff_rules.go's own park Detail cap headroom comment
// still points at that constant by name; it named this cap, not a specific
// symbol).
const parkDetailBytes = 4000

// unchallengedBlockingFindings returns the scored findings the merge gate
// marked blocking that the adversary did not challenge -- confirmed or never
// reviewed by the adversary both count, since only an explicit challenge
// argues the finding away (docs/prds/pr-review-agent.md, Triggers: "archie's
// own PRs").
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

// StagePRReviewAndOpenPR runs the pr-review pipeline's decision phases (1-8)
// against the task's own uncommitted change, then opens the PR body builds
// and posts the pipeline's advisory findings against it -- archie's own PRs
// trigger (docs/prds/pr-review-agent.md, Triggers). An unchallenged blocking
// finding parks the task with the findings instead of opening a PR; a
// pipeline stage failure parks the task the same way any other stage failure
// does. Once the PR is open, posting failure is logged and does not revert
// the outcome the PR's existence already earned -- the same best-effort
// convention the deleted StagePostReviewComments followed.
//
// This must run every decision stage from inside one Stage.Run body rather
// than as separate Stage entries in the workflow's list: the engine ends a
// run the instant any stage sets tc.Outcome (workflow.go's Run loop), so a
// stage that opens the PR could never be reached if the review phases were
// spliced in as their own preceding stages -- OpenPR's own doc comment names
// this exact pattern ("call this and then do so in the same stage").
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
				// A decision stage ended the run itself (operator-approval's
				// waiting_human, or a decision stage's own park). Honor it
				// rather than proceed to open a PR the operator has not
				// cleared yet.
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
