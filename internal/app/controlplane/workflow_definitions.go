package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

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
		SchemaExtensions: map[string]any{"x-step-types": stepTypeVocabulary(steps)},
	}
}

// stepTypeVocabulary is the registered step types as the dashboard editor
// checks them: each name and whether it needs a repository.
func stepTypeVocabulary(steps workflow.StepRegistry) []map[string]any {
	vocabulary := make([]map[string]any, 0, len(steps))
	for _, name := range slices.Sorted(maps.Keys(steps)) {
		vocabulary = append(vocabulary, map[string]any{"name": name, "needs_repository": workflow.NeedsRepository(name)})
	}
	return vocabulary
}

func encodeWorkflowDefinitions(collection workflow.WorkflowDefinitionCollection, steps workflow.StepRegistry) ([]byte, error) {
	if err := workflow.ValidateDefinitionCollection(collection, steps); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return json.Marshal(collection)
}
