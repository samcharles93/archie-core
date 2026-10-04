package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/domain/stableid"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

const maxRetryAttempts = 10

// checkStep validates one step against the vocabulary and the steps before
// it, and adds the ids it declares to earlier.
func checkStep(step StepRecord, mode task.RepositoryMode, earlier map[string]bool, registry StepRegistry, inBranch bool) error {
	if err := checkStepID(step.ID, earlier); err != nil {
		return err
	}
	if err := checkStepControls(step, earlier); err != nil {
		return err
	}
	if step.Parallel != nil {
		if err := checkParallel(step, mode, earlier, registry, inBranch); err != nil {
			return err
		}
	} else if err := checkTypedStep(step, mode, earlier, registry, inBranch); err != nil {
		return err
	}
	if step.ID != "" {
		earlier[step.ID] = true
	}
	return nil
}

// checkStepControls validates the settings every step carries: when,
// on_failure and retry.
func checkStepControls(step StepRecord, earlier map[string]bool) error {
	if step.OnFailure != "" && step.OnFailure != "park" && step.OnFailure != onFailureContinue {
		return fmt.Errorf("on_failure is park or continue, not %q", step.OnFailure)
	}
	if step.Retry != nil {
		if step.Retry.Attempts < 1 || step.Retry.Attempts > maxRetryAttempts {
			return fmt.Errorf("retry.attempts is 1 to %d, not %d", maxRetryAttempts, step.Retry.Attempts)
		}
		if _, err := retryBackoff(step.Retry); err != nil {
			return err
		}
	}
	if step.When == "" {
		return nil
	}
	c, err := parseCondition(step.When)
	if err != nil {
		return err
	}
	if err := checkReference(c.path, earlier); err != nil {
		return fmt.Errorf("when: %w", err)
	}
	return nil
}

func checkTypedStep(step StepRecord, mode task.RepositoryMode, earlier map[string]bool, registry StepRegistry, inBranch bool) error {
	factory, ok := registry[step.Type]
	if !ok {
		return fmt.Errorf("unknown type %q", step.Type)
	}
	if mode != task.RepositoryRequired && NeedsRepository(step.Type) {
		return fmt.Errorf("%q needs a repository, but the workflow's repository is %s", step.Type, mode)
	}
	if inBranch && !parallelSafe(step) {
		return fmt.Errorf("%q cannot run in a parallel branch: branches share the worktree, so only read-only agent.run and workflow.call steps may run there", step.Type)
	}
	if err := checkReferences(step.Settings, earlier); err != nil {
		return err
	}
	if _, err := factory(step.Settings); err != nil {
		return fmt.Errorf("%q settings: %w", step.Type, err)
	}
	return nil
}

// checkParallel validates each branch on its own: a branch sees the steps
// before the parallel step and its own earlier steps, never a sibling's.
// Every branch's ids are visible after the parallel step.
func checkParallel(step StepRecord, mode task.RepositoryMode, earlier map[string]bool, registry StepRegistry, inBranch bool) error {
	switch {
	case step.Type != "" || step.Settings.Kind != 0:
		return errors.New("a parallel step has branches, not a type or settings")
	case inBranch:
		return errors.New("parallel steps do not nest")
	case len(step.Parallel) < 2:
		return errors.New("parallel needs at least two branches")
	}
	declared := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(step.Parallel)) {
		if !stableid.Valid(name) {
			return fmt.Errorf("branch name %q is not a stable identifier", name)
		}
		branch := step.Parallel[name]
		if len(branch) == 0 {
			return fmt.Errorf("branch %q has no steps", name)
		}
		seen := maps.Clone(earlier)
		for i, branchStep := range branch {
			if declared[branchStep.ID] {
				return fmt.Errorf("branch %q step %d: step id %q is declared twice", name, i+1, branchStep.ID)
			}
			if err := checkStep(branchStep, mode, seen, registry, true); err != nil {
				return fmt.Errorf("branch %q step %d: %w", name, i+1, err)
			}
			if branchStep.ID != "" {
				declared[branchStep.ID] = true
			}
		}
	}
	maps.Copy(earlier, declared)
	return nil
}

// parallelSafe reports whether a step may run beside others in one worktree.
func parallelSafe(step StepRecord) bool {
	switch step.Type {
	case WorkflowCallStepName:
		return true
	case AgentRunStepName:
		var s agentRunSettings
		return step.Settings.Decode(&s) == nil && s.ReadOnly
	}
	return false
}

func retryBackoff(policy *RetryPolicy) (time.Duration, error) {
	if policy == nil || policy.Backoff == "" {
		return 0, nil
	}
	backoff, err := time.ParseDuration(policy.Backoff)
	if err != nil || backoff < 0 {
		return 0, fmt.Errorf("retry.backoff %q is not a duration", policy.Backoff)
	}
	return backoff, nil
}

// Compile resolves a validated YAML definition to executable stages.
func Compile(definition YAMLDefinition, registry StepRegistry) (Workflow, error) {
	stages := make([]Stage, 0, len(definition.Steps))
	for _, step := range definition.Steps {
		stage, err := compileStep(step, registry)
		if err != nil {
			return Workflow{}, err
		}
		stages = append(stages, stage)
	}
	return Workflow{Name: definition.ID, Stages: stages, Interface: definition.WorkflowInterface}, nil
}

