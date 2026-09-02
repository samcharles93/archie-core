package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/events"
)

// Bounds on how much of a failing gate command's output rides along at each
// point in this stage. They serve different purposes and are sized
// independently, not derived from one another:
//   - baselineWarnLogBytes is the tail shown in daemon/container logs. Test
//     runners put the useful failure after their long list of passing packages,
//     so keeping the head made the operator log actively misleading.
//   - baselineMissionBytes is what the builder agent sees -- generous, since
//     it needs enough context to actually diagnose the failure.
//   - baselineParkOutputBytes is what a human (or Archie's own chat tools)
//     sees in the park reason when the builder couldn't fix it. It used to
//     be zero: the returned error carried only a status string, so a park
//     reason like "go build ./... fails ... (status: failed)" gave no way to
//     see the actual compiler error. It stays comfortably under the store's
//     own park-reason cap (see internal/store's Task.ParkReason clip).
const (
	baselineWarnLogBytes    = 2048
	baselineMissionBytes    = 3000
	baselineParkOutputBytes = 2048
)

// StageBaselineGate verifies the repo's gate is green at the base commit.
// If the gate is red due to pre-existing failures, the builder attempts to
// fix them via TDD before proceeding. Only parks if the builder cannot fix
// the failures.
func StageBaselineGate() Stage {
	return Stage{Name: "baseline", Run: func(ctx context.Context, tc *TaskContext) error {
		for _, argv := range tc.Repo.Gate {
			if len(argv) == 0 {
				continue
			}
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = tc.Dir
			out, err := cmd.CombinedOutput()
			if err == nil {
				continue
			}
			tc.Log.Warn("baseline red  --  auto-fixing pre-existing gate failure",
				"cmd", strings.Join(argv, " "),
				"err", clipTail(string(out), baselineWarnLogBytes))

			// Run builder to fix the failure via TDD.
			mission := fmt.Sprintf(
				"The baseline gate check `%s` failed on the repository %s at the base commit "+
					"(before any feature work). This is a pre-existing issue  --  not caused by your work. "+
					"Fix it using TDD:\n\n"+
					"1. Write a test that proves the failure exists\n"+
					"2. Run the test  --  it should FAIL (confirming the bug)\n"+
					"3. Fix the root cause\n"+
					"4. Run the test  --  it should PASS\n"+
					"5. Repeat until the gate `%s` passes for all packages\n\n"+
					"Gate output:\n%s\n\n"+
					"When the gate passes, call finish with status \"passed\".",
				strings.Join(argv, " "), tc.Repo.FullName(), strings.Join(argv, " "), clip(string(out), baselineMissionBytes),
			)

			modelRef := tc.Cfg.Models["builder"]
			req := agentexec.Request{
				Version:       agentexec.ProtocolVersion,
				TaskID:        tc.Task.ID,
				Attempt:       tc.Task.Attempt,
				Stage:         "baseline-fix",
				Workflow:      tc.Task.Workflow,
				Model:         modelRef,
				ContextWindow: modelContextBudget(tc.Cfg, modelRef),
				Mission:       mission,
				Budget: agentexec.Budget{
					MaxSteps:  tc.Cfg.Budgets.MaxSteps,
					WallClock: tc.Cfg.Budgets.WallClock.Std(),
				},
				Gate:       GateFromRepo(tc.Repo, tc.Cfg.Budgets),
				Protection: agentexec.Protection{Suffixes: append([]string(nil), tc.Repo.Protect...)},
			}

			res, agentErr := tc.Agent.Run(ctx, tc.Dir, req, tc.toolCallReporter("baseline-fix"))
			if agentErr != nil && res.Version == 0 {
				return fmt.Errorf("baseline-fix agent run: %w", agentErr)
			}
			tc.Task.TokensUsed += res.TokensUsed
			tc.Task.Iterations += res.Iterations
			if emitErr := tc.EmitDurable(ctx, events.KindAgentFinish, "baseline-fix", res.Summary, agentFinishData(res, modelRef)); emitErr != nil {
				return fmt.Errorf("persist baseline-fix agent finish: %w", emitErr)
			}
			if agentErr != nil {
				return fmt.Errorf("baseline-fix agent run: %w", agentErr)
			}
			if res.Status != agentexec.StatusPassed {
				return fmt.Errorf("baseline red  --  %s fails and builder could not auto-fix (status: %s)\n\ngate output:\n%s",
					strings.Join(argv, " "), res.Status, clipTail(string(out), baselineParkOutputBytes))
			}

			// Commit the baseline fix.
			changed, commitErr := tc.Trees.CommitAll(ctx, tc.Dir,
				fmt.Sprintf("fix: baseline gate repair (%s)", strings.Join(argv, " ")))
			if commitErr != nil {
				tc.Log.Warn("baseline fix commit failed", "err", commitErr)
			}
			if changed {
				tc.BuildSummary = res.Summary
				tc.BaselineFixed = true
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
			StageRepoStages(),
			StageBaselineGate(),

			AgentStage{
				Name:     "plan",
				Role:     "planner",
				ReadOnly: true,
				Mission: func(tc *TaskContext) string {
					prd := ""
					if tc.Task.Plan != "" {
						// An approved feasibility PRD precedes this run;
						// the plan refines it rather than starting cold.
						prd = "\n<approved_prd>\n" + tc.Task.Plan + "\n</approved_prd>\n"
					}
					return fmt.Sprintf(
						"Produce a concrete implementation plan for this %s on the repository %s.\n"+prd+"\n"+
							"%s\n\n"+
							"Explore the codebase with your read-only tools, then call finish with status "+
							"\"passed\" and the plan as the summary: files to touch, the approach, acceptance "+
							"criteria, and what tests should prove it. Keep the plan tightly scoped to the task  --  "+
							"call finish with status \"blocked\" if the task is too vague or too large for one PR.",
						taskKind(tc.Task), tc.Repo.FullName(), taskPromptBlock(tc.Task),
					)
				},
				OnResult: func(tc *TaskContext, res agentexec.Result) error {
					tc.Task.Plan = res.Summary
					return nil
				},
			}.Stage(),

			AgentStage{
				Name: "build",
				Role: "builder",
				Gate: func(tc *TaskContext) agentexec.Gate {
					return GateFromRepo(tc.Repo, tc.Cfg.Budgets)
				},
				ExtraRules: "Files matching the repository's protected suffixes (e.g. generated code) are " +
					"write-blocked  --  edit their sources instead; the gate regenerates them.",
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Implement this %s on the repository %s, following the plan below.\n\n"+
							"%s\n\n<plan>\n%s\n</plan>\n\n"+
							"Make the smallest change that satisfies the task and the plan's acceptance criteria. "+
							"Do not run git  --  the orchestrator commits and pushes for you. When done, call finish "+
							"with status \"passed\" and a summary written for the human who will review the pull "+
							"request: what changed, why, and how it was verified.",
						taskKind(tc.Task), tc.Repo.FullName(), taskPromptBlock(tc.Task), tc.Task.Plan,
					)
				},
				OnResult: func(tc *TaskContext, res agentexec.Result) error {
					tc.BuildSummary = res.Summary
					if res.Status == agentexec.StatusPassed && len(res.Changes) == 0 && !tc.BaselineFixed {
						tc.BuildNoChanges = true
					}
					return nil
				},
			}.Stage(),

			StageCommitPush(func(tc *TaskContext) string {
				return fmt.Sprintf("%s (archie)", tc.Task.Title) +
					commitIssueReference("Implements", tc.Task)
			}),
			StageYaegiGate(),
			StageDiffCap(),
			StageReview(),
			StageOpenPR(func(tc *TaskContext) string {
				return fmt.Sprintf("%s\n\n---\n*workflow: implement · %d iterations · %d tokens*",
					tc.BuildSummary, tc.Task.Iterations, tc.Task.TokensUsed)
			}),
		},
	}
}
