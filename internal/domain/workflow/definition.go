package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
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
// run with no worktree at all. workflow.call needs none of the caller's own:
// the callee's own repository mode decides whether its run clones.
var repoFreeStepTypes = map[string]bool{
	AgentRunStepName: true, WorkflowCallStepName: true, FinishStepName: true,
	HandoffStepName: true, ApproveStepName: true, CloseIssueStepName: true, CommentStepName: true,
}

// NeedsRepository reports whether a step type can run only with a worktree.
func NeedsRepository(stepType string) bool { return !repoFreeStepTypes[stepType] }

// StepRecord selects one registered step type and supplies its typed settings.
type StepRecord struct {
	// ID names the step so later steps can reference its result.
	ID       string    `yaml:"id,omitempty" json:"id,omitempty"`
	Type     string    `yaml:"type" json:"type"`
	Settings yaml.Node `yaml:"settings,omitempty" json:"-"`
	// When skips the step unless the condition holds: a reference that must
	// be truthy, negated with !, or compared with == or != to a literal.
	When string `yaml:"when,omitempty" json:"when,omitempty"`
	// OnFailure is "park" (the default) or "continue".
	OnFailure string `yaml:"on_failure,omitempty" json:"on_failure,omitempty"`
}

const onFailureContinue = "continue"

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
	earlier := map[string]bool{}
	for i, step := range definition.Steps {
		if err := checkStep(step, mode, earlier, registry); err != nil {
			return YAMLDefinition{}, fmt.Errorf("workflow %q step %d: %w", definition.ID, i+1, err)
		}
		if step.ID != "" {
			earlier[step.ID] = true
		}
	}
	return definition, nil
}

// checkStep validates one step against the vocabulary and the steps before it.
func checkStep(step StepRecord, mode task.RepositoryMode, earlier map[string]bool, registry StepRegistry) error {
	if err := checkStepID(step.ID, earlier); err != nil {
		return err
	}
	if step.OnFailure != "" && step.OnFailure != "park" && step.OnFailure != onFailureContinue {
		return fmt.Errorf("on_failure is park or continue, not %q", step.OnFailure)
	}
	factory, ok := registry[step.Type]
	if !ok {
		return fmt.Errorf("unknown type %q", step.Type)
	}
	if mode != task.RepositoryRequired && NeedsRepository(step.Type) {
		return fmt.Errorf("%q needs a repository, but the workflow's repository is %s", step.Type, mode)
	}
	if err := checkReferences(step.Settings, earlier); err != nil {
		return err
	}
	if step.When != "" {
		c, err := parseCondition(step.When)
		if err != nil {
			return err
		}
		if err := checkReference(c.path, earlier); err != nil {
			return fmt.Errorf("when: %w", err)
		}
	}
	if _, err := factory(step.Settings); err != nil {
		return fmt.Errorf("%q settings: %w", step.Type, err)
	}
	return nil
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
		stages = append(stages, compiledStep(step, stage.Name, factory))
	}
	return Workflow{Name: definition.ID, Stages: stages, Interface: definition.WorkflowInterface}, nil
}

