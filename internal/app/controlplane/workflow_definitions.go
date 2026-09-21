package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

const WorkflowDefinitionsKind = "workflow-definitions"

// stepRegistry is this package's single resolution site for the workflow step
// vocabulary, and the guard on the manager it must be handed. The manager is
// built at the composition root (infrastructure/workflowsteps.NewManager) and
// injected, so the State Store server -- the validating side -- and the
// workflow-definitions client both resolve the vocabulary that composition
// root registered. Agreement is within one build: the State Store and
// archie-agent are separately deployed binaries, so a State Store built from
// newer source than the agent it dispatches to can still disagree, and only a
// matching deploy fixes that.
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
