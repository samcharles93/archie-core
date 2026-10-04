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

func workflowDefinitionsDefinition(steps workflow.StepRegistry, settings map[string]any) Definition {
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
		SchemaExtensions: map[string]any{"x-step-types": stepTypeVocabulary(steps, settings), "x-intake-routes": workflow.IntakeRoutes()},
	}
}

// stepTypeVocabulary is the registered step types as the dashboard editor
// reads them: each name, whether it needs a repository, and the schema of its
// settings (absent for a step that takes none).
func stepTypeVocabulary(steps workflow.StepRegistry, settings map[string]any) []map[string]any {
	vocabulary := make([]map[string]any, 0, len(steps))
	for _, name := range slices.Sorted(maps.Keys(steps)) {
		entry := map[string]any{"name": name, "needs_repository": workflow.NeedsRepository(name)}
		if schema := settingsSchema(settings[name]); schema != nil {
			entry["settings"] = schema
		}
		vocabulary = append(vocabulary, entry)
	}
	return vocabulary
}

func encodeWorkflowDefinitions(collection workflow.WorkflowDefinitionCollection, steps workflow.StepRegistry) ([]byte, error) {
	if err := workflow.ValidateDefinitionCollection(collection, steps); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return json.Marshal(collection)
}
