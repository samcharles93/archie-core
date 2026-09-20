package task

// WorkflowDefinitionEntry is one workflow's portable YAML as the control plane
// stores and serves it. YAML stays canonical and can be copied between Archie
// installations.
//
// It lives with the task vocabulary rather than with the engine for the same
// reason Definition does: a dashboard reads the stored collection without
// linking the workflow engine, which the UI process must never pull in.
type WorkflowDefinitionEntry struct {
	ID   string `json:"id"`
	YAML string `json:"yaml"`
}

// WorkflowDefinitionCollection is the workflow-definitions resource value.
type WorkflowDefinitionCollection struct {
	Definitions []WorkflowDefinitionEntry `json:"definitions"`
}

// DefinitionByID resolves one workflow from a collection.
func (c WorkflowDefinitionCollection) DefinitionByID(id string) (WorkflowDefinitionEntry, bool) {
	for _, entry := range c.Definitions {
		if entry.ID == id {
			return entry, true
		}
	}
	return WorkflowDefinitionEntry{}, false
}
