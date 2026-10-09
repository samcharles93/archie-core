package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/domain/stableid"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

const maxRetryAttempts = 10

// checkStep validates one step against the vocabulary and the steps before
// it, and adds the ids it declares to earlier.
func checkStep(step StepRecord, mode task.RepositoryMode, earlier *refScope, registry StepRegistry, inBranch bool) error {
	if err := checkStepID(step.ID, earlier); err != nil {
		return err
	}
	if err := checkStepControls(step, earlier); err != nil {
		return err
	}
	if step.Switch != nil {
		if err := checkSwitch(step, mode, earlier, registry, inBranch); err != nil {
			return err
		}
	} else if step.Parallel != nil {
		if err := checkParallel(step, mode, earlier, registry, inBranch); err != nil {
			return err
		}
	} else if err := checkTypedStep(step, mode, earlier, registry, inBranch); err != nil {
		return err
	}
	if step.ID != "" {
		earlier.steps[step.ID] = resultFields(step)
	}
	return nil
}

// checkStepControls validates the settings every step carries: when,
// on_failure and retry.
func checkStepControls(step StepRecord, earlier *refScope) error {
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

func checkTypedStep(step StepRecord, mode task.RepositoryMode, earlier *refScope, registry StepRegistry, inBranch bool) error {
	factory, ok := registry[step.Type]
	if !ok {
		return fmt.Errorf("unknown type %q", step.Type)
	}
	// A workflow.call's step id is the identity of the call it starts -- it
	// keys the callee, so a retry of the call site returns the child already
	// started rather than a second one. It is refused here rather than in
	// validateWorkflowCalls because a pinned definition is compiled on its own
	// (ParseAndCompile), never through the collection.
	if step.Type == WorkflowCallStepName && step.ID == "" {
		return fmt.Errorf("%q needs an id: the step's id is the key of the call it starts", step.Type)
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
func checkParallel(step StepRecord, mode task.RepositoryMode, earlier *refScope, registry StepRegistry, inBranch bool) error {
	switch {
	case step.Type != "" || step.Settings.Kind != 0:
		return errors.New("a parallel step has branches, not a type or settings")
	case inBranch:
		return errors.New("parallel steps do not nest")
	case len(step.Parallel) < 2:
		return errors.New("parallel needs at least two branches")
	}
	declared := map[string]map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(step.Parallel)) {
		if !stableid.Valid(name) {
			return fmt.Errorf("branch name %q is not a stable identifier", name)
		}
		branch := step.Parallel[name]
		if len(branch) == 0 {
			return fmt.Errorf("branch %q has no steps", name)
		}
		seen := earlier.clone()
		for i, branchStep := range branch {
			if _, twice := declared[branchStep.ID]; twice {
				return fmt.Errorf("branch %q step %d: step id %q is declared twice", name, i+1, branchStep.ID)
			}
			if err := checkStep(branchStep, mode, seen, registry, true); err != nil {
				return fmt.Errorf("branch %q step %d: %w", name, i+1, err)
			}
			if branchStep.ID != "" {
				declared[branchStep.ID] = resultFields(branchStep)
			}
		}
	}
	maps.Copy(earlier.steps, declared)
	return nil
}

// checkSwitch validates each case like a branch: a case sees the steps before
// the switch and its own earlier steps. Only one case runs, so its steps need
// not be safe to run side by side, and every case's ids are visible after it.
func checkSwitch(step StepRecord, mode task.RepositoryMode, earlier *refScope, registry StepRegistry, inBranch bool) error {
	switch {
	case step.Type != "" || step.Settings.Kind != 0 || step.Parallel != nil:
		return errors.New("a switch step has cases, not a type, settings or parallel branches")
	case len(step.Switch.Cases) == 0:
		return errors.New("switch needs at least one case")
	}
	if err := checkReference(strings.TrimSpace(step.Switch.On), earlier); err != nil {
		return fmt.Errorf("switch.on: %w", err)
	}
	declared := map[string]map[string]bool{}
	arms := step.Switch.Cases
	for _, name := range slices.Sorted(maps.Keys(arms)) {
		if name == "" || strings.Contains(name, "/") {
			return fmt.Errorf("case %q is empty or contains /", name)
		}
		if len(arms[name]) == 0 {
			return fmt.Errorf("case %q has no steps", name)
		}
		seen := earlier.clone()
		for i, caseStep := range arms[name] {
			if _, twice := declared[caseStep.ID]; twice && caseStep.ID != "" {
				return fmt.Errorf("case %q step %d: step id %q is declared twice", name, i+1, caseStep.ID)
			}
			if err := checkStep(caseStep, mode, seen, registry, inBranch); err != nil {
				return fmt.Errorf("case %q step %d: %w", name, i+1, err)
			}
			if caseStep.ID != "" {
				declared[caseStep.ID] = resultFields(caseStep)
			}
		}
	}
	maps.Copy(earlier.steps, declared)
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
	if step.Switch != nil {
		return compileSwitch(step, registry)
	}
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
	return wrapStep(step, func(ctx context.Context, tc *TaskContext) error {
		stage, err := factory(renderSettings(step.Settings, tc))
		if err != nil {
			return fmt.Errorf("step %q settings: %w", step.Type, err)
		}
		return stage.Run(ctx, tc)
	}), nil
}

// StageName is the name a step runs and is recorded under: its id, else its
// type, else "parallel".
func (step StepRecord) StageName() string {
	switch {
	case step.ID != "":
		return step.ID
	case step.Parallel != nil:
		return "parallel"
	case step.Switch != nil:
		return "switch"
	}
	return step.Type
}

// wrapStep applies the controls every step shares around its body: when,
// retry, and recording its result under its id.
func wrapStep(step StepRecord, body func(context.Context, *TaskContext) error) Stage {
	name := step.StageName()
	// ParseDefinition has already refused a condition or backoff that does
	// not parse.
	when, _ := parseCondition(step.When)
	backoff, _ := retryBackoff(step.Retry)
	attempts := 1
	if step.Retry != nil {
		attempts = step.Retry.Attempts
	}
	return Stage{Name: name, ContinueOnFailure: step.OnFailure == onFailureContinue, Run: func(ctx context.Context, tc *TaskContext) error {
		// The step's path is its name qualified by the branch it runs in and
		// by every step above it. A workflow.call below keys its child on it,
		// so two runs of one call site agree and two call sites do not.
		// Restored on return: a sibling step must not inherit the path of the
		// step before it, nor a branch its parent's.
		parentPath := tc.stepPath
		tc.stepPath = joinStepPath(parentPath, branchStepName(tc.workflowBranch, name))
		defer func() { tc.stepPath = parentPath }()
		tc.stepResult = StepResult{}
		tc.skippedWhen = ""
		if step.When != "" && !when.holds(tc) {
			tc.Log.Info("step skipped", "step", name, "when", step.When)
			tc.skippedWhen = step.When
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

// joinStepPath qualifies a step name with the path of the step that encloses
// it; a top-level step's path is its own name.
func joinStepPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func runAttempts(ctx context.Context, tc *TaskContext, name string, attempts int, backoff time.Duration, body func(context.Context, *TaskContext) error) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = body(ctx, tc); err == nil || attempt == attempts {
			return err
		}
		tc.Log.Warn("step failed; retrying", "step", name, "attempt", attempt, "of", attempts, "err", err)
		if emitErr := tc.EmitDurable(ctx, events.KindStepRetried, name,
			fmt.Sprintf("attempt %d of %d failed: %v", attempt, attempts, err),
			map[string]any{"attempt": attempt, "of": attempts, "error": err.Error()}); emitErr != nil {
			tc.Log.Warn("step retry not recorded", "step", name, "err", emitErr)
		}
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
	return wrapStep(step, func(ctx context.Context, tc *TaskContext) error {
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

// compileSwitch runs the matching case in the run's own context, recording
// its steps under the case name as a parallel branch records its own.
func compileSwitch(step StepRecord, registry StepRegistry) (Stage, error) {
	arms := map[string][]Stage{}
	for name, steps := range step.Switch.Cases {
		for _, caseStep := range steps {
			stage, err := compileStep(caseStep, registry)
			if err != nil {
				return Stage{}, fmt.Errorf("case %q: %w", name, err)
			}
			arms[name] = append(arms[name], stage)
		}
	}
	on := strings.Split(strings.TrimSpace(step.Switch.On), ".")
	return wrapStep(step, func(ctx context.Context, tc *TaskContext) error {
		name := stringify(lookup(referenceScope(tc), on))
		stages, ok := arms[name]
		if !ok {
			name, stages = switchDefault, arms[switchDefault]
		}
		tc.Log.Info("switch case chosen", "step", step.StageName(), "value", name)
		if stages == nil {
			return nil
		}
		parent := tc.workflowBranch
		tc.workflowBranch = joinStepPath(parent, name)
		defer func() { tc.workflowBranch = parent }()
		return runBranch(ctx, tc, stages)
	}), nil
}

func runBranch(ctx context.Context, tc *TaskContext, stages []Stage) error {
	for _, stage := range stages {
		parentID := tc.StepID
		stepID, _, err := tc.startChildStep(ctx, task.StepKindStage, branchStepName(tc.workflowBranch, stage.Name))
		if err != nil {
			return err
		}
		tc.StepID = stepID
		runErr := stage.Run(ctx, tc)
		tc.StepID = parentID
		to, detail := stageOutcome(tc.skippedWhen, runErr)
		if runErr != nil && ctx.Err() != nil {
			to, detail = taskstate.StepInterrupted, runErr.Error()
		}
		if err := tc.finishChildStep(ctx, stepID, to, detail, 0); err != nil {
			return err
		}
		if runErr != nil && !stage.ContinueOnFailure {
			return runErr
		}
		if tc.Outcome.Status != "" {
			return nil
		}
	}
	return nil
}

// branchStepName qualifies a branch stage's recorded name with its branch, so
// two branches running the same step are distinct rows. It mirrors the name
// RunAgentChild gives a branch's agent steps; a branch step and the agent it
// runs may share a name, which is safe because they are different step kinds.
func branchStepName(branch, name string) string {
	if branch == "" {
		return name
	}
	return branch + "/" + name
}

// branch is a copy of the run's context for one parallel branch. The task is
// copied too, with its usage counters zeroed, so join can add what the branch
// spent without the branches racing on one task.
func (tc *TaskContext) branch(name string) *TaskContext {
	clone := *tc
	clone.workflowBranch = name
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
