package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/store"
)

// playbookStore loads a single playbook document from a temp directory, so
// these tests exercise the real loader rather than a hand-built store.
func playbookStore(t *testing.T, document string) *playbook.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pb.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := playbook.Load(dir, module.New())
	if err != nil {
		t.Fatalf("load playbook: %v", err)
	}
	return loaded
}

// routableDefinitions is the definition collection these tests pin against:
// two real ids so a playbook's choice is distinguishable from the choice the
// kind bindings would have made for the same labels.
func routableDefinitions() *workflowDefinitionsStub {
	return &workflowDefinitionsStub{
		collection: workflow.WorkflowDefinitionCollection{Definitions: []workflow.WorkflowDefinitionEntry{
			{ID: "tdd", YAML: "id: tdd\nsteps:\n  - type: bootstrap.apply\n"},
			{ID: "feasibility", YAML: "id: feasibility\nsteps:\n  - type: bootstrap.prepare\n"},
		}},
		version: 1,
	}
}

// forgeTask enqueues a labelled forge-sourced task and returns its row.
func forgeTask(t *testing.T, resources *store.Store, labels string) *workflow.Task {
	t.Helper()
	if _, err := resources.EnqueueIssue(t.Context(), "acme", "widget", 7, "title", "body", labels, ""); err != nil {
		t.Fatal(err)
	}
	task, err := resources.TaskByIssue(t.Context(), "acme", "widget", 7)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// TestPinWorkflowDefinitionPrefersMatchingPlaybook is the coordinator's
// production wiring: a forge task whose labels match a loaded playbook pins
// the workflow that playbook names, not the one the kind bindings would have
// chosen for the same labels. Bypassing the coordinator pins "tdd" (the
// default binding for kind bug) and fails this test.
func TestPinWorkflowDefinitionPrefersMatchingPlaybook(t *testing.T) {
	resources := store.OpenTest(t)
	defer resources.Close()
	task := forgeTask(t, resources, "bug")

	d := &Daemon{
		Store:               resources,
		WorkflowDefinitions: routableDefinitions(),
		Log:                 slog.New(slog.DiscardHandler),
		Playbooks: playbookStore(t, `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: feasibility
`),
	}
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if task.Workflow != "feasibility" {
		t.Fatalf("pinned workflow = %q, want feasibility (the playbook's choice, not the kind binding's)", task.Workflow)
	}
	stored, err := resources.TaskByID(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Workflow != "feasibility" {
		t.Fatalf("persisted workflow = %q, want feasibility", stored.Workflow)
	}
}

// TestPinWorkflowDefinitionFallsBackWhenNoPlaybookMatches: the coordinator is
// an additional routing source, not a replacement. A non-matching playbook
// set leaves the existing kind/label bindings in charge.
func TestPinWorkflowDefinitionFallsBackWhenNoPlaybookMatches(t *testing.T) {
	resources := store.OpenTest(t)
	defer resources.Close()
	task := forgeTask(t, resources, "bug")

	d := &Daemon{
		Store:               resources,
		WorkflowDefinitions: routableDefinitions(),
		Log:                 slog.New(slog.DiscardHandler),
		Playbooks: playbookStore(t, `
trigger:
  kind: bootstrap
actions:
  - position: workflow
    workflow: feasibility
`),
	}
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if task.Workflow != "tdd" {
		t.Fatalf("pinned workflow = %q, want tdd (kind binding for bug)", task.Workflow)
	}
}

// TestPinWorkflowDefinitionPlaybookWorkflowMustExist: an operator who binds a
// trigger to a workflow that is not defined gets a reported failure, the same
// shape ResolveWorkflowID gives a task pre-assigned an undefined workflow.
// Silently running the kind binding's choice instead would run the wrong
// workflow under a rule the operator wrote.
func TestPinWorkflowDefinitionPlaybookWorkflowMustExist(t *testing.T) {
	resources := store.OpenTest(t)
	defer resources.Close()
	task := forgeTask(t, resources, "bug")

	d := &Daemon{
		Store:               resources,
		WorkflowDefinitions: routableDefinitions(),
		Log:                 slog.New(slog.DiscardHandler),
		Playbooks: playbookStore(t, `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: nonesuch
`),
	}
	err := d.pinWorkflowDefinition(t.Context(), task)
	if err == nil {
		t.Fatal("pin succeeded, want an error naming the undefined workflow")
	}
	if !strings.Contains(err.Error(), "nonesuch") || !strings.Contains(err.Error(), "pb.yaml") {
		t.Fatalf("error = %v, want it to name both the workflow and the playbook", err)
	}
}

// TestPinWorkflowDefinitionPreAssignedWorkflowBeatsPlaybook: a task carrying
// an explicit workflow is the waiting_human -> approved requeue handoff. That
// assignment is a decision already made about this task and must not be
// re-routed by a playbook trigger that still matches its labels.
func TestPinWorkflowDefinitionPreAssignedWorkflowBeatsPlaybook(t *testing.T) {
	resources := store.OpenTest(t)
	defer resources.Close()
	task := forgeTask(t, resources, "bug")
	task.Workflow = "tdd"

	d := &Daemon{
		Store:               resources,
		WorkflowDefinitions: routableDefinitions(),
		Log:                 slog.New(slog.DiscardHandler),
		Playbooks: playbookStore(t, `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: feasibility
`),
	}
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if task.Workflow != "tdd" {
		t.Fatalf("pinned workflow = %q, want the pre-assigned tdd", task.Workflow)
	}
}

// TestPinWorkflowDefinitionEvaluatesConditionAgainstIntakeEvent: a `when`
// condition reads the event surface the daemon really builds from the task
// row. This is the field-name contract between playbookInput and every
// operator-written condition, including the shipped example's -- a rename on
// either side makes conditions silently evaluate false, which this catches.
func TestPinWorkflowDefinitionEvaluatesConditionAgainstIntakeEvent(t *testing.T) {
	document := `
trigger:
  kind: bug
actions:
  - position: workflow
    workflow: feasibility
    when: '"regression" in event.labels && event.repo == "widget"'
`
	for _, tc := range []struct {
		name   string
		labels string
		want   string
	}{
		{name: "condition true selects the playbook's workflow", labels: "bug,regression", want: "feasibility"},
		{name: "condition false leaves the kind binding in charge", labels: "bug", want: "tdd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := store.OpenTest(t)
			defer resources.Close()
			task := forgeTask(t, resources, tc.labels)
			d := &Daemon{
				Store:               resources,
				WorkflowDefinitions: routableDefinitions(),
				Log:                 slog.New(slog.DiscardHandler),
				Playbooks:           playbookStore(t, document),
			}
			if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if task.Workflow != tc.want {
				t.Fatalf("pinned workflow = %q, want %q", task.Workflow, tc.want)
			}
		})
	}
}

// TestPinWorkflowDefinitionTriggersOnInstanceDefinedLabel: the trigger's kind
// axis is the closed workintake vocabulary (it addresses the task subjects),
// so the label axis is what an instance defines for itself. A label no kind
// recognises still triggers a playbook, which is the only way an operator can
// bind a workflow to their own taxonomy without a code change.
func TestPinWorkflowDefinitionTriggersOnInstanceDefinedLabel(t *testing.T) {
	resources := store.OpenTest(t)
	defer resources.Close()
	task := forgeTask(t, resources, "ops::security-review,needs-triage")

	d := &Daemon{
		Store:               resources,
		WorkflowDefinitions: routableDefinitions(),
		Log:                 slog.New(slog.DiscardHandler),
		Playbooks: playbookStore(t, `
trigger:
  labels:
    - ops::security-review
actions:
  - position: workflow
    workflow: feasibility
`),
	}
	if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if task.Workflow != "feasibility" {
		t.Fatalf("pinned workflow = %q, want feasibility (triggered by an instance-defined label)", task.Workflow)
	}
}
