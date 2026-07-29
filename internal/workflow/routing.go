package workflow

import "github.com/samcharles93/archie-core/internal/domain/workintake"

// kindWorkflows names the registry entry each intake kind prefers. Bootstrap
// exercises the full pipeline deterministically (no LLM spend) -- invites,
// clone, push, PR, labels.
//
// The label vocabulary itself belongs to workintake, which is what reads
// forge issues. This package owns only the choice of workflow for a kind:
// keeping both here meant the transport and the registry each had their own
// copy of the label table.
var kindWorkflows = map[workintake.Kind]string{
	workintake.KindBug:       "tdd",
	workintake.KindFeature:   "feasibility",
	workintake.KindBootstrap: "bootstrap",
}

// workflowForLabels returns the registered workflow name for a task's
// labels, trying each recognised kind in label order so a "bug,feature" task
// still reaches feasibility when no tdd workflow is registered.
func workflowForLabels(reg Registry, labels string) (Workflow, bool) {
	for _, kind := range workintake.KindsForLabels(workintake.SplitLabels(labels)) {
		name, ok := kindWorkflows[kind]
		if !ok {
			continue
		}
		if wf, ok := reg[name]; ok {
			return wf, true
		}
	}
	return Workflow{}, false
}
