package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// WorkflowCallStepName is the step type that starts another workflow as its
// own run. The callee is a first-class task the State Store derives from the
// caller's row: its own pinned definition, its own agent profile, its own
// container and its own outcome.
const WorkflowCallStepName = "workflow.call"

// MaxCallDepth bounds how deep one run may call callees. A cycle is refused
// at save, so the limit is not a loop guard -- it bounds a legitimate fan-out
// chain, and every wait:true caller on the path holds its container while it
// waits, so an unbounded depth would pin unbounded containers.
const MaxCallDepth = 5

// callPollInterval is how often a wait:true caller re-reads its callee's
// status. A var so tests do not sleep.
var callPollInterval = 2 * time.Second

// callReadBackoff is the wait before re-reading a callee's status after a
// transient dependency error, doubling each further failure up to
// callReadMaxBackoff. Vars so tests do not sleep.
var (
	callReadBackoff    = 500 * time.Millisecond
	callReadMaxBackoff = 5 * time.Second
)

// workflowCallSettings are the workflow.call step's settings. An inputs value is
// a literal or "inputs.<name>", a reference to the calling workflow's declared
// input.
type workflowCallSettings struct {
	Workflow string         `yaml:"workflow" doc:"The workflow to start as a child run."`
	Inputs   map[string]any `yaml:"inputs,omitempty" doc:"Callee input names mapped to scalar literals or inputs.<name> references to caller inputs."`
	Wait     bool           `yaml:"wait,omitempty" doc:"Wait for the child run to finish before continuing."`
	// Outputs publishes a callee output as one of the caller's own: the key
	// names the callee's declared output, the value an outputs.<name>
	// reference naming the caller's declared output
	Outputs map[string]string `yaml:"outputs,omitempty" doc:"Callee output names mapped to outputs.<name> references to caller outputs; requires wait: true."`
}

// callReference is the prefix an inputs value must carry to be read as a
// reference to the calling workflow's declared input.
const callReference = "inputs."

// outputReference is the prefix a call step's outputs value must carry to be
// read as a reference to the caller's own declared output (the mirror image
// of callReference: there the prefixed side is the caller's, here the map
// key is the callee's).
const outputReference = "outputs."

// refName is the identifier grammar a reference suffix must match, shared
// with the workflow interface's input names.
var refName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// WorkflowCallStepType contributes the workflow.call step type.
func WorkflowCallStepType() StepType {
	return StepType{Name: WorkflowCallStepName, Factory: newWorkflowCallStage, Settings: workflowCallSettings{}}
}

func newWorkflowCallStage(settings yaml.Node) (Stage, error) {
	var s workflowCallSettings
	if settings.Kind == 0 {
		return Stage{}, fmt.Errorf("%s: settings.workflow is required", WorkflowCallStepName)
	}
	if err := settings.Decode(&s); err != nil {
		return Stage{}, fmt.Errorf("%s: %w", WorkflowCallStepName, err)
	}
	if strings.TrimSpace(s.Workflow) == "" {
		return Stage{}, fmt.Errorf("%s: settings.workflow is required", WorkflowCallStepName)
	}
	for name, value := range s.Inputs {
		if _, ok := value.(map[string]any); ok {
			return Stage{}, fmt.Errorf("%s: input %q must be a literal or an inputs.<name> reference, not a nested document", WorkflowCallStepName, name)
		}
		if ref, ok := value.(string); ok && strings.HasPrefix(ref, callReference) && !refName.MatchString(strings.TrimPrefix(ref, callReference)) {
			return Stage{}, fmt.Errorf("%s: input %q reference %q must name an input as inputs.<name>", WorkflowCallStepName, name, ref)
		}
	}
	for name, ref := range s.Outputs {
		if !strings.HasPrefix(ref, outputReference) || !refName.MatchString(strings.TrimPrefix(ref, outputReference)) {
			return Stage{}, fmt.Errorf("%s: output %q must name a declared output as outputs.<name>", WorkflowCallStepName, name)
		}
	}
	if !s.Wait && len(s.Outputs) > 0 {
		return Stage{}, fmt.Errorf("%s: outputs on a wait:false call can never be read: nothing has finished when the caller moves on", WorkflowCallStepName)
	}
	return Stage{Name: WorkflowCallStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		return runWorkflowCall(ctx, s, tc)
	}}, nil
}