// compiledStep builds the step's stage at run time from its settings with
// every reference resolved, and records what it leaves for later steps.
func compiledStep(step StepRecord, name string, factory StepFactory) Stage {
	// ParseDefinition has already refused a condition that does not parse.
	when, _ := parseCondition(step.When)
	return Stage{Name: name, ContinueOnFailure: step.OnFailure == onFailureContinue, Run: func(ctx context.Context, tc *TaskContext) error {
		tc.stepResult = StepResult{}
		if step.When != "" && !when.holds(tc) {
			tc.Log.Info("step skipped", "when", step.When)
			return nil
		}
		stage, err := factory(renderSettings(step.Settings, tc))
		if err != nil {
			return fmt.Errorf("step %q settings: %w", step.Type, err)
		}
		err = stage.Run(ctx, tc)
		if step.ID != "" {
			if tc.stepResults == nil {
				tc.stepResults = map[string]StepResult{}
			}
			tc.stepResults[step.ID] = tc.stepResult
		}
		return err
	}}
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

// DefinitionID returns the workflow id a definition declares without
// compiling or validating it.
func DefinitionID(src string) (string, error) {
	var declared struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal([]byte(src), &declared); err != nil {
		return "", fmt.Errorf("parse workflow YAML: %w", err)
	}
	if strings.TrimSpace(declared.ID) == "" {
		return "", fmt.Errorf("workflow id is required")
	}
	return declared.ID, nil
}

// WorkflowDefinitionEntry and WorkflowDefinitionCollection are defined in
// package task so dashboards can decode the stored projection without linking
// the engine. Validation and compilation stay here.
type (
	WorkflowDefinitionEntry      = task.WorkflowDefinitionEntry
	WorkflowDefinitionCollection = task.WorkflowDefinitionCollection
)

// ValidateDefinitionCollection rejects malformed, duplicate, or mismatched
// definitions, and every workflow.call step a definition carries: the callee
// must exist, its inputs must be satisfiable, and the call graph must be
// acyclic.
func ValidateDefinitionCollection(collection WorkflowDefinitionCollection, registry StepRegistry) error {
	seen := make(map[string]struct{}, len(collection.Definitions))
	parsed := make(map[string]YAMLDefinition, len(collection.Definitions))
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
		parsed[entry.ID] = definition
	}
	return validateWorkflowCalls(parsed)
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

// retiredSteps are step types no shipped workflow uses that still resolve, as
// inert stages, so stored definitions naming them keep parsing.
var retiredSteps = map[string]Stage{
	// The remediate workflow's in-container resume stage moved to daemon
	// preparation; see retiredResumeStep.
	"remediate.resume": retiredResumeStep(),
}

// BuiltinStepRegistry exposes every shipped stage as a typed, non-interpreted step.
func BuiltinStepRegistry() StepRegistry {
	registry := StepRegistry{}
	for id, wf := range legacyBuiltinWorkflows() {
		for _, stage := range wf.Stages {
			registry[id+"."+stage.Name] = noSettingsFactory(stage)
		}
	}
	for name, stage := range retiredSteps {
		registry[name] = noSettingsFactory(stage)
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
	collection := WorkflowDefinitionCollection{Definitions: shippedYAML()}
	for _, id := range ids {
		wf := workflows[id]
		var builder strings.Builder
		fmt.Fprintf(&builder, "id: %s\nsteps:\n", id)
		for _, stage := range wf.Stages {
			fmt.Fprintf(&builder, "  - type: %s.%s\n", id, stage.Name)
		}
		// Only pr-review declares inputs today; every other builtin's
		// generated YAML stays byte-identical to before this field existed.
		if len(wf.Interface.Inputs) > 0 {
			builder.WriteString("inputs:\n")
			for _, name := range sortedInputNames(wf.Interface.Inputs) {
				spec := wf.Interface.Inputs[name]
				fmt.Fprintf(&builder, "  %s:\n    type: %s\n    required: %t\n", name, spec.Type, spec.Required)
			}
		}
		collection.Definitions = append(collection.Definitions, WorkflowDefinitionEntry{ID: id, YAML: builder.String()})
	}
	slices.SortFunc(collection.Definitions, func(a, b WorkflowDefinitionEntry) int { return strings.Compare(a.ID, b.ID) })
	return collection
}

//go:embed shipped/*.yaml
var shippedFiles embed.FS

// shippedYAML is the shipped workflows written in the general step types.
func shippedYAML() []WorkflowDefinitionEntry {
	files, _ := fs.Glob(shippedFiles, "shipped/*.yaml")
	entries := make([]WorkflowDefinitionEntry, 0, len(files))
	for _, name := range files {
		data, err := shippedFiles.ReadFile(name)
		if err != nil {
			panic(err) // embedded at build time; unreadable means a broken binary
		}
		id := strings.TrimSuffix(path.Base(name), ".yaml")
		entries = append(entries, WorkflowDefinitionEntry{ID: id, YAML: string(data)})
	}
	return entries
}

// sortedInputNames returns a Workflow.Interface's declared input names in a
// deterministic order, so the generated YAML (and its digest) never depends
// on map iteration order.
func sortedInputNames(inputs map[string]task.InputSpec) []string {
	names := make([]string, 0, len(inputs))
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func legacyBuiltinWorkflows() Registry {
	return Registry{
		"remediate": Remediate(),
		"pr-review": PRReview(),
	}
}
