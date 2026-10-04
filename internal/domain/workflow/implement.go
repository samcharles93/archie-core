package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/events"
)

// Byte limits on failing gate output: the tail logged, what the builder sees,
// and what the park reason keeps.
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
	return Stage{Name: "baseline", Run: stageBaselineGateRun}
}

// stageBaselineGateRun is StageBaselineGate's body, extracted to keep the
// composition's own length off the gate; the auto-fix agent it may dispatch
// records itself under the stage's step.
func stageBaselineGateRun(ctx context.Context, tc *TaskContext) error {
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
		// An environment failure cannot be repaired in the worktree, so park.
		if cause := gateEnvironmentCause(string(out), err); cause != "" {
			return baselineEnvironmentError(argv, string(out), cause)
		}
		tc.Log.Warn("baseline red  --  auto-fixing pre-existing gate failure",
			"cmd", strings.Join(argv, " "),
			"err", clipTail(string(out), baselineWarnLogBytes))
		if fixErr := runBaselineFix(ctx, tc, argv, out); fixErr != nil {
			return fixErr
		}
	}
	return nil
}

// runBaselineFix dispatches the builder to repair one pre-existing gate
// failure via TDD and commits the result. It is reached only for a code
// failure: an environment failure is parked by the caller before it runs.
func runBaselineFix(ctx context.Context, tc *TaskContext, argv []string, out []byte) error {
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
		strings.Join(argv, " "), tc.Repo.FullName(), strings.Join(argv, " "),
		clip(extractFailingGateOutput(string(out)), baselineMissionBytes),
	)

	modelRef := tc.Cfg.Models["builder"]
	req := agentrun.Request{
		Version:       agentrun.ProtocolVersion,
		TaskID:        tc.Task.ID,
		Attempt:       tc.Task.Attempt,
		Stage:         "baseline-fix",
		Workflow:      tc.Task.Workflow,
		Model:         modelRef,
		ContextWindow: modelContextBudget(tc.Cfg, modelRef),
		Mission:       mission,
		Budget: agentrun.Budget{
			MaxSteps:  tc.Cfg.Budgets.MaxSteps,
			WallClock: tc.Cfg.Budgets.WallClock.Std(),
		},
		Gate:       GateFromRepo(tc.Repo, tc.Cfg.Budgets),
		Protection: agentrun.Protection{Suffixes: append([]string(nil), tc.Repo.Protect...)},
	}

	res, agentErr := tc.RunAgentChild(ctx, "baseline-fix", func() (agentrun.Result, error) {
		return tc.Agent.Run(ctx, tc.Dir, req, tc.toolCallReporter("baseline-fix"))
	})
	if agentErr != nil && res.Version == 0 {
		return baselineFixFailedError(argv, string(out), fmt.Sprintf("the builder could not run: %v", agentErr))
	}
	tc.Task.TokensUsed += res.TokensUsed
	tc.Task.Iterations += res.Iterations
	accumulateUsage(&tc.RunUsage, res.Usage)
	if emitErr := tc.EmitDurable(ctx, events.KindAgentFinish, "baseline-fix", res.Summary, agentFinishData(res, modelRef)); emitErr != nil {
		return fmt.Errorf("persist baseline-fix agent finish: %w", emitErr)
	}
	if agentErr != nil {
		return baselineFixFailedError(argv, string(out), fmt.Sprintf("the builder could not run: %v", agentErr))
	}
	if res.Status != agentrun.StatusPassed {
		return baselineFixFailedError(argv, string(out), "builder status "+res.Status)
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
		tc.captureChanges(ctx, capturedAfterBaselineFix)
	}
	return nil
}
