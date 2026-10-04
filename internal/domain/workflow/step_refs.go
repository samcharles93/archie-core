package workflow

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/stableid"
)

// StepResult is what a finished step leaves for later steps to reference as
// steps.<id>.summary and steps.<id>.result.<field>.
type StepResult struct {
	Summary string         `json:"summary"`
	Result  map[string]any `json:"result,omitempty"`
}

// referencePattern matches {{ path }} in a step's string settings.
var referencePattern = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// checkReferences rejects a reference a run could not resolve: an unknown
// root, or a step id that is not declared by an earlier step.
func checkReferences(settings yaml.Node, earlier map[string]bool) error {
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

func checkReference(path string, earlier map[string]bool) error {
	parts := strings.Split(path, ".")
	switch parts[0] {
	case "task", "inputs":
		if len(parts) < 2 {
			return fmt.Errorf("reference {{ %s }} names no field", path)
		}
		if parts[0] == "task" && !taskFields[parts[1]] {
			return fmt.Errorf("reference {{ %s }}: task has no field %q", path, parts[1])
		}
		return nil
	case "steps":
		if len(parts) < 3 {
			return fmt.Errorf("reference {{ %s }} must name a step and a field, e.g. steps.plan.summary", path)
		}
		if !earlier[parts[1]] {
			return fmt.Errorf("reference {{ %s }}: no earlier step has id %q", path, parts[1])
		}
		if parts[2] != "summary" && parts[2] != "result" {
			return fmt.Errorf("reference {{ %s }}: a step exposes summary and result", path)
		}
		return nil
	}
	return fmt.Errorf("reference {{ %s }} must start with task, inputs or steps", path)
}

var taskFields = map[string]bool{"plan": true, "title": true, "body": true, "prompt": true, "repository": true, "kind": true, "issue": true, "pr": true}

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
			"plan": approvedPlan(t), "title": t.Title, "body": t.Body, "prompt": taskPromptBlock(t),
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
func checkStepID(id string, seen map[string]bool) error {
	if id == "" {
		return nil
	}
	if !stableid.Valid(id) {
		return fmt.Errorf("step id %q is not a stable identifier", id)
	}
	if seen[id] {
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
