package task

// WorkflowDefinitionEntry is one workflow's YAML as stored in the control
// plane.
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
