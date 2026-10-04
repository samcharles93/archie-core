// Package workflowsteps is the compiled-in set of workflow step-type
// providers, registered by every process that resolves step types.
package workflowsteps

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// reviewSteps provides the pull request review and remediation step types.
type reviewSteps struct{}

func (reviewSteps) Name() string { return "review" }

func (reviewSteps) StepTypes() []workflow.StepType { return workflow.ReviewStepTypes() }

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
	return []workflow.StepTypeProvider{reviewSteps{}, repoHooks{}, eventSteps{}, commandSteps{}, repositorySteps{}, controlSteps{}}
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
