package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// YAMLDefinition is the portable, versioned workflow representation.
type YAMLDefinition struct {
	ID    string       `yaml:"id" json:"id"`
	Steps []StepRecord `yaml:"steps" json:"steps"`
	// WorkflowInterface declares the workflow's inputs, repository mode and
	// agent profile.
	task.WorkflowInterface `yaml:",inline" json:",inline"`
}

// repoFreeStepTypes are the step types that run without a repository. A
// workflow whose repository is not required may use only these, since it may
// run with no worktree at all.
var repoFreeStepTypes = map[string]bool{AgentRunStepName: true}

// StepRecord selects one registered step type and supplies its typed settings.
type StepRecord struct {
	Type     string    `yaml:"type" json:"type"`
	Settings yaml.Node `yaml:"settings,omitempty" json:"-"`
}

type StepFactory func(yaml.Node) (Stage, error)

// StepRegistry is the closed set of safe workflow operations available to YAML.
type StepRegistry map[string]StepFactory

// ParseDefinition strictly parses and validates one YAML workflow.
func ParseDefinition(src string, registry StepRegistry) (YAMLDefinition, error) {
	var definition YAMLDefinition
	decoder := yaml.NewDecoder(strings.NewReader(src))
	decoder.KnownFields(true)
	if err := decoder.Decode(&definition); err != nil {
		return YAMLDefinition{}, fmt.Errorf("parse workflow YAML: %w", err)
	}
	if strings.TrimSpace(definition.ID) == "" {
		return YAMLDefinition{}, fmt.Errorf("workflow id is required")
	}
	if len(definition.Steps) == 0 {
		return YAMLDefinition{}, fmt.Errorf("workflow %q has no steps", definition.ID)
	}
	if err := definition.Validate(); err != nil {
		return YAMLDefinition{}, fmt.Errorf("workflow %q: %w", definition.ID, err)
	}
	mode := definition.RepositoryMode()
	for i, step := range definition.Steps {
		factory, ok := registry[step.Type]
		if !ok {
			return YAMLDefinition{}, fmt.Errorf("workflow %q step %d: unknown type %q", definition.ID, i+1, step.Type)
		}
		if mode != task.RepositoryRequired && !repoFreeStepTypes[step.Type] {
			return YAMLDefinition{}, fmt.Errorf("workflow %q step %d: %q needs a repository, but the workflow's repository is %s", definition.ID, i+1, step.Type, mode)
		}
		if _, err := factory(step.Settings); err != nil {
			return YAMLDefinition{}, fmt.Errorf("workflow %q step %q settings: %w", definition.ID, step.Type, err)
		}
	}
	return definition, nil
}

// Compile resolves a validated YAML definition to executable stages.
func Compile(definition YAMLDefinition, registry StepRegistry) (Workflow, error) {
	stages := make([]Stage, 0, len(definition.Steps))
	for _, step := range definition.Steps {
		factory, ok := registry[step.Type]
		if !ok {
			return Workflow{}, fmt.Errorf("unknown workflow step type %q", step.Type)
		}
		stage, err := factory(step.Settings)
		if err != nil {
			return Workflow{}, fmt.Errorf("build workflow step %q: %w", step.Type, err)
		}
		stages = append(stages, stage)
	}
	return Workflow{Name: definition.ID, Stages: stages}, nil
}

// ParseAndCompile validates and compiles one definition in a single operation.
func ParseAndCompile(src string, registry StepRegistry) (Workflow, error) {
	definition, err := ParseDefinition(src, registry)
	if err != nil {
		return Workflow{}, err
	}
	return Compile(definition, registry)
}

// DigestDefinition returns the immutable content identity stored with a run.
func DigestDefinition(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

// WorkflowDefinitionEntry and WorkflowDefinitionCollection are defined in
// package task so dashboards can decode the stored projection without linking
// the engine. Validation and compilation stay here.
type (
	WorkflowDefinitionEntry      = task.WorkflowDefinitionEntry
	WorkflowDefinitionCollection = task.WorkflowDefinitionCollection
)

// ValidateDefinitionCollection rejects malformed, duplicate, or mismatched definitions.
func ValidateDefinitionCollection(collection WorkflowDefinitionCollection, registry StepRegistry) error {
	seen := make(map[string]struct{}, len(collection.Definitions))
	for _, entry := range collection.Definitions {
		if _, ok := seen[entry.ID]; ok {
			return fmt.Errorf("duplicate workflow id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		definition, err := ParseDefinition(entry.YAML, registry)
		if err != nil {
			return err
		}
		if definition.ID != entry.ID {
			return fmt.Errorf("workflow entry id %q does not match YAML id %q", entry.ID, definition.ID)
		}
	}
	return nil
}

// DecodeDefinitionCollection strictly decodes the control-plane projection.
func DecodeDefinitionCollection(value []byte, registry StepRegistry) (WorkflowDefinitionCollection, error) {
	var collection WorkflowDefinitionCollection
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&collection); err != nil {
		return WorkflowDefinitionCollection{}, err
	}
	if err := ValidateDefinitionCollection(collection, registry); err != nil {
		return WorkflowDefinitionCollection{}, err
	}
	return collection, nil
}

func noSettingsFactory(stage Stage) StepFactory {
	return func(settings yaml.Node) (Stage, error) {
		if settings.Kind != 0 {
			var value map[string]any
			if err := settings.Decode(&value); err != nil {
				return Stage{}, err
			}
			if len(value) != 0 {
				return Stage{}, fmt.Errorf("step type accepts no settings")
			}
		}
		return stage, nil
	}
}

// BuiltinStepRegistry exposes every shipped stage as a typed, non-interpreted step.
func BuiltinStepRegistry() StepRegistry {
	registry := StepRegistry{}
	for id, wf := range legacyBuiltinWorkflows() {
		for _, stage := range wf.Stages {
			registry[id+"."+stage.Name] = noSettingsFactory(stage)
		}
	}
	return registry
}

// ShippedDefinitions returns the restorable factory definitions.
func ShippedDefinitions() WorkflowDefinitionCollection {
	workflows := legacyBuiltinWorkflows()
	ids := make([]string, 0, len(workflows))
	for id := range workflows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	collection := WorkflowDefinitionCollection{Definitions: make([]WorkflowDefinitionEntry, 0, len(ids))}
	for _, id := range ids {
		wf := workflows[id]
		var builder strings.Builder
		fmt.Fprintf(&builder, "id: %s\nsteps:\n", id)
		for _, stage := range wf.Stages {
			fmt.Fprintf(&builder, "  - type: %s.%s\n", id, stage.Name)
		}
		collection.Definitions = append(collection.Definitions, WorkflowDefinitionEntry{ID: id, YAML: builder.String()})
	}
	return collection
}

func legacyBuiltinWorkflows() Registry {
	return Registry{
		"bootstrap": Bootstrap(), "implement": Implement(), "tdd": TDD(),
		"feasibility": Feasibility(), "triage": Triage(), "remediate": Remediate(),
	}
}
