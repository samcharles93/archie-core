package workflow

import (
	"fmt"

	"github.com/samcharles93/ai-sdk/agentloop"
)

// TDD is the bugfix workflow: prove the bug with failing tests before
// fixing it. The repro stage's gate INVERTS the test command
// (ExpectFailure) — the run cannot proceed until the new tests fail for
// the right reason — and the fix stage restores the repo's full gate.
// Routed via the "bug" label.
func TDD() Workflow {
	return Workflow{
		Name: "tdd",
		Stages: []Stage{
			StagePrepareWorktree(),
			StageBaselineGate(),

			AgentStage{
				Name:     "analyse",
				Role:     "planner",
				ReadOnly: true,
				MaxSteps: 15,
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Analyse this bug report for the repository %s and determine the problem surface.\n\n"+
							"<issue number=%d>\n# %s\n\n%s\n</issue>\n\n"+
							"Explore the code read-only and call finish with status \"passed\" and a summary "+
							"containing: the root cause (file and function), the exact conditions that trigger it, "+
							"the expected vs actual behaviour, and which test cases would prove the bug. "+
							"Call finish with status \"blocked\" if you cannot locate a plausible cause.",
						tc.Repo.FullName(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body,
					)
				},
				OnResult: func(tc *TaskContext, res agentloop.Result) error {
					tc.Task.Plan = res.Summary
					return nil
				},
			}.Stage(),

			AgentStage{
				Name: "repro-tests",
				Role: "builder",
				// The inverted gate: code must still vet, but the test
				// suite MUST fail — proof the repro captures the bug.
				Gate: func(tc *TaskContext) agentloop.GateConfig {
					return agentloop.GateConfig{
						Commands: []agentloop.GateCommand{
							{Name: "vet", Argv: []string{"go", "vet", "./..."}},
							{Name: "repro-must-fail", Argv: []string{"go", "test", "./...", "-count=1"}, ExpectFailure: true},
						},
						MaxConsecutiveFailures: tc.Cfg.Budgets.GateMaxFailures,
					}
				},
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Write tests that REPRODUCE this bug in the repository %s. Do NOT fix the bug yet.\n\n"+
							"<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<analysis>\n%s\n</analysis>\n\n"+
							"Add tests that pass once the bug is fixed but FAIL today because of it. The gate is "+
							"inverted for this stage: it requires 'go vet' to pass and 'go test ./...' to FAIL. "+
							"Do not touch non-test files. Call finish with status \"passed\" once the failing "+
							"repro is in place, summarising which tests capture the bug.",
						tc.Repo.FullName(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Plan,
					)
				},
			}.Stage(),

			StageCommit("commit-repro", func(tc *TaskContext) string {
				return fmt.Sprintf("test: failing repro for #%d", tc.Task.IssueNumber)
			}),

			AgentStage{
				Name: "fix",
				Role: "builder",
				Gate: func(tc *TaskContext) agentloop.GateConfig {
					return GateFromRepo(tc.Repo, tc.Cfg.Budgets)
				},
				ExtraRules: "The repro tests written in the previous stage are the bug's specification. " +
					"Never weaken, skip, or delete them — make them pass by fixing the code they exercise.",
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Fix the bug in the repository %s. Failing repro tests are already committed; make them pass.\n\n"+
							"<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<analysis>\n%s\n</analysis>\n\n"+
							"The full quality gate (including 'go test ./...') must pass. Make the smallest fix "+
							"that makes the repro tests pass without changing them. Call finish with status "+
							"\"passed\" and a summary for the PR reviewer: root cause, the fix, and verification.",
						tc.Repo.FullName(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Plan,
					)
				},
				OnResult: func(tc *TaskContext, res agentloop.Result) error {
					tc.BuildSummary = res.Summary
					return nil
				},
			}.Stage(),

			StageCommitPush(func(tc *TaskContext) string {
				return fmt.Sprintf("fix: %s (archie)\n\nFixes #%d", tc.Task.Title, tc.Task.IssueNumber)
			}),
			StageDiffCap(),
			StageOpenPR(func(tc *TaskContext) string {
				return fmt.Sprintf("%s\n\n---\n*workflow: tdd (failing repro committed first) · %d iterations · %d tokens*\n\nCloses #%d",
					tc.BuildSummary, tc.Task.Iterations, tc.Task.TokensUsed, tc.Task.IssueNumber)
			}),
		},
	}
}
