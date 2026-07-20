package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/samcharles93/ai-sdk/agentloop"
)

// StageBaselineGate verifies the repo's gate is green at the base commit
// before any planning starts — a red baseline would park-storm the
// builder with failures it didn't cause.
func StageBaselineGate() Stage {
	return Stage{Name: "baseline", Run: func(ctx context.Context, tc *TaskContext) error {
		for _, argv := range tc.Repo.Gate {
			if len(argv) == 0 {
				continue
			}
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = tc.Dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("baseline red — %s fails at %s before archie touched anything:\n%s",
					strings.Join(argv, " "), tc.Repo.BaseBranch(), clip(string(out), 2000))
			}
		}
		return nil
	}}
}

// Implement is the default workflow: plan read-only, post the plan to
// the issue for visibility, build behind the repo's gate, open the PR
// with the builder's own summary.
func Implement() Workflow {
	return Workflow{
		Name: "implement",
		Stages: []Stage{
			StagePrepareWorktree(),
			StageBaselineGate(),

			AgentStage{
				Name:     "plan",
				Role:     "planner",
				ReadOnly: true,
				MaxSteps: 15,
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Produce a concrete implementation plan for this GitHub issue on the repository %s.\n\n"+
							"<issue number=%d>\n# %s\n\n%s\n</issue>\n\n"+
							"Explore the codebase with your read-only tools, then call finish with status "+
							"\"passed\" and the plan as the summary: files to touch, the approach, acceptance "+
							"criteria, and what tests should prove it. Keep the plan tightly scoped to the issue — "+
							"call finish with status \"blocked\" if the issue is too vague or too large for one PR.",
						tc.Repo.FullName(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body,
					)
				},
				OnResult: func(tc *TaskContext, res agentloop.Result) error {
					tc.Task.Plan = res.Summary
					body := fmt.Sprintf("**archie's plan** (review now if you want to veto — building starts immediately):\n\n%s", res.Summary)
					if err := tc.Forge.Comment(context.Background(), tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, body); err != nil {
						tc.Log.Warn("failed to post plan comment", "err", err)
					}
					return nil
				},
			}.Stage(),

			AgentStage{
				Name: "build",
				Role: "builder",
				Gate: func(tc *TaskContext) agentloop.GateConfig {
					return GateFromRepo(tc.Repo, tc.Cfg.Budgets)
				},
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Implement this GitHub issue on the repository %s, following the plan below.\n\n"+
							"<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<plan>\n%s\n</plan>\n\n"+
							"Make the smallest change that satisfies the issue and the plan's acceptance criteria. "+
							"Do not run git — the orchestrator commits and pushes for you. When done, call finish "+
							"with status \"passed\" and a summary written for the human who will review the pull "+
							"request: what changed, why, and how it was verified.",
						tc.Repo.FullName(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Plan,
					)
				},
				OnResult: func(tc *TaskContext, res agentloop.Result) error {
					tc.BuildSummary = res.Summary
					return nil
				},
			}.Stage(),

			StageCommitPush(func(tc *TaskContext) string {
				return fmt.Sprintf("%s (archie)\n\nImplements #%d", tc.Task.Title, tc.Task.IssueNumber)
			}),
			StageDiffCap(),
			StageOpenPR(func(tc *TaskContext) string {
				return fmt.Sprintf("%s\n\n---\n*workflow: implement · %d iterations · %d tokens*\n\nCloses #%d",
					tc.BuildSummary, tc.Task.Iterations, tc.Task.TokensUsed, tc.Task.IssueNumber)
			}),
		},
	}
}
