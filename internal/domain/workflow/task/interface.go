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

// WorkflowInterface is what a workflow declares to whatever starts it: the
// inputs it takes, whether it works on a repository, and the agent profile it
// runs under. It lives beside the definition entry so a process that does not
// link the engine can check a binding against it.
type WorkflowInterface struct {
	Inputs     map[string]InputSpec `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Repository RepositoryMode       `yaml:"repository,omitempty" json:"repository,omitempty"`
	// Profile names a [containers.profiles] entry. It is resolved when a task
	// is dispatched, not when the workflow is saved.
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
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

// Validate rejects an unknown repository mode, a malformed input name and an
// unknown input type.
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
