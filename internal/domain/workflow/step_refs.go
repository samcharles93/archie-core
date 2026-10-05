package workflow

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/stableid"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// StepResult is what a finished step leaves for later steps to reference as
// steps.<id>.summary and steps.<id>.result.<field>.
type StepResult struct {
	Summary string         `json:"summary"`
	Result  map[string]any `json:"result,omitempty"`
}

// referencePattern matches {{ path }} in a step's string settings.
var referencePattern = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// refScope is what a step's references may read: the workflow's declared
// inputs and the steps declared before it, each with the result fields its
// schema declares. A step with no result schema has nil fields, and any field
// of its result is accepted.
type refScope struct {
	inputs map[string]bool
	steps  map[string]map[string]bool
}

func newRefScope(inputs map[string]task.InputSpec) *refScope {
	scope := &refScope{inputs: map[string]bool{}, steps: map[string]map[string]bool{}}
	for name := range inputs {
		scope.inputs[name] = true
	}
	return scope
}

func (s *refScope) has(id string) bool {
	_, ok := s.steps[id]
	return ok
}

func (s *refScope) clone() *refScope {
	return &refScope{inputs: s.inputs, steps: maps.Clone(s.steps)}
}

// resultFields returns the fields step's result schema declares, or nil when
// it declares none.
func resultFields(step StepRecord) map[string]bool {
	if step.Type != AgentRunStepName {
		return nil
	}
	var s agentRunSettings
	if err := step.Settings.Decode(&s); err != nil {
		return nil
	}
	properties, ok := s.Result["properties"].(map[string]any)
	if !ok {
		return nil
	}
	fields := make(map[string]bool, len(properties))
	for name := range properties {
		fields[name] = true
	}
	return fields
}

// checkReferences rejects a reference a run could not resolve: an unknown
// root, an undeclared input, a step id that is not declared by an earlier
// step, or a result field that step's schema does not declare.
func checkReferences(settings yaml.Node, earlier *refScope) error {
	var problem error
	walkStrings(&settings, func(value string) string {
		for _, match := range referencePattern.FindAllStringSubmatch(value, -1) {
			if err := checkReference(match[1], earlier); err != nil && problem == nil {
				problem = err
			}
		}
		return value
	})
	return problem
}

func checkReference(path string, earlier *refScope) error {
	parts := strings.Split(path, ".")
	switch parts[0] {
	case "task", "inputs":
		if len(parts) < 2 {
			return fmt.Errorf("reference {{ %s }} names no field", path)
		}
		if parts[0] == "task" && !taskFields[parts[1]] {
			return fmt.Errorf("reference {{ %s }}: task has no field %q", path, parts[1])
		}
		if parts[0] == "inputs" && !earlier.inputs[parts[1]] {
			return fmt.Errorf("reference {{ %s }}: the workflow declares no input %q", path, parts[1])
		}
		return nil
	case "steps":
		return checkStepReference(path, parts, earlier)
	}
	return fmt.Errorf("reference {{ %s }} must start with task, inputs or steps", path)
}

// checkStepReference checks steps.<id>.summary and steps.<id>.result.<field>
// against the steps declared before the referencing one.
func checkStepReference(path string, parts []string, earlier *refScope) error {
	if len(parts) < 3 {
		return fmt.Errorf("reference {{ %s }} must name a step and a field, e.g. steps.plan.summary", path)
	}
	if !earlier.has(parts[1]) {
		return fmt.Errorf("reference {{ %s }}: no earlier step has id %q", path, parts[1])
	}
	if parts[2] != "summary" && parts[2] != "result" {
		return fmt.Errorf("reference {{ %s }}: a step exposes summary and result", path)
	}
	if fields := earlier.steps[parts[1]]; parts[2] == "result" && len(parts) > 3 && fields != nil && !fields[parts[3]] {
		return fmt.Errorf("reference {{ %s }}: step %q's result schema has no field %q", path, parts[1], parts[3])
	}
	return nil
}

var taskFields = map[string]bool{"review": true, "plan": true, "title": true, "body": true, "prompt": true, "repository": true, "kind": true, "issue": true, "pr": true}

// renderSettings returns a copy of settings with every reference replaced by
// its value in this run. A reference to an absent value renders empty: a
// skipped earlier step leaves nothing behind, and that is not an error.
func renderSettings(settings yaml.Node, tc *TaskContext) yaml.Node {
	scope := referenceScope(tc)
	rendered := deepCopyNode(&settings)
	walkStrings(rendered, func(value string) string {
		return referencePattern.ReplaceAllStringFunc(value, func(token string) string {
			path := referencePattern.FindStringSubmatch(token)[1]
			return stringify(lookup(scope, strings.Split(path, ".")))
		})
	})
	return *rendered
}

func referenceScope(tc *TaskContext) map[string]any {
	t := tc.Task
	steps := make(map[string]any, len(tc.stepResults))
	for id, result := range tc.stepResults {
		steps[id] = map[string]any{"summary": result.Summary, "result": result.Result}
	}
	inputs := map[string]any{}
	maps.Copy(inputs, t.Inputs)
	return map[string]any{
		"task": map[string]any{
			"review": reviewMission(t), "plan": approvedPlan(t), "title": t.Title, "body": t.Body, "prompt": taskPromptBlock(t),
			"repository": tc.Repo.FullName(), "kind": taskKind(t),
			"issue": t.IssueNumber, "pr": t.PRNumber,
		},
		"inputs": inputs,
		"steps":  steps,
	}
}

func lookup(value any, path []string) any {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func stringify(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(encoded)
	}
}

// walkStrings rewrites every string scalar under node in place.
func walkStrings(node *yaml.Node, rewrite func(string) string) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		node.Value = rewrite(node.Value)
	}
	for _, child := range node.Content {
		walkStrings(child, rewrite)
	}
}

func deepCopyNode(node *yaml.Node) *yaml.Node {
	copied := *node
	copied.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copied.Content[i] = deepCopyNode(child)
	}
	return &copied
}

// checkStepID requires each declared step id to be a stable identifier and
// unique within the workflow.
func checkStepID(id string, seen *refScope) error {
	if id == "" {
		return nil
	}
	if !stableid.Valid(id) {
		return fmt.Errorf("step id %q is not a stable identifier", id)
	}
	if seen.has(id) {
		return fmt.Errorf("step id %q is declared twice", id)
	}
	return nil
}

// approvedPlan is the PRD a feasibility run handed over, framed for a mission.
func approvedPlan(t *Task) string {
	if t.Plan == "" {
		return ""
	}
	return "<approved_prd>\n" + t.Plan + "\n</approved_prd>"
}

// reviewMission is the review a remediation round addresses, as the builder
// reads it.
func reviewMission(t *Task) string {
	unit, err := DecodeReviewUnit(t.ReviewPayload)
	if err != nil || t.ReviewPayload == "" {
		return ""
	}
	return renderReviewUnitMission(unit)
}
