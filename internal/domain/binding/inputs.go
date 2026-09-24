package binding

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// InputSource assigns one workflow input: either the value of a mapped
// parameter (Param) or a constant (Value), never both.
type InputSource struct {
	Param string `json:"param,omitempty"`
	Value any    `json:"value,omitempty"`
}

func (s InputSource) validate(name string) error {
	if (s.Param == "") == (s.Value == nil) {
		return fmt.Errorf("binding: input %q must take either a parameter or a constant", name)
	}
	return nil
}

// CheckWorkflow checks the binding against the workflow it targets and the
// mapping it reads, as the binding is saved: every assigned input is declared,
// every required input is assigned, a parameter exists and its type feeds the
// input's, a constant has the input's type, and the repository agrees with the
// workflow's repository mode.
func (b Binding) CheckWorkflow(w task.WorkflowInterface, fields []mapping.Field) error {
	byName := make(map[string]mapping.Field, len(fields))
	for _, f := range fields {
		byName[f.Name] = f
	}
	for _, name := range sortedKeys(b.Inputs) {
		src := b.Inputs[name]
		spec, ok := w.Inputs[name]
		if !ok {
			return fmt.Errorf("binding: input %q is not declared by workflow %q", name, b.Workflow)
		}
		if src.Param != "" {
			f, ok := byName[src.Param]
			if !ok {
				return fmt.Errorf("binding: input %q reads parameter %q, which the mapping does not have", name, src.Param)
			}
			if !task.TypeAccepts(spec.Type, string(f.Type)) {
				return fmt.Errorf("binding: input %q is %s, but parameter %q is %s", name, spec.Type, src.Param, f.Type)
			}
			continue
		}
		if got := task.ValueType(src.Value); !task.TypeAccepts(spec.Type, got) {
			return fmt.Errorf("binding: input %q is %s, but its constant is %s", name, spec.Type, got)
		}
	}
	for _, name := range sortedKeys(w.Inputs) {
		if _, ok := b.Inputs[name]; w.Inputs[name].Required && !ok {
			return fmt.Errorf("binding: workflow %q requires input %q", b.Workflow, name)
		}
	}
	return b.checkRepository(w.RepositoryMode(), byName)
}

func (b Binding) checkRepository(mode task.RepositoryMode, fields map[string]mapping.Field) error {
	if mode == task.RepositoryNone && (b.Owner != "" || b.RepoParam != "") {
		return fmt.Errorf("binding: workflow %q runs without a repository, so the binding cannot name one", b.Workflow)
	}
	if b.RepoParam == "" {
		return nil
	}
	f, ok := fields[b.RepoParam]
	if !ok {
		return fmt.Errorf("binding: repository parameter %q is not in the mapping", b.RepoParam)
	}
	if f.Type != mapping.TypeString {
		return fmt.Errorf("binding: repository parameter %q is %s, want string", b.RepoParam, f.Type)
	}
	return nil
}

// ResolveInputs builds the workflow input values from the mapping's resolved
// parameters and the binding's constants. A parameter that did not resolve is
// left out, so the workflow's required-input check reports it.
func (b Binding) ResolveInputs(values map[string]any) map[string]any {
	if len(b.Inputs) == 0 {
		return nil
	}
	out := make(map[string]any, len(b.Inputs))
	for name, src := range b.Inputs {
		if src.Param == "" {
			out[name] = src.Value
			continue
		}
		if v, ok := values[src.Param]; ok {
			out[name] = v
		}
	}
	return out
}

// RepositoryFrom returns the owner and repository a dispatch targets when the
// binding reads it from a parameter holding "owner/name". ok is false when the
// binding has no repository parameter; err reports a value that is not an
// owner/name pair.
func (b Binding) RepositoryFrom(values map[string]any) (owner, repo string, ok bool, err error) {
	if b.RepoParam == "" {
		return "", "", false, nil
	}
	full, _ := values[b.RepoParam].(string)
	owner, repo, found := strings.Cut(full, "/")
	if !found || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", true, fmt.Errorf("binding: repository parameter %q is %q, want owner/name", b.RepoParam, full)
	}
	return owner, repo, true, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
