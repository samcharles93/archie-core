package workflow

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// Declared workflow outputs: how a run writes one and how a wait:true caller
// publishes one (docs/prds/workflow-call-outputs.md). A run's only structured
// results are capture calls, so declared outputs ride the capture path: each
// is offered to the run's agent stages as a capture tool named after it, and
// the engine applies, validates, publishes and persists the accepted values.

// workflowInterface is the declaration the run's compiled workflow carries
// (Workflow.Interface, set by Run); a stage body invoked outside Run falls
// back to the pinned definition's YAML, which the pin guarantees matches the
// compile the request came from.
func (tc *TaskContext) workflowInterface() task.WorkflowInterface {
	if tc.runInterfaceSet {
		return tc.runInterface
	}
	if tc.ifaceParsed == nil {
		parsed, err := task.ParseWorkflowInterface(tc.Task.WorkflowDefinitionYAML)
		if err != nil {
			// The pinned YAML was validated when the definition was saved, so
			// a parse failure here cannot be a definition the engine accepts;
			// an empty interface keeps such a run output-less.
			parsed = task.WorkflowInterface{}
			tc.Log.Warn("pinned workflow definition does not parse for outputs; ignoring declared outputs", "err", err)
		}
		tc.ifaceParsed = &parsed
	}
	return *tc.ifaceParsed
}

// WriteOutput records one declared output's value for this attempt: the path
// a deterministic Go stage takes. An undeclared key, a mistyped value or a
// second write is refused, and the refusing error parks the run before its
// outcome transition.
func (tc *TaskContext) WriteOutput(name string, value any) error {
	spec, ok := tc.workflowInterface().Outputs[name]
	if !ok {
		return fmt.Errorf("output %q is not declared by the workflow", name)
	}
	if _, written := tc.Task.Outputs[name]; written {
		return fmt.Errorf("output %q has already been written; each output is written once per attempt", name)
	}
	if value != nil {
		if got := task.ValueType(value); !task.TypeAccepts(spec.Type, got) {
			return fmt.Errorf("output %q is %s, want %s", name, got, spec.Type)
		}
	}
	if tc.Task.Outputs == nil {
		tc.Task.Outputs = map[string]any{}
	}
	tc.Task.Outputs[name] = value
	return nil
}

// outputCaptureTool builds the capture tool one declared output is offered
// as: named after the output, whose arguments are {"value": ...}, with the
// declared type as the value's schema. The schema guides the agent; the
// run-time re-check is what enforces.
func outputCaptureTool(name string, spec task.OutputSpec) agentexec.CaptureTool {
	value := map[string]any{"description": fmt.Sprintf("The value of the %q output.", name)}
	if typ := jsonSchemaType(spec.Type); typ != "" {
		value["type"] = typ
	}
	params, err := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"value": value},
		"required":   []string{"value"},
	})
	if err != nil {
		// A fixed-shape map cannot fail to marshal. Keep the call acceptable
		// through RequiredFields alone if it ever did.
		params = []byte(`{"type":"object"}`)
	}
	return agentexec.CaptureTool{
		Name:        name,
		Description: fmt.Sprintf("Write the workflow output %q. Call exactly once, with the value as the value argument.", name),
		Parameters:  params,
		// The value member is required, but a null value counts as not written
		// (there is nothing to reject: the run simply does not write it).
		RequiredFields: []string{"value"},
		MaxCalls:       1,
	}
}

// jsonSchemaType maps the workflow value vocabulary onto JSON Schema's type
// names. "any" has no constraint, so it yields no type at all -- an empty
// string, never a null that is not a valid JSON Schema type.
func jsonSchemaType(vocab string) string {
	switch vocab {
	case "string", "number", "object", "array":
		return vocab
	case "bool":
		return "boolean"
	default: // "any"
		return ""
	}
}