// runWorkflowCall starts the callee and, with wait true, waits for its
// terminal state. The callee reports on its own run; the call is recorded on
// the caller's timeline.
func runWorkflowCall(ctx context.Context, s workflowCallSettings, tc *TaskContext) error {
	if tc.Calls == nil {
		return fmt.Errorf("%s: this runner carries no callee capability", WorkflowCallStepName)
	}
	// The key is what makes a retry of this call site return the same child: the
	// call site's declared path, plus the callee. The callee belongs in it
	// because one task can run a second definition at a colliding path
	// (workflow.handoff and human.approve set the task's workflow, and the
	// daemon re-pins when the pin's id differs), and that run must not be
	// handed the first callee's child. A step path cannot contain "@": its
	// segments are stable identifiers joined with "/".
	path, err := workflowCallPath(tc)
	if err != nil {
		return err
	}
	key := path + "@" + s.Workflow
	if tc.Task.CallDepth+1 > MaxCallDepth {
		return fmt.Errorf("%s: call depth %d would pass the limit of %d", WorkflowCallStepName, tc.Task.CallDepth+1, MaxCallDepth)
	}
	inputs, err := resolveCallInputs(s.Inputs, tc.Task.Inputs)
	if err != nil {
		return fmt.Errorf("%s: %w", WorkflowCallStepName, err)
	}
	callee, err := tc.Calls.StartCall(ctx, tc.Task.ID, key, s.Workflow, inputs)
	if err != nil {
		return fmt.Errorf("%s: start %q: %w", WorkflowCallStepName, s.Workflow, err)
	}
	started := map[string]any{"callee_task_id": callee.ID, "workflow": s.Workflow, "wait": s.Wait}
	if err := tc.EmitDurable(ctx, events.KindWorkflowCallStarted, path,
		fmt.Sprintf("started %q as task %d", s.Workflow, callee.ID), started); err != nil {
		tc.Log.Warn("workflow call start not persisted", "err", err)
	}
	// Record the call as a step holding the callee's execution ID, named for
	// the call site -- the path, not the key that also carries the callee --
	// so the dashboard shows which call started it.
	callStep, _, err := tc.startCallStep(ctx, path, callee.ID)
	if err != nil {
		return fmt.Errorf("%s: could not be recorded: %w", WorkflowCallStepName, err)
	}
	if !s.Wait {
		return tc.closeUnwaitedCall(ctx, s, callStep, callee.ID)
	}
	// Close the call step with the callee's outcome, unless the caller itself was
	// cancelled.
	calleeOutputs, waitErr := awaitCallee(ctx, s, tc, path, callee.ID)
	if waitErr != nil {
		if ctx.Err() == nil {
			if ferr := tc.finishChildStep(ctx, callStep, taskstate.StepFailed, waitErr.Error(), 0); ferr != nil {
				return fmt.Errorf("%s: could not be closed: %w", WorkflowCallStepName, ferr)
			}
		}
		return waitErr
	}
	// The callee succeeded: the outputs its finished run wrote are what
	// this call publishes into the caller's own output set. A publish
	// failure is the caller's failure, not the call's, so the call step
	// closes failed and the run parks naming the output.
	if err := tc.publishCalleeOutputs(s.Outputs, calleeOutputs); err != nil {
		if ferr := tc.finishChildStep(ctx, callStep, taskstate.StepFailed, err.Error(), 0); ferr != nil {
			return fmt.Errorf("%s: could not be closed: %w", WorkflowCallStepName, ferr)
		}
		return err
	}
	// Later steps read the callee's outputs as this step's result.
	tc.stepResult = StepResult{Summary: fmt.Sprintf("%q (task %d) finished", s.Workflow, callee.ID), Result: calleeOutputs}
	if err := tc.finishChildStep(ctx, callStep, taskstate.StepSucceeded, tc.stepResult.Summary, 0); err != nil {
		return fmt.Errorf("%s: could not be closed: %w", WorkflowCallStepName, err)
	}
	return nil
}

// closeUnwaitedCall closes the call step of a wait:false call at once: the
// callee runs on, and nothing the caller waits on remains open.
func (tc *TaskContext) closeUnwaitedCall(ctx context.Context, s workflowCallSettings, callStep, calleeTaskID int64) error {
	if err := tc.finishChildStep(ctx, callStep, taskstate.StepSucceeded,
		fmt.Sprintf("started %q (task %d); no wait", s.Workflow, calleeTaskID), 0); err != nil {
		return fmt.Errorf("%s: could not be closed: %w", WorkflowCallStepName, err)
	}
	return nil
}

