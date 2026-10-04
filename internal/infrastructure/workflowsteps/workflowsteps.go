// Package workflowsteps is the compiled-in set of workflow step-type
// providers, registered by every process that resolves step types.
package workflowsteps

import (
	"fmt"
	"maps"
	"slices"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// shippedStages provides every stage of the shipped workflows as a step
// type.
type shippedStages struct{}

func (shippedStages) Name() string { return "shipped" }

func (shippedStages) StepTypes() []workflow.StepType {
	registry := workflow.BuiltinStepRegistry()
	stepTypes := make([]workflow.StepType, 0, len(registry))
	for _, name := range slices.Sorted(maps.Keys(registry)) {
		stepTypes = append(stepTypes, workflow.StepType{Name: name, Factory: registry[name]})
	}
	return stepTypes
}

// repoHooks provides the gate.diff-rules step type.
type repoHooks struct{}

func (repoHooks) Name() string { return "repo-hooks" }

func (repoHooks) StepTypes() []workflow.StepType {
	return []workflow.StepType{workflow.DiffRulesStepType()}
}

// eventSteps is the provider for step types that event-started workflows
// use: steps that need no repository, so a workflow declaring repository none
// or optional has something to run.
type eventSteps struct{}

func (eventSteps) Name() string { return "event" }

func (eventSteps) StepTypes() []workflow.StepType {
	return []workflow.StepType{workflow.AgentRunStepType(), workflow.WorkflowCallStepType(), workflow.FinishStepType()}
}

// commandSteps provides the command.run step type: operator-authored commands
// from the stored workflow definition. It is separate from repoHooks, which
// carries repository-authored gate rules.
type commandSteps struct{}

func (commandSteps) Name() string { return "command" }

func (commandSteps) StepTypes() []workflow.StepType {
	return []workflow.StepType{workflow.CommandRunStepType()}
}

// repositorySteps provides the general repository step types: prepare,
// commit, open a PR, and the repository and diff-size gates.
type repositorySteps struct{}

func (repositorySteps) Name() string { return "repository" }

func (repositorySteps) StepTypes() []workflow.StepType { return workflow.RepoStepTypes() }

// controlSteps provides the step types that route the task: handoff, human
// approval, and forge issue actions.
type controlSteps struct{}

func (controlSteps) Name() string { return "control" }

func (controlSteps) StepTypes() []workflow.StepType { return workflow.ControlStepTypes() }

// Providers returns the step-type providers to register.
func Providers() []workflow.StepTypeProvider {
	return []workflow.StepTypeProvider{shippedStages{}, repoHooks{}, eventSteps{}, commandSteps{}, repositorySteps{}, controlSteps{}}
}

// NewManager returns a new manager with Providers registered.
func NewManager() (*workflow.Manager, error) {
	manager := workflow.NewManager()
	for _, provider := range Providers() {
		if err := manager.Register(provider); err != nil {
			return nil, fmt.Errorf("register workflow step type provider %q: %w", provider.Name(), err)
		}
	}
	return manager, nil
}
