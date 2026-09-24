package archied

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// stepVocabulary builds the workflow step vocabulary a process resolves
// workflow definitions against: the shared provider set
// (internal/infrastructure/workflowsteps), registered at the composition root,
// before the first resolution.
//
// Two roots call it, and both are roots that resolve a step type: RunStateStore
// (the State Store's validating side) and openDaemonWorkflowDefinitions (the
// daemon's workflow-definitions client). A root that cannot reach a workflow
// definition registers no vocabulary and cannot be failed by one, which is why
// the gateway root and internal/app/archiemessaging are not here.
//
// It is a named function so that step_vocabulary_test.go can fail if a root
// stops resolving the shipped stages, or hands a constructor a manager other
// than the one this returns; the roots themselves open a database and dial
// gRPC, so the test asserts their wiring by parsing their bodies.
//
// Agreement is within one build: the State Store and archie-agent are
// separately deployed binaries, so a State Store built from newer source than
// the agent it dispatches to can still disagree, and only a matching deploy
// fixes that.
func stepVocabulary() (*workflow.Manager, error) { return workflowsteps.NewManager() }

// openDaemonWorkflowDefinitions builds the daemon's workflow-definitions
// client on the process step vocabulary, and is called by the daemon root alone
// (main.go's Run) before buildDaemon captures the client.
//
// It is deliberately not part of openStateStoreAdapter, which the gateway root
// shares: the gateway reads catalog, runtime settings and personas, never a
// workflow definition, so registering a vocabulary there would be the dead
// vocabulary this shape removed from internal/app/archiemessaging -- a root that
// cannot consume it must not be failed by it.
//
// The daemon reads definitions through it; no production process replaces
// definitions yet (see controlplane.WorkflowDefinitionsClient.
// ReplaceWorkflowDefinitions).
func (b *boot) openDaemonWorkflowDefinitions() error {
	steps, err := stepVocabulary()
	if err != nil {
		return fmt.Errorf("register workflow step vocabulary: %w", err)
	}
	definitions, err := controlplane.NewWorkflowDefinitionsClient(b.controlPlaneRPC, steps)
	if err != nil {
		return fmt.Errorf("build workflow definitions client: %w", err)
	}
	b.workflowDefinitions = definitions
	return nil
}

// openDaemonStateSurfaces opens the daemon's State Store surfaces in one step:
// the adapter shared with every other root (openStateStoreAdapter) and, on top
// of it, the daemon-only workflow-definitions client
// (openDaemonWorkflowDefinitions).
//
// It exists because the daemon root is a flat composition sequence with no
// complexity budget left to spend on a second adjacent guard, and the order the
// two calls owe each other is a property of the pair, not of that sequence: the
// definitions client wraps the control-plane transport the adapter dials, so it
// cannot be built first, and buildDaemon captures the client, so it cannot be
// built later. step_vocabulary_test.go pins exactly that order here.
//
// The gateway root calls openStateStoreAdapter directly and stops there; the
// daemon-only half stays out of that function for the reason above it.
func (b *boot) openDaemonStateSurfaces() error {
	if err := b.openStateStoreAdapter(); err != nil {
		return err
	}
	if err := b.openDaemonWorkflowDefinitions(); err != nil {
		b.log.Error("workflow definitions client", "err", err)
		return err
	}
	return nil
}