// workflowCallPath is the running call site's declared path within the run,
// which names the step and identifies the call. Every registry stage is
// wrapped, so a run reaching here without a path has no identity to key its
// child on: the store's index ignores an empty key, so keying on one would
// start a fresh child on every attempt -- the duplicate the key prevents.
func workflowCallPath(tc *TaskContext) (string, error) {
	if tc.stepPath == "" {
		return "", fmt.Errorf("%s: the running step has no declared path, so this call has no key", WorkflowCallStepName)
	}
	return tc.stepPath, nil
}

// startCallStep records the call step for a callee this run just started.
func (tc *TaskContext) startCallStep(ctx context.Context, path string, calleeTaskID int64) (int64, events.Event, error) {
	if tc.Store == nil {
		return 0, events.Event{}, nil
	}
	stepID, event, err := tc.Store.StartStep(ctx, task.StepStart{
		ExecutionID: tc.Task.ID, Attempt: tc.Task.Attempt, ParentID: tc.StepID,
		Kind: task.StepKindCall, Name: path, CalledExecutionID: calleeTaskID,
	})
	if err != nil {
		return 0, events.Event{}, err
	}
	publishEvent(tc, event)
	return stepID, event, nil
}

// resolveCallInputs turns the call's saved values into the callee's inputs: a
// reference reads the caller's actual input, a literal passes through.
func resolveCallInputs(call, callerInputs map[string]any) (map[string]any, error) {
	if len(call) == 0 {
		return nil, nil
	}
	resolved := make(map[string]any, len(call))
	for name, value := range call {
		if ref, ok := value.(string); ok && isCallReference(ref) {
			got, present := callerInputs[strings.TrimPrefix(ref, callReference)]
			if !present {
				return nil, fmt.Errorf("input %q references %q, which the calling workflow's run does not carry", name, ref)
			}
			resolved[name] = got
			continue
		}
		resolved[name] = value
	}
	return resolved, nil
}

// isCallReference reports whether a saved string value is a reference to the
// calling workflow's declared input. A literal that merely begins with
// "inputs." but does not name an input reads as the literal it is.
func isCallReference(value string) bool {
	return strings.HasPrefix(value, callReference) && refName.MatchString(strings.TrimPrefix(value, callReference))
}

// callEnded reports whether a callee status ends the caller's wait. A parked
// callee ends it too: the callee will not move without an operator, so the
// caller must not wait for one.
func callEnded(status string) bool {
	switch status {
	case StatusCompleted, StatusPROpen, StatusMerged, StatusParked, StatusDead, StatusRejected, StatusClosedWontDo:
		return true
	default:
		return false
	}
}

// awaitCallee polls the callee until it is terminal, failing the stage with
// the callee's detail when the callee did not succeed, and returning the
// callee's written outputs when it did. The caller's own wall clock bounds the
// wait.
//
// A status read that fails because the dependency is briefly unavailable is
// retried rather than failed: the callee may still be running, and the caller
// keeps the child it already started instead of starting another. Every retry
// stays inside the caller's deadline.
func awaitCallee(ctx context.Context, s workflowCallSettings, tc *TaskContext, path string, callTaskID int64) (map[string]any, error) {
	retries := 0
	for {
		status, detail, outputs, err := tc.Calls.CallStatus(ctx, tc.Task.ID, callTaskID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !transientCallError(err) {
				return nil, fmt.Errorf("%s: read task %d: %w", WorkflowCallStepName, callTaskID, err)
			}
			retries++
			wait := callReadBackoffFor(retries)
			tc.Log.Warn("callee status read failed; retrying",
				"callee_task_id", callTaskID, "attempt", retries, "in", wait, "err", err)
			if !sleepOrDone(ctx, wait) {
				return nil, ctx.Err()
			}
			continue
		}
		retries = 0
		if callEnded(status) {
			if callSucceeded(status) {
				if err := tc.EmitDurable(ctx, events.KindWorkflowCallFinished, path,
					fmt.Sprintf("%q (task %d) finished: %s", s.Workflow, callTaskID, detail),
					map[string]any{"callee_task_id": callTaskID, "workflow": s.Workflow, "status": status, "detail": detail}); err != nil {
					tc.Log.Warn("workflow call finish not persisted", "err", err)
				}
				return outputs, nil
			}
			return nil, fmt.Errorf("%s: callee %q (task %d) ended %s: %s", WorkflowCallStepName, s.Workflow, callTaskID, status, detail)
		}
		if !sleepOrDone(ctx, callPollInterval) {
			return nil, ctx.Err()
		}
	}
}