func compileStep(step StepRecord, registry StepRegistry) (Stage, error) {
	if step.Parallel != nil {
		return compileParallel(step, registry)
	}
	factory, ok := registry[step.Type]
	if !ok {
		return Stage{}, fmt.Errorf("unknown workflow step type %q", step.Type)
	}
	if _, err := factory(step.Settings); err != nil {
		return Stage{}, fmt.Errorf("build workflow step %q: %w", step.Type, err)
	}
	// Settings are built per run, with every reference resolved.
	return wrapStep(step, step.Type, func(ctx context.Context, tc *TaskContext) error {
		stage, err := factory(renderSettings(step.Settings, tc))
		if err != nil {
			return fmt.Errorf("step %q settings: %w", step.Type, err)
		}
		return stage.Run(ctx, tc)
	}), nil
}

// wrapStep applies the controls every step shares around its body: when,
// retry, and recording its result under its id.
func wrapStep(step StepRecord, name string, body func(context.Context, *TaskContext) error) Stage {
	if step.ID != "" {
		name = step.ID
	}
	// ParseDefinition has already refused a condition or backoff that does
	// not parse.
	when, _ := parseCondition(step.When)
	backoff, _ := retryBackoff(step.Retry)
	attempts := 1
	if step.Retry != nil {
		attempts = step.Retry.Attempts
	}
	return Stage{Name: name, ContinueOnFailure: step.OnFailure == onFailureContinue, Run: func(ctx context.Context, tc *TaskContext) error {
		tc.stepResult = StepResult{}
		if step.When != "" && !when.holds(tc) {
			tc.Log.Info("step skipped", "step", name, "when", step.When)
			return nil
		}
		err := runAttempts(ctx, tc, name, attempts, backoff, body)
		if step.ID != "" {
			if tc.stepResults == nil {
				tc.stepResults = map[string]StepResult{}
			}
			tc.stepResults[step.ID] = tc.stepResult
		}
		return err
	}}
}

func runAttempts(ctx context.Context, tc *TaskContext, name string, attempts int, backoff time.Duration, body func(context.Context, *TaskContext) error) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = body(ctx, tc); err == nil || attempt == attempts {
			return err
		}
		tc.Log.Warn("step failed; retrying", "step", name, "attempt", attempt, "of", attempts, "err", err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
	}
	return err
}

// compileParallel runs each branch in its own copy of the task context, then
// folds the branches' results, usage and first outcome back into the run.
func compileParallel(step StepRecord, registry StepRegistry) (Stage, error) {
	names := slices.Sorted(maps.Keys(step.Parallel))
	branches := make([][]Stage, len(names))
	for i, name := range names {
		for _, branchStep := range step.Parallel[name] {
			stage, err := compileStep(branchStep, registry)
			if err != nil {
				return Stage{}, fmt.Errorf("branch %q: %w", name, err)
			}
			branches[i] = append(branches[i], stage)
		}
	}
	return wrapStep(step, "parallel", func(ctx context.Context, tc *TaskContext) error {
		clones := make([]*TaskContext, len(branches))
		errs := make([]error, len(branches))
		var wg sync.WaitGroup
		for i := range branches {
			clones[i] = tc.branch(names[i])
			wg.Go(func() { errs[i] = runBranch(ctx, clones[i], branches[i]) })
		}
		wg.Wait()
		for i, clone := range clones {
			tc.join(clone)
			if errs[i] != nil {
				errs[i] = fmt.Errorf("branch %q: %w", names[i], errs[i])
			}
		}
		return errors.Join(errs...)
	}), nil
}

func runBranch(ctx context.Context, tc *TaskContext, stages []Stage) error {
	for _, stage := range stages {
		if err := stage.Run(ctx, tc); err != nil && !stage.ContinueOnFailure {
			return err
		}
		if tc.Outcome.Status != "" {
			return nil
		}
	}
	return nil
}

// branch is a copy of the run's context for one parallel branch. The task is
// copied too, with its usage counters zeroed, so join can add what the branch
// spent without the branches racing on one task.
func (tc *TaskContext) branch(name string) *TaskContext {
	clone := *tc
	taskCopy := *tc.Task
	taskCopy.TokensUsed, taskCopy.Iterations = 0, 0
	clone.Task = &taskCopy
	clone.RunUsage = agentrun.Usage{}
	clone.Outcome = Outcome{}
	clone.stepResults = maps.Clone(tc.stepResults)
	clone.Log = tc.Log.With("branch", name)
	return &clone
}

// join folds a finished branch back into the run: its step results, what it
// spent, and its outcome when the run has none yet.
func (tc *TaskContext) join(branch *TaskContext) {
	for id, result := range branch.stepResults {
		if _, ok := tc.stepResults[id]; !ok {
			if tc.stepResults == nil {
				tc.stepResults = map[string]StepResult{}
			}
			tc.stepResults[id] = result
		}
	}
	tc.Task.TokensUsed += branch.Task.TokensUsed
	tc.Task.Iterations += branch.Task.Iterations
	accumulateUsage(&tc.RunUsage, branch.RunUsage)
	if tc.Outcome.Status == "" {
		tc.Outcome = branch.Outcome
	}
}
