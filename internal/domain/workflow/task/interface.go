package task

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// RepositoryMode says whether a workflow works on a repository.
type RepositoryMode string

const (
	// RepositoryRequired gives the agent a worktree of the task's repository.
	// It is the default, so a workflow that does not declare one keeps working
	// on a repository.
	RepositoryRequired RepositoryMode = "required"
	// RepositoryOptional runs with a worktree when the binding names a
	// repository, and in a scratch workspace when it does not.
	RepositoryOptional RepositoryMode = "optional"
	// RepositoryNone runs in a scratch workspace with no clone.
	RepositoryNone RepositoryMode = "none"
)

// InputSpec declares one workflow input. Type is one of the mapping field
// types, so a mapped parameter and the input it feeds are checked alike.
type InputSpec struct {
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

// OutputSpec declares one workflow output: the same type vocabulary an
// input uses. A required output the run never writes parks it before any
// terminal state (docs/prds/workflow-call-outputs.md, "Failure rules");
// required defaults to false.
type OutputSpec struct {
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

// WorkflowInterface is what a workflow declares to whatever starts it: the
// inputs it takes, whether it works on a repository, and the agent profile it
// runs under. It lives beside the definition entry so a process that does not
// link the engine can check a binding against it.
type WorkflowInterface struct {
	Inputs     map[string]InputSpec `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Repository RepositoryMode       `yaml:"repository,omitempty" json:"repository,omitempty"`
	// Outputs declares the named, typed structured results a run writes
	// once each (docs/prds/workflow-call-outputs.md). A wait:true caller
	// reads them through WorkflowCallStatus; a call step publishes one as
	// one of the caller's own outputs.
	Outputs map[string]OutputSpec `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	// Profile names a [containers.profiles] entry. It is resolved when a task
	// is dispatched, not when the workflow is saved.
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
	// DeclaredNeeds is what the definition's needs: block declares. Read
	// Needs, not this, when deciding a harness requirement: a declared output
	// forces the captures capability and Needs resolves it.
	DeclaredNeeds WorkflowNeeds `yaml:"needs" json:"needs"`
}

// Needs reports the harness requirements the interface carries: the declared
// needs, plus the captures a declared output forces, because an output is
// written through a capture tool (docs/prds/workflow-call-outputs.md, "How a
// run writes one"). Profile selection reads this rather than DeclaredNeeds,
// so a workflow that declares outputs cannot name a Kit profile whose harness
// serves no capture tools (docs/prds/external-agent-harness.md, "Contract").
func (w WorkflowInterface) Needs() WorkflowNeeds {
	needs := w.DeclaredNeeds
	if len(w.Outputs) > 0 {
		needs.Captures = true
	}
	return needs
}

// WorkflowNeeds is WorkflowInterface's declared harness requirements.
type WorkflowNeeds struct {
	// Captures declares that a stage returns structured output through
	// capture tools.
	Captures bool `yaml:"captures,omitempty" json:"captures,omitempty"`
	// GateRetries is the largest gate-retry budget any stage declares. Zero
	// means no stage gates its result, so no resume capability is needed.
	GateRetries int `yaml:"gate_retries,omitempty" json:"gate_retries,omitempty"`
}

var (
	inputName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	inputTypes = []string{"string", "number", "bool", "object", "array", "any"}
)

// ParseWorkflowInterface reads the interface a workflow YAML declares,
// ignoring its steps.
func ParseWorkflowInterface(src string) (WorkflowInterface, error) {
	var w WorkflowInterface
	if err := yaml.Unmarshal([]byte(src), &w); err != nil {
		return WorkflowInterface{}, fmt.Errorf("parse workflow YAML: %w", err)
	}
	return w, w.Validate()
}

// Validate rejects an unknown repository mode, a malformed input or output
// name and an unknown input or output type.
func (w WorkflowInterface) Validate() error {
	switch w.Repository {
	case "", RepositoryRequired, RepositoryOptional, RepositoryNone:
	default:
		return fmt.Errorf("workflow repository %q must be none, optional or required", w.Repository)
	}
	for name, spec := range w.Inputs {
		if !inputName.MatchString(name) {
			return fmt.Errorf("workflow input %q must be a letter or underscore followed by letters, digits or underscores", name)
		}
		if !slices.Contains(inputTypes, spec.Type) {
			return fmt.Errorf("workflow input %q type %q must be one of %s", name, spec.Type, strings.Join(inputTypes, ", "))
		}
	}
	for name, spec := range w.Outputs {
		if !inputName.MatchString(name) {
			return fmt.Errorf("workflow output %q must be a letter or underscore followed by letters, digits or underscores", name)
		}
		if !slices.Contains(inputTypes, spec.Type) {
			return fmt.Errorf("workflow output %q type %q must be one of %s", name, spec.Type, strings.Join(inputTypes, ", "))
		}
	}
	if w.DeclaredNeeds.GateRetries < 0 {
		return fmt.Errorf("workflow needs.gate_retries must not be negative")
	}
	return nil
}

// RepositoryMode returns the declared mode, defaulting to required.
func (w WorkflowInterface) RepositoryMode() RepositoryMode {
	if w.Repository == "" {
		return RepositoryRequired
	}
	return w.Repository
}

// CheckInputs rejects values that do not satisfy the declared inputs: a
// required input that is missing or null, a value for an undeclared input, or
// a value whose type is not the declared one.
func (w WorkflowInterface) CheckInputs(values map[string]any) error {
	for name, spec := range w.Inputs {
		v, ok := values[name]
		if spec.Required && (!ok || v == nil) {
			return fmt.Errorf("input %q is required", name)
		}
	}
	for name, v := range values {
		spec, ok := w.Inputs[name]
		if !ok {
			return fmt.Errorf("input %q is not declared by the workflow", name)
		}
		if got := ValueType(v); v != nil && !TypeAccepts(spec.Type, got) {
			return fmt.Errorf("input %q is %s, want %s", name, got, spec.Type)
		}
	}
	return nil
}

// CheckOutputs rejects declared-output values that do not satisfy the
// declared outputs: the same rules CheckInputs applies to inputs (required,
// undeclared, wrong type), read from the other side of the interface
// (docs/prds/workflow-call-outputs.md, "Failure rules"). A written null
// counts as not written, as CheckInputs treats a null input; an unwritten
// optional output leaves its key absent rather than null.
func (w WorkflowInterface) CheckOutputs(values map[string]any) error {
	for name, spec := range w.Outputs {
		v, ok := values[name]
		if spec.Required && (!ok || v == nil) {
			return fmt.Errorf("output %q is required", name)
		}
	}
	for name, v := range values {
		spec, ok := w.Outputs[name]
		if !ok {
			return fmt.Errorf("output %q is not declared by the workflow", name)
		}
		if got := ValueType(v); v != nil && !TypeAccepts(spec.Type, got) {
			return fmt.Errorf("output %q is %s, want %s", name, got, spec.Type)
		}
	}
	return nil
}

// TypeAccepts reports whether a value or parameter of type got may feed an
// input declared as want.
func TypeAccepts(want, got string) bool {
	return want == "any" || got == "any" || want == got
}

// ValueType names the JSON type of a decoded value, in the mapping field type
// vocabulary.
func ValueType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case json.Number, float64, float32, int, int32, int64:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// EncodeInputs is the stored and wire form of a task's inputs: a JSON
// object, or empty for none.
func EncodeInputs(inputs map[string]any) (string, error) {
	if len(inputs) == 0 {
		return "", nil
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		return "", fmt.Errorf("encode task inputs: %w", err)
	}
	return string(data), nil
}

// DecodeInputs reverses EncodeInputs, keeping numbers exact.
func DecodeInputs(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(s))
	decoder.UseNumber()
	var inputs map[string]any
	if err := decoder.Decode(&inputs); err != nil {
		return nil, fmt.Errorf("decode task inputs: %w", err)
	}
	return inputs, nil
}

// EncodeOutputs is the stored and wire form of a task's written outputs: a
// JSON object, or empty for none (docs/prds/workflow-call-outputs.md,
// "Storage and wire").
func EncodeOutputs(outputs map[string]any) (string, error) {
	if len(outputs) == 0 {
		return "", nil
	}
	data, err := json.Marshal(outputs)
	if err != nil {
		return "", fmt.Errorf("encode task outputs: %w", err)
	}
	return string(data), nil
}

// DecodeOutputs reverses EncodeOutputs, keeping numbers exact.
func DecodeOutputs(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(s))
	decoder.UseNumber()
	var outputs map[string]any
	if err := decoder.Decode(&outputs); err != nil {
		return nil, fmt.Errorf("decode task outputs: %w", err)
	}
	return outputs, nil
}
