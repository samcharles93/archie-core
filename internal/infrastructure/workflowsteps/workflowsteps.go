// Package workflowsteps is the compiled-in workflow step-type provider set:
// the one place a step type is bundled into the Archie binaries that resolve
// one, so no such binary can keep a provider set of its own.
//
// Two composition roots register it, both of them roots that resolve a step
// type: archied's RunStateStore (the State Store's validating side) and
// archied's daemon root (openDaemonWorkflowDefinitions, the executing side's
// workflow-definitions client, alongside agentworker's
// productionWorkerDependencies). Sharing one package is what keeps them
// registering the same vocabulary. Processes that resolve no step type do not
// import it and register nothing: archie-gateway, archie-messaging,
// archie-playbooks and archie-ui.
package workflowsteps

import (
	"fmt"
	"maps"
	"slices"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// shippedStages is the provider that contributes every stage of the shipped
// workflows (bootstrap, implement, tdd, feasibility, triage, remediate) as a
// workflow step type. It is what makes the shipped vocabulary arrive by
// registration rather than by the manager implying it, so the vocabulary a
// process resolves is always the vocabulary a provider set claimed.
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

// repoHooks is the provider that contributes the step types replacing the
// repository-authored hooks that were deleted with the interpreted workflow
// engine  --  .archie/gate.go, whose rules now arrive as settings on
// workflow.DiffRulesStepName instead of as Go read out of the worktree being
// worked on.
//
// It is a provider of its own rather than part of shippedStages, which is
// scoped to the stages of the shipped workflows: a repository's migration target
// is not part of any shipped workflow, and a root that bundles one set should be
// able to tell which set a step type came from.
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
	return []workflow.StepType{workflow.AgentRunStepType(), workflow.WorkflowCallStepType()}
}

// Providers returns the provider set the roots that resolve a workflow step
// type register at their composition root, before the first resolution: it is
// what NewManager registers, and what the roots' guards read to hold a
// registered vocabulary to the set it came from. A step type added here becomes
// reachable from the validating side and the executing side at once.
//
// The set is a compiled-in constant rather than a directory scan: a step type
// is a Go factory, and no bridge has been built from the Yaegi-interpreted
// plugins internal/plugin loads to workflow.StepFactory -- those plugins
// satisfy plugin.Plugin's metadata contract only. Such a bridge is possible:
// internal/secret/secretextract wraps an interpreted Engine into the typed
// contract secret.Registry.LoadDir registers (internal/app/archied/
// provider_secrets.go). Until one exists for step types, adding a step type
// means adding a provider here.
func Providers() []workflow.StepTypeProvider {
	return []workflow.StepTypeProvider{shippedStages{}, repoHooks{}, eventSteps{}}
}

// NewManager builds this process's workflow step-type manager by registering
// the provider set. Every call constructs an independent manager: there is no
// package-level registry and nothing registers at init(), so building the
// vocabulary twice in one process — as `-count=2` does — cannot collide with
// the first build.
func NewManager() (*workflow.Manager, error) {
	manager := workflow.NewManager()
	for _, provider := range Providers() {
		if err := manager.Register(provider); err != nil {
			return nil, fmt.Errorf("register workflow step type provider %q: %w", provider.Name(), err)
		}
	}
	return manager, nil
}
