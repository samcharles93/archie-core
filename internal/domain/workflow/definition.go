package workflow

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
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
	Type     string    `yaml:"type,omitempty" json:"type,omitempty"`
	Settings yaml.Node `yaml:"settings,omitempty" json:"-"`
	// When skips the step unless the condition holds: a reference that must
	// be truthy, negated with !, or compared with == or != to a literal.
	When string `yaml:"when,omitempty" json:"when,omitempty"`
	// OnFailure is "park" (the default) or "continue".
	OnFailure string `yaml:"on_failure,omitempty" json:"on_failure,omitempty"`
	// Retry runs a failed step again before on_failure applies.
	Retry *RetryPolicy `yaml:"retry,omitempty" json:"retry,omitempty"`
	// Parallel makes this step a set of named branches that run at the same
	// time instead of a typed step. Each branch is a list of steps.
	Parallel map[string][]StepRecord `yaml:"parallel,omitempty" json:"parallel,omitempty"`
}

// RetryPolicy is how often a failed step runs again, and how long it waits
// between attempts.
type RetryPolicy struct {
	Attempts int    `yaml:"attempts" json:"attempts"`
	Backoff  string `yaml:"backoff,omitempty" json:"backoff,omitempty"`
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
	earlier := newRefScope(definition.Inputs)
	for i, step := range definition.Steps {
		if err := checkStep(step, mode, earlier, registry, false); err != nil {
			return YAMLDefinition{}, fmt.Errorf("workflow %q step %d: %w", definition.ID, i+1, err)
		}
	}
	return definition, nil
}

// StageNames returns the names a definition's steps run under, in order. It
// reads a definition already pinned, so it decodes without validating.
func StageNames(src string) ([]string, error) {
	var definition YAMLDefinition
	if err := yaml.Unmarshal([]byte(src), &definition); err != nil {
		return nil, fmt.Errorf("parse workflow YAML: %w", err)
	}
	names := make([]string, len(definition.Steps))
	for i, step := range definition.Steps {
		names[i] = step.StageName()
	}
	return names, nil
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
	if err := validateWorkflowCalls(parsed); err != nil {
		return err
	}
	return validateControlTargets(parsed)
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

// ShippedDefinitions returns the restorable factory definitions.
func ShippedDefinitions() WorkflowDefinitionCollection {
	return WorkflowDefinitionCollection{Definitions: shippedYAML()}
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
