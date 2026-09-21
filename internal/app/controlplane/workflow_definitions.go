package controlplane

import (
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

const WorkflowDefinitionsKind = "workflow-definitions"

// stepRegistry is this package's single resolution site for the workflow step
// vocabulary. The manager is built at the composition root
// (infrastructure/workflowsteps.NewManager) and injected, so the State Store
// server -- the validating side -- and the control-plane client both resolve
// the vocabulary that composition root registered, and neither can validate a
// definition against a vocabulary the other does not have.
func stepRegistry(steps *workflow.Manager) workflow.StepRegistry { return steps.Registry() }

func workflowDefinitionsDefinition(steps workflow.StepRegistry) Definition {
	return Definition{
		Kind:      WorkflowDefinitionsKind,
		Title:     "Workflow definitions",
		Schema:    objectSchema,
		ApplyMode: "live",
		Seed: func(config.Config) any {
			return workflow.ShippedDefinitions()
		},
		Defaults: func() any {
			return workflow.ShippedDefinitions()
		},
		Validate: func(input []byte) error {
			_, err := workflow.DecodeDefinitionCollection(input, steps)
			return err
		},
	}
}

func encodeWorkflowDefinitions(collection workflow.WorkflowDefinitionCollection, steps workflow.StepRegistry) ([]byte, error) {
	if err := workflow.ValidateDefinitionCollection(collection, steps); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return json.Marshal(collection)
}
