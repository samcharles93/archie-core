package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

const WorkflowDefinitionsKind = "workflow-definitions"

// stepRegistry returns the step vocabulary from the injected manager.
func stepRegistry(steps *workflow.Manager) (workflow.StepRegistry, error) {
	if steps == nil {
		return nil, errors.New("control plane: no workflow step vocabulary: the composition root must register the provider set (infrastructure/workflowsteps.NewManager) before the first resolution")
	}
	return steps.Registry(), nil
}

func workflowDefinitionsDefinition(steps workflow.StepRegistry) Definition {
	return Definition{
		Kind:      WorkflowDefinitionsKind,
		Title:     "Workflow definitions",
		Document:  workflow.WorkflowDefinitionCollection{},
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