// appendOutputTools offers the declared outputs the run has not written yet
// to this stage as capture tools, refusing a stage capture tool that shares a
// declared output's name: the value would otherwise be ambiguous.
func (tc *TaskContext) appendOutputTools(tools []agentexec.CaptureTool) ([]agentexec.CaptureTool, error) {
	iface := tc.workflowInterface()
	for _, tool := range tools {
		if _, clash := iface.Outputs[tool.Name]; clash {
			return nil, fmt.Errorf("output %q is also a stage capture tool's name; a declared output is written through its own tool", tool.Name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(iface.Outputs)) {
		// A stage is not offered an output the run has written.
		if _, already := tc.Task.Outputs[name]; already {
			continue
		}
		tools = append(tools, outputCaptureTool(name, iface.Outputs[name]))
	}
	return tools, nil
}

// applyOutputCaptures applies the declared-output capture calls a passed
// agent result carries: the accepted call's value member is the output's
// value, re-validated against the declared type the way readCaptures
// re-checks every record, before it enters the run's set.
func (tc *TaskContext) applyOutputCaptures(res agentexec.Result) error {
	iface := tc.workflowInterface()
	for name, calls := range res.Captures {
		spec, ok := iface.Outputs[name]
		if !ok {
			// Every other capture tool is the stage's own; its OnResult owns
			// its records.
			continue
		}
		for _, call := range calls {
			value, written, err := decodeCapturedOutput(call)
			if err != nil {
				return fmt.Errorf("output %q: %w", name, err)
			}
			if !written {
				continue
			}
			if got := task.ValueType(value); !task.TypeAccepts(spec.Type, got) {
				return fmt.Errorf("output %q is %s, want %s", name, got, spec.Type)
			}
			if _, written := tc.Task.Outputs[name]; written {
				return fmt.Errorf("output %q has already been written; each output is written once per attempt", name)
			}
			if tc.Task.Outputs == nil {
				tc.Task.Outputs = map[string]any{}
			}
			tc.Task.Outputs[name] = value
		}
	}
	return nil
}

// decodeCapturedOutput reads one accepted capture call's value member. A call
// with no value, or a null value, counts as not written, as CheckInputs
// treats a null input.
func decodeCapturedOutput(call json.RawMessage) (value any, written bool, err error) {
	var args struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(call, &args); err != nil {
		return nil, false, err
	}
	if len(args.Value) == 0 || string(args.Value) == "null" {
		return nil, false, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(args.Value)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// validateFinishOutputs checks the attempt's written outputs before the
// outcome transition: required, undeclared and mistyped values park here, so
// no caller observes a terminal state that breaks the run's own promise.
func (tc *TaskContext) validateFinishOutputs() error {
	return tc.workflowInterface().CheckOutputs(tc.Task.Outputs)
}

// publishCalleeOutputs publishes a successful callee's outputs the call step
// assigns into the caller's own set. An output the callee never wrote leaves
// the caller's output absent -- the caller's own required judgment decides; a
// written null counts as not written; a value whose type the caller's declared
// output cannot accept is refused, naming the output.
func (tc *TaskContext) publishCalleeOutputs(assignments map[string]string, calleeOutputs map[string]any) error {
	if len(assignments) == 0 {
		return nil
	}
	iface := tc.workflowInterface()
	for _, calleeName := range slices.Sorted(maps.Keys(assignments)) {
		ref := assignments[calleeName]
		callerName := strings.TrimPrefix(ref, outputReference)
		spec, ok := iface.Outputs[callerName]
		if !ok {
			// The save check refuses an undeclared reference; this re-check
			// keeps a swapped callee from publishing into nothing.
			return fmt.Errorf("output %q references %q, which this workflow does not declare", calleeName, ref)
		}
		value, ok := calleeOutputs[calleeName]
		if !ok || value == nil {
			// Declared but never written: the caller's output stays absent.
			continue
		}
		if got := task.ValueType(value); !task.TypeAccepts(spec.Type, got) {
			return fmt.Errorf("callee output %q is %s, which this workflow's output %q (%s) cannot accept", calleeName, got, callerName, spec.Type)
		}
		if err := tc.WriteOutput(callerName, value); err != nil {
			return err
		}
	}
	return nil
}
