package workflow

import (
	"context"
	"fmt"

	"github.com/samcharles93/ai-sdk/agentloop"

	"github.com/samcharles93/archie-core/internal/config"
)

// AgentStage is the reusable bridge from a workflow stage to an
// agentloop run. Every LLM-driven stage in any workflow (implement's
// planner/builder, tdd's test-writer/fixer, feasibility's analyst)
// is an AgentStage with a different mission, gate, and result handler —
// never a new engine.
type AgentStage struct {
	Name string
	// Role selects the model via cfg.Models[Role]; falls back to
	// cfg.Models["builder"].
	Role     string
	ReadOnly bool
	// Mission produces the task statement from the current context.
	Mission func(*TaskContext) string
	// Gate returns the stage's quality gate; nil means ungated (e.g.
	// read-only analysis stages).
	Gate func(*TaskContext) agentloop.GateConfig
	// ExtraRules is appended to the system prompt.
	ExtraRules string
	// MaxSteps overrides the configured step budget when > 0 (planner
	// stages are cheaper than builder stages).
	MaxSteps int
	// ProtectPaths blocks write/edit on matching paths for this stage —
	// an environmental constraint, not a prompt rule (TDD's fix stage
	// protects the committed repro tests).
	ProtectPaths func(path string) bool
	// OnResult consumes a successful (passed) result. Parked and idle
	// results park the workflow before OnResult is called.
	OnResult func(*TaskContext, agentloop.Result) error
}

// Stage adapts the AgentStage to the engine.
func (a AgentStage) Stage() Stage {
	return Stage{Name: a.Name, Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.Runtime == nil {
			return fmt.Errorf("no LLM runtime configured (missing [providers] in config?)")
		}
		modelRef := tc.Cfg.Models[a.Role]
		if modelRef == "" {
			modelRef = tc.Cfg.Models["builder"]
		}
		if modelRef == "" {
			return fmt.Errorf("no model configured for role %q (set [models] in config)", a.Role)
		}

		budget := agentloop.Budget{
			MaxSteps:  tc.Cfg.Budgets.MaxSteps,
			MaxTokens: tc.Cfg.Budgets.MaxTokens,
			WallClock: tc.Cfg.Budgets.WallClock.Std(),
		}
		if a.MaxSteps > 0 {
			budget.MaxSteps = a.MaxSteps
		}

		var gate agentloop.GateConfig
		if a.Gate != nil {
			gate = a.Gate(tc)
		}

		res, err := agentloop.Run(ctx, agentloop.Config{
			Runtime:    tc.Runtime,
			ModelRef:   modelRef,
			WorkDir:    tc.Dir,
			Mission:    a.Mission(tc),
			ExtraRules: a.ExtraRules,
			Notes:      taskNotes{tc: tc},
			Gate:       gate,
			Preflight: []agentloop.GateCommand{
				{Name: "go-version", Argv: []string{"go", "version"}},
			},
			Budget:       budget,
			ReadOnly:     a.ReadOnly,
			ProtectPaths: a.ProtectPaths,
			Logger:       tc.Log.With("stage", a.Name, "model", modelRef),
		})
		if err != nil {
			return fmt.Errorf("agent run: %w", err)
		}

		tc.Task.TokensUsed += res.TokensUsed
		tc.Task.Iterations += res.Iterations
		tc.Emit("agent_finish", a.Name, res.Summary, map[string]any{
			"status": string(res.Status), "stop_reason": res.StopReason,
			"tokens": res.TokensUsed, "iterations": res.Iterations, "model": modelRef,
		})

		if res.Status != agentloop.StatusPassed {
			detail := res.Detail
			if detail == "" {
				detail = res.Summary
			}
			return fmt.Errorf("agent %s (%s): %s", res.Status, res.StopReason, clip(detail, 2000))
		}
		if a.OnResult != nil {
			return a.OnResult(tc, res)
		}
		return nil
	}}
}

// taskNotes stores agent notes on the task row — orchestrator-side, so
// they survive worktree cleanup and never pollute the PR diff.
type taskNotes struct{ tc *TaskContext }

func (n taskNotes) Load(context.Context) (string, error) {
	return n.tc.Task.Notes, nil
}

func (n taskNotes) Append(ctx context.Context, entry string) error {
	n.tc.Task.Notes += "- " + entry + "\n"
	return n.tc.Store.Update(ctx, n.tc.Task)
}

// GateFromRepo converts the repo's configured command lists into an
// agentloop gate.
func GateFromRepo(repo config.Repo, budgets config.Budgets) agentloop.GateConfig {
	cmds := make([]agentloop.GateCommand, 0, len(repo.Gate))
	for _, argv := range repo.Gate {
		if len(argv) == 0 {
			continue
		}
		cmds = append(cmds, agentloop.GateCommand{Name: argv[0], Argv: argv})
	}
	return agentloop.GateConfig{Commands: cmds, MaxConsecutiveFailures: budgets.GateMaxFailures}
}