// sleepOrDone waits for d, reporting false when the caller's context ended
// first.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// transientCallError reports whether a failed callee status read is worth
// retrying: the dependency could not serve the request now, or the read's own
// timeout elapsed while the caller still had budget. A contract error -- the
// call is not this caller's, or the caller is not running -- is not transient.
func transientCallError(err error) bool {
	return errors.Is(err, storecontract.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded)
}

// callReadBackoffFor is the wait before retry n (1-based): doubling from
// callReadBackoff, capped at callReadMaxBackoff.
func callReadBackoffFor(attempt int) time.Duration {
	backoff := callReadBackoff << (attempt - 1)
	if backoff <= 0 || backoff > callReadMaxBackoff {
		return callReadMaxBackoff
	}
	return backoff
}

// callSucceeded reports whether a callee's terminal state satisfies its
// caller. pr_open and merged are successes: a repository callee opens its PR
// and reports on it as its own outcome.
func callSucceeded(status string) bool {
	switch status {
	case StatusCompleted, StatusPROpen, StatusMerged:
		return true
	default:
		return false
	}
}

// validateWorkflowCalls checks every workflow.call step against the rest of
// the collection: the callee must exist, the call's inputs must satisfy the
// callee's declared inputs, and no workflow may reach itself through calls
func validateWorkflowCalls(parsed map[string]YAMLDefinition) error {
	calls := make(map[string][]string, len(parsed))
	for id, d := range parsed {
		results := map[string]callResult{}
		err := walkSteps(d.Steps, fmt.Sprintf("workflow %q", id), func(where string, step StepRecord) error {
			if step.Type != WorkflowCallStepName {
				return nil
			}
			s, err := decodeCallSettings(step)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			callee, ok := parsed[s.Workflow]
			if !ok {
				return fmt.Errorf("%s calls %q, which is not defined", where, s.Workflow)
			}
			if err := checkCallInputs(where, d.WorkflowInterface, callee, s.Inputs); err != nil {
				return err
			}
			if err := checkCallOutputs(where, d.WorkflowInterface, callee, s.Outputs); err != nil {
				return err
			}
			calls[id] = append(calls[id], s.Workflow)
			if step.ID != "" {
				results[step.ID] = callResult{wait: s.Wait, outputs: callee.Outputs}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := checkCallResultReferences(d, fmt.Sprintf("workflow %q", id), results); err != nil {
			return err
		}
	}
	return refuseCallCycles(calls)
}

// callResult is what a workflow.call step leaves as its result: the callee's
// declared outputs, and only when the call waits for them.
type callResult struct {
	wait    bool
	outputs map[string]task.OutputSpec
}

// checkCallResultReferences refuses a reference to a call's result field the
// callee does not declare as an output, or to the result of a call that does
// not wait and so has none, in settings and when conditions alike.
func checkCallResultReferences(d YAMLDefinition, where string, results map[string]callResult) error {
	if len(results) == 0 {
		return nil
	}
	check := func(at, path string) error {
		parts := strings.Split(path, ".")
		if len(parts) < 4 || parts[0] != "steps" || parts[2] != "result" {
			return nil
		}
		call, ok := results[parts[1]]
		switch {
		case !ok:
			return nil
		case !call.wait:
			return fmt.Errorf("%s: reference {{ %s }}: call %q does not wait, so it has no result", at, path, parts[1])
		}
		if _, declared := call.outputs[parts[3]]; !declared {
			return fmt.Errorf("%s: reference {{ %s }}: the workflow call %q declares no output %q", at, path, parts[1], parts[3])
		}
		return nil
	}
	return walkSteps(d.Steps, where, func(at string, step StepRecord) error {
		var problem error
		settings := step.Settings
		walkStrings(&settings, func(value string) string {
			for _, match := range referencePattern.FindAllStringSubmatch(value, -1) {
				if err := check(at, match[1]); err != nil && problem == nil {
					problem = err
				}
			}
			return value
		})
		if problem != nil || step.When == "" {
			return problem
		}
		if c, err := parseCondition(step.When); err == nil {
			return check(at+" when", c.path)
		}
		return nil
	})
}

// walkSteps visits each step a definition carries in order, descending into
// parallel branches. A call inside a branch is a call like any other, so
// validation must see it and name it by its branch. where locates the step in
// an error message.
func walkSteps(steps []StepRecord, where string, visit func(where string, step StepRecord) error) error {
	for i, step := range steps {
		at := fmt.Sprintf("%s step %d", where, i+1)
		if err := visit(at, step); err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(step.Parallel)) {
			if err := walkSteps(step.Parallel[name], fmt.Sprintf("%s branch %q", at, name), visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeCallSettings(step StepRecord) (workflowCallSettings, error) {
	var s workflowCallSettings
	if err := step.Settings.Decode(&s); err != nil {
		return workflowCallSettings{}, err
	}
	return s, nil
}

// checkCallInputs validates one call's saved inputs against the callee's
// declared inputs, resolving references against the calling workflow's own
// declared input types. Values are not known at save time -- types are.
func checkCallInputs(where string, caller task.WorkflowInterface, callee YAMLDefinition, inputs map[string]any) error {
	for name, value := range inputs {
		spec, ok := callee.Inputs[name]
		if !ok {
			return fmt.Errorf("%s: input %q is not declared by %q", where, name, callee.ID)
		}
		resolved, err := callInputType(caller, name, value)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if resolved == "" {
			// A null literal satisfies the type check vacuously; the
			// callee's own run enforces it against the real value.
			continue
		}
		if !task.TypeAccepts(spec.Type, resolved) {
			return fmt.Errorf("%s: input %q is %s, want %s", where, name, resolved, spec.Type)
		}
	}
	for name, spec := range callee.Inputs {
		if _, assigned := inputs[name]; !assigned && spec.Required {
			return fmt.Errorf("%s: input %q is required by %q", where, name, callee.ID)
		}
	}
	return nil
}

// checkCallOutputs validates a call's outputs assignment against the callee's
// and caller's declarations.
func checkCallOutputs(where string, caller task.WorkflowInterface, callee YAMLDefinition, outputs map[string]string) error {
	for calleeName, ref := range outputs {
		calleeSpec, ok := callee.Outputs[calleeName]
		if !ok {
			return fmt.Errorf("%s: output %q is not declared by %q", where, calleeName, callee.ID)
		}
		callerName := strings.TrimPrefix(ref, outputReference)
		callerSpec, ok := caller.Outputs[callerName]
		if !ok {
			return fmt.Errorf("%s: output %q references %q, which the calling workflow does not declare", where, calleeName, ref)
		}
		if !task.TypeAccepts(callerSpec.Type, calleeSpec.Type) {
			return fmt.Errorf("%s: output %q is %s, want %s", where, calleeName, calleeSpec.Type, callerSpec.Type)
		}
	}
	return nil
}

// callInputType names the type a saved call value carries at run time: a
// reference carries the calling workflow's declared input type, a literal its
// own JSON type. An error means the reference names an input the caller does
// not declare.
func callInputType(caller task.WorkflowInterface, name string, value any) (string, error) {
	if ref, ok := value.(string); ok && isCallReference(ref) {
		spec, ok := caller.Inputs[strings.TrimPrefix(ref, callReference)]
		if !ok {
			return "", fmt.Errorf("input %q references %q, which the calling workflow does not declare", name, ref)
		}
		return spec.Type, nil
	}
	if value == nil {
		return "", nil
	}
	return task.ValueType(value), nil
}

// refuseCallCycles walks the call graph from every workflow and refuses one
// that reaches itself, directly or through other workflows, naming the path.
func refuseCallCycles(calls map[string][]string) error {
	const (
		unseen = 0
		open   = 1
		done   = 2
	)
	state := make(map[string]int, len(calls))
	var path []string
	var walk func(id string) error
	walk = func(id string) error {
		state[id] = open
		path = append(path, id)
		defer func() { state[id] = done; path = path[:len(path)-1] }()
		for _, callee := range calls[id] {
			switch state[callee] {
			case open:
				cycle := append(append([]string{}, path[cycleStart(path, callee):]...), callee)
				return fmt.Errorf("workflow %q calls itself through %s", callee, strings.Join(cycle, " -> "))
			case unseen:
				if err := walk(callee); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// Sorted, not range-order: a map iteration order picks a different DFS
	// root on every run, so an operator reading "calls itself through b ->
	// a -> b" one time and "a -> b -> a" the next would see the same cycle
	// reported two different ways with nothing having changed.
	for _, id := range slices.Sorted(maps.Keys(calls)) {
		if state[id] == unseen {
			if err := walk(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// cycleStart finds where the open path enters the cycle, so the reported path
// runs from the cycle's first workflow rather than from the DFS root.
func cycleStart(path []string, cycleRoot string) int {
	for i, id := range path {
		if id == cycleRoot {
			return i
		}
	}
	return 0
}
