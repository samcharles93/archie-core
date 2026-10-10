package workflow

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
)

// defaultKindWorkflows maps each intake kind to its default workflow.
var defaultKindWorkflows = map[workintake.Kind]string{
	workintake.KindBug:     "tdd",
	workintake.KindFeature: "feasibility",
}

// IntakeRoutes is where an issue goes by its label, as a dashboard shows it:
// each kind's default workflow, and "default" for an issue with no
// recognised label, which goes to triage to be classified.
func IntakeRoutes() map[string]string {
	routes := map[string]string{string(workintake.KindDefault): "triage"}
	for kind, workflow := range defaultKindWorkflows {
		routes[string(kind)] = workflow
	}
	return routes
}

// workflowForLabels returns the registered workflow name for a task's
// labels, trying each recognised kind in label order so a "bug,feature" task
// still reaches feasibility when no tdd workflow is registered.
func workflowForLabels(reg Registry, labels string) (Workflow, bool) {
	for _, kind := range workintake.KindsForLabels(workintake.SplitLabels(labels)) {
		name, ok := defaultKindWorkflows[kind]
		if !ok {
			continue
		}
		if wf, ok := reg[name]; ok {
			return wf, true
		}
	}
	return Workflow{}, false
}

// ResolveWorkflowID deterministically selects a definition without compiling
// or executing it. It is used before worker dispatch to pin the exact YAML.
func ResolveWorkflowID(t *Task, available map[string]struct{}) (string, error) {
	if t.Workflow != "" {
		if _, ok := available[t.Workflow]; ok {
			return t.Workflow, nil
		}
		return "", fmt.Errorf("workflow %q is not defined", t.Workflow)
	}
	for _, kind := range workintake.KindsForLabels(workintake.SplitLabels(t.Labels)) {
		if id := defaultKindWorkflows[kind]; id != "" {
			if _, ok := available[id]; ok {
				return id, nil
			}
		}
	}
	if _, ok := available["triage"]; ok {
		return "triage", nil
	}
	if _, ok := available["implement"]; ok {
		return "implement", nil
	}
	return "", fmt.Errorf("no routable workflow definition")
}
