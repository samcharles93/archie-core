package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
)

// TDD is the bugfix workflow: prove the bug with failing tests before
// fixing it. The repro stage's gate INVERTS the test command
// (ExpectFailure)  --  the run cannot proceed until the new tests fail for
// the right reason  --  and the fix stage restores the repo's full gate.
// Routed via the "bug" label.
func TDD() Workflow {
	return Workflow{
		Name: "tdd",
		Stages: []Stage{
			StagePrepareWorktree(),
			StageRepoStages(),
			StageBaselineGate(),

			AgentStage{
				Name:     "analyse",
				Role:     "planner",
				ReadOnly: true,
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
				OnResult: func(tc *TaskContext, res agentexec.Result) error {
					tc.Task.Plan = res.Summary
					return nil
				},
			}.Stage(),

			AgentStage{
				Name: "repro-tests",
				Role: "builder",
				// The inverted gate: code-quality commands must still
				// pass, but the test command (last in repo.Gate) MUST
				// fail  --  proof the repro captures the bug.
				Gate: func(tc *TaskContext) agentexec.Gate {
					return tddReproGate(tc.Repo, tc.Cfg.Budgets)
				},
				Mission: func(tc *TaskContext) string {
					cmd := testCommand(tc.Repo)
					return fmt.Sprintf(
						"Write tests that REPRODUCE this bug in the repository %s. Do NOT fix the bug yet.\n\n"+
							"<issue number=%d>\n# %s\n\n%s\n</issue>\n\n<analysis>\n%s\n</analysis>\n\n"+
							"Add tests that pass once the bug is fixed but FAIL today because of it. The gate is "+
							"inverted for this stage: it requires the full gate to pass EXCEPT %s which is required "+
							"to FAIL. Do not touch non-test files. Call finish with status \"passed\" once the failing "+
							"repro is in place, summarising which tests capture the bug.",
						tc.Repo.FullName(), tc.Task.IssueNumber, tc.Task.Title, tc.Task.Body, tc.Task.Plan, cmd,
					)
				},
			}.Stage(),

			// Capture the failing test run  --  the proof the repro
			// reproduces the bug, posted on the PR later.
			{Name: "capture-proof", Run: func(ctx context.Context, tc *TaskContext) error {
				argv := testCommandArgv(tc.Repo)
				if len(argv) == 0 {
					return fmt.Errorf("tdd: repo %s has no gate configured  --  cannot capture repro proof (add at least one [[repos.gate]] command, with the test runner last)", tc.Repo.FullName())
				}
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Dir = tc.Dir
				out, err := cmd.CombinedOutput()
				if err == nil {
					return fmt.Errorf("repro tests unexpectedly pass after commit  --  the bug is not captured")
				}
				tc.ReproProof = clip(string(out), 4000)
				return nil
			}},

			StageCommit("commit-repro", func(tc *TaskContext) string {
				return fmt.Sprintf("test: failing repro for #%d", tc.Task.IssueNumber)
			}),

			AgentStage{
				Name: "fix",
				Role: "builder",
				Gate: func(tc *TaskContext) agentexec.Gate {
					return GateFromRepo(tc.Repo, tc.Cfg.Budgets)
				},
				// Environmental, not advisory: the fix stage cannot touch
				// test files at all  --  the committed repro is the spec.
				ProtectGlobs: func(tc *TaskContext) []string {
					glob := tc.Repo.ResolvedTestGlob()
					if glob == "" {
						return nil
					}
					return []string{glob}
				},
				ExtraRules: "The repro tests written in the previous stage are the bug's specification. " +
					"Test files are write-protected in this stage  --  make them pass by fixing the code they exercise.",
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
				OnResult: func(tc *TaskContext, res agentexec.Result) error {
					tc.BuildSummary = res.Summary
					return nil
				},
			}.Stage(),

			StageCommitPush(func(tc *TaskContext) string {
				return fmt.Sprintf("fix: %s (archie)\n\nFixes #%d", tc.Task.Title, tc.Task.IssueNumber)
			}),
			StageYaegiGate(),
			StageDiffCap(),

			// Open the PR, then post the captured failing-test output as
			// evidence the bug was reproduced before the fix.
			{Name: "open-pr", Run: func(ctx context.Context, tc *TaskContext) error {
				body := fmt.Sprintf("%s\n\n---\n*workflow: tdd (failing repro committed first) · %d iterations · %d tokens*\n\nCloses #%d",
					tc.BuildSummary, tc.Task.Iterations, tc.Task.TokensUsed, tc.Task.IssueNumber)
				if err := OpenPR(ctx, tc, body); err != nil {
					return err
				}
				if tc.ReproProof != "" {
					proof := fmt.Sprintf("**Proof the repro tests failed before the fix** (commit 1 of this PR):\n\n```\n%s\n```", tc.ReproProof)
					if _, err := tc.Forge.Comment(ctx, tc.Task.Owner, tc.Task.Repo, tc.Task.PRNumber, proof); err != nil {
						tc.Log.Warn("failed to post repro proof", "err", err)
					}
				}
				return nil
			}},
		},
	}
}

// tddReproGate builds the inverted gate for the repro-tests stage:
// every command from repo.Gate runs normally except the last one (the
// test runner, by convention), which gets ExpectFailure  --  the repro
// must fail the tests to prove the bug exists.
func tddReproGate(repo config.Repo, budgets config.Budgets) agentexec.Gate {
	if len(repo.Gate) == 0 {
		return agentexec.Gate{}
	}
	cmds := make([]agentexec.Command, 0, len(repo.Gate))
	for _, argv := range repo.Gate {
		if len(argv) == 0 {
			continue
		}
		gc := agentexec.Command{Name: argv[0], Argv: argv}
		cmds = append(cmds, gc)
	}
	if len(cmds) > 0 {
		cmds[len(cmds)-1].ExpectFailure = true
	}
	return agentexec.Gate{
		Commands:               cmds,
		MaxConsecutiveFailures: budgets.GateMaxFailures,
	}
}

// testCommand returns a human-readable representation of the test
// command (the last entry in repo.Gate).
func testCommand(repo config.Repo) string {
	last := testCommandArgv(repo)
	if len(last) == 0 {
		return "(no gate configured)"
	}
	return strings.Join(last, " ")
}

// testCommandArgv returns the test command argv (the last entry in
// repo.Gate), or nil when no gate is configured.
func testCommandArgv(repo config.Repo) []string {
	for i := len(repo.Gate) - 1; i >= 0; i-- {
		if len(repo.Gate[i]) > 0 {
			return repo.Gate[i]
		}
	}
	return nil
}
