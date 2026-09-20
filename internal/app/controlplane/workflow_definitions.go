package controlplane

import (
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

const WorkflowDefinitionsKind = "workflow-definitions"

func workflowDefinitionsDefinition() Definition {
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
			_, err := workflow.DecodeDefinitionCollection(input, workflow.BuiltinStepRegistry())
			return err
		},
	}
}

func encodeWorkflowDefinitions(collection workflow.WorkflowDefinitionCollection) ([]byte, error) {
	if err := workflow.ValidateDefinitionCollection(collection, workflow.BuiltinStepRegistry()); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return json.Marshal(collection)
}
