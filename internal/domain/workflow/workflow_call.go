package workflow

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

// WorkflowCallStepName is the step type that starts another workflow as its
// own run. The callee is a first-class task the State Store derives from the
// caller's row: its own pinned definition, its own agent profile, its own
// container and its own outcome (docs/prds/workflow-calls.md).
const WorkflowCallStepName = "workflow.call"

// MaxCallDepth bounds how deep one run may call callees. A cycle is refused
// at save, so the limit is not a loop guard -- it bounds a legitimate fan-out
// chain, and every wait:true caller on the path holds its container while it
// waits, so an unbounded depth would pin unbounded containers.
const MaxCallDepth = 5

// callPollInterval is how often a wait:true caller re-reads its callee's
// status. A var so tests do not sleep.
var callPollInterval = 2 * time.Second

// workflowCallSettings are the workflow.call step's settings. An inputs value
// is either a literal or a reference to the calling workflow's declared
// input, written "inputs.<name>" -- the PRD's `inputs: { src_ip:
// inputs.src_ip }` shape.
type workflowCallSettings struct {
	Workflow string         `yaml:"workflow"`
	Inputs   map[string]any `yaml:"inputs,omitempty"`
	Wait     bool           `yaml:"wait,omitempty"`
}

// callReference is the prefix an inputs value must carry to be read as a
// reference to the calling workflow's declared input.
const callReference = "inputs."

// refName is the identifier grammar a reference suffix must match, shared
// with the workflow interface's input names.
var refName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// WorkflowCallStepType contributes the workflow.call step type.
func WorkflowCallStepType() StepType {
	return StepType{Name: WorkflowCallStepName, Factory: newWorkflowCallStage}
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
	if tc.Task.CallDepth+1 > MaxCallDepth {
		return fmt.Errorf("%s: call depth %d would pass the limit of %d", WorkflowCallStepName, tc.Task.CallDepth+1, MaxCallDepth)
	}
	inputs, err := resolveCallInputs(s.Inputs, tc.Task.Inputs)
	if err != nil {
		return fmt.Errorf("%s: %w", WorkflowCallStepName, err)
	}
	callee, err := tc.Calls.StartCall(ctx, tc.Task.ID, s.Workflow, inputs)
	if err != nil {
		return fmt.Errorf("%s: start %q: %w", WorkflowCallStepName, s.Workflow, err)
	}
	started := map[string]any{"callee_task_id": callee.ID, "workflow": s.Workflow, "wait": s.Wait}
	if err := tc.EmitDurable(ctx, events.KindWorkflowCallStarted, WorkflowCallStepName,
		fmt.Sprintf("started %q as task %d", s.Workflow, callee.ID), started); err != nil {
		tc.Log.Warn("workflow call start not persisted", "err", err)
	}
	if !s.Wait {
		return nil
	}
	return awaitCallee(ctx, s, tc, callee.ID)
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
// the callee's detail when the callee did not succeed. The caller's own wall
// clock bounds the wait.
func awaitCallee(ctx context.Context, s workflowCallSettings, tc *TaskContext, callTaskID int64) error {
	for {
		status, detail, err := tc.Calls.CallStatus(ctx, tc.Task.ID, callTaskID)
		if err != nil {
			return fmt.Errorf("%s: read task %d: %w", WorkflowCallStepName, callTaskID, err)
		}
		if callEnded(status) {
			if callSucceeded(status) {
				if err := tc.EmitDurable(ctx, events.KindWorkflowCallFinished, WorkflowCallStepName,
					fmt.Sprintf("%q (task %d) finished: %s", s.Workflow, callTaskID, detail),
					map[string]any{"callee_task_id": callTaskID, "workflow": s.Workflow, "status": status, "detail": detail}); err != nil {
					tc.Log.Warn("workflow call finish not persisted", "err", err)
				}
				return nil
			}
			return fmt.Errorf("%s: callee %q (task %d) ended %s: %s", WorkflowCallStepName, s.Workflow, callTaskID, status, detail)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(callPollInterval):
		}
	}
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
// (docs/prds/workflow-calls.md).
func validateWorkflowCalls(parsed map[string]YAMLDefinition) error {
	calls := make(map[string][]string, len(parsed))
	for id, d := range parsed {
		for i, step := range d.Steps {
			if step.Type != WorkflowCallStepName {
				continue
			}
			s, err := decodeCallSettings(step)
			if err != nil {
				return fmt.Errorf("workflow %q step %d: %w", id, i+1, err)
			}
			callee, ok := parsed[s.Workflow]
			if !ok {
				return fmt.Errorf("workflow %q step %d calls %q, which is not defined", id, i+1, s.Workflow)
			}
			if err := checkCallInputs(id, i, d.WorkflowInterface, callee, s.Inputs); err != nil {
				return err
			}
			calls[id] = append(calls[id], s.Workflow)
		}
	}
	return refuseCallCycles(calls)
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
func checkCallInputs(callerID string, index int, caller task.WorkflowInterface, callee YAMLDefinition, inputs map[string]any) error {
	for name, value := range inputs {
		spec, ok := callee.Inputs[name]
		if !ok {
			return fmt.Errorf("workflow %q step %d: input %q is not declared by %q", callerID, index+1, name, callee.ID)
		}
		resolved, err := callInputType(caller, name, value)
		if err != nil {
			return fmt.Errorf("workflow %q step %d: %w", callerID, index+1, err)
		}
		if resolved == "" {
			// A null literal satisfies the type check vacuously; the
			// callee's own run enforces it against the real value.
			continue
		}
		if !task.TypeAccepts(spec.Type, resolved) {
			return fmt.Errorf("workflow %q step %d: input %q is %s, want %s", callerID, index+1, name, resolved, spec.Type)
		}
	}
	for name, spec := range callee.Inputs {
		if _, assigned := inputs[name]; !assigned && spec.Required {
			return fmt.Errorf("workflow %q step %d: input %q is required by %q", callerID, index+1, name, callee.ID)
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
