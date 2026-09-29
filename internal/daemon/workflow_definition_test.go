package daemon

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

type workflowDefinitionsStub struct {
	collection workflow.WorkflowDefinitionCollection
	version    int64
}

func (s *workflowDefinitionsStub) WorkflowDefinitions(context.Context) (workflow.WorkflowDefinitionCollection, int64, error) {
	return s.collection, s.version, nil
}

func TestWorkflowDefinitionPinSurvivesActiveOverride(t *testing.T) {
	resources := pgstore.Open(t)
	defer resources.Close()
	task, err := resources.EnqueueChatTask(t.Context(), "acme", "widget", "custom", "", "custom", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := "id: custom\nsteps:\n  - type: bootstrap.apply\n"
	provider := &workflowDefinitionsStub{collection: workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{{ID: "custom", YAML: first}}}, version: 4}
	d := &Daemon{Store: resources, WorkflowDefinitions: provider}
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	provider.collection.Definitions[0].YAML = "id: custom\nsteps:\n  - type: bootstrap.prepare\n"
	provider.version = 5
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if task.WorkflowDefinitionYAML != first || task.WorkflowDefinitionVersion != 4 {
		t.Fatalf("pin changed after override: version=%d yaml=%q", task.WorkflowDefinitionVersion, task.WorkflowDefinitionYAML)
	}
	stored, err := resources.TaskByID(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorkflowDefinitionDigest != workflow.DigestDefinition(first) {
		t.Fatal("persisted workflow digest does not identify the pinned YAML")
	}
}

// TestPinWorkflowDefinitionRepinsWhenTheTaskNamesAnotherWorkflow covers the
// waiting_human -> approved requeue: RequeueTask writes the new workflow name
// and leaves the previous run's definition pinned, so the requeued row carries
// a workflow column and a stored definition that disagree.
//
// Those two are what the worker's compile refuses at dispatch
// (agentworker.CompilePinnedWorkflow: "pinned workflow definition does not
// match task identity"), so a pin that names a different workflow than the
// task is a dispatch that can never run, and the daemon -- the producer that
// wrote the pin -- re-pins it from the collection the task's workflow is
// resolved out of. A pin that is present, digest-valid AND id-consistent stays
// a strict no-op, so an operator's newer active definition does not overwrite
// a task's existing pin on the next dispatch.
func TestPinWorkflowDefinitionRepinsWhenTheTaskNamesAnotherWorkflow(t *testing.T) {
	const (
		pinnedVersion = 4
		activeVersion = 9
	)
	feasibilityPin := "id: feasibility\nsteps:\n  - type: feasibility.assess\n"
	customPin := "id: custom\nsteps:\n  - type: bootstrap.apply\n"
	// The active collection is the one the daemon resolves a named workflow
	// out of. Its "custom" entry is deliberately different YAML from customPin:
	// that is the operator's newer active definition, and the only way to tell
	// a reuse from a re-pin is that a reused pin keeps the older YAML.
	active := &workflowDefinitionsStub{
		collection: workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{
			{ID: "feasibility", YAML: feasibilityPin},
			{ID: "implement", YAML: "id: implement\nsteps:\n  - type: implement.prepare\n"},
			{ID: "custom", YAML: "id: custom\nsteps:\n  - type: bootstrap.prepare\n"},
		}},
		version: activeVersion,
	}

	tests := []struct {
		name string
		// taskWorkflow is the row's workflow column: what the requeue wrote,
		// and the workflow the pinned definition must end up naming.
		taskWorkflow string
		// pinnedYAML is the definition the row carries into the dispatch --
		// for a requeue, the workflow the task ran before its workflow column
		// changed.
		pinnedYAML string
		// shipped pins from the shipped definitions, the way a daemon with no
		// definitions provider does.
		shipped      bool
		wantWorkflow string
		wantYAML     string
		wantVersion  int64
	}{
		{
			name:         "a requeue onto another workflow is re-pinned from the active collection",
			taskWorkflow: "implement",
			pinnedYAML:   feasibilityPin,
			wantWorkflow: "implement",
			wantYAML:     "id: implement\nsteps:\n  - type: implement.prepare\n",
			wantVersion:  activeVersion,
		},
		{
			name:         "a daemon with no definitions provider re-pins from the shipped set",
			taskWorkflow: "implement",
			pinnedYAML:   feasibilityPin,
			shipped:      true,
			wantWorkflow: "implement",
			wantVersion:  0,
		},
		{
			name:         "an id-consistent pin is reused, so an active override does not displace it",
			taskWorkflow: "custom",
			pinnedYAML:   customPin,
			wantWorkflow: "custom",
			wantYAML:     customPin,
			wantVersion:  pinnedVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantYAML := tt.wantYAML
			if tt.shipped {
				entry, ok := workflow.ShippedDefinitions().DefinitionByID(tt.wantWorkflow)
				if !ok {
					t.Fatalf("shipped definitions have no %q", tt.wantWorkflow)
				}
				wantYAML = entry.YAML
			}

			resources := pgstore.Open(t)
			defer resources.Close()
			task, err := resources.EnqueueChatTask(t.Context(), "acme", "widget", tt.name, "", tt.taskWorkflow, "operator", nil)
			if err != nil {
				t.Fatal(err)
			}
			// RequeueTask sets status, workflow and park_reason, and never
			// touches the workflow_definition_* columns, so this is the row a
			// requeue hands to the next dispatch: the new workflow name over
			// the previous run's definition.
			task.WorkflowDefinitionYAML = tt.pinnedYAML
			task.WorkflowDefinitionDigest = workflow.DigestDefinition(tt.pinnedYAML)
			task.WorkflowDefinitionVersion = pinnedVersion
			if err := resources.Update(t.Context(), task); err != nil {
				t.Fatal(err)
			}

			d := &Daemon{Store: resources, Log: slog.New(slog.DiscardHandler)}
			if !tt.shipped {
				d.WorkflowDefinitions = active
			}
			if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
				t.Fatalf("pinWorkflowDefinition: %v", err)
			}
			if task.Workflow != tt.wantWorkflow || task.WorkflowDefinitionYAML != wantYAML || task.WorkflowDefinitionVersion != tt.wantVersion {
				t.Fatalf("pin = (%q, version %d, %q),\nwant (%q, version %d, %q)",
					task.Workflow, task.WorkflowDefinitionVersion, task.WorkflowDefinitionYAML,
					tt.wantWorkflow, tt.wantVersion, wantYAML)
			}

			stored, err := resources.TaskByID(t.Context(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Workflow != tt.wantWorkflow || stored.WorkflowDefinitionYAML != wantYAML || stored.WorkflowDefinitionVersion != tt.wantVersion {
				t.Fatalf("persisted pin = (%q, version %d, %q),\nwant (%q, version %d, %q)",
					stored.Workflow, stored.WorkflowDefinitionVersion, stored.WorkflowDefinitionYAML,
					tt.wantWorkflow, tt.wantVersion, wantYAML)
			}
			requirePinMatchesTaskIdentity(t, stored)
		})
	}
}

// TestApproveRequeueRepinsTheWorkflowDefinition reproduces the operator-visible
// defect end to end through the real store writes: a task runs feasibility,
// parks waiting_human with the feasibility definition pinned, and the approval
// requeue moves it to the implement workflow. RequeueTask sets status, workflow
// and park_reason and leaves the workflow_definition_* columns, so the next
// dispatch met an id mismatch and the worker's compile parked the task again --
// identically on every further approval, leaving decline the only working
// action.
//
// What the task row must satisfy before that dispatch is the worker's own
// precondition (agentworker.CompilePinnedWorkflow), asserted here through
// requirePinMatchesTaskIdentity.
func TestApproveRequeueRepinsTheWorkflowDefinition(t *testing.T) {
	resources := pgstore.Open(t)
	defer resources.Close()
	if _, err := resources.EnqueueChatTask(t.Context(), "acme", "widget", "feature request", "", "feasibility", "operator", nil); err != nil {
		t.Fatal(err)
	}
	claim, err := resources.ClaimNext(t.Context())
	if err != nil || claim == nil {
		t.Fatalf("claim: (%v, %v)", claim, err)
	}

	// The run pins the feasibility definition and parks for the human decision.
	provider := &workflowDefinitionsStub{collection: workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{
		{ID: "feasibility", YAML: "id: feasibility\nsteps:\n  - type: feasibility.assess\n"},
		{ID: "implement", YAML: "id: implement\nsteps:\n  - type: implement.prepare\n"},
	}}, version: 2}
	d := &Daemon{Store: resources, Log: slog.New(slog.DiscardHandler), WorkflowDefinitions: provider}
	if err := d.pinWorkflowDefinition(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	if err := resources.Transition(t.Context(), claim.ID, "running", "waiting_human", "PRD delivered, awaiting go/no-go"); err != nil {
		t.Fatal(err)
	}

	// The operator approves: the same write taskactions.Service.apply performs.
	if err := resources.Requeue(t.Context(), claim.ID, "waiting_human", "implement"); err != nil {
		t.Fatal(err)
	}
	requeued, err := resources.TaskByID(t.Context(), claim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.Workflow != "implement" {
		t.Fatalf("requeued workflow = %q, want implement", requeued.Workflow)
	}
	if got, err := workflow.DefinitionID(requeued.WorkflowDefinitionYAML); err != nil || got != "feasibility" {
		t.Fatalf("the requeue changed the stored pin to (%q, %v); this test only means anything while the pin is still the previous workflow's", got, err)
	}

	// The next dispatch re-pins before the worker's compile sees the row.
	if err := d.pinWorkflowDefinition(t.Context(), requeued); err != nil {
		t.Fatal(err)
	}
	stored, err := resources.TaskByID(t.Context(), requeued.ID)
	if err != nil {
		t.Fatal(err)
	}
	requirePinMatchesTaskIdentity(t, stored)
}

// TestPinWorkflowDefinitionRefusesATamperedPin keeps the digest half of the
// guard intact: a stored definition that does not hash to the digest recorded
// with it is a corrupt row, and repairing it here would silently run a
// definition nobody wrote. It is refused, and the row is left for an operator.
func TestPinWorkflowDefinitionRefusesATamperedPin(t *testing.T) {
	resources := pgstore.Open(t)
	defer resources.Close()
	task, err := resources.EnqueueChatTask(t.Context(), "acme", "widget", "tampered", "", "implement", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	task.WorkflowDefinitionYAML = "id: implement\nsteps:\n  - type: implement.prepare\n"
	task.WorkflowDefinitionDigest = workflow.DigestDefinition("id: implement\nsteps:\n  - type: implement.build\n")
	if err := resources.Update(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{Store: resources, Log: slog.New(slog.DiscardHandler), WorkflowDefinitions: &workflowDefinitionsStub{
		collection: workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{
			{ID: "implement", YAML: "id: implement\nsteps:\n  - type: implement.prepare\n"},
		}},
		version: 3,
	}}
	err = d.pinWorkflowDefinition(t.Context(), task)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("pinWorkflowDefinition error = %v, want a digest mismatch refusal", err)
	}
	if task.WorkflowDefinitionVersion != 0 {
		t.Fatalf("refused pin wrote version %d; a refused pin must leave the row alone", task.WorkflowDefinitionVersion)
	}
}

// requirePinMatchesTaskIdentity holds a persisted pin to the invariant the
// worker's compile enforces before it runs a dispatch
// (agentworker.CompilePinnedWorkflow): the pinned definition compiles against
// the process vocabulary, names the workflow the task row names, and hashes to
// the digest stored beside it. A producer that leaves a pin failing this is
// the defect that parks a requeued task forever.
func requirePinMatchesTaskIdentity(t *testing.T, stored *workflow.Task) {
	t.Helper()
	compiled, err := workflow.ParseAndCompile(stored.WorkflowDefinitionYAML, workflow.BuiltinStepRegistry())
	if err != nil {
		t.Fatalf("pinned definition does not compile: %v", err)
	}
	if compiled.Name != stored.Workflow {
		t.Fatalf("pinned definition names %q, the task row names %q: the worker's compile refuses this pair", compiled.Name, stored.Workflow)
	}
	if got := workflow.DigestDefinition(stored.WorkflowDefinitionYAML); got != stored.WorkflowDefinitionDigest {
		t.Fatalf("pinned definition digest = %s, the row records %s", got, stored.WorkflowDefinitionDigest)
	}
}
